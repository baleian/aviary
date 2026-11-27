package sandbox

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	ateapi "go.ate.dev/substrate/api/ateapi/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// control is a Substrate API in memory, with what the client uses of it.
type control struct {
	ateapi.UnimplementedControlServer

	mu        sync.Mutex
	atespaces map[string]bool
	templates map[string]*ateapi.ActorTemplate
	actors    map[string]*ateapi.Actor
	policies  map[string]*ateapi.EgressPolicy
	// updates counts the updates of egress policies.
	updates int
	// resume is the error of the next resume, if not nil.
	resume error
	// busy is the number of deletions that find another operation on their
	// actor in progress.
	busy int
	// workers are the workers and the actors that each hosts.
	workers map[string][]*ateapi.ObjectRef
	// draining are the workers that are going away.
	draining map[string]bool
	// denied refuses to make atespaces, as for a caller that owns only the
	// atespace that the installation made.
	denied bool
}

func (c *control) CreateAtespace(_ context.Context, request *ateapi.CreateAtespaceRequest) (*ateapi.Atespace, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	name := request.GetAtespace().GetMetadata().GetName()
	if c.denied {
		return nil, status.Error(codes.PermissionDenied, "not an owner of every atespace")
	}
	if c.atespaces[name] {
		return nil, status.Error(codes.AlreadyExists, "atespace exists")
	}
	c.atespaces[name] = true
	return request.GetAtespace(), nil
}

func (c *control) CreateActorTemplate(_ context.Context, request *ateapi.CreateActorTemplateRequest) (*ateapi.ActorTemplate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	name := request.GetActorTemplate().GetMetadata().GetName()
	if c.templates[name] != nil {
		return nil, status.Error(codes.AlreadyExists, "template exists")
	}
	c.templates[name] = request.GetActorTemplate()
	return request.GetActorTemplate(), nil
}

func (c *control) CreateActor(_ context.Context, request *ateapi.CreateActorRequest) (*ateapi.Actor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	actor := proto.Clone(request.GetActor()).(*ateapi.Actor)
	if c.actors[actor.GetMetadata().GetName()] != nil {
		return nil, status.Error(codes.AlreadyExists, "actor exists")
	}
	actor.Metadata.Uid = uuid.NewString()
	actor.Status = &ateapi.ActorStatus{State: ateapi.ActorState_ACTOR_STATE_SUSPENDED}
	c.actors[actor.GetMetadata().GetName()] = actor
	return actor, nil
}

func (c *control) GetActor(_ context.Context, request *ateapi.GetActorRequest) (*ateapi.Actor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if actor := c.actors[request.GetActor().GetName()]; actor != nil {
		return actor, nil
	}
	return nil, status.Error(codes.NotFound, "no actor")
}

// ListActors lists one actor a page.
func (c *control) ListActors(_ context.Context, request *ateapi.ListActorsRequest) (*ateapi.ListActorsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.atespaces[request.GetAtespace()] {
		return nil, status.Error(codes.NotFound, "no atespace")
	}
	var names []string
	for name := range c.actors {
		if name > request.GetOptions().GetPageToken() {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return &ateapi.ListActorsResponse{}, nil
	}
	first := names[0]
	for _, name := range names {
		first = min(first, name)
	}
	next := ""
	if len(names) > 1 {
		next = first
	}
	return &ateapi.ListActorsResponse{Actors: []*ateapi.Actor{c.actors[first]}, NextPageToken: next}, nil
}

func (c *control) ResumeActor(_ context.Context, request *ateapi.ResumeActorRequest) (*ateapi.ResumeActorResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.resume; err != nil {
		c.resume = nil
		return nil, err
	}
	actor := c.actors[request.GetActor().GetName()]
	if actor == nil {
		return nil, status.Error(codes.NotFound, "no actor")
	}
	actor.Status.State = ateapi.ActorState_ACTOR_STATE_RUNNING
	return &ateapi.ResumeActorResponse{}, nil
}

func (c *control) DeleteActor(_ context.Context, request *ateapi.DeleteActorRequest) (*ateapi.Actor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	actor := c.actors[request.GetActor().GetName()]
	switch {
	case c.busy > 0:
		c.busy--
		return nil, status.Error(codes.Aborted, "another operation is in progress for this actor")
	case actor == nil:
		return nil, status.Error(codes.NotFound, "no actor")
	case request.GetOptions().GetUid() != "" && request.GetOptions().GetUid() != actor.GetMetadata().GetUid():
		return nil, status.Error(codes.Aborted, "another actor")
	}
	delete(c.actors, request.GetActor().GetName())
	return actor, nil
}

func (c *control) CreateActorEgressPolicy(_ context.Context, request *ateapi.CreateActorEgressPolicyRequest) (*ateapi.EgressPolicy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	name := request.GetActor().GetName()
	switch {
	case c.actors[name] == nil:
		return nil, status.Error(codes.NotFound, "no actor")
	case c.policies[name] != nil:
		return nil, status.Error(codes.AlreadyExists, "policy exists")
	}
	policy := proto.Clone(request.GetEgressPolicy()).(*ateapi.EgressPolicy)
	policy.Metadata.Uid, policy.Metadata.Version = uuid.NewString(), 1
	c.policies[name] = policy
	return policy, nil
}

func (c *control) GetActorEgressPolicy(_ context.Context, request *ateapi.GetActorEgressPolicyRequest) (*ateapi.EgressPolicy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if policy := c.policies[request.GetActor().GetName()]; policy != nil {
		return policy, nil
	}
	return nil, status.Error(codes.NotFound, "no policy")
}

func (c *control) UpdateActorEgressPolicy(_ context.Context, request *ateapi.UpdateActorEgressPolicyRequest) (*ateapi.EgressPolicy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.policies[request.GetActor().GetName()]
	given := request.GetEgressPolicy()
	if current == nil || given.GetMetadata().GetUid() != current.GetMetadata().GetUid() || given.GetMetadata().GetVersion() != current.GetMetadata().GetVersion() {
		return nil, status.Error(codes.Aborted, "the policy has changed")
	}
	policy := proto.Clone(given).(*ateapi.EgressPolicy)
	policy.Metadata.Version++
	c.policies[request.GetActor().GetName()] = policy
	c.updates++
	return policy, nil
}

func (c *control) ListWorkers(context.Context, *ateapi.ListWorkersRequest) (*ateapi.ListWorkersResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	listed := &ateapi.ListWorkersResponse{}
	for name := range c.workers {
		state := ateapi.WorkerState_WORKER_STATE_ACTIVE
		if c.draining[name] {
			state = ateapi.WorkerState_WORKER_STATE_DRAINING
		}
		listed.Workers = append(listed.Workers, &ateapi.Worker{Metadata: &ateapi.ResourceMetadata{Name: name}, Status: &ateapi.WorkerStatus{State: state}})
	}
	return listed, nil
}

func (c *control) ListWorkerActorAssignments(_ context.Context, request *ateapi.ListWorkerActorAssignmentsRequest) (*ateapi.ListWorkerActorAssignmentsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	listed := &ateapi.ListWorkerActorAssignmentsResponse{}
	for _, actor := range c.workers[request.GetWorker().GetName()] {
		listed.ActorAssignments = append(listed.ActorAssignments, &ateapi.ActorAssignment{Actor: actor})
	}
	return listed, nil
}

// served returns a client of the sandboxes of the atespace "aviary", with the
// harness "opencode", and the API that it calls.
func served(t *testing.T) (*Client, *control) {
	t.Helper()
	api := &control{
		atespaces: map[string]bool{}, templates: map[string]*ateapi.ActorTemplate{}, actors: map[string]*ateapi.Actor{},
		policies: map[string]*ateapi.EgressPolicy{}, workers: map[string][]*ateapi.ObjectRef{}, draining: map[string]bool{},
	}
	server := grpc.NewServer()
	ateapi.RegisterControlServer(server, api)
	// The connection is in memory, so that what the tests read of the API
	// follows what it served.
	listener := bufconn.Listen(1 << 20)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///control", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &Client{
		settings: Settings{
			Atespace: "aviary", Pool: map[string]string{"pool": "sandboxes"}, Snapshot: "rootfs", Router: "https://router.example.com/",
			Runner: "registry.example.com/runner@sha256:89ab",
		},
		conn: conn, control: ateapi.NewControlClient(conn), templates: map[string]*ateapi.ActorTemplate{}, made: map[string]bool{},
	}
	template, err := c.template("opencode", Harness{
		Image:     "registry.example.com/opencode@sha256:0123",
		Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.templates["opencode"] = template
	return c, api
}

// A sandbox is made asleep from the template of its harness, with the egress
// of its session, which a later SetEgress replaces. Created again, it stays
// as it is.
func TestCreate(t *testing.T) {
	c, api := served(t)
	ctx := context.Background()
	session := uuid.New()
	name := Name(session)
	egress := Egress{Hosts: []Host{{TLS: true, Name: "api.example.com", Port: 443}}}
	if err := c.Create(ctx, Request{Session: session, Harness: "opencode", Egress: egress}); err != nil {
		t.Fatal(err)
	}
	// As for a file that is written into its workspace while a turn runs
	// there with that egress.
	if err := c.Create(ctx, Request{Session: session, Harness: "opencode"}); err != nil {
		t.Fatalf("create again: %v", err)
	}
	if len(api.templates) != 1 || len(api.actors) != 1 || !api.atespaces["aviary"] || api.updates != 0 {
		t.Errorf("made %d templates, %d actors, atespaces %v, %d policy updates", len(api.templates), len(api.actors), api.atespaces, api.updates)
	}
	first, _ := egress.rules()
	if rules := api.policies[name].GetRules(); len(rules) != 1 || !proto.Equal(rules[0], first[0]) {
		t.Errorf("policy of a sandbox that was created again: %v, want that of its creation", rules)
	}
	template := c.templates["opencode"]
	if !strings.HasPrefix(template.GetMetadata().GetName(), "opencode-") || api.actors[name].GetActorTemplate().GetName() != template.GetMetadata().GetName() ||
		template.GetSnapshotConfig().GetPreferredFidelity() != ateapi.SnapshotFidelity_SNAPSHOT_FIDELITY_ROOTFS ||
		template.GetResources().GetLimits().GetCpuMillicores() != 2000 || template.GetWorkerSelector().GetMatchLabels()["pool"] != "sandboxes" {
		t.Errorf("template %v of actor %v", template, api.actors[name])
	}
	// The sandbox runs the runner from the runner's image.
	container := template.GetContainers()[0]
	mounted := map[string]string{}
	for _, mount := range container.GetVolumeMounts() {
		mounted[mount.GetName()] = mount.GetMountPath()
	}
	var runner string
	for _, volume := range template.GetVolumes() {
		if volume.GetName() == "runner" {
			runner = volume.GetImage().GetReference()
		}
	}
	if !slices.Equal(container.GetCommand(), []string{"/opt/aviary/aviary-runner"}) || mounted["runner"] != "/opt/aviary" ||
		runner != "registry.example.com/runner@sha256:89ab" {
		t.Errorf("container %v of the template, with the runner %q", container, runner)
	}
	seen, err := c.Sandbox(ctx, name)
	if err != nil || seen.State != Asleep || seen.Session != session.String() || seen.UID == "" {
		t.Errorf("Sandbox = %+v, %v; want the session's, asleep", seen, err)
	}

	changed := Egress{Hosts: []Host{{TLS: true, Name: "other.example.com", Port: 443}}}
	if err := c.SetEgress(ctx, name, changed); err != nil {
		t.Fatal(err)
	}
	want, _ := changed.rules()
	if rules := api.policies[name].GetRules(); api.updates != 1 || len(rules) != 1 || !proto.Equal(rules[0], want[0]) {
		t.Errorf("policy after SetEgress: %v, %d updates", rules, api.updates)
	}
	if err := c.SetEgress(ctx, Name(uuid.New()), changed); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetEgress of no sandbox = %v, want ErrNotFound", err)
	}
	if err := c.Create(ctx, Request{Session: uuid.New(), Harness: "codex"}); err == nil {
		t.Error("Create with a harness that is not configured succeeded")
	}
}

// An installation that makes the atespace itself may let the Supervisors own
// only that atespace.
func TestCreateInTheAtespaceOfTheInstallation(t *testing.T) {
	c, api := served(t)
	api.denied = true
	if err := c.Create(context.Background(), Request{Session: uuid.New(), Harness: "opencode"}); err != nil {
		t.Fatal(err)
	}
	if len(api.templates) != 1 || len(api.actors) != 1 {
		t.Errorf("made %d templates and %d actors, want 1 of each", len(api.templates), len(api.actors))
	}
}

// A harness changes its template's name, since a template does not change.
func TestTemplateName(t *testing.T) {
	c, _ := served(t)
	harness := Harness{
		Image:     "registry.example.com/opencode@sha256:0123",
		Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")}},
	}
	first, err := c.template("opencode", harness)
	if err != nil {
		t.Fatal(err)
	}
	harness.Image = "registry.example.com/opencode@sha256:4567"
	second, err := c.template("opencode", harness)
	if err != nil || first.GetMetadata().GetName() == second.GetMetadata().GetName() {
		t.Errorf("templates of two images: %s and %s, %v", first.GetMetadata().GetName(), second.GetMetadata().GetName(), err)
	}
	c.settings.Runner = "registry.example.com/runner@sha256:cdef"
	third, err := c.template("opencode", harness)
	if err != nil || third.GetMetadata().GetName() == second.GetMetadata().GetName() {
		t.Errorf("templates of two runners: %s and %s, %v", second.GetMetadata().GetName(), third.GetMetadata().GetName(), err)
	}
	harness.Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")}
	if _, err := c.template("opencode", harness); err == nil {
		t.Error("a harness without a CPU limit has a template")
	}
	harness.Resources.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")}
	c.settings.Runner = ""
	if _, err := c.template("opencode", harness); err == nil {
		t.Error("a harness has a template without the image of the runner")
	}
}

func TestSandboxStates(t *testing.T) {
	for state, want := range map[ateapi.ActorState]State{
		ateapi.ActorState_ACTOR_STATE_RUNNING:    Running,
		ateapi.ActorState_ACTOR_STATE_RESUMING:   Starting,
		ateapi.ActorState_ACTOR_STATE_SUSPENDING: Asleep,
		ateapi.ActorState_ACTOR_STATE_SUSPENDED:  Asleep,
		ateapi.ActorState_ACTOR_STATE_CRASHED:    Crashed,
		ateapi.ActorState_ACTOR_STATE_REVERTING:  Crashed,
		ateapi.ActorState_ACTOR_STATE_DELETING:   Lost,
	} {
		actor := &ateapi.Actor{Metadata: &ateapi.ResourceMetadata{Name: "session-a"}, Status: &ateapi.ActorStatus{State: state}}
		if got := seen(actor).State; got != want {
			t.Errorf("an actor %v is %s, want %s", state, got, want)
		}
	}
}

// Resume tells a pool without room, and a crashed sandbox, from other
// failures.
func TestResume(t *testing.T) {
	c, api := served(t)
	ctx := context.Background()
	session := uuid.New()
	name := Name(session)
	if err := c.Create(ctx, Request{Session: session, Harness: "opencode"}); err != nil {
		t.Fatal(err)
	}
	api.resume = status.Error(codes.ResourceExhausted, "no worker has room")
	if err := c.Resume(ctx, name); !errors.Is(err, ErrNoWorker) {
		t.Errorf("Resume without room = %v, want ErrNoWorker", err)
	}
	api.resume = status.Error(codes.Aborted, "another operation is in progress for this actor")
	if err := c.Resume(ctx, name); !errors.Is(err, ErrOperating) {
		t.Errorf("Resume during another operation = %v, want ErrOperating", err)
	}
	api.actors[name].Status = &ateapi.ActorStatus{State: ateapi.ActorState_ACTOR_STATE_CRASHED, Crash: &ateapi.ActorCrash{Message: "out of memory"}}
	api.resume = status.Error(codes.FailedPrecondition, "the actor has crashed")
	if err := c.Resume(ctx, name); !errors.Is(err, ErrCrashed) || !strings.Contains(err.Error(), "out of memory") {
		t.Errorf("Resume of a crashed sandbox = %v, want ErrCrashed with its message", err)
	}
	api.actors[name].Status = &ateapi.ActorStatus{State: ateapi.ActorState_ACTOR_STATE_SUSPENDED}
	if err := c.Resume(ctx, name); err != nil {
		t.Fatal(err)
	}
	if seen, err := c.Sandbox(ctx, name); err != nil || seen.State != Running {
		t.Errorf("Sandbox after Resume = %+v, %v; want it running", seen, err)
	}
	if err := c.Resume(ctx, Name(uuid.New())); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resume of no sandbox = %v, want ErrNotFound", err)
	}
}

// Sandboxes lists the sandboxes of sessions over pages, and Delete deletes
// one only if it has the UID, when the UID is given.
func TestSandboxesAndDelete(t *testing.T) {
	c, api := served(t)
	ctx := context.Background()
	if all, err := c.Sandboxes(ctx); err != nil || len(all) != 0 {
		t.Errorf("Sandboxes before the atespace exists = %v, %v; want none", all, err)
	}
	sessions := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, session := range sessions {
		if err := c.Create(ctx, Request{Session: session, Harness: "opencode"}); err != nil {
			t.Fatal(err)
		}
	}
	api.actors["another-actor"] = &ateapi.Actor{Metadata: &ateapi.ResourceMetadata{Atespace: "aviary", Name: "another-actor"}}
	all, err := c.Sandboxes(ctx)
	if err != nil || len(all) != len(sessions) {
		t.Fatalf("Sandboxes = %v, %v; want the %d of the sessions", all, err, len(sessions))
	}

	name := Name(sessions[0])
	uid := api.actors[name].GetMetadata().GetUid()
	if err := c.Delete(ctx, name, uuid.NewString()); err != nil || api.actors[name] == nil {
		t.Errorf("Delete with another UID = %v, and the sandbox is gone", err)
	}
	if err := c.Delete(ctx, name, uid); err != nil || api.actors[name] != nil {
		t.Errorf("Delete with its UID = %v, and the sandbox stays", err)
	}
	if err := c.Delete(ctx, name, ""); err != nil {
		t.Errorf("Delete of no sandbox = %v", err)
	}
}

// Delete waits for another operation on the sandbox, such as putting it to
// sleep, to finish.
func TestDeleteWhileBusy(t *testing.T) {
	c, api := served(t)
	ctx := context.Background()
	session := uuid.New()
	name := Name(session)
	if err := c.Create(ctx, Request{Session: session, Harness: "opencode"}); err != nil {
		t.Fatal(err)
	}
	api.busy = 1
	if err := c.Delete(ctx, name, ""); err != nil || api.actors[name] != nil {
		t.Errorf("Delete while another operation is in progress = %v, and the sandbox stays", err)
	}
}

// Draining returns the sandboxes of the atespace on workers that go away.
func TestDraining(t *testing.T) {
	c, api := served(t)
	api.workers["worker-a"] = []*ateapi.ObjectRef{{Atespace: "aviary", Name: "session-a"}, {Atespace: "other", Name: "session-b"}, {Atespace: "aviary", Name: "another-actor"}}
	api.workers["worker-b"] = []*ateapi.ObjectRef{{Atespace: "aviary", Name: "session-c"}}
	api.draining["worker-a"] = true
	if names, err := c.Draining(context.Background()); err != nil || len(names) != 1 || names[0] != "session-a" {
		t.Errorf("Draining = %v, %v; want session-a", names, err)
	}
}

// The runner of a sandbox is reached through the router, which the request
// names the sandbox to.
func TestRunnerThroughTheRouter(t *testing.T) {
	c, _ := served(t)
	runner := c.Runner("session-a")
	if runner.address != "https://router.example.com" || runner.target != "aviary/session-a" {
		t.Errorf("runner at %s for %s", runner.address, runner.target)
	}
}
