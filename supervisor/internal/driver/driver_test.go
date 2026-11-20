package driver

import (
	"context"
	"encoding/json"
	"errors"
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

// The tests of this file run drivers on records in memory and a sandbox
// service whose runners the test plays.

var ctx = context.Background()

type installation struct {
	t         *testing.T
	store     *memorystore.Store
	events    *events.Streams
	service   *sandboxtest.Service
	sandboxes *sandbox.Client
	config    Config
}

// install sets an installation up whose release has built the template of
// its harness.
func install(t *testing.T, changes ...func(*Config)) *installation {
	t.Helper()
	i := &installation{t: t, store: memorystore.New(), events: events.New(memorylog.New()), service: sandboxtest.New(t)}
	var err error
	if i.sandboxes, err = sandbox.New(i.service.URL, sandboxtest.Key); err != nil {
		t.Fatal(err)
	}
	i.config = Config{
		Templates: sandbox.Templates{
			Harnesses: map[string]sandbox.Harness{
				"opencode": {Image: "registry.example.com/opencode:1.0", Sandbox: sandbox.Size{CPU: "1", Memory: "2Gi", Disk: "4Gi"}},
			},
			Store: "s3://sandboxes/templates",
			Poll:  5 * time.Millisecond,
		},
		MaxConcurrentTurns: 50,
		MaxStartup:         time.Minute,
		MaxTurn:            time.Hour,
		Sweep:              50 * time.Millisecond,
	}
	if err := i.sandboxes.Bake(ctx, i.config.Templates); err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		change(&i.config)
	}
	return i
}

// supervisor starts a driver of the installation, and returns it with what
// stops it: from then on it dispatches no turn and sweeps no more. It stops
// with the test at the latest.
func (i *installation) supervisor() (*Driver, func()) {
	running, stop := context.WithCancel(ctx)
	i.t.Cleanup(stop)
	driver := New(i.config, i.sandboxes, i.store, i.events, metrics.New())
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

// session makes a session of a new agent.
func (i *installation) session(agent store.Agent) store.Session {
	i.t.Helper()
	project, err := i.store.CreateProject(ctx, "test")
	if err != nil {
		i.t.Fatal(err)
	}
	agent.Harness = "opencode"
	if agent.Model != nil {
		provider, err := i.store.CreateProvider(ctx, store.Provider{ProjectID: project.ID, Name: "models", URL: "https://models.example.com/v1/"})
		if err != nil {
			i.t.Fatal(err)
		}
		agent.Model.Provider = provider.ID
	}
	made, err := i.store.CreateAgent(ctx, project.ID, agent)
	if err != nil {
		i.t.Fatal(err)
	}
	session, err := i.store.CreateSession(ctx, made.ID, uuid.Nil)
	if err != nil {
		i.t.Fatal(err)
	}
	return session
}

// sessionOf returns the session with the ID.
func (i *installation) sessionOf(id uuid.UUID) store.Session {
	i.t.Helper()
	session, err := i.store.Session(ctx, id)
	if err != nil {
		i.t.Fatal(err)
	}
	return session
}

// send accepts a message to the session as its next turn.
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

// sandbox returns the sandbox that the session has, or nothing.
func (i *installation) sandbox(session store.Session) string {
	i.t.Helper()
	session, err := i.store.Session(ctx, session.ID)
	if err != nil {
		i.t.Fatal(err)
	}
	return session.Sandbox
}

// told returns the types of the events that the clients of the turn read.
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

// ended waits for the turn to end, and returns it.
func (i *installation) ended(id uuid.UUID) store.Turn {
	i.t.Helper()
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if turn := i.turn(id); turn.EndedAt != nil {
			return turn
		}
		if time.Now().After(end) {
			i.t.Fatalf("the turn did not end: %v", i.told(id))
		}
	}
}

// eventually waits for what holds to hold.
func (i *installation) eventually(what string, holds func() bool) {
	i.t.Helper()
	for end := time.Now().Add(5 * time.Second); !holds(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			i.t.Fatalf("it did not come about that %s", what)
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
	session := i.session(store.Agent{Instructions: "be kind", Approval: Ask, Model: &store.Model{Name: "a-model"}})
	turn := i.send(driver, session, "hello")

	given := i.service.Turn(t)
	if given.ID != turn.ID.String() || given.Text != "hello" || given.Instructions != "be kind" || given.Approval != Ask {
		t.Errorf("the runner was given %+v", given)
	}
	want := map[string]string{
		"AVIARY_LLM_BASE_URL": "https://models.example.com/v1",
		"AVIARY_LLM_MODEL":    "a-model",
		"AVIARY_LLM_API_KEY":  "none",
		"AVIARY_APPROVAL":     Ask,
	}
	for name, value := range want {
		if given.Environment[name] != value {
			t.Errorf("the harness is started with %s=%q, and not %q", name, given.Environment[name], value)
		}
	}
	if len(given.Environment) != len(want) {
		t.Errorf("the harness is started with %v", given.Environment)
	}
	// The sandbox is that of the session, and says whose it is.
	agent, _ := i.store.Agent(ctx, session.AgentID)
	metadata := i.service.Sandboxes()[given.Sandbox]
	if i.sandbox(session) != given.Sandbox || metadata[metadataSession] != session.ID.String() ||
		metadata[metadataAgent] != agent.ID.String() || metadata[metadataProject] != agent.ProjectID.String() {
		t.Errorf("the session has the sandbox %q, and the turn runs in %q of %v", i.sandbox(session), given.Sandbox, metadata)
	}

	given.Tell("started", started)
	given.Tell("message.delta", map[string]string{"text": "hi"})
	given.Tell("finished", map[string]any{"stop_reason": "end_turn", "usage": map[string]int{"total_tokens": 7, "input_tokens": 5, "output_tokens": 2}})
	ended := i.ended(turn.ID)
	if ended.State != store.TurnCompleted || ended.StopReason == nil || *ended.StopReason != "end_turn" ||
		ended.Usage == nil || ended.Usage.TotalTokens != 7 || ended.StartedAt == nil {
		t.Errorf("the turn ended as %+v", ended)
	}
	if told := i.told(turn.ID); !slices.Equal(told, []string{events.TurnStarted, "message.delta", events.TurnCompleted}) {
		t.Errorf("the clients of the turn read %v", told)
	}

	// The next turn of the session runs in the same sandbox, and an agent
	// that names no model and asks nobody is started with nothing.
	next := i.send(driver, session, "and then")
	again := i.service.Turn(t)
	if again.Sandbox != given.Sandbox || again.ID != next.ID.String() || len(i.service.Sandboxes()) != 1 {
		t.Errorf("the next turn runs in %q, of the sandboxes %v", again.Sandbox, i.service.Sandboxes())
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	i.ended(next.ID)

	plain := i.session(store.Agent{})
	i.send(driver, plain, "hello")
	if other := i.service.Turn(t); other.Sandbox == given.Sandbox || len(other.Environment) != 0 || other.Approval != "" {
		t.Errorf("the turn of another session was given as %+v", other)
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

	if err := i.sandboxes.DeleteSandbox(ctx, given.Sandbox); err != nil {
		t.Fatal(err)
	}
	second := i.send(driver, session, "are you there")
	again := i.service.Turn(t)
	if again.Sandbox == given.Sandbox || i.sandbox(session) != again.Sandbox || len(i.service.Sandboxes()) != 1 {
		t.Errorf("the turn runs in %q, the session has %q, of the sandboxes %v", again.Sandbox, i.sandbox(session), i.service.Sandboxes())
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	if ended := i.ended(second.ID); ended.State != store.TurnCompleted {
		t.Errorf("the turn ended as %+v", ended)
	}
}

func TestATurnWhoseSandboxIsLostFails(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	i.eventually("the turn has started", func() bool { return i.turn(turn.ID).StartedAt != nil })

	if err := i.sandboxes.DeleteSandbox(ctx, given.Sandbox); err != nil {
		t.Fatal(err)
	}
	given.Cut()
	if ended := i.ended(turn.ID); !failed(ended, store.FailureSandboxLost) {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	if kept := i.sandbox(session); kept != "" {
		t.Errorf("the session still has the sandbox %q", kept)
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
		t.Errorf("the turn ended as %+v", ended)
	}
	want := []string{events.TurnStarted, "message.delta", "message.delta", events.TurnCompleted}
	if told := i.told(turn.ID); !slices.Equal(told, want) {
		t.Errorf("the clients of the turn read %v", told)
	}
}

func TestATurnThatDoesNotStartInTimeIsGivenUp(t *testing.T) {
	// The harness has changed, and no release has had its template built.
	i := install(t, func(config *Config) {
		config.MaxStartup = 300 * time.Millisecond
		config.Templates.Store = "s3://elsewhere/templates"
	})
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	ended := i.ended(turn.ID)
	if !failed(ended, store.FailureStartup) || !strings.Contains(ended.Failure.Message, "the template of the harness opencode") {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	if len(i.service.Sandboxes()) != 0 {
		t.Errorf("the service has the sandboxes %v", i.service.Sandboxes())
	}

	// A harness that does not take the message: its sandbox goes with the
	// turn, and the next turn of the session gets a new one.
	i = install(t, func(config *Config) { config.MaxStartup = 300 * time.Millisecond })
	driver, _ = i.supervisor()
	session = i.session(store.Agent{})
	turn = i.send(driver, session, "hello")
	given := i.service.Turn(t)
	if ended := i.ended(turn.ID); !failed(ended, store.FailureStartup) {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	if len(i.service.Sandboxes()) != 0 || i.sandbox(session) != "" {
		t.Errorf("the service has the sandboxes %v, and the session %q", i.service.Sandboxes(), i.sandbox(session))
	}
	next := i.send(driver, session, "again")
	again := i.service.Turn(t)
	if again.Sandbox == given.Sandbox || again.ID != next.ID.String() {
		t.Errorf("the next turn was given as %+v", again)
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
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	if len(i.service.Sandboxes()) != 0 || i.sandbox(session) != "" {
		t.Errorf("the service has the sandboxes %v, and the session %q", i.service.Sandboxes(), i.sandbox(session))
	}
	if told := i.told(turn.ID); !slices.Equal(told, []string{events.TurnStarted, events.TurnFailed}) {
		t.Errorf("the clients of the turn read %v", told)
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
		t.Errorf("the turn that waited for a person ended as %+v, %+v", ended, ended.Failure)
	}
}

// cancel asks the turn to stop, as the API does.
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
	// The Supervisor that follows the turn hears of it, or finds it.
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
			t.Errorf("the turn ended as %+v", ended)
		}
		if len(i.service.Sandboxes()) != 1 {
			t.Errorf("the service has the sandboxes %v", i.service.Sandboxes())
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
		t.Errorf("the turn ended as %+v", ended)
	}
	if len(i.service.Sandboxes()) != 0 || i.sandbox(session) != "" {
		t.Errorf("the service has the sandboxes %v, and the session %q", i.service.Sandboxes(), i.sandbox(session))
	}
}

func TestATurnThatIsAskedToStopBeforeItsRunnerHasItIsNotRun(t *testing.T) {
	// The turn waits for a template that is not there.
	i := install(t, func(config *Config) { config.Templates.Store = "s3://elsewhere/templates" })
	driver, _ := i.supervisor()
	turn := i.send(driver, i.session(store.Agent{}), "hello")
	i.eventually("the turn is followed", func() bool { return i.turn(turn.ID).State == store.TurnRunning })
	i.cancel(turn.ID, true)
	if ended := i.ended(turn.ID); ended.State != store.TurnCancelled {
		t.Errorf("the turn ended as %+v", ended)
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

	// The Supervisor that follows the turn goes. While nobody follows the
	// turn, the runner tells more of it, and the turn is asked to stop.
	leave()
	given.Tell("message.delta", map[string]string{"text": "two"})
	i.cancel(turn.ID, false)
	// Another Supervisor takes the turn up: it records what was told, and
	// passes on what was asked.
	second, _ := i.supervisor()
	i.eventually("the other supervisor records the turn", func() bool { return len(i.told(turn.ID)) == 3 })
	given.Stopped(t)
	given.Tell("finished", map[string]string{"stop_reason": "cancelled"})
	if ended := i.ended(turn.ID); ended.State != store.TurnCancelled {
		t.Errorf("the turn ended as %+v", ended)
	}
	want := []string{events.TurnStarted, "message.delta", "message.delta", events.TurnCancelled}
	if told := i.told(turn.ID); !slices.Equal(told, want) {
		t.Errorf("the clients of the turn read %v", told)
	}
	// The runner was given the turn once, and the session has one sandbox.
	if len(i.service.Sandboxes()) != 1 {
		t.Errorf("the service has the sandboxes %v", i.service.Sandboxes())
	}
	next := i.send(second, session, "and then")
	if again := i.service.Turn(t); again.ID != next.ID.String() || again.Sandbox != given.Sandbox {
		t.Errorf("the next turn was given as %+v", again)
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

	// While no Supervisor follows the turn, its runner is started again.
	i.service.Restart(given.Sandbox)
	i.supervisor()
	ended := i.ended(turn.ID)
	if !failed(ended, store.FailureRunner) || !strings.Contains(ended.Failure.Message, "does not have the turn") {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	// The sandbox stays, with what the agent has in it.
	if i.sandbox(session) != given.Sandbox || len(i.service.Sandboxes()) != 1 {
		t.Errorf("the session has the sandbox %q, of %v", i.sandbox(session), i.service.Sandboxes())
	}
}

func TestARunnerThatRunsAnotherTurnGoesWithItsSandbox(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(first.ID)

	// A turn that the installation knows nothing of runs in the sandbox.
	runner, err := i.sandboxes.Runner(ctx, given.Sandbox)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Start(ctx, uuid.New(), sandbox.Turn{Text: "another"}); err != nil {
		t.Fatal(err)
	}
	i.service.Turn(t)
	next := i.send(driver, session, "and then")
	if ended := i.ended(next.ID); !failed(ended, store.FailureRunner) || !strings.Contains(ended.Failure.Message, "runs another turn") {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	if len(i.service.Sandboxes()) != 0 || i.sandbox(session) != "" {
		t.Errorf("the service has the sandboxes %v, and the session %q", i.service.Sandboxes(), i.sandbox(session))
	}
}

func TestTwoSupervisorsMakeOneSandboxForASession(t *testing.T) {
	i := install(t)
	driver := New(i.config, i.sandboxes, i.store, i.events, metrics.New())
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	if _, err := i.store.DispatchTurns(ctx, 1); err != nil {
		t.Fatal(err)
	}
	followed, err := i.store.FollowTurn(ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Both find the session without a sandbox, and each makes one.
	var made [2]string
	for n := range made {
		if made[n], err = driver.provide(ctx, &run{turn: followed}); err != nil {
			t.Fatal(err)
		}
	}
	if made[0] != made[1] || i.sandbox(session) != made[0] {
		t.Errorf("the supervisors have the sandboxes %v, and the session %q", made, i.sandbox(session))
	}
	if sandboxes := i.service.Sandboxes(); len(sandboxes) != 1 || sandboxes[made[0]] == nil {
		t.Errorf("the service has the sandboxes %v", sandboxes)
	}
}

func TestWhatARunnerTellsIsNotTakenAtItsWord(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	// An event that only the Supervisor tells, and one that nobody knows.
	given.Tell(events.TurnCompleted, map[string]string{"stop_reason": "end_turn"})
	given.Tell("something.new", map[string]string{})
	given.Tell("message.delta", map[string]string{"text": "hi"})
	i.eventually("the events are recorded", func() bool { return len(i.told(turn.ID)) == 2 })
	if state := i.turn(turn.ID).State; state != store.TurnRunning {
		t.Errorf("the turn is %s", state)
	}
	// A failure that is none of a runner's.
	given.Tell("failed", map[string]string{"reason": "timed_out", "message": "no"})
	ended := i.ended(turn.ID)
	if !failed(ended, store.FailureRunner) || ended.Failure.Message != "no" {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	if told := i.told(turn.ID); !slices.Equal(told, []string{events.TurnStarted, "message.delta", events.TurnFailed}) {
		t.Errorf("the clients of the turn read %v", told)
	}
}

func TestASessionThatSleepsIsWokenForItsNextTurn(t *testing.T) {
	i := install(t, func(config *Config) { config.Idle = 5 * time.Minute })
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	if idle := i.service.Idle(given.Sandbox); idle != 300 {
		t.Errorf("the sandbox of the session is put to sleep after %d idle seconds", idle)
	}
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(first.ID)

	i.service.Sleep(given.Sandbox)
	next := i.send(driver, session, "and then")
	again := i.service.Turn(t)
	if again.ID != next.ID.String() || again.Sandbox != given.Sandbox || i.service.Woken(given.Sandbox) != 1 {
		t.Errorf("the next turn was given as %+v, and the sandbox woken %d times", again, i.service.Woken(given.Sandbox))
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	if ended := i.ended(next.ID); ended.State != store.TurnCompleted {
		t.Errorf("the turn ended as %+v", ended)
	}
}

func TestASandboxThatIsNotWokenInTimeStaysWithItsSession(t *testing.T) {
	i := install(t, func(config *Config) {
		config.Idle = 5 * time.Minute
		config.MaxStartup = time.Second
	})
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(first.ID)

	// The sandbox wakes as slowly as on a node that does not have its
	// template yet. The turn did not reach it, and it stays with what the
	// agent had in it.
	i.service.Sleep(given.Sandbox)
	i.service.Slow(true)
	late := i.send(driver, session, "and then")
	if ended := i.ended(late.ID); !failed(ended, store.FailureStartup) {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	if i.sandbox(session) != given.Sandbox || len(i.service.Sandboxes()) != 1 {
		t.Errorf("the service has the sandboxes %v, and the session %q", i.service.Sandboxes(), i.sandbox(session))
	}

	i.service.Slow(false)
	next := i.send(driver, session, "again")
	if again := i.service.Turn(t); again.ID != next.ID.String() || again.Sandbox != given.Sandbox {
		t.Errorf("the next turn was given as %+v", again)
	}
}

// asks is the event of a harness that asks a person before it goes on.
var asks = map[string]any{"id": "1", "options": []map[string]string{{"id": "once", "kind": "allow_once", "name": "Once"}}}

func TestATurnThatWaitsForAPersonSleepsUntilItIsToldSomething(t *testing.T) {
	// The turn is answered, or told to stop.
	for _, answered := range []bool{true, false} {
		i := install(t)
		driver, _ := i.supervisor()
		turn := i.send(driver, i.session(store.Agent{Approval: Ask}), "hello")
		given := i.service.Turn(t)
		given.Tell("started", started)
		given.Tell("approval.requested", asks)
		i.eventually("the request is recorded", func() bool { return len(i.told(turn.ID)) == 2 })

		// The session is not at work, and its sandbox is put to sleep. It
		// is left to sleep for as long as nobody answers.
		i.service.Sleep(given.Sandbox)
		time.Sleep(6 * i.config.Sweep)
		if woken := i.service.Woken(given.Sandbox); woken != 0 {
			t.Fatalf("the sandbox of a turn that waits for a person was woken %d times", woken)
		}
		if state := i.turn(turn.ID).State; state != store.TurnRunning {
			t.Fatalf("the turn that waits for a person is %s", state)
		}

		if answered {
			if kept, err := i.store.AnswerApproval(ctx, turn.ID, "1", "once", uuid.Nil); err != nil || !kept {
				t.Fatalf("the answer was kept: %t, %v", kept, err)
			}
			if err := i.events.AnnounceAnswer(ctx, turn.ID); err != nil {
				t.Fatal(err)
			}
			if request, option := given.Answered(t); request != "1" || option != "once" {
				t.Errorf("the request %q was answered with %q", request, option)
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
			t.Errorf("the turn ended as %+v", ended)
		}
		if woken := i.service.Woken(given.Sandbox); woken != 1 {
			t.Errorf("the sandbox was woken %d times", woken)
		}
		want := []string{events.TurnStarted, "approval.requested", "approval.resolved", events.TurnCompleted}
		if !answered {
			want[3] = events.TurnCancelled
		}
		if told := i.told(turn.ID); !slices.Equal(told, want) {
			t.Errorf("the clients of the turn read %v", told)
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

	template, err := i.sandboxes.Template(ctx, i.config.Templates, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	// stray makes a sandbox as a Supervisor does that then goes, before it
	// has told the session.
	stray := func(session string) string {
		made, err := i.sandboxes.CreateSandbox(ctx, sandbox.Request{Template: template, Metadata: map[string]string{metadataSession: session}})
		if err != nil {
			t.Fatal(err)
		}
		return made.ID
	}
	left, young := stray(session.ID.String()), stray(session.ID.String())
	// The sandbox of a session that these records do not know, and one
	// that is no sandbox of a session.
	foreign, other := stray(uuid.NewString()), stray("")
	for _, id := range []string{given.Sandbox, left, foreign, other} {
		i.service.Age(id, time.Hour)
	}
	i.eventually("the sandbox that the session does not have is deleted", func() bool {
		_, there := i.service.Sandboxes()[left]
		return !there
	})
	sandboxes := i.service.Sandboxes()
	for what, id := range map[string]string{
		"of the session": given.Sandbox, "that is young": young, "of an unknown session": foreign, "of no session": other,
	} {
		if _, there := sandboxes[id]; !there {
			t.Errorf("the sandbox %s was deleted", what)
		}
	}
}

func TestATurnThatEndedWhileNobodyReadItIsReadFromItsSleepingSandbox(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	turn := i.send(driver, i.session(store.Agent{}), "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	i.eventually("the turn has started", func() bool { return i.turn(turn.ID).StartedAt != nil })

	// The reading is cut off, the runner ends the turn, and the sandbox is
	// put to sleep before anyone reads that: the session is not at work.
	i.service.Sleep(given.Sandbox)
	given.Tell("message.delta", map[string]string{"text": "done"})
	given.Tell("finished", finished)
	if ended := i.ended(turn.ID); ended.State != store.TurnCompleted {
		t.Errorf("the turn ended as %+v", ended)
	}
	if woken := i.service.Woken(given.Sandbox); woken != 1 {
		t.Errorf("the sandbox was woken %d times", woken)
	}
	if told := i.told(turn.ID); !slices.Equal(told, []string{events.TurnStarted, "message.delta", events.TurnCompleted}) {
		t.Errorf("the clients of the turn read %v", told)
	}
}

func TestASessionThatIsNotUsedForLongerThanItsSandboxIsKeptHasEnded(t *testing.T) {
	i := install(t, func(config *Config) { config.Retention = time.Hour })
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	// The sandbox is kept for that long from when a turn is given to it.
	if left := time.Until(i.service.Expires(given.Sandbox)); left < 59*time.Minute || left > time.Hour {
		t.Errorf("the sandbox of the session is kept for %s", left)
	}
	given.Tell("started", started)
	given.Tell("finished", finished)
	i.ended(first.ID)

	// A sandbox that is lost sooner is another matter: the session goes on
	// in a new one.
	if err := i.sandboxes.DeleteSandbox(ctx, given.Sandbox); err != nil {
		t.Fatal(err)
	}
	second := i.send(driver, session, "are you there")
	again := i.service.Turn(t)
	if again.Sandbox == given.Sandbox || again.ID != second.ID.String() {
		t.Errorf("the turn was given as %+v", again)
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	i.ended(second.ID)

	// Two hours later, the service has deleted the sandbox.
	i.store.Age(2*time.Hour, first.ID, second.ID)
	if err := i.sandboxes.DeleteSandbox(ctx, again.Sandbox); err != nil {
		t.Fatal(err)
	}
	third := i.send(driver, session, "and now")
	ended := i.ended(third.ID)
	if !failed(ended, store.FailureSessionExpired) || !strings.Contains(ended.Failure.Message, "1h0m0s") {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	i.eventually("the session has ended", func() bool { return i.sessionOf(session.ID).EndedAt != nil })
	if _, err := driver.Accept(ctx, session.ID, uuid.Nil, "once more"); !errors.Is(err, store.ErrSessionEnded) {
		t.Errorf("a message to the session that has ended: %v", err)
	}
	if len(i.service.Sandboxes()) != 0 || len(i.service.Given()) != 2 {
		t.Errorf("the service has the sandboxes %v, and the runners were given %v", i.service.Sandboxes(), i.service.Given())
	}
}

func TestTheSandboxOfASessionReachesTheHostsOfTheInstallation(t *testing.T) {
	// ask is where the egress of a sandbox is to ask about its requests.
	ask := func(sandbox string) (string, error) { return "https://resolver.example.com/for-" + sandbox, nil }
	for _, test := range []struct {
		hosts         []string
		policy, vault string
	}{
		{
			hosts:  []string{"pypi.org", "*.example.com"},
			policy: `{"defaultAction":"deny","egress":[{"action":"allow","target":"pypi.org"},{"action":"allow","target":"*.example.com"}]}`,
			vault:  `["pypi.org","*.example.com","models.example.com"]`,
		},
		// Of an installation that lets every host be reached, the requests
		// to the model endpoint are asked about.
		{hosts: []string{"*"}, policy: `{"defaultAction":"allow"}`, vault: `["models.example.com"]`},
	} {
		i := install(t, func(config *Config) {
			config.Hosts, config.Ask = test.hosts, ask
		})
		driver, _ := i.supervisor()
		session := i.session(store.Agent{Model: &store.Model{Name: "a-model"}})
		for _, text := range []string{"hello", "and then"} {
			turn := i.send(driver, session, text)
			given := i.service.TurnWith(t, turn.ID.String())
			given.Tell("started", started)
			given.Tell("finished", finished)
			i.ended(turn.ID)
		}
		sandbox := i.sandbox(session)
		if policy := string(i.service.Policy(sandbox)); policy != test.policy {
			t.Errorf("of the hosts %v, the sandbox may reach %s, not %s", test.hosts, policy, test.policy)
		}
		var vault struct {
			Credentials []struct {
				Name   string
				Source struct{ Type, Endpoint string }
			}
			Bindings []struct {
				Match struct{ Hosts, Schemes []string }
				Auth  struct{ Credential string }
			}
		}
		if err := json.Unmarshal(i.service.Vault(sandbox), &vault); err != nil || len(vault.Credentials) != 1 || len(vault.Bindings) != 1 {
			t.Fatalf("of the hosts %v, the egress of the sandbox was given %s: %v", test.hosts, i.service.Vault(sandbox), err)
		}
		credential, binding := vault.Credentials[0], vault.Bindings[0]
		hosts, _ := json.Marshal(binding.Match.Hosts)
		if credential.Source.Type != "exchange" || credential.Source.Endpoint != "https://resolver.example.com/for-"+sandbox ||
			binding.Auth.Credential != credential.Name || string(hosts) != test.vault || !slices.Equal(binding.Match.Schemes, []string{"https"}) {
			t.Errorf("of the hosts %v, the egress of the sandbox was given %s", test.hosts, i.service.Vault(sandbox))
		}
	}
}

func TestAHarnessThatAnInstallationHas(t *testing.T) {
	i := install(t)
	driver := New(i.config, i.sandboxes, i.store, i.events, metrics.New())
	if !driver.HasHarness("opencode") || driver.HasHarness("codex") {
		t.Error("the driver has other harnesses than the installation")
	}
}
