// Package driver runs each turn as a Kubernetes Job and ends the turn once
// Kubernetes reports that the Job has ended.
package driver

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"aviary/supervisor/internal/events"
	"aviary/supervisor/internal/store"
)

// What the chart names the things that the turn pods use.
const (
	namespace  = "aviary-runtime"
	storeClaim = "aviary-store-default"
	llmSecret  = "aviary-llm"
)

const (
	// turnLabel is on the Job of a turn and holds the turn's ID.
	turnLabel = "aviary.io/turn"
	// agentUser is the user that the harness images run as.
	agentUser = 10000
	// deadline is how long a turn's pod may run before Kubernetes ends it.
	deadline = 30 * time.Minute
	// resync is how often the Jobs are looked at again, which retries the
	// ending of a turn that failed.
	resync = 30 * time.Second
	// recheck is how often the driver looks for queued turns without being
	// woken, which retries a dispatch that failed.
	recheck = 5 * time.Second
)

type Config struct {
	// GatewayURL is where the runners reach the Supervisor.
	GatewayURL string
	// RunnerImage holds the runner, which every turn pod mounts.
	RunnerImage string
	// Harnesses are the harnesses that agents can use, by name.
	Harnesses map[string]Harness
	// MaxConcurrentTurns is the most turns that run at once. The turns
	// beyond it wait in the order in which they were accepted.
	MaxConcurrentTurns int
}

type Harness struct {
	Image string `json:"image"`
}

type Driver struct {
	config Config
	client kubernetes.Interface
	store  *store.Store
	events *events.Streams
	wake   chan struct{}
}

// New returns a driver for the cluster that the Supervisor runs in.
func New(config Config, st *store.Store, ev *events.Streams) (*Driver, error) {
	cluster, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("find the cluster: %w", err)
	}
	client, err := kubernetes.NewForConfig(cluster)
	if err != nil {
		return nil, err
	}
	return &Driver{config: config, client: client, store: st, events: ev, wake: make(chan struct{}, 1)}, nil
}

// HasHarness reports whether turns can run on the harness of that name.
func (d *Driver) HasHarness(name string) bool {
	_, found := d.config.Harnesses[name]
	return found
}

// Wake makes the driver look for queued turns to run. It is called when a
// turn was queued or a running one ended.
func (d *Driver) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
		// The driver has not yet acted on an earlier call, which covers this one.
	}
}

// dispatch runs the queued turns that fit under the limit, oldest first.
func (d *Driver) dispatch(ctx context.Context) {
	dispatches, err := d.store.DispatchTurns(ctx, d.config.MaxConcurrentTurns)
	if err != nil {
		slog.ErrorContext(ctx, "dispatch turns", "error", err)
		return
	}
	for _, dispatch := range dispatches {
		if err := d.launch(ctx, dispatch); err != nil {
			slog.ErrorContext(ctx, "launch turn", "turn", dispatch.TurnID, "error", err)
		}
	}
}

// launch creates the Job that runs a dispatched turn.
func (d *Driver) launch(ctx context.Context, dispatch store.Dispatch) error {
	harness, found := d.config.Harnesses[dispatch.Harness]
	if !found {
		return fmt.Errorf("harness %q is not configured", dispatch.Harness)
	}
	_, err := d.client.BatchV1().Jobs(namespace).Create(ctx, d.job(dispatch, harness), metav1.CreateOptions{})
	return err
}

// Run runs the queued turns and ends the turns whose Jobs have ended, until
// ctx is done. It also sees the turns that were queued and the Jobs that
// ended while no Supervisor was running.
func (d *Driver) Run(ctx context.Context) {
	factory := informers.NewSharedInformerFactoryWithOptions(d.client, resync,
		informers.WithNamespace(namespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = turnLabel
		}),
	)
	_, err := factory.Batch().V1().Jobs().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(job any) { d.observe(ctx, job.(*batchv1.Job)) },
		UpdateFunc: func(_, job any) { d.observe(ctx, job.(*batchv1.Job)) },
	})
	if err != nil {
		// Handlers can only be refused by an informer that has stopped.
		panic(err)
	}
	factory.Start(ctx.Done())
	defer factory.Shutdown()

	ticker := time.NewTicker(recheck)
	defer ticker.Stop()
	for {
		d.dispatch(ctx)
		select {
		case <-d.wake:
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// observe ends the turn of a Job that has ended and deletes the Job. A Job
// gets its Complete or Failed condition only after its pod has stopped, so
// from then on nothing uses the session's files.
func (d *Driver) observe(ctx context.Context, job *batchv1.Job) {
	succeeded, ended := outcome(job)
	if !ended {
		return
	}
	turnID, err := uuid.Parse(job.Labels[turnLabel])
	if err != nil {
		slog.ErrorContext(ctx, "job has no turn", "job", job.Name, "error", err)
		return
	}
	turn, changed, err := d.store.EndTurn(ctx, turnID, succeeded)
	if err != nil {
		slog.ErrorContext(ctx, "end turn", "turn", turnID, "error", err)
		return
	}
	if changed {
		slog.InfoContext(ctx, "turn ended", "turn", turnID, "state", turn.State)
		// A client that misses this event gets it from the turn's record.
		if err := d.events.AppendLast(ctx, turn); err != nil {
			slog.ErrorContext(ctx, "publish turn event", "turn", turnID, "error", err)
		}
		// The turn no longer counts against the limit.
		d.Wake()
	}
	background := metav1.DeletePropagationBackground
	err = d.client.BatchV1().Jobs(namespace).Delete(ctx, job.Name, metav1.DeleteOptions{PropagationPolicy: &background})
	if err != nil && !apierrors.IsNotFound(err) {
		slog.ErrorContext(ctx, "delete job", "job", job.Name, "error", err)
	}
}

// outcome reports whether the Job has ended and, if so, whether its pod
// succeeded.
func outcome(job *batchv1.Job) (succeeded, ended bool) {
	for _, condition := range job.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		switch condition.Type {
		case batchv1.JobComplete:
			return true, true
		case batchv1.JobFailed:
			return false, true
		}
	}
	return false, false
}

// job returns the Job that runs a turn. The harness container sees only the
// home of its session. The runner comes from its own image, so the harness
// images do not have to contain it.
func (d *Driver) job(dispatch store.Dispatch, harness Harness) *batchv1.Job {
	home := fmt.Sprintf("projects/%s/agents/%s/sessions/%s/home", dispatch.ProjectID, dispatch.AgentID, dispatch.SessionID)
	labels := map[string]string{turnLabel: dispatch.TurnID.String()}
	restricted := &corev1.SecurityContext{
		AllowPrivilegeEscalation: new(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "turn-" + dispatch.TurnID.String(), Labels: labels},
		Spec: batchv1.JobSpec{
			// A turn is not repeated: the agent may already have acted.
			BackoffLimit:          new(int32(0)),
			ActiveDeadlineSeconds: new(int64(deadline.Seconds())),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: new(false),
					EnableServiceLinks:           new(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   new(true),
						RunAsUser:      new(int64(agentUser)),
						RunAsGroup:     new(int64(agentUser)),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					// Kubernetes would create the missing home as root, which
					// the agent cannot write to. The runner creates it first.
					InitContainers: []corev1.Container{{
						Name:            "home",
						Image:           d.config.RunnerImage,
						Command:         []string{"/aviary-runner", "init-home", "/store/" + home},
						SecurityContext: restricted,
						VolumeMounts:    []corev1.VolumeMount{{Name: "store", MountPath: "/store"}},
					}},
					Containers: []corev1.Container{{
						Name:       "agent",
						Image:      harness.Image,
						Command:    []string{"/aviary/bin/aviary-runner"},
						WorkingDir: "/home/workspace",
						Env: []corev1.EnvVar{
							{Name: "HOME", Value: "/home"},
							{Name: "AVIARY_GATEWAY_URL", Value: d.config.GatewayURL},
							{Name: "AVIARY_TURN_TOKEN", Value: dispatch.Token},
						},
						EnvFrom: []corev1.EnvFromSource{{
							SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: llmSecret}},
						}},
						SecurityContext: restricted,
						VolumeMounts: []corev1.VolumeMount{
							{Name: "store", MountPath: "/home", SubPath: home},
							{Name: "runner", MountPath: "/aviary/bin", ReadOnly: true},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "store", VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: storeClaim},
						}},
						{Name: "runner", VolumeSource: corev1.VolumeSource{
							Image: &corev1.ImageVolumeSource{Reference: d.config.RunnerImage},
						}},
					},
				},
			},
		},
	}
}
