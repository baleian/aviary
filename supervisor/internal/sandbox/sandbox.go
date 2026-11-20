// Package sandbox has the sessions run in sandboxes of the cluster: a
// sandbox is a pod that its controller keeps, with a volume of the session
// that outlives the pod. A session that sleeps has no pod, and its volume.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	agents "sigs.k8s.io/agent-sandbox/api/v1beta1"
	"sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned"
	typed "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/typed/api/v1beta1"
)

const (
	// RunnerPort is where the runner in a sandbox listens.
	RunnerPort = 8700
	// SessionLabel names the session of a sandbox, and of its pod.
	SessionLabel = "aviary.io/session"
	// turnAnnotation names the last turn that a sandbox was kept for.
	turnAnnotation = "aviary.io/turn"
	// home is the volume of the session, where the harness keeps what it
	// remembers and the agent its work.
	home = "home"
	// agentUser is the user that the agent runs as, which the volume is
	// given to.
	agentUser = 10000
)

// session is what a sandbox runs, which the image of a harness has.
var session = []string{"aviary-session"}

var (
	// ErrNotFound is the error for what is asked for and is not there: a
	// sandbox that is gone, a turn that a runner does not have.
	ErrNotFound = errors.New("not found")
	// ErrChanged is the error for a change to a sandbox as it was seen, which
	// has changed since.
	ErrChanged = errors.New("changed since it was seen")
)

// Refusal is an answer of a runner that is no success.
type Refusal struct {
	// Who answered, and what it was asked.
	Who, Asked string
	// Status is the HTTP status of the answer, and Said what it said.
	Status int
	Said   string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("%s answered %d %s to %s: %s", r.Who, r.Status, http.StatusText(r.Status), r.Asked, r.Said)
}

// Is makes an answer that something is not there an ErrNotFound.
func (r *Refusal) Is(target error) bool {
	return target == ErrNotFound && r.Status == http.StatusNotFound
}

// State is what a sandbox does.
type State string

const (
	// Running: its runner can be reached.
	Running State = "Running"
	// Asleep: it was put to sleep. It has no pod, or its pod is going, and
	// it keeps its volume.
	Asleep State = "Asleep"
	// Starting: it was made or woken, and does not run yet.
	Starting State = "Starting"
	// Lost: it is being deleted, or it has expired.
	Lost State = "Lost"
)

// Sandbox is a sandbox as it is seen.
type Sandbox struct {
	Name string
	// Session is the session whose sandbox it is, as its label says.
	Session string
	State   State
	// Message tells why it does not run, if it does not.
	Message string
	Created time.Time
	// Address is where its runner is reached, while it runs.
	Address string
	// Version is the version of the sandbox as it was seen.
	Version string
}

// Harness is a harness as the installation sets it up.
type Harness struct {
	// Image is the image of the harness, with the runner in it.
	Image string `json:"image"`
	// Resources are what the pod of a sandbox of its sessions asks for and
	// may use at most.
	Resources corev1.ResourceRequirements `json:"resources"`
}

// Request is a sandbox to make, for a session.
type Request struct {
	Session uuid.UUID
	// Labels tell whose the sandbox is, besides its session.
	Labels  map[string]string
	Harness Harness
	// Kept is for how long the sandbox is kept from now, after which it is
	// deleted with its volume. Zero is until it is deleted.
	Kept time.Duration
}

// Settings are what every sandbox of the installation is made with.
type Settings struct {
	// Namespace is where the sandboxes are.
	Namespace string
	// Volume is the size of the volume of a session, and StorageClass the
	// class that it is made with: the default class of the cluster if empty.
	Volume       resource.Quantity
	StorageClass string
	// MaxToolOutputBytes is the longest text of a tool call that the runner
	// tells as it is. Zero is no limit.
	MaxToolOutputBytes int
}

// Client has the sandboxes made.
type Client struct {
	sandboxes typed.SandboxInterface
	settings  Settings
}

// New returns a client of the sandboxes of the cluster that config reaches.
func New(config *rest.Config, settings Settings) (*Client, error) {
	clients, err := versioned.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return &Client{sandboxes: clients.AgentsV1beta1().Sandboxes(settings.Namespace), settings: settings}, nil
}

// Name is the name of the sandbox of the session. A session has one.
func Name(session uuid.UUID) string {
	return "session-" + session.String()
}

// Create makes the sandbox of the session. A sandbox of the session that
// there is already is no error.
func (c *Client) Create(ctx context.Context, request Request) error {
	labels := map[string]string{SessionLabel: request.Session.String()}
	for key, value := range request.Labels {
		labels[key] = value
	}
	claim := corev1.PersistentVolumeClaimSpec{
		AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: c.settings.Volume}},
	}
	if c.settings.StorageClass != "" {
		claim.StorageClassName = &c.settings.StorageClass
	}
	made := &agents.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: Name(request.Session), Labels: labels},
		Spec: agents.SandboxSpec{
			SandboxBlueprint: agents.SandboxBlueprint{
				PodTemplate: agents.PodTemplate{
					ObjectMeta: agents.PodMetadata{Labels: labels},
					Spec:       c.pod(request.Harness),
				},
				VolumeClaimTemplates: []agents.PersistentVolumeClaimTemplate{{
					EmbeddedObjectMetadata: agents.EmbeddedObjectMetadata{Name: home},
					Spec:                   claim,
				}},
				// A runner that is gone is started again, with the volume.
				FailedPodPolicy: agents.FailedPodPolicyRecreate,
			},
		},
	}
	// A sandbox that expires is deleted, with its volume.
	made.Spec.ShutdownPolicy = new(agents.ShutdownPolicyDelete)
	if request.Kept > 0 {
		made.Spec.ShutdownTime = new(metav1.NewTime(time.Now().Add(request.Kept)))
	}
	_, err := c.sandboxes.Create(ctx, made, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// pod is the pod of a sandbox of the harness.
func (c *Client) pod(harness Harness) corev1.PodSpec {
	return corev1.PodSpec{
		// The pod has nothing to ask the cluster.
		AutomountServiceAccountToken: new(false),
		EnableServiceLinks:           new(false),
		SecurityContext: &corev1.PodSecurityContext{
			// The agent can write to the volume, which is made for root.
			FSGroup: new(int64(agentUser)),
		},
		Containers: []corev1.Container{{
			Name:      "harness",
			Image:     harness.Image,
			Command:   session,
			Resources: harness.Resources,
			Env: []corev1.EnvVar{
				{Name: "AVIARY_MAX_TOOL_OUTPUT_BYTES", Value: strconv.Itoa(c.settings.MaxToolOutputBytes)},
			},
			Ports: []corev1.ContainerPort{{Name: "runner", ContainerPort: RunnerPort}},
			// The sandbox runs once its runner listens.
			ReadinessProbe: &corev1.Probe{
				ProbeHandler:  corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(RunnerPort)}},
				PeriodSeconds: 1,
			},
			VolumeMounts: []corev1.VolumeMount{{Name: home, MountPath: "/home"}},
		}},
	}
}

// Sandbox returns the sandbox with the name, or ErrNotFound.
func (c *Client) Sandbox(ctx context.Context, name string) (Sandbox, error) {
	found, err := c.sandboxes.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Sandbox{}, fmt.Errorf("the sandbox %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return Sandbox{}, err
	}
	return seen(found), nil
}

// Sandboxes returns the sandboxes of the sessions.
func (c *Client) Sandboxes(ctx context.Context) ([]Sandbox, error) {
	found, err := c.sandboxes.List(ctx, metav1.ListOptions{LabelSelector: SessionLabel})
	if err != nil {
		return nil, err
	}
	all := make([]Sandbox, 0, len(found.Items))
	for i := range found.Items {
		all = append(all, seen(&found.Items[i]))
	}
	return all, nil
}

// seen is the sandbox as its object tells of it.
func seen(found *agents.Sandbox) Sandbox {
	sandbox := Sandbox{
		Name: found.Name, Session: found.Labels[SessionLabel], Created: found.CreationTimestamp.Time, Version: found.ResourceVersion,
	}
	ready := meta.FindStatusCondition(found.Status.Conditions, string(agents.SandboxConditionReady))
	// A status of an older spec does not tell of this one: of a sandbox that
	// was woken just now, it can still tell of the pod that went.
	current := ready != nil && ready.ObservedGeneration == found.Generation
	switch {
	case found.DeletionTimestamp != nil:
		sandbox.State, sandbox.Message = Lost, "it is being deleted"
	case ready != nil && ready.Reason == agents.SandboxReasonExpired:
		sandbox.State, sandbox.Message = Lost, "it has expired"
	case found.Spec.OperatingMode == agents.SandboxOperatingModeSuspended:
		// What ran in it stops, also while its pod is still going.
		sandbox.State = Asleep
	case current && ready.Status == metav1.ConditionTrue && len(found.Status.PodIPs) > 0:
		sandbox.State = Running
		sandbox.Address = "http://" + net.JoinHostPort(found.Status.PodIPs[0], strconv.Itoa(RunnerPort))
	default:
		sandbox.State = Starting
		if current {
			sandbox.Message = ready.Reason + ": " + ready.Message
		}
	}
	return sandbox
}

// Delete deletes the sandbox, with its volume. A sandbox that is gone is no
// error.
func (c *Client) Delete(ctx context.Context, name string) error {
	err := c.sandboxes.Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Suspend puts the sandbox to sleep: its pod is deleted, and its volume
// kept. With the version of the sandbox as it was seen, it does so only if
// the sandbox has not changed since, and returns ErrChanged if it has.
func (c *Client) Suspend(ctx context.Context, name, version string) error {
	patch := map[string]any{"spec": map[string]any{"operatingMode": agents.SandboxOperatingModeSuspended}}
	if version != "" {
		// The cluster makes no change to an object of another version.
		patch["metadata"] = map[string]any{"resourceVersion": version}
	}
	_, err := c.patch(ctx, name, types.MergePatchType, patch)
	return err
}

// Resume wakes the sandbox: a pod is made for it again, with its volume. The
// pod is of the harness as the installation has it now.
func (c *Client) Resume(ctx context.Context, name string, harness Harness) error {
	// The pod is replaced as a whole: what an older pod had and this one
	// does not have does not stay.
	_, err := c.patch(ctx, name, types.JSONPatchType, []map[string]any{
		{"op": "add", "path": "/spec/podTemplate/spec", "value": c.pod(harness)},
		{"op": "add", "path": "/spec/operatingMode", "value": agents.SandboxOperatingModeRunning},
	})
	return err
}

// Keep has the sandbox kept for the turn: it is marked with the turn, and
// kept until then, after which it is deleted with its volume. The zero time
// is until it is deleted. It returns the sandbox as it is then. A sandbox
// that is marked has changed for one who would put it to sleep as it saw it
// before.
func (c *Client) Keep(ctx context.Context, name string, turn uuid.UUID, until time.Time) (Sandbox, error) {
	var shutdown any
	if !until.IsZero() {
		shutdown = until.UTC().Format(time.RFC3339)
	}
	kept, err := c.patch(ctx, name, types.MergePatchType, map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{turnAnnotation: turn.String()}},
		"spec":     map[string]any{"shutdownTime": shutdown},
	})
	if err != nil {
		return Sandbox{}, err
	}
	return seen(kept), nil
}

// patch changes the sandbox, and returns it as it is then.
func (c *Client) patch(ctx context.Context, name string, kind types.PatchType, patch any) (*agents.Sandbox, error) {
	body, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}
	patched, err := c.sandboxes.Patch(ctx, name, kind, body, metav1.PatchOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return nil, fmt.Errorf("the sandbox %s: %w", name, ErrNotFound)
	case apierrors.IsConflict(err):
		return nil, fmt.Errorf("the sandbox %s: %w", name, ErrChanged)
	}
	return patched, err
}
