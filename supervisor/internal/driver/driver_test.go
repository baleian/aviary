package driver

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/events"
	memorylog "aviary/supervisor/events/memory"
	"aviary/supervisor/internal/metrics"
	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/internal/sandbox/sandboxtest"
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

var ctx = context.Background()

type installation struct {
	t       *testing.T
	store   *memorystore.Store
	events  *events.Streams
	service *sandboxtest.Service
	config  Config
}

// install returns an in-memory installation with the harness opencode.
func install(t *testing.T, changes ...func(*Config)) *installation {
	t.Helper()
	i := &installation{t: t, store: memorystore.New(), events: events.New(memorylog.New()), service: sandboxtest.New(t)}
	i.config = Config{
		Harnesses: map[string]sandbox.Harness{"opencode": {Image: "registry.example.com/opencode:1.0"}},
		Models:    &url.URL{Scheme: "http", Host: "egress-proxy.example.com:8080"},
		Token: func(session uuid.UUID) (string, error) {
			return "the-token-of-" + session.String(), nil
		},
		MaxConcurrentTurns: 50,
		MaxStartup:         time.Minute,
		MaxTurn:            time.Hour,
		Sweep:              50 * time.Millisecond,
	}
	for _, change := range changes {
		change(&i.config)
	}
	return i
}

// supervisor starts a driver and returns it with a function that stops it.
// The driver stops at the end of the test at the latest.
func (i *installation) supervisor() (*Driver, func()) {
	running, stop := context.WithCancel(ctx)
	i.t.Cleanup(stop)
	driver := New(i.config, i.service, i.store, i.events, metrics.New(nil, nil))
	stopped := make(chan struct{})
	go func() {
		driver.Run(running)
		close(stopped)
	}()
	return driver, func() {
		stop()
		<-stopped
	}
}

// session creates a session of a new agent in a new project. The agent's
// model is a-model of a new provider.
func (i *installation) session(agent store.Agent) store.Session {
	i.t.Helper()
	project, err := i.store.CreateProject(ctx, store.Project{Slug: "project-" + uuid.NewString()})
	if err != nil {
		i.t.Fatal(err)
	}
	provider, err := i.store.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "models", URL: "https://models.example.com/v1/"})
	if err != nil {
		i.t.Fatal(err)
	}
	agent.Slug, agent.Harness, agent.Model = "agent", "opencode", store.Model{Provider: provider.ID, Name: "a-model"}
	made, err := i.store.CreateAgent(ctx, project.ID, agent)
	if err != nil {
		i.t.Fatal(err)
	}
	session, err := i.store.CreateSession(ctx, made.ID, uuid.Nil, "")
	if err != nil {
		i.t.Fatal(err)
	}
	return session
}

func (i *installation) sessionOf(id uuid.UUID) store.Session {
	i.t.Helper()
	session, err := i.store.Session(ctx, id)
	if err != nil {
		i.t.Fatal(err)
	}
	return session
}

// send accepts the message as the session's next turn and wakes the driver.
func (i *installation) send(driver *Driver, session store.Session, text string) store.Turn {
	i.t.Helper()
	turn, err := driver.Accept(ctx, session.ID, uuid.Nil, text)
	if err != nil {
		i.t.Fatal(err)
	}
	driver.Wake()
	return turn
}

func (i *installation) turn(id uuid.UUID) store.Turn {
	i.t.Helper()
	turn, err := i.store.Turn(ctx, id)
	if err != nil {
		i.t.Fatal(err)
	}
	return turn
}

// sandbox returns the name of the session's sandbox, or "" if it has none.
func (i *installation) sandbox(session store.Session) string {
	i.t.Helper()
	session, err := i.store.Session(ctx, session.ID)
	if err != nil {
		i.t.Fatal(err)
	}
	return session.Sandbox
}

// told returns the types of the turn's events, as clients read them.
func (i *installation) told(turn uuid.UUID) []string {
	i.t.Helper()
	var types []string
	after := events.Start
	for {
		batch, err := i.events.Read(ctx, turn, after, time.Millisecond)
		if err != nil {
			i.t.Fatal(err)
		}
		if len(batch) == 0 {
			return types
		}
		for _, event := range batch {
			types = append(types, event.Type)
		}
		after = batch[len(batch)-1].ID
	}
}

// ended waits for the turn to end and returns it.
func (i *installation) ended(id uuid.UUID) store.Turn {
	i.t.Helper()
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if turn := i.turn(id); turn.EndedAt != nil {
			return turn
		}
		if time.Now().After(end) {
			i.t.Fatalf("turn did not end; events: %v", i.told(id))
		}
	}
}

// eventually waits until holds returns true.
func (i *installation) eventually(what string, holds func() bool) {
	i.t.Helper()
	for end := time.Now().Add(5 * time.Second); !holds(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			i.t.Fatalf("timed out waiting until %s", what)
		}
	}
}

func failed(turn store.Turn, reason string) bool {
	return turn.State == store.TurnFailed && turn.Failure != nil && turn.Failure.Reason == reason
}

var (
	started  = map[string]string{"harness_session_id": "s-1"}
	finished = map[string]string{"stop_reason": "end_turn"}
)

func TestATurnRunsInTheSandboxOfItsSession(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{Instructions: "be kind", Approval: Ask})
	turn := i.send(driver, session, "hello")

	given := i.service.Turn(t)
	if given.ID != turn.ID.String() || given.Text != "hello" || given.Instructions != "be kind" || given.Approval != Ask {
		t.Errorf("runner got %+v", given)
	}
	// The model URL is the Egress Proxy's, with the provider's path.
	want := map[string]string{
		"AVIARY_LLM_BASE_URL": "http://egress-proxy.example.com:8080/v1",
		"AVIARY_LLM_MODEL":    "a-model",
		"AVIARY_APPROVAL":     Ask,
	}
	for name, value := range want {
		if given.Environment[name] != value {
			t.Errorf("harness env %s = %q, want %q", name, given.Environment[name], value)
		}
	}
	if len(given.Environment) != len(want) {
		t.Errorf("harness env = %v", given.Environment)
	}
	agent, _ := i.store.Agent(ctx, session.AgentID)
	made := i.service.Made()[given.Sandbox]
	if i.sandbox(session) != given.Sandbox || made.Session != session.ID || made.Pod.Harness.Image != "registry.example.com/opencode:1.0" ||
		made.Pod.Token != "the-token-of-"+session.ID.String() ||
		made.Labels[labelAgent] != agent.ID.String() || made.Labels[labelProject] != agent.ProjectID.String() {
		t.Errorf("session sandbox = %q, turn sandbox = %q, request = %+v", i.sandbox(session), given.Sandbox, made)
	}
	if state := i.sessionOf(session.ID).SandboxState; state != store.SandboxAwake {
		t.Errorf("recorded sandbox state = %q, want awake", state)
	}

	given.Tell("started", started)
	given.Tell("message.delta", map[string]string{"text": "hi"})
	given.Tell("finished", map[string]any{"stop_reason": "end_turn", "usage": map[string]int{"total_tokens": 7, "input_tokens": 5, "output_tokens": 2}})
	ended := i.ended(turn.ID)
	if ended.State != store.TurnCompleted || ended.StopReason == nil || *ended.StopReason != "end_turn" ||
		ended.Usage == nil || ended.Usage.TotalTokens != 7 || ended.StartedAt == nil {
		t.Errorf("ended turn = %+v", ended)
	}
	if told := i.told(turn.ID); !slices.Equal(told, []string{events.TurnStarted, "message.delta", events.TurnCompleted}) {
		t.Errorf("events = %v", told)
	}

	next := i.send(driver, session, "and then")
	again := i.service.Turn(t)
	if again.Sandbox != given.Sandbox || again.ID != next.ID.String() || len(i.service.Made()) != 1 {
		t.Errorf("next turn sandbox = %q, sandboxes = %v", again.Sandbox, i.service.Made())
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	i.ended(next.ID)

	// An agent without approval gets no AVIARY_APPROVAL.
	plain := i.session(store.Agent{})
	i.send(driver, plain, "hello")
	other := i.service.Turn(t)
	if _, asks := other.Environment["AVIARY_APPROVAL"]; asks || other.Sandbox == given.Sandbox || other.Approval != "" {
		t.Errorf("other session's turn = %+v", other)
	}
}

func TestASessionWhoseSandboxIsGoneGetsANewOne(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(first.ID)

	// The new sandbox gets the same name.
	i.service.Lose(given.Sandbox)
	second := i.send(driver, session, "are you there")
	again := i.service.Turn(t)
	if i.sandbox(session) != again.Sandbox || len(i.service.Made()) != 1 {
		t.Errorf("turn sandbox = %q, session sandbox = %q, sandboxes = %v", again.Sandbox, i.sandbox(session), i.service.Made())
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	if ended := i.ended(second.ID); ended.State != store.TurnCompleted {
		t.Errorf("ended turn = %+v", ended)
	}
}

func TestATurnWhoseSandboxIsLostFails(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	// The sandbox is gone, or is being deleted.
	for _, lose := range []func(string){i.service.Lose, i.service.Terminate} {
		session := i.session(store.Agent{})
		turn := i.send(driver, session, "hello")
		given := i.service.Turn(t)
		given.Tell("started", started)
		i.eventually("the turn has started", func() bool { return i.turn(turn.ID).StartedAt != nil })

		lose(given.Sandbox)
		given.Cut()
		if ended := i.ended(turn.ID); !failed(ended, store.FailureSandboxLost) {
			t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
		}
		if state := i.sessionOf(session.ID).SandboxState; state != store.SandboxLost {
			t.Errorf("recorded sandbox state = %q, want lost", state)
		}

		// The next turn gets a new sandbox.
		next := i.send(driver, session, "and then")
		again := i.service.Turn(t)
		if kept := i.sessionOf(session.ID); again.ID != next.ID.String() || kept.Sandbox != again.Sandbox || kept.SandboxState != store.SandboxAwake {
			t.Errorf("next turn = %+v, session sandbox = %q, recorded as %q", again, kept.Sandbox, kept.SandboxState)
		}
		if state := i.service.State(again.Sandbox); state != sandbox.Running {
			t.Errorf("state of the new sandbox = %s, want Running", state)
		}
	}
}

func TestAReadingThatIsCutOffGoesOn(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	turn := i.send(driver, i.session(store.Agent{}), "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("message.delta", map[string]string{"text": "one"})
	i.eventually("the events are recorded", func() bool { return len(i.told(turn.ID)) == 2 })
	given.Cut()
	given.Tell("message.delta", map[string]string{"text": "two"})
	given.Cut()
	given.Tell("finished", finished)
	if ended := i.ended(turn.ID); ended.State != store.TurnCompleted {
		t.Errorf("ended turn = %+v", ended)
	}
	want := []string{events.TurnStarted, "message.delta", "message.delta", events.TurnCompleted}
	if told := i.told(turn.ID); !slices.Equal(told, want) {
		t.Errorf("events = %v", told)
	}
}

func TestATurnThatDoesNotStartInTimeIsGivenUp(t *testing.T) {
	// The agent's harness is not configured.
	i := install(t, func(config *Config) {
		config.MaxStartup = 300 * time.Millisecond
		config.Harnesses = nil
	})
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	ended := i.ended(turn.ID)
	if !failed(ended, store.FailureStartup) || !strings.Contains(ended.Failure.Message, "harness opencode is not configured") {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
	if len(i.service.Made()) != 0 {
		t.Errorf("sandboxes = %v", i.service.Made())
	}

	// A harness that never starts the turn: its sandbox is put to sleep, and
	// the next turn wakes it.
	i = install(t, func(config *Config) { config.MaxStartup = 300 * time.Millisecond })
	driver, _ = i.supervisor()
	session = i.session(store.Agent{})
	turn = i.send(driver, session, "hello")
	given := i.service.Turn(t)
	if ended := i.ended(turn.ID); !failed(ended, store.FailureStartup) {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
	if i.service.State(given.Sandbox) != sandbox.Asleep || i.sandbox(session) != given.Sandbox {
		t.Errorf("sandbox state = %s, session sandbox = %q", i.service.State(given.Sandbox), i.sandbox(session))
	}
	next := i.send(driver, session, "again")
	again := i.service.Turn(t)
	if again.Sandbox != given.Sandbox || again.ID != next.ID.String() || i.service.Woken(given.Sandbox) != 1 {
		t.Errorf("next turn = %+v, sandbox woken %d times", again, i.service.Woken(given.Sandbox))
	}
}

func TestATurnThatRunsForTooLongIsGivenUp(t *testing.T) {
	i := install(t, func(config *Config) { config.MaxTurn = 300 * time.Millisecond })
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	ended := i.ended(turn.ID)
	if !failed(ended, store.FailureTimedOut) || !strings.Contains(ended.Failure.Message, "300ms") {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
	// The sandbox is put to sleep, and the session keeps it.
	if i.service.State(given.Sandbox) != sandbox.Asleep || i.sandbox(session) != given.Sandbox {
		t.Errorf("sandbox state = %s, session sandbox = %q", i.service.State(given.Sandbox), i.sandbox(session))
	}
	if told := i.told(turn.ID); !slices.Equal(told, []string{events.TurnStarted, events.TurnFailed}) {
		t.Errorf("events = %v", told)
	}
}

func TestATurnThatWaitsForAPersonHasNoMoreTimeThanAnyOther(t *testing.T) {
	i := install(t, func(config *Config) { config.MaxTurn = 300 * time.Millisecond })
	driver, _ := i.supervisor()
	turn := i.send(driver, i.session(store.Agent{Approval: Ask}), "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("approval.requested", asks)
	if ended := i.ended(turn.ID); !failed(ended, store.FailureTimedOut) {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
}

// cancel cancels the turn as the API does, with or without announcing it.
func (i *installation) cancel(turn uuid.UUID, announce bool) {
	i.t.Helper()
	if _, _, err := i.store.CancelTurn(ctx, turn); err != nil {
		i.t.Fatal(err)
	}
	if announce {
		if err := i.events.AnnounceCancel(ctx, turn); err != nil {
			i.t.Fatal(err)
		}
	}
}

func TestATurnIsToldToStop(t *testing.T) {
	// The cancellation is announced, or found by a sweep.
	for _, announce := range []bool{true, false} {
		i := install(t)
		driver, _ := i.supervisor()
		turn := i.send(driver, i.session(store.Agent{}), "hello")
		given := i.service.Turn(t)
		given.Tell("started", started)
		i.cancel(turn.ID, announce)
		given.Stopped(t)
		given.Tell("finished", map[string]string{"stop_reason": "cancelled"})
		if ended := i.ended(turn.ID); ended.State != store.TurnCancelled {
			t.Errorf("ended turn = %+v", ended)
		}
		if i.service.State(given.Sandbox) != sandbox.Running {
			t.Errorf("sandbox state = %s, want Running", i.service.State(given.Sandbox))
		}
	}
}

func TestATurnThatDoesNotStopIsStopped(t *testing.T) {
	i := install(t, func(config *Config) { config.CancelGrace = 200 * time.Millisecond })
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	i.cancel(turn.ID, true)
	given.Stopped(t)
	if ended := i.ended(turn.ID); ended.State != store.TurnCancelled {
		t.Errorf("ended turn = %+v", ended)
	}
	if i.service.State(given.Sandbox) != sandbox.Asleep || i.sandbox(session) != given.Sandbox {
		t.Errorf("sandbox state = %s, session sandbox = %q", i.service.State(given.Sandbox), i.sandbox(session))
	}
}

func TestATurnThatIsAskedToStopBeforeItsRunnerHasItIsNotRun(t *testing.T) {
	// The sandbox stays starting.
	i := install(t)
	i.service.Slow(true)
	driver, _ := i.supervisor()
	turn := i.send(driver, i.session(store.Agent{}), "hello")
	i.eventually("the turn is followed", func() bool { return i.turn(turn.ID).State == store.TurnRunning })
	i.cancel(turn.ID, true)
	if ended := i.ended(turn.ID); ended.State != store.TurnCancelled {
		t.Errorf("ended turn = %+v", ended)
	}
}

func TestAnotherSupervisorTakesUpATurn(t *testing.T) {
	i := install(t)
	first, leave := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(first, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("message.delta", map[string]string{"text": "one"})
	i.eventually("the events are recorded", func() bool { return len(i.told(turn.ID)) == 2 })

	// While no Supervisor follows the turn, the runner sends another event
	// and the turn is cancelled.
	leave()
	given.Tell("message.delta", map[string]string{"text": "two"})
	i.cancel(turn.ID, false)
	// The next Supervisor records the event and passes the cancellation on.
	second, _ := i.supervisor()
	i.eventually("the second supervisor records the event", func() bool { return len(i.told(turn.ID)) == 3 })
	given.Stopped(t)
	given.Tell("finished", map[string]string{"stop_reason": "cancelled"})
	if ended := i.ended(turn.ID); ended.State != store.TurnCancelled {
		t.Errorf("ended turn = %+v", ended)
	}
	want := []string{events.TurnStarted, "message.delta", "message.delta", events.TurnCancelled}
	if told := i.told(turn.ID); !slices.Equal(told, want) {
		t.Errorf("events = %v", told)
	}
	// The turn was given once, and the session has one sandbox.
	if len(i.service.Made()) != 1 {
		t.Errorf("sandboxes = %v", i.service.Made())
	}
	next := i.send(second, session, "and then")
	if again := i.service.Turn(t); again.ID != next.ID.String() || again.Sandbox != given.Sandbox {
		t.Errorf("next turn = %+v", again)
	}
}

func TestATurnThatItsRunnerHasLostIsNotRunAgain(t *testing.T) {
	i := install(t)
	first, leave := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(first, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	i.eventually("the turn has started", func() bool { return i.turn(turn.ID).StartedAt != nil })
	leave()

	// The runner restarts while no Supervisor follows the turn.
	i.service.Restart(given.Sandbox)
	i.supervisor()
	ended := i.ended(turn.ID)
	if !failed(ended, store.FailureRunner) || !strings.Contains(ended.Failure.Message, "no longer has the turn") {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
	// The sandbox is kept, with its volume.
	if i.sandbox(session) != given.Sandbox || len(i.service.Made()) != 1 {
		t.Errorf("session sandbox = %q, sandboxes = %v", i.sandbox(session), i.service.Made())
	}
}

func TestARunnerThatRunsAnotherTurnIsStoppedWithItsPod(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(first.ID)

	// The runner runs a turn that the store does not know.
	seen, err := i.service.Sandbox(ctx, given.Sandbox)
	if err != nil {
		t.Fatal(err)
	}
	if err := sandbox.RunnerAt(seen.Address).Start(ctx, uuid.New(), sandbox.Turn{Text: "another"}); err != nil {
		t.Fatal(err)
	}
	i.service.Turn(t)
	next := i.send(driver, session, "and then")
	if ended := i.ended(next.ID); !failed(ended, store.FailureRunner) || !strings.Contains(ended.Failure.Message, "running another turn") {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
	// Putting the sandbox to sleep stops the other turn with the pod.
	kept := i.sessionOf(session.ID)
	if i.service.State(given.Sandbox) != sandbox.Asleep || kept.Sandbox != given.Sandbox || kept.SandboxState != store.SandboxAsleep {
		t.Errorf("sandbox state = %s, session sandbox = %q, recorded as %q", i.service.State(given.Sandbox), kept.Sandbox, kept.SandboxState)
	}
}

func TestTwoSupervisorsMakeOneSandboxForASession(t *testing.T) {
	i := install(t)
	driver := New(i.config, i.service, i.store, i.events, metrics.New(nil, nil))
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	if _, err := i.store.DispatchTurns(ctx, 1); err != nil {
		t.Fatal(err)
	}
	followed, err := i.store.FollowTurn(ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Both find the session without a sandbox and create one.
	var made [2]string
	for n := range made {
		if made[n], err = driver.provide(ctx, &run{turn: followed}); err != nil {
			t.Fatal(err)
		}
	}
	if made[0] != made[1] || i.sandbox(session) != made[0] {
		t.Errorf("provided sandboxes = %v, session sandbox = %q", made, i.sandbox(session))
	}
	if sandboxes := i.service.Made(); len(sandboxes) != 1 || sandboxes[made[0]].Session != session.ID {
		t.Errorf("sandboxes = %v", sandboxes)
	}
}

func TestWhatARunnerTellsIsNotTakenAtItsWord(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	// An event that only the Supervisor may send, and an unknown one.
	given.Tell(events.TurnCompleted, map[string]string{"stop_reason": "end_turn"})
	given.Tell("something.new", map[string]string{})
	given.Tell("message.delta", map[string]string{"text": "hi"})
	i.eventually("the events are recorded", func() bool { return len(i.told(turn.ID)) == 2 })
	if state := i.turn(turn.ID).State; state != store.TurnRunning {
		t.Errorf("turn state = %s, want running", state)
	}
	// A failure reason that a runner may not report.
	given.Tell("failed", map[string]string{"reason": "timed_out", "message": "no"})
	ended := i.ended(turn.ID)
	if !failed(ended, store.FailureRunner) || ended.Failure.Message != "no" {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
	if told := i.told(turn.ID); !slices.Equal(told, []string{events.TurnStarted, "message.delta", events.TurnFailed}) {
		t.Errorf("events = %v", told)
	}
}

func TestASessionThatSleepsIsWokenForItsNextTurn(t *testing.T) {
	i := install(t)
	driver, stop := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(first.ID)
	if err := i.service.Suspend(ctx, given.Sandbox, ""); err != nil {
		t.Fatal(err)
	}

	// The harness is upgraded while the session sleeps, and the sandbox
	// wakes with the new image.
	stop()
	i.config.Harnesses = map[string]sandbox.Harness{"opencode": {Image: "registry.example.com/opencode:2.0"}}
	driver, _ = i.supervisor()
	next := i.send(driver, session, "and then")
	again := i.service.Turn(t)
	if again.ID != next.ID.String() || again.Sandbox != given.Sandbox || i.service.Woken(given.Sandbox) != 1 {
		t.Errorf("next turn = %+v, sandbox woken %d times", again, i.service.Woken(given.Sandbox))
	}
	if image := i.service.Made()[given.Sandbox].Pod.Harness.Image; image != "registry.example.com/opencode:2.0" {
		t.Errorf("woken sandbox image = %s", image)
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	if ended := i.ended(next.ID); ended.State != store.TurnCompleted {
		t.Errorf("ended turn = %+v", ended)
	}
}

func TestASandboxThatIsNotWokenInTimeStaysWithItsSession(t *testing.T) {
	i := install(t, func(config *Config) { config.MaxStartup = time.Second })
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(first.ID)

	// The sandbox wakes slowly, as on a node without the image. The turn
	// never reaches it, so it is kept.
	if err := i.service.Suspend(ctx, given.Sandbox, ""); err != nil {
		t.Fatal(err)
	}
	i.service.Slow(true)
	late := i.send(driver, session, "and then")
	if ended := i.ended(late.ID); !failed(ended, store.FailureStartup) {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
	if i.sandbox(session) != given.Sandbox || len(i.service.Made()) != 1 {
		t.Errorf("sandboxes = %v, session sandbox = %q", i.service.Made(), i.sandbox(session))
	}

	i.service.Slow(false)
	next := i.send(driver, session, "again")
	if again := i.service.Turn(t); again.ID != next.ID.String() || again.Sandbox != given.Sandbox {
		t.Errorf("next turn = %+v", again)
	}
}

func TestASessionThatIsNotAtWorkIsPutToSleep(t *testing.T) {
	i := install(t, func(config *Config) {
		config.Idle = 300 * time.Millisecond
		// Sandboxes are looked at only every few sweeps.
		config.Sweep = 20 * time.Millisecond
	})
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	// A session with a running turn is not idle, however long the turn runs.
	given.Tell("started", started)
	time.Sleep(3 * i.config.Idle)
	if state := i.service.State(given.Sandbox); state != sandbox.Running {
		t.Fatalf("sandbox state during the turn = %s, want Running", state)
	}
	given.Tell("finished", finished)
	ended := i.ended(first.ID)
	i.eventually("the idle sandbox is put to sleep", func() bool {
		return i.service.State(given.Sandbox) == sandbox.Asleep
	})
	if idle := time.Since(*ended.EndedAt); idle < i.config.Idle {
		t.Errorf("sandbox put to sleep after only %s", idle)
	}
	i.eventually("the session records that its sandbox is asleep", func() bool {
		return i.sessionOf(session.ID).SandboxState == store.SandboxAsleep
	})

	next := i.send(driver, session, "and then")
	again := i.service.Turn(t)
	if again.ID != next.ID.String() || again.Sandbox != given.Sandbox || i.service.Woken(given.Sandbox) != 1 {
		t.Errorf("next turn = %+v, sandbox woken %d times", again, i.service.Woken(given.Sandbox))
	}
	if state := i.sessionOf(session.ID).SandboxState; state != store.SandboxAwake {
		t.Errorf("recorded state of the woken sandbox = %q, want awake", state)
	}
}

func TestATurnThatComesAsItsSessionIsPutToSleepWakesIt(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(first.ID)

	// Another Supervisor puts the idle sandbox to sleep after the next turn
	// read it, but before the turn is given to it. The turn wakes it and
	// runs.
	i.service.SleepBeforeKeep(given.Sandbox)
	next := i.send(driver, session, "and then")
	again := i.service.Turn(t)
	if again.ID != next.ID.String() || i.service.Woken(given.Sandbox) != 1 {
		t.Errorf("turn = %+v, sandbox woken %d times", again, i.service.Woken(given.Sandbox))
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	if ended := i.ended(next.ID); ended.State != store.TurnCompleted {
		t.Errorf("ended turn = %+v", ended)
	}
}

// earlier is a sandbox service whose list was read before the sandboxes
// changed.
type earlier struct {
	*sandboxtest.Service
	listed []sandbox.Sandbox
}

func (e earlier) Sandboxes(context.Context) ([]sandbox.Sandbox, error) {
	return e.listed, nil
}

// A session records whether its sandbox is awake or asleep: what a Supervisor
// does to the sandbox, and what it finds that something else has done.
func TestASessionRecordsWhetherItsSandboxIsAsleep(t *testing.T) {
	i := install(t)
	driver := New(i.config, i.service, i.store, i.events, metrics.New(nil, nil))
	session := i.session(store.Agent{})
	name := sandbox.Name(session.ID)
	if err := i.service.Create(ctx, sandbox.Request{Session: session.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := i.store.SetSandbox(ctx, session.ID, "", name); err != nil {
		t.Fatal(err)
	}
	recorded := func(after, state string) {
		t.Helper()
		if kept := i.sessionOf(session.ID).SandboxState; kept != state {
			t.Errorf("after %s: recorded sandbox state = %q, want %q", after, kept, state)
		}
	}
	sleep := func() {
		t.Helper()
		if err := i.service.Suspend(ctx, name, ""); err != nil {
			t.Fatal(err)
		}
	}

	// Something else puts the sandbox to sleep and wakes it.
	sleep()
	driver.look(ctx)
	recorded("something else put the sandbox to sleep", store.SandboxAsleep)
	if err := i.service.Resume(ctx, name, sandbox.Pod{}); err != nil {
		t.Fatal(err)
	}
	driver.look(ctx)
	recorded("something else woke the sandbox", store.SandboxAwake)

	// Another Supervisor puts the sandbox to sleep after this one has listed
	// the sandboxes. What the session records since then is kept.
	listed, err := i.service.Sandboxes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sleep()
	if err := i.store.SetSandboxState(ctx, session.ID, name, store.SandboxAsleep); err != nil {
		t.Fatal(err)
	}
	New(i.config, earlier{i.service, listed}, i.store, i.events, metrics.New(nil, nil)).look(ctx)
	recorded("a look at sandboxes that were listed earlier", store.SandboxAsleep)

	// A turn wakes the sandbox, and giving the turn up puts it to sleep.
	turn := i.send(driver, session, "hello")
	if _, err := i.store.DispatchTurns(ctx, 1); err != nil {
		t.Fatal(err)
	}
	followed, err := i.store.FollowTurn(ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := &run{turn: followed, sandbox: followed.Sandbox}
	if runner, err := driver.attempt(ctx, ctx, r, false); runner != nil || err == nil {
		t.Fatalf("attempt on a sleeping sandbox = %v, %v, want it to wait for the sandbox", runner, err)
	}
	recorded("a turn woke the sandbox", store.SandboxAwake)
	r.give()
	driver.giveUp(ctx, r, store.Failure{Reason: store.FailureRunner})
	recorded("the turn was given up", store.SandboxAsleep)
	if state := i.service.State(name); state != sandbox.Asleep {
		t.Errorf("sandbox state after the turn was given up = %s, want Asleep", state)
	}

	// The sandbox of an idle session is put to sleep.
	if err := i.service.Resume(ctx, name, sandbox.Pod{}); err != nil {
		t.Fatal(err)
	}
	driver.look(ctx)
	recorded("something else woke the sandbox again", store.SandboxAwake)
	i.store.Age(time.Hour, turn.ID)
	i.config.Idle = time.Minute
	New(i.config, i.service, i.store, i.events, metrics.New(nil, nil)).look(ctx)
	recorded("the idle sandbox was put to sleep", store.SandboxAsleep)
	if state := i.service.State(name); state != sandbox.Asleep {
		t.Errorf("state of the idle sandbox = %s, want Asleep", state)
	}
}

// A session whose sandbox is gone records that it is lost, unless the session
// has a turn that has not ended.
func TestASessionRecordsThatItsSandboxIsLost(t *testing.T) {
	i := install(t)
	driver := New(i.config, i.service, i.store, i.events, metrics.New(nil, nil))
	session := i.session(store.Agent{})
	name := sandbox.Name(session.ID)
	create := func() {
		t.Helper()
		if err := i.service.Create(ctx, sandbox.Request{Session: session.ID}); err != nil {
			t.Fatal(err)
		}
	}
	recorded := func(after, state string) {
		t.Helper()
		if kept := i.sessionOf(session.ID); kept.Sandbox != name || kept.SandboxState != state {
			t.Errorf("after %s: session sandbox = %q, recorded as %q, want %q recorded as %q", after, kept.Sandbox, kept.SandboxState, name, state)
		}
	}
	create()
	if _, err := i.store.SetSandbox(ctx, session.ID, "", name); err != nil {
		t.Fatal(err)
	}

	// The sandbox was created after this Supervisor listed the sandboxes.
	New(i.config, earlier{i.service, nil}, i.store, i.events, metrics.New(nil, nil)).look(ctx)
	recorded("a look at sandboxes that were listed before the session's was created", store.SandboxAwake)

	// A queued turn has not ended.
	turn := i.send(driver, session, "hello")
	i.service.Lose(name)
	driver.look(ctx)
	recorded("the sandbox was deleted while a turn had not ended", store.SandboxAwake)
	i.cancel(turn.ID, false)
	driver.look(ctx)
	recorded("the sandbox was deleted", store.SandboxLost)
	// A sandbox that is recorded as it is found is not read again.
	settled := New(i.config, unread{i.service, t}, i.store, i.events, metrics.New(nil, nil))
	settled.look(ctx)

	// The sandbox is there again.
	create()
	driver.look(ctx)
	recorded("the sandbox was created again", store.SandboxAwake)
	settled.look(ctx)

	// A turn is given up for another reason when its sandbox is gone.
	turn = i.send(driver, session, "and then")
	if _, err := i.store.DispatchTurns(ctx, 1); err != nil {
		t.Fatal(err)
	}
	followed, err := i.store.FollowTurn(ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := &run{turn: followed, sandbox: followed.Sandbox}
	r.give()
	i.service.Lose(name)
	driver.giveUp(ctx, r, store.Failure{Reason: store.FailureTimedOut})
	recorded("a turn was given up without its sandbox", store.SandboxLost)
	if ended := i.turn(turn.ID); !failed(ended, store.FailureTimedOut) {
		t.Errorf("turn given up without its sandbox = %+v, failure = %+v", ended, ended.Failure)
	}

	create()
	driver.look(ctx)
	i.service.Terminate(name)
	driver.look(ctx)
	recorded("the sandbox began to be deleted", store.SandboxLost)
}

// unread is a sandbox service that fails the test if a single sandbox is
// read.
type unread struct {
	*sandboxtest.Service
	t *testing.T
}

func (u unread) Sandbox(_ context.Context, name string) (sandbox.Sandbox, error) {
	u.t.Errorf("sandbox %s was read", name)
	return sandbox.Sandbox{}, errors.New("not to be read")
}

// stuck is a sandbox service that cannot delete sandboxes.
type stuck struct {
	*sandboxtest.Service
}

func (stuck) Delete(context.Context, string) error {
	return errors.New("the cluster does not answer")
}

func TestTheSandboxOfASessionThatHasEndedIsDeleted(t *testing.T) {
	i := install(t)
	driver := New(i.config, i.service, i.store, i.events, metrics.New(nil, nil))
	// housed returns a new session with a sandbox.
	housed := func() (store.Session, string) {
		t.Helper()
		session := i.session(store.Agent{})
		if err := i.service.Create(ctx, sandbox.Request{Session: session.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := i.store.SetSandbox(ctx, session.ID, "", sandbox.Name(session.ID)); err != nil {
			t.Fatal(err)
		}
		return session, sandbox.Name(session.ID)
	}
	there := func(name string) bool {
		_, there := i.service.Made()[name]
		return there
	}

	// The sandbox cannot be deleted when the session is ended, nor later.
	session, name := housed()
	failing := New(i.config, stuck{i.service}, i.store, i.events, metrics.New(nil, nil))
	if ended, err := failing.EndSession(ctx, session.ID); err == nil || ended.EndedAt == nil {
		t.Fatalf("EndSession = %+v, %v, want the ended session and an error", ended, err)
	}
	failing.look(ctx)
	if kept := i.sessionOf(session.ID); kept.EndedAt == nil || kept.Sandbox != name || !there(name) {
		t.Fatalf("ended session = %+v, its sandbox is there: %t; want it to keep the sandbox that was not deleted", kept, there(name))
	}
	driver.look(ctx)
	if kept := i.sandbox(session); kept != "" || there(name) {
		t.Errorf("after a look, the ended session has sandbox %q, and the sandbox is there: %t; want neither", kept, there(name))
	}

	// The sandbox was deleted, and the session still records it.
	session, name = housed()
	if _, err := i.store.EndSession(ctx, session.ID, ""); err != nil {
		t.Fatal(err)
	}
	i.service.Lose(name)
	driver.look(ctx)
	if kept := i.sessionOf(session.ID); kept.Sandbox != "" || kept.SandboxState != "" {
		t.Errorf("ended session whose sandbox is gone has sandbox %q, recorded as %q; want none", kept.Sandbox, kept.SandboxState)
	}

	// The sandbox of a session that has not ended is kept.
	session, name = housed()
	driver.look(ctx)
	if kept := i.sandbox(session); kept != name || !there(name) {
		t.Errorf("session that has not ended has sandbox %q, and the sandbox is there: %t; want it kept", kept, there(name))
	}
}

// asks is the data of an approval.requested event.
var asks = map[string]any{"id": "1", "options": []map[string]string{{"id": "once", "kind": "allow_once", "name": "Once"}}}

func TestATurnThatWaitsForAPersonGoesOnWhenItIsToldSomething(t *testing.T) {
	// The request is answered, or the turn is cancelled.
	for _, answered := range []bool{true, false} {
		i := install(t)
		driver, _ := i.supervisor()
		turn := i.send(driver, i.session(store.Agent{Approval: Ask}), "hello")
		given := i.service.Turn(t)
		given.Tell("started", started)
		given.Tell("approval.requested", asks)
		i.eventually("the request is recorded", func() bool { return len(i.told(turn.ID)) == 2 })

		if answered {
			if kept, err := i.store.AnswerApproval(ctx, turn.ID, "1", "once", uuid.Nil); err != nil || !kept {
				t.Fatalf("AnswerApproval = %t, %v", kept, err)
			}
			if err := i.events.AnnounceAnswer(ctx, turn.ID); err != nil {
				t.Fatal(err)
			}
			if request, option := given.Answered(t); request != "1" || option != "once" {
				t.Errorf("answered request %q with option %q", request, option)
			}
			given.Tell("approval.resolved", map[string]string{"id": "1", "outcome": "selected", "option_id": "once"})
			given.Tell("finished", finished)
		} else {
			i.cancel(turn.ID, true)
			given.Stopped(t)
			given.Tell("approval.resolved", map[string]string{"id": "1", "outcome": "cancelled"})
			given.Tell("finished", map[string]string{"stop_reason": "cancelled"})
		}
		ended := i.ended(turn.ID)
		if answered != (ended.State == store.TurnCompleted) || !answered != (ended.State == store.TurnCancelled) {
			t.Errorf("ended turn = %+v", ended)
		}
		want := []string{events.TurnStarted, "approval.requested", "approval.resolved", events.TurnCompleted}
		if !answered {
			want[3] = events.TurnCancelled
		}
		if told := i.told(turn.ID); !slices.Equal(told, want) {
			t.Errorf("events = %v", told)
		}
	}
}

func TestASandboxThatItsSessionDoesNotHaveIsDeleted(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(turn.ID)

	// stray creates a session's sandbox as a Supervisor that stops before
	// recording it would.
	stray := func(session uuid.UUID) string {
		if err := i.service.Create(ctx, sandbox.Request{Session: session}); err != nil {
			t.Fatal(err)
		}
		return sandbox.Name(session)
	}
	left, young := stray(i.session(store.Agent{}).ID), stray(i.session(store.Agent{}).ID)
	// The sandbox of a session that the store does not know.
	foreign := stray(uuid.New())
	for _, name := range []string{given.Sandbox, left, foreign} {
		i.service.Age(name, time.Hour)
	}
	i.eventually("the orphaned sandbox is deleted", func() bool {
		_, there := i.service.Made()[left]
		return !there
	})
	made := i.service.Made()
	for what, name := range map[string]string{"the session's sandbox": given.Sandbox, "the young sandbox": young, "the unknown session's sandbox": foreign} {
		if _, there := made[name]; !there {
			t.Errorf("%s was deleted", what)
		}
	}

	// A session got its sandbox after a Supervisor read the sessions.
	New(i.config, i.service, before{Store: i.store}, i.events, metrics.New(nil, nil)).look(ctx)
	if _, there := i.service.Made()[given.Sandbox]; !there {
		t.Error("the sandbox that its session got after the sessions were read was deleted")
	}
}

// before is a store whose sessions with a sandbox were read before any
// session had one.
type before struct {
	store.Store
}

func (before) SessionsWithSandbox(context.Context) ([]store.Session, error) {
	return nil, nil
}

func TestATurnWhoseSandboxIsPutToSleepFails(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	i.eventually("the turn has started", func() bool { return i.turn(turn.ID).StartedAt != nil })

	if err := i.service.Suspend(ctx, given.Sandbox, ""); err != nil {
		t.Fatal(err)
	}
	ended := i.ended(turn.ID)
	if !failed(ended, store.FailureRunner) || !strings.Contains(ended.Failure.Message, "put to sleep") {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
	// The session continues in the same sandbox.
	next := i.send(driver, session, "and then")
	if again := i.service.Turn(t); again.ID != next.ID.String() || again.Sandbox != given.Sandbox {
		t.Errorf("next turn = %+v", again)
	}
}

func TestASessionThatIsNotUsedForLongerThanItsSandboxIsKeptHasEnded(t *testing.T) {
	i := install(t, func(config *Config) { config.Retention = time.Hour })
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	// The sandbox expires Retention after a turn is given to it.
	if left := time.Until(i.service.Expires(given.Sandbox)); left < 59*time.Minute || left > time.Hour {
		t.Errorf("sandbox expires in %s, want about an hour", left)
	}
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(first.ID)

	// If the sandbox is lost sooner, the session continues in a new one.
	i.service.Lose(given.Sandbox)
	second := i.send(driver, session, "are you there")
	again := i.service.Turn(t)
	if again.ID != second.ID.String() {
		t.Errorf("turn = %+v", again)
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	i.ended(second.ID)

	// Two hours later, the sandbox has expired and is gone.
	i.store.Age(2*time.Hour, first.ID, second.ID)
	i.service.Lose(again.Sandbox)
	third := i.send(driver, session, "and now")
	ended := i.ended(third.ID)
	if !failed(ended, store.FailureSessionExpired) || !strings.Contains(ended.Failure.Message, "1h0m0s") {
		t.Errorf("ended turn = %+v, failure = %+v", ended, ended.Failure)
	}
	i.eventually("the session has ended", func() bool { return i.sessionOf(session.ID).EndedAt != nil })
	if reason := i.sessionOf(session.ID).EndReason; reason != store.EndExpired {
		t.Errorf("end reason of the session = %q, want expired", reason)
	}
	if _, err := driver.Accept(ctx, session.ID, uuid.Nil, "once more"); !errors.Is(err, store.ErrSessionEnded) {
		t.Errorf("Accept on an ended session = %v, want ErrSessionEnded", err)
	}
	if len(i.service.Made()) != 0 || len(i.service.Given()) != 2 {
		t.Errorf("sandboxes = %v, given turns = %v", i.service.Made(), i.service.Given())
	}
}

// A session whose sandbox has expired ends at the next look at the sandboxes,
// without a message to it.
func TestASessionWhoseSandboxHasExpiredEnds(t *testing.T) {
	i := install(t, func(config *Config) { config.Retention = time.Hour })
	// ran returns a session that has run a turn, with the turn and the name of
	// the sandbox.
	ran := func() (store.Session, uuid.UUID, string) {
		t.Helper()
		driver, stop := i.supervisor()
		defer stop()
		session := i.session(store.Agent{})
		turn := i.send(driver, session, "hello")
		given := i.service.Turn(t)
		given.Tell("started", started)
		given.Tell("finished", finished)
		i.ended(turn.ID)
		return session, turn.ID, given.Sandbox
	}
	driver := New(i.config, i.service, i.store, i.events, metrics.New(nil, nil))
	expired := func(session store.Session) bool {
		kept := i.sessionOf(session.ID)
		return kept.EndedAt != nil && kept.EndReason == store.EndExpired && kept.Sandbox == ""
	}

	// The sandbox is gone at the time until which it was kept.
	session, turn, name := ran()
	i.store.Age(time.Until(i.service.Expires(name)), turn)
	i.service.Lose(name)
	driver.look(ctx)
	if !expired(session) {
		t.Errorf("session whose sandbox expired = %+v, want it ended as expired, without a sandbox", i.sessionOf(session.ID))
	}

	// A sandbox that is gone sooner is lost. Its session ends once it has not
	// been used for as long as a sandbox is kept.
	session, turn, name = ran()
	i.service.Lose(name)
	driver.look(ctx)
	if kept := i.sessionOf(session.ID); kept.EndedAt != nil || kept.SandboxState != store.SandboxLost {
		t.Errorf("session whose sandbox is gone early = %+v, want it to go on with its sandbox recorded as lost", kept)
	}
	New(i.config, unread{i.service, t}, i.store, i.events, metrics.New(nil, nil)).look(ctx)
	i.store.Age(2*time.Hour, turn)
	driver.look(ctx)
	if !expired(session) {
		t.Errorf("unused session whose sandbox is lost = %+v, want it ended as expired, without a sandbox", i.sessionOf(session.ID))
	}

	// A session whose sandbox is still there goes on, also if the sandbox
	// was not listed.
	session, turn, name = ran()
	i.store.Age(2*time.Hour, turn)
	driver.look(ctx)
	New(i.config, earlier{i.service, nil}, i.store, i.events, metrics.New(nil, nil)).look(ctx)
	if kept := i.sessionOf(session.ID); kept.EndedAt != nil || kept.Sandbox != name {
		t.Errorf("unused session whose sandbox is there = %+v, want it to go on in sandbox %s", kept, name)
	}
}

func TestAHarnessThatAnInstallationHas(t *testing.T) {
	i := install(t)
	driver := New(i.config, i.service, i.store, i.events, metrics.New(nil, nil))
	if !driver.HasHarness("opencode") || driver.HasHarness("codex") {
		t.Error("HasHarness does not match the configured harnesses")
	}
}
