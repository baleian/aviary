// Package driver runs each turn as a Kubernetes Job and ends the turn once
// Kubernetes reports that the Job has ended.
package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
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

	"aviary/supervisor/events"
	"aviary/supervisor/internal/metrics"
	"aviary/supervisor/internal/store"
)

// What the chart names the things that the turn pods use.
const (
	namespace  = "aviary-runtime"
	storeClaim = "aviary-store-default"
)

const (
	// turnLabel is on the Job of a turn and holds the turn's ID.
	turnLabel = "aviary.io/turn"
	// agentContainer is the container of a turn pod in which the runner
	// and the harness run.
	agentContainer = "agent"
	// agentUser is the user that the harness images run as.
	agentUser = 10000
	// resync is how often the Jobs are looked at again, which retries the
	// ending of a turn that failed, and how often the turns and the Jobs are
	// reconciled.
	resync = 30 * time.Second
	// recheck is how often the driver looks for queued turns without being
	// woken, which retries a dispatch that failed.
	recheck = 5 * time.Second
	// lostAfter is how long after its dispatch a turn without a Job is taken
	// to have lost it. The Job is created right after the dispatch, so
	// until then it may only be on its way.
	lostAfter = time.Minute
	// cancelGrace is how long a turn that was asked to stop is given to end
	// by itself before its Job is deleted.
	cancelGrace = 30 * time.Second
)

type Config struct {
	// GatewayURL is where the runners reach the Supervisor.
	GatewayURL string
	// RunnerImage holds the runner, which every turn pod mounts.
	RunnerImage string
	// LLMBaseURL is where the harnesses reach their models, which is the
	// Egress Proxy. Its path is not used: a harness reaches the model
	// endpoint of its turn under the path of the endpoint's own URL.
	LLMBaseURL string
	// ProxyURL is the Egress Proxy's forward proxy, for everything else
	// that a turn pod sends out.
	ProxyURL string
	// Harnesses are the harnesses that agents can use, by name.
	Harnesses map[string]Harness
	// MaxToolOutputBytes is the longest text of a tool call, in what the
	// tool was called with or returned, that the runner reports as it is.
	// It cuts a longer one. Zero is no limit.
	MaxToolOutputBytes int
	// MaxStartup is how long the pod of a turn may take until its runner
	// has connected. A turn whose pod takes longer fails. Zero is no limit.
	MaxStartup time.Duration
	// MaxTurn is how long a turn may run before Kubernetes ends its pod.
	MaxTurn time.Duration
	// MaxReconnect is how long the runner of a turn goes on when it has
	// lost its Supervisor. A turn whose runner reaches none in that time
	// fails. Zero is no limit.
	MaxReconnect time.Duration
	// MaxQueueWait is how long a turn may wait to run. A turn that waits
	// longer ends as expired. Zero is no limit.
	MaxQueueWait time.Duration
	// MaxQueuedTurns is the most turns that may wait to run. A message that
	// would make one more is refused. Zero is no limit.
	MaxQueuedTurns int
	// MaxConcurrentTurns is the most turns that run at once. The turns
	// beyond it wait in the order in which they were accepted.
	MaxConcurrentTurns int
}

type Harness struct {
	Image string `json:"image"`
	// Resources is what the pod of a turn gets if its agent has no resources
	// of its own.
	Resources corev1.ResourceRequirements `json:"resources"`
}

type Driver struct {
	config Config
	// hosts are the names of what a turn pod connects to: the Supervisor
	// and the Egress Proxy.
	hosts []string
	// llm is where a turn pod reaches its model: the Egress Proxy.
	llm *url.URL
	// proxy is the forward proxy, and noProxy the hosts that a turn pod
	// reaches without it.
	proxy   *url.URL
	noProxy string
	client  kubernetes.Interface
	store   *store.Store
	events  *events.Streams
	metrics *metrics.Metrics
	wake    chan struct{}
	// moved is set when a turn left the queue without being dispatched.
	moved atomic.Bool
}

// InCluster returns a client of the cluster that the Supervisor runs in.
func InCluster() (kubernetes.Interface, error) {
	cluster, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("find the cluster: %w", err)
	}
	return newClient(cluster)
}

func newClient(cluster *rest.Config) (kubernetes.Interface, error) {
	// By itself the client sends no more than five requests a second, and a
	// turn takes a few of them: its Job is created and then deleted. That
	// would hold the turns back long before their limit does. The API server
	// guards itself against too many requests.
	cluster.QPS = -1
	return kubernetes.NewForConfig(cluster)
}

// New returns a driver that runs the turns in the cluster of the client.
func New(config Config, client kubernetes.Interface, st *store.Store, ev *events.Streams, m *metrics.Metrics) (*Driver, error) {
	for name, harness := range config.Harnesses {
		if err := checkResources(harness.Resources); err != nil {
			return nil, fmt.Errorf("the resources of the harness %s: %w", name, err)
		}
	}
	gateway, err := url.Parse(config.GatewayURL)
	if err != nil {
		return nil, err
	}
	llm, err := url.Parse(config.LLMBaseURL)
	if err != nil {
		return nil, err
	}
	proxy, err := url.Parse(config.ProxyURL)
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, address := range []*url.URL{gateway, llm, proxy} {
		if !slices.Contains(hosts, address.Hostname()) {
			hosts = append(hosts, address.Hostname())
		}
	}
	// A harness may talk to itself over the loopback interface. The model
	// endpoint is the proxy itself, on another port, and the Supervisor is
	// reached directly.
	noProxy := []string{"localhost", "127.0.0.1", "::1", llm.Hostname(), gateway.Hostname()}
	return &Driver{
		config: config, hosts: hosts, llm: llm, proxy: proxy, noProxy: strings.Join(noProxy, ","),
		client: client, store: st, events: ev, metrics: m, wake: make(chan struct{}, 1),
	}, nil
}

// hostAliases looks up the addresses of what a turn pod connects to. The pod
// has no DNS, so it gets them written into its hosts file.
func (d *Driver) hostAliases(ctx context.Context) ([]corev1.HostAlias, error) {
	var aliases []corev1.HostAlias
	for _, host := range d.hosts {
		ips, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return nil, err
		}
		// A host that is configured by its address needs no entry.
		if ips[0] != host {
			aliases = append(aliases, corev1.HostAlias{IP: ips[0], Hostnames: []string{host}})
		}
	}
	return aliases, nil
}

// Ask is the approval of an agent whose harness asks a person before it
// acts.
const Ask = "ask"

// HasHarness reports whether turns can run on the harness of that name.
func (d *Driver) HasHarness(name string) bool {
	_, found := d.config.Harnesses[name]
	return found
}

// AgentResources returns the resources that were asked for an agent, as
// they are kept with the agent. It returns nothing if none were asked for:
// the turns of such an agent get the default of its harness, as that is when
// each turn starts. An error tells what is wrong with what was asked for.
func AgentResources(asked json.RawMessage) (json.RawMessage, error) {
	if len(asked) == 0 || string(asked) == "null" {
		return nil, nil
	}
	var resources corev1.ResourceRequirements
	decoder := json.NewDecoder(bytes.NewReader(asked))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&resources); err != nil {
		return nil, errors.New(`resources must be requests and limits of cpu and memory, ` +
			`such as {"requests": {"cpu": "500m"}, "limits": {"memory": "2Gi"}}`)
	}
	if err := checkResources(resources); err != nil {
		return nil, err
	}
	if len(resources.Requests) == 0 && len(resources.Limits) == 0 {
		return nil, nil
	}
	return json.Marshal(resources)
}

// checkResources tells what keeps resources from being those of a turn pod.
func checkResources(resources corev1.ResourceRequirements) error {
	if len(resources.Claims) > 0 {
		return errors.New("resources must be requests and limits of cpu and memory")
	}
	for _, list := range []corev1.ResourceList{resources.Requests, resources.Limits} {
		for name, quantity := range list {
			if name != corev1.ResourceCPU && name != corev1.ResourceMemory {
				return fmt.Errorf("resources must be cpu and memory, not %s", name)
			}
			if quantity.Sign() < 0 {
				return fmt.Errorf("the %s in resources must not be below zero", name)
			}
		}
	}
	for name, limit := range resources.Limits {
		if request, found := resources.Requests[name]; found && request.Cmp(limit) > 0 {
			return fmt.Errorf("the request for %s in resources is above its limit", name)
		}
	}
	return nil
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

// Accept queues a message as the session's next turn, if the queue has
// room. It returns the errors of store.CreateTurn.
func (d *Driver) Accept(ctx context.Context, sessionID uuid.UUID, text string) (store.Turn, error) {
	turn, waiting, err := d.store.CreateTurn(ctx, sessionID, text, d.config.MaxQueuedTurns, d.config.MaxConcurrentTurns)
	switch {
	case err == nil:
		d.metrics.Accepted(waiting)
	case errors.Is(err, store.ErrQueueFull):
		d.metrics.Refused("queue_full")
	case errors.Is(err, store.ErrSessionBusy):
		d.metrics.Refused("session_busy")
	}
	return turn, err
}

// QueueMoved tells the driver that a turn left the queue without running,
// so that it tells the turns behind it their new positions.
func (d *Driver) QueueMoved() {
	d.moved.Store(true)
	d.Wake()
}

// dispatch runs the queued turns that fit under the limit, oldest first.
func (d *Driver) dispatch(ctx context.Context) {
	expired := d.expire(ctx)
	dispatches, err := d.store.DispatchTurns(ctx, d.config.MaxConcurrentTurns)
	if err != nil {
		slog.ErrorContext(ctx, "dispatch turns", "error", err)
		return
	}
	var hosts []corev1.HostAlias
	if len(dispatches) > 0 {
		// Without the addresses no turn is launched. The turns then fail as
		// turns that lost their Job.
		if hosts, err = d.hostAliases(ctx); err != nil {
			slog.ErrorContext(ctx, "look up the addresses for the turn pods", "error", err)
			return
		}
	}
	for _, dispatch := range dispatches {
		d.metrics.Dispatched(dispatch.Harness, dispatch.Waited)
		if err := d.launch(ctx, dispatch, hosts); err != nil {
			slog.ErrorContext(ctx, "launch turn", "turn", dispatch.TurnID, "error", err)
		}
	}
	if d.moved.Swap(false) || expired || len(dispatches) > 0 {
		d.announcePositions(ctx)
	}
}

// expire ends the turns that have waited to run for too long. It reports
// whether there were any.
func (d *Driver) expire(ctx context.Context) bool {
	if d.config.MaxQueueWait == 0 {
		return false
	}
	turns, err := d.store.ExpireTurns(ctx, d.config.MaxQueueWait)
	if err != nil {
		slog.ErrorContext(ctx, "expire turns", "error", err)
		return false
	}
	for _, turn := range turns {
		d.Ended(ctx, turn)
	}
	return len(turns) > 0
}

// announcePositions tells the turns that still wait their new positions in
// the queue, after the turns ahead of them were dispatched.
func (d *Driver) announcePositions(ctx context.Context) {
	err := d.store.AnnounceQueue(ctx, func(queued []uuid.UUID) error {
		for i, turnID := range queued {
			if err := d.events.AppendQueued(ctx, turnID, i+1); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "tell the queued turns their positions", "error", err)
	}
}

// launch creates the Job that runs a dispatched turn.
func (d *Driver) launch(ctx context.Context, dispatch store.Dispatch, hosts []corev1.HostAlias) error {
	harness, found := d.config.Harnesses[dispatch.Harness]
	if !found {
		return fmt.Errorf("harness %q is not configured", dispatch.Harness)
	}
	// An agent without resources of its own gets those of its harness, as
	// they are now.
	resources := harness.Resources
	if len(dispatch.Resources) > 0 {
		resources = corev1.ResourceRequirements{}
		if err := json.Unmarshal(dispatch.Resources, &resources); err != nil {
			return fmt.Errorf("the resources of the agent: %w", err)
		}
	}
	job := d.job(dispatch, harness, resources, hosts)
	_, err := d.client.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
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
	reconciling := time.NewTicker(resync)
	defer reconciling.Stop()
	for {
		d.dispatch(ctx)
		select {
		case <-d.wake:
		case <-ticker.C:
		case <-reconciling.C:
			d.reconcile(ctx)
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
	var failure store.Failure
	if !succeeded {
		failure = d.failure(ctx, job)
	}
	if d.end(ctx, turnID, succeeded, failure) {
		d.deleteJob(ctx, job.Name)
	}
}

// failure tells why the Job of a turn failed, from what Kubernetes says of
// the Job and its pod and from what the runner left with the pod.
func (d *Driver) failure(ctx context.Context, job *batchv1.Job) store.Failure {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Reason == batchv1.JobReasonDeadlineExceeded {
			return store.Failure{
				Reason:  store.FailureTimedOut,
				Message: fmt.Sprintf("the turn ran for longer than %s", d.config.MaxTurn),
			}
		}
	}
	selector := metav1.ListOptions{LabelSelector: turnLabel + "=" + job.Labels[turnLabel]}
	pods, err := d.client.CoreV1().Pods(namespace).List(ctx, selector)
	if err != nil {
		slog.ErrorContext(ctx, "read the pod of a failed turn", "job", job.Name, "error", err)
		return store.Failure{Reason: store.FailureRunner}
	}
	if len(pods.Items) == 0 {
		return store.Failure{Reason: store.FailurePodLost, Message: "the pod of the turn was deleted"}
	}
	pod := pods.Items[0]
	if pod.Status.Reason == "Evicted" {
		return store.Failure{Reason: store.FailurePodLost, Message: pod.Status.Message}
	}
	// The runner makes the home in one container and runs the agent in the
	// next. The one that failed tells why the pod did.
	for _, container := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		terminated := container.State.Terminated
		if terminated == nil || terminated.ExitCode == 0 && terminated.Signal == 0 {
			continue
		}
		// What the runner left is the most that is known.
		var left store.Failure
		if json.Unmarshal([]byte(terminated.Message), &left) == nil {
			switch left.Reason {
			case store.FailureHarness, store.FailureResume, store.FailureSupervisorLost, store.FailureRunner:
				return left
			}
		}
		switch {
		case terminated.Reason == "OOMKilled":
			return store.Failure{Reason: store.FailureOutOfMemory, Message: "the pod used more memory than it may"}
		case terminated.Signal != 0 || terminated.ExitCode > 128:
			return store.Failure{Reason: store.FailurePodLost, Message: "the pod was stopped"}
		}
		return store.Failure{
			Reason:  store.FailureRunner,
			Message: fmt.Sprintf("the runner ended with exit code %d", terminated.ExitCode),
		}
	}
	return store.Failure{Reason: store.FailureRunner, Message: "the pod failed before the agent started"}
}

// reconcile ends the turns that have lost their Job and deletes the Jobs
// whose turn has ended. Neither comes about while everything works: a Job
// was not created, or was deleted by someone else, or a turn was ended
// without its Job. Left alone, such a turn would keep its session busy and
// count against the limit for good.
func (d *Driver) reconcile(ctx context.Context) {
	// The Jobs are read before the turns. The other way around, the Job of a
	// turn that is dispatched in between would look like it has no turn.
	selector := metav1.ListOptions{LabelSelector: turnLabel}
	jobs, err := d.client.BatchV1().Jobs(namespace).List(ctx, selector)
	if err != nil {
		slog.ErrorContext(ctx, "list jobs", "error", err)
		return
	}
	pods, err := d.client.CoreV1().Pods(namespace).List(ctx, selector)
	if err != nil {
		slog.ErrorContext(ctx, "list pods", "error", err)
		return
	}
	turns, err := d.store.DispatchedTurns(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "read dispatched turns", "error", err)
		return
	}

	// A turn whose Job is gone may still have a pod that is shutting down.
	inCluster, hasJob := map[string]bool{}, map[string]bool{}
	for _, job := range jobs.Items {
		inCluster[job.Labels[turnLabel]] = true
		hasJob[job.Labels[turnLabel]] = true
	}
	podOf := map[string]*corev1.Pod{}
	for i, pod := range pods.Items {
		inCluster[pod.Labels[turnLabel]] = true
		podOf[pod.Labels[turnLabel]] = &pods.Items[i]
	}
	dispatched := map[string]bool{}
	for _, turn := range turns {
		dispatched[turn.ID.String()] = true
		if !inCluster[turn.ID.String()] && turn.Age > lostAfter {
			slog.WarnContext(ctx, "turn has no job", "turn", turn.ID)
			d.end(ctx, turn.ID, false, store.Failure{
				Reason: store.FailurePodLost, Message: "the Job and the pod of the turn are gone",
			})
		}
		// The pod has not started: it cannot be scheduled, or an image
		// cannot be pulled. The turn is given up, and ends when the pod is
		// gone.
		if d.config.MaxStartup > 0 && !turn.Taken && turn.Age > d.config.MaxStartup && hasJob[turn.ID.String()] {
			failure := store.Failure{Reason: store.FailureStartup, Message: holdup(podOf[turn.ID.String()])}
			givenUp, err := d.store.GiveUpTurn(ctx, turn.ID, failure)
			if err != nil {
				slog.ErrorContext(ctx, "give up turn", "turn", turn.ID, "error", err)
			} else if givenUp {
				slog.WarnContext(ctx, "the pod of the turn did not start", "turn", turn.ID, "holdup", failure.Message)
				d.deleteJob(ctx, jobName(turn.ID))
			}
		}
		// The harness did not stop when it was asked to. Without its Job
		// the pod is stopped, and the turn ends when the pod is gone.
		if hasJob[turn.ID.String()] && turn.Cancelling > cancelGrace {
			slog.WarnContext(ctx, "turn did not stop when cancelled", "turn", turn.ID)
			d.deleteJob(ctx, jobName(turn.ID))
		}
	}
	for _, job := range jobs.Items {
		if dispatched[job.Labels[turnLabel]] {
			continue
		}
		// The Job of a turn that has ended is on its way out: the turn ended
		// after the Jobs were read, or its Job could not be deleted then.
		if !d.turnEnded(ctx, &job) {
			slog.WarnContext(ctx, "job has no running turn", "job", job.Name)
		}
		d.deleteJob(ctx, job.Name)
	}
}

// turnEnded reports whether the turn of the Job has ended. It has not if
// there is no such turn.
func (d *Driver) turnEnded(ctx context.Context, job *batchv1.Job) bool {
	turnID, err := uuid.Parse(job.Labels[turnLabel])
	if err != nil {
		return false
	}
	turn, err := d.store.Turn(ctx, turnID)
	return err == nil && turn.EndedAt != nil
}

// end ends a turn whose pod has stopped, and reports whether the turn is
// now ended.
func (d *Driver) end(ctx context.Context, turnID uuid.UUID, succeeded bool, failure store.Failure) bool {
	turn, changed, err := d.store.EndTurn(ctx, turnID, succeeded, failure, func(ctx context.Context) ([]json.RawMessage, error) {
		return d.events.Items(ctx, turnID)
	})
	if err != nil {
		slog.ErrorContext(ctx, "end turn", "turn", turnID, "error", err)
		return false
	}
	if changed {
		d.Ended(ctx, turn)
		// The turn no longer counts against the limit.
		d.Wake()
	}
	return true
}

// Ended tells of a turn that has ended: its clients, with the last event of
// the turn, and the metrics.
func (d *Driver) Ended(ctx context.Context, turn store.Turn) {
	slog.InfoContext(ctx, "turn ended", "turn", turn.ID, "state", turn.State)
	// A client that misses this event gets it from the turn's record.
	if err := d.events.AppendLast(ctx, turn); err != nil {
		slog.ErrorContext(ctx, "publish turn event", "turn", turn.ID, "error", err)
	}
	harness, err := d.store.TurnHarness(ctx, turn.ID)
	if err != nil {
		slog.ErrorContext(ctx, "read the harness of the turn", "turn", turn.ID, "error", err)
		return
	}
	d.metrics.Ended(harness, turn)
}

// holdup tells what keeps a pod from starting, as far as Kubernetes says.
func holdup(pod *corev1.Pod) string {
	if pod == nil {
		return "the pod was not created"
	}
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses} {
		for _, container := range statuses {
			if waiting := container.State.Waiting; waiting != nil && waiting.Message != "" {
				return waiting.Reason + ": " + waiting.Message
			}
		}
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
			return condition.Reason + ": " + condition.Message
		}
	}
	return "the runner did not connect"
}

func jobName(turnID uuid.UUID) string {
	return "turn-" + turnID.String()
}

func (d *Driver) deleteJob(ctx context.Context, name string) {
	background := metav1.DeletePropagationBackground
	err := d.client.BatchV1().Jobs(namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &background})
	if err != nil && !apierrors.IsNotFound(err) {
		slog.ErrorContext(ctx, "delete job", "job", name, "error", err)
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
// images do not have to contain it. Both containers get the resources: a pod
// asks for what the larger of the two asks for, not for the sum.
func (d *Driver) job(dispatch store.Dispatch, harness Harness, resources corev1.ResourceRequirements, hosts []corev1.HostAlias) *batchv1.Job {
	home := fmt.Sprintf("projects/%s/agents/%s/sessions/%s/home", dispatch.ProjectID, dispatch.AgentID, dispatch.SessionID)
	// The password of the proxy is the turn token, by which the proxy knows
	// the turn of a request.
	proxy := *d.proxy
	proxy.User = url.UserPassword("turn", dispatch.Token)
	labels := map[string]string{turnLabel: dispatch.TurnID.String()}
	restricted := &corev1.SecurityContext{
		AllowPrivilegeEscalation: new(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
	// The harness reaches its model at the Egress Proxy, which passes a
	// request on to the endpoint of the provider that the turn's agent names.
	// The proxy serves the endpoint under the path of the provider's URL,
	// which was checked when the provider was given it.
	llm := *d.llm
	if endpoint, err := url.Parse(dispatch.ProviderURL); err == nil {
		llm.Path = strings.TrimSuffix(endpoint.Path, "/")
	}
	env := []corev1.EnvVar{
		{Name: "HOME", Value: "/home"},
		{Name: "AVIARY_GATEWAY_URL", Value: d.config.GatewayURL},
		{Name: "AVIARY_TURN_TOKEN", Value: dispatch.Token},
		// The pod holds no key of the model endpoint. The harness sends the
		// turn token as its API key, and the Egress Proxy puts the key in
		// its place.
		{Name: "AVIARY_LLM_BASE_URL", Value: llm.String()},
		{Name: "AVIARY_LLM_API_KEY", Value: dispatch.Token},
		{Name: "AVIARY_LLM_MODEL", Value: dispatch.Model},
		{Name: "AVIARY_MAX_TOOL_OUTPUT_BYTES", Value: strconv.Itoa(d.config.MaxToolOutputBytes)},
		{Name: "AVIARY_MAX_RECONNECT_SECONDS", Value: strconv.Itoa(int(d.config.MaxReconnect.Seconds()))},
		// Everything else that the programs in the pod fetch goes through
		// the proxy. Some programs read the names in capitals and some the
		// others.
		{Name: "HTTP_PROXY", Value: proxy.String()},
		{Name: "HTTPS_PROXY", Value: proxy.String()},
		{Name: "NO_PROXY", Value: d.noProxy},
		{Name: "http_proxy", Value: proxy.String()},
		{Name: "https_proxy", Value: proxy.String()},
		{Name: "no_proxy", Value: d.noProxy},
	}
	// The harness image and the runner read this: the image starts its
	// harness so that it asks, and the runner has a person answer.
	if dispatch.Approval != "" {
		env = append(env, corev1.EnvVar{Name: "AVIARY_APPROVAL", Value: dispatch.Approval})
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName(dispatch.TurnID), Labels: labels},
		Spec: batchv1.JobSpec{
			// A turn is not repeated: the agent may already have acted.
			BackoffLimit:          new(int32(0)),
			ActiveDeadlineSeconds: new(int64(d.config.MaxTurn.Seconds())),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					// The pod has no DNS: the network policy lets no lookup
					// out, because a lookup can carry data out of the
					// cluster. The pod finds the Supervisor and the Egress
					// Proxy in its hosts file. A program that looks another
					// name up gets its failure in a second or two, with
					// these options, and not in twenty.
					HostAliases: hosts,
					DNSConfig: &corev1.PodDNSConfig{Options: []corev1.PodDNSConfigOption{
						{Name: "timeout", Value: new("1")},
						{Name: "attempts", Value: new("1")},
						{Name: "ndots", Value: new("1")},
					}},
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
						Resources:       resources,
						SecurityContext: restricted,
						VolumeMounts:    []corev1.VolumeMount{{Name: "store", MountPath: "/store"}},
					}},
					Containers: []corev1.Container{{
						Name:            agentContainer,
						Image:           harness.Image,
						Command:         []string{"/aviary/bin/aviary-runner"},
						WorkingDir:      "/home/workspace",
						Resources:       resources,
						Env:             env,
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
