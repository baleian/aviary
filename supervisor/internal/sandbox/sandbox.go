// Package sandbox manages the agent-sandbox Sandbox of each session: a pod
// that runs the harness, and a volume that outlives the pod while the
// session sleeps.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
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
	// RunnerPort is the port the runner listens on.
	RunnerPort = 8700
	// SessionLabel holds the session ID on a Sandbox and its pod.
	SessionLabel = "aviary.io/session"
	// turnAnnotation holds the last turn a Sandbox was kept for.
	turnAnnotation = "aviary.io/turn"
	home           = "home"
	// agentUser is the UID and GID the harness runs as.
	agentUser = 10000
)

// session is the entrypoint that every harness image provides.
var session = []string{"aviary-session"}

var (
	// ErrNotFound is returned for a Sandbox, or a turn of a runner, that does
	// not exist.
	ErrNotFound = errors.New("not found")
	// ErrChanged is returned when a Sandbox has changed since it was read.
	ErrChanged = errors.New("changed since it was read")
)

// Refusal is an error response from a runner.
type Refusal struct {
	Who, Asked string
	Status     int
	Said       string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("%s returned %d %s for %s: %s", r.Who, r.Status, http.StatusText(r.Status), r.Asked, r.Said)
}

// Is reports a 404 response as ErrNotFound.
func (r *Refusal) Is(target error) bool {
	return target == ErrNotFound && r.Status == http.StatusNotFound
}

// State is the observed state of a Sandbox.
type State string

const (
	// Running means the runner is reachable.
	Running State = "Running"
	// Asleep means the Sandbox is suspended: its pod is gone or terminating,
	// and its volume is kept.
	Asleep State = "Asleep"
	// Starting means the Sandbox was created or resumed and is not ready yet.
	Starting State = "Starting"
	// Lost means the Sandbox is being deleted or has expired.
	Lost State = "Lost"
)

// Sandbox is a Sandbox as read from the cluster.
type Sandbox struct {
	Name    string
	Session string
	State   State
	// Message explains why the Sandbox is not running, if it is not.
	Message string
	Created time.Time
	// Address is the URL of the runner while the Sandbox is running.
	Address string
	// Version is the resourceVersion the Sandbox was read at.
	Version string
}

// Harness is a harness as the installation configures it.
type Harness struct {
	// Image contains the harness and the runner.
	Image     string                      `json:"image"`
	Resources corev1.ResourceRequirements `json:"resources"`
}

// Pod is what the pod of a Sandbox is made from.
type Pod struct {
	Harness Harness
	// Token identifies the session to the Egress Proxy. The pod sends it with
	// every outbound request.
	Token string
}

// Request describes the Sandbox to create for a session.
type Request struct {
	Session uuid.UUID
	// Labels are added to the session label.
	Labels map[string]string
	Pod    Pod
	// Kept is how long the Sandbox lives before it is deleted with its
	// volume. Zero keeps it until it is deleted.
	Kept time.Duration
}

// Settings apply to every Sandbox of the installation.
type Settings struct {
	Namespace string
	// Volume is the size of the session volume. An empty StorageClass uses
	// the default class of the cluster.
	Volume       resource.Quantity
	StorageClass string
	// MaxToolOutputBytes caps the text of a tool call that the runner
	// reports. Zero means no limit.
	MaxToolOutputBytes int
	// Proxy is the forward proxy of the Egress Proxy, and Models its
	// endpoint for model requests.
	Proxy, Models *url.URL
}

// Client manages the Sandboxes of the sessions.
type Client struct {
	sandboxes typed.SandboxInterface
	settings  Settings
}

// New returns a Client for the cluster that config connects to.
func New(config *rest.Config, settings Settings) (*Client, error) {
	clients, err := versioned.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return &Client{sandboxes: clients.AgentsV1beta1().Sandboxes(settings.Namespace), settings: settings}, nil
}

// Name returns the name of the Sandbox of the session.
func Name(session uuid.UUID) string {
	return "session-" + session.String()
}

// Create creates the Sandbox of the session. It is not an error if the
// Sandbox already exists.
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
	pod, err := c.pod(ctx, request.Pod)
	if err != nil {
		return err
	}
	made := &agents.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: Name(request.Session), Labels: labels},
		Spec: agents.SandboxSpec{
			SandboxBlueprint: agents.SandboxBlueprint{
				PodTemplate: agents.PodTemplate{
					ObjectMeta: agents.PodMetadata{Labels: labels},
					Spec:       pod,
				},
				VolumeClaimTemplates: []agents.PersistentVolumeClaimTemplate{{
					EmbeddedObjectMetadata: agents.EmbeddedObjectMetadata{Name: home},
					Spec:                   claim,
				}},
				FailedPodPolicy: agents.FailedPodPolicyRecreate,
			},
		},
	}
	made.Spec.ShutdownPolicy = new(agents.ShutdownPolicyDelete)
	if request.Kept > 0 {
		made.Spec.ShutdownTime = new(metav1.NewTime(time.Now().Add(request.Kept)))
	}
	_, err = c.sandboxes.Create(ctx, made, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (c *Client) pod(ctx context.Context, pod Pod) (corev1.PodSpec, error) {
	aliases, err := c.aliases(ctx)
	if err != nil {
		return corev1.PodSpec{}, fmt.Errorf("look up the Egress Proxy: %w", err)
	}
	proxy := *c.settings.Proxy
	proxy.User = url.UserPassword("session", pod.Token)
	// Loopback traffic and model requests, which go to a listener of the
	// Egress Proxy itself, bypass the forward proxy.
	direct := strings.Join([]string{"localhost", "127.0.0.1", "::1", c.settings.Models.Hostname()}, ",")
	env := []corev1.EnvVar{
		{Name: "AVIARY_MAX_TOOL_OUTPUT_BYTES", Value: strconv.Itoa(c.settings.MaxToolOutputBytes)},
		// The pod holds no API key of a model provider. The harness sends the
		// session token instead, and the Egress Proxy replaces it with the key.
		{Name: "AVIARY_LLM_API_KEY", Value: pod.Token},
	}
	// Some programs read only the uppercase names, others only the lowercase.
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		env = append(env, corev1.EnvVar{Name: name, Value: proxy.String()})
	}
	env = append(env, corev1.EnvVar{Name: "NO_PROXY", Value: direct}, corev1.EnvVar{Name: "no_proxy", Value: direct})
	return corev1.PodSpec{
		// The network policy blocks DNS, since lookups can carry data out of
		// the cluster, so the pod finds the Egress Proxy in its hosts file.
		// The options make any other lookup fail fast.
		HostAliases: aliases,
		DNSConfig: &corev1.PodDNSConfig{Options: []corev1.PodDNSConfigOption{
			{Name: "timeout", Value: new("1")},
			{Name: "attempts", Value: new("1")},
			{Name: "ndots", Value: new("1")},
		}},
		AutomountServiceAccountToken: new(false),
		EnableServiceLinks:           new(false),
		// Meets the restricted Pod Security Standard.
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: new(true),
			RunAsUser:    new(int64(agentUser)),
			RunAsGroup:   new(int64(agentUser)),
			// Makes the volume writable by the agent, without changing the
			// ownership of every file at each start.
			FSGroup:             new(int64(agentUser)),
			FSGroupChangePolicy: new(corev1.FSGroupChangeOnRootMismatch),
			SeccompProfile:      &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		Containers: []corev1.Container{{
			Name:      "harness",
			Image:     pod.Harness.Image,
			Command:   session,
			Resources: pod.Harness.Resources,
			Env:       env,
			SecurityContext: &corev1.SecurityContext{
				AllowPrivilegeEscalation: new(false),
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
			Ports: []corev1.ContainerPort{{Name: "runner", ContainerPort: RunnerPort}},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler:  corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(RunnerPort)}},
				PeriodSeconds: 1,
			},
			VolumeMounts: []corev1.VolumeMount{{Name: home, MountPath: "/home"}},
		}},
	}, nil
}

// aliases resolves the hosts of the Egress Proxy for the hosts file of a pod.
func (c *Client) aliases(ctx context.Context) ([]corev1.HostAlias, error) {
	var aliases []corev1.HostAlias
	for _, host := range slices.Compact([]string{c.settings.Proxy.Hostname(), c.settings.Models.Hostname()}) {
		ips, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return nil, err
		}
		// A host given as an IP address needs no entry.
		if ips[0] != host {
			aliases = append(aliases, corev1.HostAlias{IP: ips[0], Hostnames: []string{host}})
		}
	}
	return aliases, nil
}

// Sandbox returns the named Sandbox, or ErrNotFound.
func (c *Client) Sandbox(ctx context.Context, name string) (Sandbox, error) {
	found, err := c.sandboxes.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Sandbox{}, fmt.Errorf("sandbox %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return Sandbox{}, err
	}
	return seen(found), nil
}

// Sandboxes returns the Sandboxes of all sessions.
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

func seen(found *agents.Sandbox) Sandbox {
	sandbox := Sandbox{
		Name: found.Name, Session: found.Labels[SessionLabel], Created: found.CreationTimestamp.Time, Version: found.ResourceVersion,
	}
	ready := meta.FindStatusCondition(found.Status.Conditions, string(agents.SandboxConditionReady))
	// A condition observed for an older generation can still describe the
	// previous pod, right after a resume.
	current := ready != nil && ready.ObservedGeneration == found.Generation
	switch {
	case found.DeletionTimestamp != nil:
		sandbox.State, sandbox.Message = Lost, "it is being deleted"
	case ready != nil && ready.Reason == agents.SandboxReasonExpired:
		sandbox.State, sandbox.Message = Lost, "it has expired"
	case found.Spec.OperatingMode == agents.SandboxOperatingModeSuspended:
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

// Delete deletes the Sandbox and its volume. It is not an error if the
// Sandbox does not exist.
func (c *Client) Delete(ctx context.Context, name string) error {
	err := c.sandboxes.Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Suspend puts the Sandbox to sleep: its pod is deleted and its volume is
// kept. If version is not empty, Suspend returns ErrChanged when the Sandbox
// has changed since that version.
func (c *Client) Suspend(ctx context.Context, name, version string) error {
	patch := map[string]any{"spec": map[string]any{"operatingMode": agents.SandboxOperatingModeSuspended}}
	if version != "" {
		patch["metadata"] = map[string]any{"resourceVersion": version}
	}
	_, err := c.patch(ctx, name, types.MergePatchType, patch)
	return err
}

// Resume wakes the Sandbox in a new pod made from pod, with the same volume.
func (c *Client) Resume(ctx context.Context, name string, pod Pod) error {
	spec, err := c.pod(ctx, pod)
	if err != nil {
		return err
	}
	// Replaces the whole pod spec, so that nothing of the previous one stays.
	_, err = c.patch(ctx, name, types.JSONPatchType, []map[string]any{
		{"op": "add", "path": "/spec/podTemplate/spec", "value": spec},
		{"op": "add", "path": "/spec/operatingMode", "value": agents.SandboxOperatingModeRunning},
	})
	return err
}

// Keep annotates the Sandbox with the turn and has it deleted, with its
// volume, at until. A zero until keeps it until it is deleted. Keep returns
// the updated Sandbox. Since the update changes the resourceVersion, a
// Suspend with the version read before it fails with ErrChanged.
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

func (c *Client) patch(ctx context.Context, name string, kind types.PatchType, patch any) (*agents.Sandbox, error) {
	body, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}
	patched, err := c.sandboxes.Patch(ctx, name, kind, body, metav1.PatchOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return nil, fmt.Errorf("sandbox %s: %w", name, ErrNotFound)
	case apierrors.IsConflict(err):
		return nil, fmt.Errorf("sandbox %s: %w", name, ErrChanged)
	}
	return patched, err
}
