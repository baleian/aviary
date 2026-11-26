// Package sandbox runs the sandbox of each session as an actor of Agent
// Substrate. The actor runs the harness of the session's agent with the
// runner, on a worker of the installation's pool, and keeps the session's
// home in a durable directory, which its snapshot takes along while the
// sandbox sleeps.
package sandbox

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	ateapi "go.ate.dev/substrate/api/ateapi/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	// RunnerPort is the port of the runner in the sandbox, which the router
	// of Substrate reaches.
	RunnerPort = 80
	// home is the volume of the session's home.
	home = "home"
	// trustDir holds, as ca.pem, the certificates that the sandbox trusts:
	// public ones, and the egress gateway's, which answers for the hosts into
	// whose requests it puts secrets.
	trustDir  = "/etc/aviary/trust"
	trustFile = trustDir + "/ca.pem"
	// prefix begins the name of every sandbox, which the session ID ends.
	prefix = "session-"
	// pageSize is the number of resources that a list asks for at a time.
	pageSize = 500
)

// session is the entrypoint that every harness image provides.
var session = []string{"aviary-session"}

var (
	// ErrNotFound is returned for a sandbox, or a turn of a runner, that does
	// not exist.
	ErrNotFound = errors.New("not found")
	// ErrNoWorker is returned when no worker of the pool has room for a
	// sandbox that is woken up.
	ErrNoWorker = errors.New("no worker has room for the sandbox")
	// ErrCrashed is returned when a sandbox that is woken up has crashed. It
	// runs again once it is reverted to its last snapshot.
	ErrCrashed = errors.New("the sandbox has crashed")
)

// State is the observed state of a sandbox.
type State string

const (
	// Running means that the runner is reachable.
	Running State = "Running"
	// Asleep means that the sandbox is suspended, or being suspended: its
	// snapshot holds the session, and it takes no worker.
	Asleep State = "Asleep"
	// Starting means that the sandbox is being created or woken up.
	Starting State = "Starting"
	// Crashed means that the sandbox stopped by itself. It is reverted to its
	// last snapshot before it runs again.
	Crashed State = "Crashed"
	// Lost means that the sandbox is being deleted.
	Lost State = "Lost"
)

// Sandbox is a sandbox as read from Substrate.
type Sandbox struct {
	Name string
	// UID tells the sandbox from others that have had its name, which is
	// that of its session.
	UID     string
	Session string
	State   State
	// Message explains why the sandbox is not running, if it is not.
	Message string
	Created time.Time
}

// Harness is a harness as the installation configures it.
type Harness struct {
	// Image contains the harness and the runner. Substrate takes an image
	// only by its digest, as in registry.example.com/harness@sha256:....
	Image string `json:"image"`
	// Resources has the limits of CPU and memory of a sandbox, which is its
	// size. A sandbox has no other share of its worker.
	Resources corev1.ResourceRequirements `json:"resources"`
}

// Request describes the sandbox to create for a session.
type Request struct {
	Session uuid.UUID
	Harness string
	// Egress is where the sandbox may connect.
	Egress Egress
}

// Settings apply to every sandbox of the installation.
type Settings struct {
	// API is the address of the Substrate API, such as
	// api.ate-system.svc.cluster.local:443, and Authority the name of its
	// certificate. TokenFile has the bearer token of the Supervisor, and
	// CAFile the certificates that the API's certificate is signed with.
	API, Authority, TokenFile, CAFile string
	// Router is the URL of the router that reaches the sandboxes, and
	// RouterName the name of its certificate, which is signed by those of
	// CAFile. IdentityFile has the Supervisor's own certificate, with its
	// key, which the router asks for.
	Router, RouterName, IdentityFile string
	// Atespace holds the sandboxes and their templates.
	Atespace string
	// Pool selects the workers of the sandboxes by the labels of their pool.
	Pool map[string]string
	// Snapshot is what the snapshot of a sleeping sandbox keeps: "memory",
	// "rootfs" or "volumes".
	Snapshot string
	// MaxToolOutputBytes caps the text of a tool call that the runner
	// reports. Zero means no limit.
	MaxToolOutputBytes int
	// Harnesses are the harnesses that agents can use, by name.
	Harnesses map[string]Harness
}

// Snapshot levels, as the settings name them.
var snapshots = map[string]ateapi.SnapshotFidelity{
	"memory":  ateapi.SnapshotFidelity_SNAPSHOT_FIDELITY_MEMORY,
	"rootfs":  ateapi.SnapshotFidelity_SNAPSHOT_FIDELITY_ROOTFS,
	"volumes": ateapi.SnapshotFidelity_SNAPSHOT_FIDELITY_VOLUMES,
}

// Client manages the sandboxes of the sessions.
type Client struct {
	settings Settings
	conn     *grpc.ClientConn
	control  ateapi.ControlClient
	router   *http.Client
	// templates are the actor templates of the harnesses, by harness.
	templates map[string]*ateapi.ActorTemplate

	mu sync.Mutex
	// made are the templates known to exist.
	made map[string]bool
}

// New returns a Client of the Substrate API and router of the settings. It
// does not connect yet.
func New(settings Settings) (*Client, error) {
	if len(validation.IsDNS1123Label(settings.Atespace)) > 0 {
		return nil, fmt.Errorf("atespace %q is not a DNS label", settings.Atespace)
	}
	if _, known := snapshots[settings.Snapshot]; !known {
		return nil, fmt.Errorf("snapshot %q is not memory, rootfs or volumes", settings.Snapshot)
	}
	c := &Client{settings: settings, templates: map[string]*ateapi.ActorTemplate{}, made: map[string]bool{}}
	for name, harness := range settings.Harnesses {
		template, err := c.template(name, harness)
		if err != nil {
			return nil, err
		}
		c.templates[name] = template
	}
	authorities, err := certificates(settings.CAFile)
	if err != nil {
		return nil, err
	}
	c.conn, err = grpc.NewClient("dns:///"+settings.API,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: authorities, ServerName: settings.Authority, MinVersion: tls.VersionTLS12})),
		grpc.WithPerRPCCredentials(tokenFile(settings.TokenFile)),
		// The Service of the API is headless: each replica has an address.
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin": {}}]}`),
	)
	if err != nil {
		return nil, err
	}
	c.control = ateapi.NewControlClient(c.conn)
	c.router = &http.Client{Transport: &http.Transport{
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			RootCAs: authorities, ServerName: settings.RouterName, MinVersion: tls.VersionTLS12,
			// The certificate is renewed in its file, so it is read for each
			// connection.
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				identity, err := tls.LoadX509KeyPair(settings.IdentityFile, settings.IdentityFile)
				return &identity, err
			},
		},
	}}
	return c, nil
}

// Close closes the connection to the API.
func (c *Client) Close() error {
	return c.conn.Close()
}

// certificates reads the PEM certificates of the file into a pool.
func certificates(file string) (*x509.CertPool, error) {
	encoded, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(encoded) {
		return nil, fmt.Errorf("%s has no certificate", file)
	}
	return pool, nil
}

// tokenFile sends the token in the file as the bearer token of every call.
// The kubelet renews the token in its file, so the file is read each time.
type tokenFile string

func (f tokenFile) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	token, err := os.ReadFile(string(f))
	if err != nil {
		return nil, err
	}
	return map[string]string{"authorization": "Bearer " + strings.TrimSpace(string(token))}, nil
}

func (tokenFile) RequireTransportSecurity() bool {
	return true
}

// Name returns the name of the sandbox of the session.
func Name(session uuid.UUID) string {
	return prefix + session.String()
}

// template returns the actor template of the harness. Its name is that of
// the harness and a hash of its definition, since a template does not
// change: a harness that the installation changes gets a new one.
func (c *Client) template(name string, harness Harness) (*ateapi.ActorTemplate, error) {
	cpu, memory := harness.Resources.Limits.Cpu(), harness.Resources.Limits.Memory()
	if cpu.IsZero() || memory.IsZero() {
		return nil, fmt.Errorf("harness %s needs limits of cpu and memory", name)
	}
	env := []*ateapi.EnvVar{
		{Name: "AVIARY_RUNNER_ADDRESS", Value: "0.0.0.0:" + strconv.Itoa(RunnerPort)},
		{Name: "AVIARY_MAX_TOOL_OUTPUT_BYTES", Value: strconv.Itoa(c.settings.MaxToolOutputBytes)},
	}
	// Programs that verify TLS by themselves find the certificates by these
	// variables, each by its own.
	for _, variable := range []string{"SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO", "PIP_CERT"} {
		env = append(env, &ateapi.EnvVar{Name: variable, Value: trustFile})
	}
	template := &ateapi.ActorTemplate{
		Metadata:       &ateapi.ResourceMetadata{Atespace: c.settings.Atespace},
		WorkerSelector: &ateapi.Selector{MatchLabels: c.settings.Pool},
		Containers: []*ateapi.Container{{
			Name:    "harness",
			Image:   harness.Image,
			Command: session,
			Env:     env,
			WakeupProbe: &ateapi.WakeupProbe{
				HttpGet:        &ateapi.HttpGetAction{Path: "/healthz", Port: RunnerPort},
				TimeoutSeconds: 60,
			},
			VolumeMounts: []*ateapi.VolumeMount{{Name: home, MountPath: "/home"}, {Name: "trust", MountPath: trustDir}},
		}},
		Volumes: []*ateapi.Volume{
			{Name: home, DurableDir: &ateapi.DurableDirVolumeSource{}},
			{Name: "trust", SystemInfo: &ateapi.SystemInfoVolumeSource{DataSources: []*ateapi.SystemInfoDataSource{{
				TrustBundle: &ateapi.TrustBundleDataSource{Names: []string{"system-roots.ate.dev", "egress-mitm.ate.dev"}, Path: "ca.pem"},
			}}}},
		},
		Resources:      &ateapi.ResourceRequirements{Limits: &ateapi.Resources{CpuMillicores: cpu.MilliValue(), MemoryBytes: memory.Value()}},
		SnapshotConfig: &ateapi.SnapshotConfig{PreferredFidelity: snapshots[c.settings.Snapshot]},
	}
	definition, err := proto.MarshalOptions{Deterministic: true}.Marshal(template)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(definition)
	template.Metadata.Name = name + "-" + hex.EncodeToString(sum[:])[:10]
	if len(validation.IsDNS1123Label(template.Metadata.Name)) > 0 {
		return nil, fmt.Errorf("harness name %q does not make a DNS label", name)
	}
	return template, nil
}

// prepare makes the atespace and the template of the harness, if they do not
// exist yet, and returns the template's name.
func (c *Client) prepare(ctx context.Context, harness string) (string, error) {
	template, found := c.templates[harness]
	if !found {
		return "", fmt.Errorf("harness %s is not configured", harness)
	}
	name := template.Metadata.Name
	c.mu.Lock()
	made := c.made[name]
	c.mu.Unlock()
	if made {
		return name, nil
	}
	_, err := c.control.CreateAtespace(ctx, &ateapi.CreateAtespaceRequest{Atespace: &ateapi.Atespace{Metadata: &ateapi.ResourceMetadata{Name: c.settings.Atespace}}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return "", fmt.Errorf("create atespace %s: %w", c.settings.Atespace, err)
	}
	_, err = c.control.CreateActorTemplate(ctx, &ateapi.CreateActorTemplateRequest{ActorTemplate: template})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return "", fmt.Errorf("create actor template %s: %w", name, err)
	}
	c.mu.Lock()
	c.made[name] = true
	c.mu.Unlock()
	return name, nil
}

// ref returns the reference to the named sandbox.
func (c *Client) ref(name string) *ateapi.ObjectRef {
	return &ateapi.ObjectRef{Atespace: c.settings.Atespace, Name: name}
}

// Create creates the sandbox of the session, asleep, with its egress. It is
// not an error if the sandbox exists: its egress is then replaced.
func (c *Client) Create(ctx context.Context, request Request) error {
	template, err := c.prepare(ctx, request.Harness)
	if err != nil {
		return err
	}
	name := Name(request.Session)
	_, err = c.control.CreateActor(ctx, &ateapi.CreateActorRequest{Actor: &ateapi.Actor{
		Metadata:      &ateapi.ResourceMetadata{Atespace: c.settings.Atespace, Name: name},
		ActorTemplate: c.ref(template),
	}})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return fmt.Errorf("create actor %s: %w", name, err)
	}
	return c.SetEgress(ctx, name, request.Egress)
}

// SetEgress sets where the sandbox may connect. The egress gateway applies a
// change to new connections within seconds.
func (c *Client) SetEgress(ctx context.Context, name string, egress Egress) error {
	rules, err := egress.rules()
	if err != nil {
		return err
	}
	policy := &ateapi.EgressPolicy{Metadata: &ateapi.ResourceMetadata{Atespace: c.settings.Atespace, Name: "default"}, Rules: rules}
	_, err = c.control.CreateActorEgressPolicy(ctx, &ateapi.CreateActorEgressPolicyRequest{Actor: c.ref(name), EgressPolicy: policy})
	if status.Code(err) != codes.AlreadyExists {
		return found(name, err)
	}
	current, err := c.control.GetActorEgressPolicy(ctx, &ateapi.GetActorEgressPolicyRequest{Actor: c.ref(name), Name: "default"})
	if err != nil {
		return found(name, err)
	}
	if proto.Equal(&ateapi.EgressPolicy{Rules: current.GetRules()}, &ateapi.EgressPolicy{Rules: rules}) {
		return nil
	}
	policy.Metadata.Uid, policy.Metadata.Version = current.GetMetadata().GetUid(), current.GetMetadata().GetVersion()
	_, err = c.control.UpdateActorEgressPolicy(ctx, &ateapi.UpdateActorEgressPolicyRequest{Actor: c.ref(name), EgressPolicy: policy})
	return found(name, err)
}

// found returns the error of a call about the named sandbox, with ErrNotFound
// in place of NotFound.
func found(name string, err error) error {
	if status.Code(err) == codes.NotFound {
		return fmt.Errorf("sandbox %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("sandbox %s: %w", name, err)
	}
	return nil
}

// Sandbox returns the named sandbox, or ErrNotFound.
func (c *Client) Sandbox(ctx context.Context, name string) (Sandbox, error) {
	actor, err := c.control.GetActor(ctx, &ateapi.GetActorRequest{Actor: c.ref(name)})
	if err != nil {
		return Sandbox{}, found(name, err)
	}
	return seen(actor), nil
}

// Sandboxes returns the sandboxes of all sessions.
func (c *Client) Sandboxes(ctx context.Context) ([]Sandbox, error) {
	var all []Sandbox
	page := ""
	for {
		listed, err := c.control.ListActors(ctx, &ateapi.ListActorsRequest{
			Atespace: c.settings.Atespace, Options: &ateapi.ListOptions{PageSize: pageSize, PageToken: page},
		})
		if status.Code(err) == codes.NotFound {
			// The atespace is made with the first sandbox.
			return all, nil
		}
		if err != nil {
			return nil, err
		}
		for _, actor := range listed.GetActors() {
			if strings.HasPrefix(actor.GetMetadata().GetName(), prefix) {
				all = append(all, seen(actor))
			}
		}
		if page = listed.GetNextPageToken(); page == "" {
			return all, nil
		}
	}
}

func seen(actor *ateapi.Actor) Sandbox {
	metadata := actor.GetMetadata()
	sandbox := Sandbox{
		Name: metadata.GetName(), UID: metadata.GetUid(),
		Session: strings.TrimPrefix(metadata.GetName(), prefix), Created: metadata.GetCreateTime().AsTime(),
	}
	switch state := actor.GetStatus().GetState(); {
	case metadata.GetDeleteTime() != nil || state == ateapi.ActorState_ACTOR_STATE_DELETING:
		sandbox.State, sandbox.Message = Lost, "it is being deleted"
	case state == ateapi.ActorState_ACTOR_STATE_RUNNING:
		sandbox.State = Running
	case state == ateapi.ActorState_ACTOR_STATE_SUSPENDED || state == ateapi.ActorState_ACTOR_STATE_SUSPENDING:
		sandbox.State = Asleep
	case state == ateapi.ActorState_ACTOR_STATE_CRASHED || state == ateapi.ActorState_ACTOR_STATE_REVERTING:
		sandbox.State, sandbox.Message = Crashed, actor.GetStatus().GetCrash().GetMessage()
	default:
		sandbox.State = Starting
	}
	return sandbox
}

// Resume wakes the sandbox up on a worker of the pool, and returns once its
// runner serves. It returns ErrNoWorker if no worker has room for it now, and
// ErrCrashed if it has crashed.
func (c *Client) Resume(ctx context.Context, name string) error {
	_, err := c.control.ResumeActor(ctx, &ateapi.ResumeActorRequest{Actor: c.ref(name)})
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.ResourceExhausted:
		return fmt.Errorf("sandbox %s: %w", name, ErrNoWorker)
	case codes.FailedPrecondition:
		if seen, readErr := c.Sandbox(ctx, name); readErr == nil && seen.State == Crashed {
			return fmt.Errorf("sandbox %s: %w: %s", name, ErrCrashed, seen.Message)
		}
	}
	return found(name, err)
}

// Suspend puts the sandbox to sleep. A durable suspension returns once the
// snapshot is in object storage, where it outlives the worker's node.
// Otherwise the snapshot is uploaded later, and lost if the node goes first.
func (c *Client) Suspend(ctx context.Context, name string, durable bool) error {
	mode := ateapi.DurabilityMode_DURABILITY_MODE_ASYNCHRONOUS
	if durable {
		mode = ateapi.DurabilityMode_DURABILITY_MODE_SYNCHRONOUS
	}
	_, err := c.control.SuspendActor(ctx, &ateapi.SuspendActorRequest{Actor: c.ref(name), Options: &ateapi.SuspendOptions{Mode: mode}})
	return found(name, err)
}

// Revert stops whatever runs in the sandbox, and puts it to sleep as its
// last snapshot has it. What the sandbox did since that snapshot is lost.
func (c *Client) Revert(ctx context.Context, name string) error {
	_, err := c.control.RevertActor(ctx, &ateapi.RevertActorRequest{Actor: c.ref(name)})
	return found(name, err)
}

// Delete deletes the sandbox and its snapshot. If uid is not empty, it
// deletes only the sandbox with that UID: one that was made under the name
// since stays. It is not an error if there is no sandbox to delete.
func (c *Client) Delete(ctx context.Context, name, uid string) error {
	_, err := c.control.DeleteActor(ctx, &ateapi.DeleteActorRequest{Actor: c.ref(name), Options: &ateapi.DeleteOptions{Uid: uid}})
	switch status.Code(err) {
	case codes.OK, codes.NotFound:
		return nil
	case codes.Aborted:
		if uid != "" {
			return nil
		}
	}
	return found(name, err)
}

// Draining returns the sandboxes that run on workers that are going away.
// Each is to be put to sleep, durably, before its worker goes.
func (c *Client) Draining(ctx context.Context) ([]string, error) {
	var names []string
	page := ""
	for {
		listed, err := c.control.ListWorkers(ctx, &ateapi.ListWorkersRequest{Options: &ateapi.ListOptions{PageSize: pageSize, PageToken: page}})
		if err != nil {
			return nil, err
		}
		for _, worker := range listed.GetWorkers() {
			if worker.GetStatus().GetState() != ateapi.WorkerState_WORKER_STATE_DRAINING {
				continue
			}
			hosted, err := c.hosted(ctx, worker.GetMetadata().GetName())
			if err != nil {
				return nil, err
			}
			names = append(names, hosted...)
		}
		if page = listed.GetNextPageToken(); page == "" {
			return names, nil
		}
	}
}

// hosted returns the sandboxes that the named worker hosts.
func (c *Client) hosted(ctx context.Context, worker string) ([]string, error) {
	var names []string
	page := ""
	for {
		listed, err := c.control.ListWorkerActorAssignments(ctx, &ateapi.ListWorkerActorAssignmentsRequest{
			Worker: &ateapi.ObjectRef{Name: worker}, Options: &ateapi.ListOptions{PageSize: pageSize, PageToken: page},
		})
		if err != nil {
			return nil, err
		}
		for _, assigned := range listed.GetActorAssignments() {
			actor := assigned.GetActor()
			if actor.GetAtespace() == c.settings.Atespace && strings.HasPrefix(actor.GetName(), prefix) {
				names = append(names, actor.GetName())
			}
		}
		if page = listed.GetNextPageToken(); page == "" {
			return names, nil
		}
	}
}

// Runner returns a client of the runner in the named sandbox, which reaches
// it through the router.
func (c *Client) Runner(name string) *Runner {
	return &Runner{address: strings.TrimSuffix(c.settings.Router, "/"), target: c.settings.Atespace + "/" + name, http: c.router, quiet: time.Minute}
}
