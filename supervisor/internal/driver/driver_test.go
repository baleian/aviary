package driver

import (
	"context"
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

// The tests of this file run drivers on records in memory and sandboxes
// whose runners the test plays.

var ctx = context.Background()

type installation struct {
	t       *testing.T
	store   *memorystore.Store
	events  *events.Streams
	service *sandboxtest.Service
	config  Config
}

// install sets an installation up with a harness, opencode.
func install(t *testing.T, changes ...func(*Config)) *installation {
	t.Helper()
	i := &installation{t: t, store: memorystore.New(), events: events.New(memorylog.New()), service: sandboxtest.New(t)}
	i.config = Config{
		Harnesses:          map[string]sandbox.Harness{"opencode": {Image: "registry.example.com/opencode:1.0"}},
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

// supervisor starts a driver of the installation, and returns it with what
// stops it: from then on it dispatches no turn and sweeps no more. It stops
// with the test at the latest.
func (i *installation) supervisor() (*Driver, func()) {
	running, stop := context.WithCancel(ctx)
	i.t.Cleanup(stop)
	driver := New(i.config, i.service, i.store, i.events, metrics.New())
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
	made := i.service.Made()[given.Sandbox]
	if i.sandbox(session) != given.Sandbox || made.Session != session.ID || made.Harness.Image != "registry.example.com/opencode:1.0" ||
		made.Labels[labelAgent] != agent.ID.String() || made.Labels[labelProject] != agent.ProjectID.String() {
		t.Errorf("the session has the sandbox %q, and the turn runs in %q, made for %+v", i.sandbox(session), given.Sandbox, made)
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
	if again.Sandbox != given.Sandbox || again.ID != next.ID.String() || len(i.service.Made()) != 1 {
		t.Errorf("the next turn runs in %q, of the sandboxes %v", again.Sandbox, i.service.Made())
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

	// The new one has the name of the session too.
	i.service.Lose(given.Sandbox)
	second := i.send(driver, session, "are you there")
	again := i.service.Turn(t)
	if i.sandbox(session) != again.Sandbox || len(i.service.Made()) != 1 {
		t.Errorf("the turn runs in %q, the session has %q, of the sandboxes %v", again.Sandbox, i.sandbox(session), i.service.Made())
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

	i.service.Lose(given.Sandbox)
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
	// The installation has no more the harness of the agent.
	i := install(t, func(config *Config) {
		config.MaxStartup = 300 * time.Millisecond
		config.Harnesses = nil
	})
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	ended := i.ended(turn.ID)
	if !failed(ended, store.FailureStartup) || !strings.Contains(ended.Failure.Message, "the installation has no harness opencode") {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	if len(i.service.Made()) != 0 {
		t.Errorf("the sandboxes are %v", i.service.Made())
	}

	// A harness that does not take the message: its sandbox is put to sleep
	// with the turn, and the next turn of the session wakes it.
	i = install(t, func(config *Config) { config.MaxStartup = 300 * time.Millisecond })
	driver, _ = i.supervisor()
	session = i.session(store.Agent{})
	turn = i.send(driver, session, "hello")
	given := i.service.Turn(t)
	if ended := i.ended(turn.ID); !failed(ended, store.FailureStartup) {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	if i.service.State(given.Sandbox) != sandbox.Asleep || i.sandbox(session) != given.Sandbox {
		t.Errorf("the sandbox is %s, and the session has %q", i.service.State(given.Sandbox), i.sandbox(session))
	}
	next := i.send(driver, session, "again")
	again := i.service.Turn(t)
	if again.Sandbox != given.Sandbox || again.ID != next.ID.String() || i.service.Woken(given.Sandbox) != 1 {
		t.Errorf("the next turn was given as %+v, and the sandbox woken %d times", again, i.service.Woken(given.Sandbox))
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
	// Its sandbox is put to sleep, which stops what runs in it, and the
	// session keeps it.
	if i.service.State(given.Sandbox) != sandbox.Asleep || i.sandbox(session) != given.Sandbox {
		t.Errorf("the sandbox is %s, and the session has %q", i.service.State(given.Sandbox), i.sandbox(session))
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
		if i.service.State(given.Sandbox) != sandbox.Running {
			t.Errorf("the sandbox of a turn that stopped is %s", i.service.State(given.Sandbox))
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
	if i.service.State(given.Sandbox) != sandbox.Asleep || i.sandbox(session) != given.Sandbox {
		t.Errorf("the sandbox is %s, and the session has %q", i.service.State(given.Sandbox), i.sandbox(session))
	}
}

func TestATurnThatIsAskedToStopBeforeItsRunnerHasItIsNotRun(t *testing.T) {
	// The turn waits for its sandbox, which does not run yet.
	i := install(t)
	i.service.Slow(true)
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
	if len(i.service.Made()) != 1 {
		t.Errorf("the sandboxes are %v", i.service.Made())
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
	if i.sandbox(session) != given.Sandbox || len(i.service.Made()) != 1 {
		t.Errorf("the session has the sandbox %q, of %v", i.sandbox(session), i.service.Made())
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

	// A turn that the installation knows nothing of runs in the sandbox.
	seen, err := i.service.Sandbox(ctx, given.Sandbox)
	if err != nil {
		t.Fatal(err)
	}
	if err := sandbox.RunnerAt(seen.Address).Start(ctx, uuid.New(), sandbox.Turn{Text: "another"}); err != nil {
		t.Fatal(err)
	}
	i.service.Turn(t)
	next := i.send(driver, session, "and then")
	if ended := i.ended(next.ID); !failed(ended, store.FailureRunner) || !strings.Contains(ended.Failure.Message, "runs another turn") {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	// The sandbox is put to sleep, which stops the other turn with the pod.
	if i.service.State(given.Sandbox) != sandbox.Asleep || i.sandbox(session) != given.Sandbox {
		t.Errorf("the sandbox is %s, and the session has %q", i.service.State(given.Sandbox), i.sandbox(session))
	}
}

func TestTwoSupervisorsMakeOneSandboxForASession(t *testing.T) {
	i := install(t)
	driver := New(i.config, i.service, i.store, i.events, metrics.New())
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
	if sandboxes := i.service.Made(); len(sandboxes) != 1 || sandboxes[made[0]].Session != session.ID {
		t.Errorf("the sandboxes are %v", sandboxes)
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

	// The installation is upgraded while the session sleeps. Its sandbox
	// wakes with the harness as the installation has it now.
	stop()
	i.config.Harnesses = map[string]sandbox.Harness{"opencode": {Image: "registry.example.com/opencode:2.0"}}
	driver, _ = i.supervisor()
	next := i.send(driver, session, "and then")
	again := i.service.Turn(t)
	if again.ID != next.ID.String() || again.Sandbox != given.Sandbox || i.service.Woken(given.Sandbox) != 1 {
		t.Errorf("the next turn was given as %+v, and the sandbox woken %d times", again, i.service.Woken(given.Sandbox))
	}
	if image := i.service.Made()[given.Sandbox].Harness.Image; image != "registry.example.com/opencode:2.0" {
		t.Errorf("the sandbox was woken with the image %s", image)
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	if ended := i.ended(next.ID); ended.State != store.TurnCompleted {
		t.Errorf("the turn ended as %+v", ended)
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

	// The sandbox wakes as slowly as on a node that does not have its image
	// yet. The turn did not reach it, and it stays with what the agent had in
	// it.
	if err := i.service.Suspend(ctx, given.Sandbox, ""); err != nil {
		t.Fatal(err)
	}
	i.service.Slow(true)
	late := i.send(driver, session, "and then")
	if ended := i.ended(late.ID); !failed(ended, store.FailureStartup) {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	if i.sandbox(session) != given.Sandbox || len(i.service.Made()) != 1 {
		t.Errorf("the sandboxes are %v, and the session has %q", i.service.Made(), i.sandbox(session))
	}

	i.service.Slow(false)
	next := i.send(driver, session, "again")
	if again := i.service.Turn(t); again.ID != next.ID.String() || again.Sandbox != given.Sandbox {
		t.Errorf("the next turn was given as %+v", again)
	}
}

func TestASessionThatIsNotAtWorkIsPutToSleep(t *testing.T) {
	i := install(t, func(config *Config) {
		config.Idle = 300 * time.Millisecond
		// The sandboxes are looked at every six sweeps.
		config.Sweep = 20 * time.Millisecond
	})
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	first := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	// A session whose turn runs is at work, for however long the turn runs.
	given.Tell("started", started)
	time.Sleep(3 * i.config.Idle)
	if state := i.service.State(given.Sandbox); state != sandbox.Running {
		t.Fatalf("the sandbox of a session whose turn runs is %s", state)
	}
	given.Tell("finished", finished)
	ended := i.ended(first.ID)
	i.eventually("the sandbox of the session that is not at work is put to sleep", func() bool {
		return i.service.State(given.Sandbox) == sandbox.Asleep
	})
	if idle := time.Since(*ended.EndedAt); idle < i.config.Idle {
		t.Errorf("the sandbox was put to sleep after %s", idle)
	}

	next := i.send(driver, session, "and then")
	again := i.service.Turn(t)
	if again.ID != next.ID.String() || again.Sandbox != given.Sandbox || i.service.Woken(given.Sandbox) != 1 {
		t.Errorf("the next turn was given as %+v, and the sandbox woken %d times", again, i.service.Woken(given.Sandbox))
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

	// Another Supervisor found the session not at work, and puts the
	// sandbox to sleep after the next turn has looked at it, before the turn
	// is given to it. The turn wakes it and runs.
	i.service.SleepBeforeKeep(given.Sandbox)
	next := i.send(driver, session, "and then")
	again := i.service.Turn(t)
	if again.ID != next.ID.String() || i.service.Woken(given.Sandbox) != 1 {
		t.Errorf("the turn was given as %+v, and the sandbox woken %d times", again, i.service.Woken(given.Sandbox))
	}
	again.Tell("started", started)
	again.Tell("finished", finished)
	if ended := i.ended(next.ID); ended.State != store.TurnCompleted {
		t.Errorf("the turn ended as %+v", ended)
	}
}

// asks is the event of a harness that asks a person before it goes on.
var asks = map[string]any{"id": "1", "options": []map[string]string{{"id": "once", "kind": "allow_once", "name": "Once"}}}

func TestATurnThatWaitsForAPersonGoesOnWhenItIsToldSomething(t *testing.T) {
	// The turn is answered, or told to stop.
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

	// stray makes the sandbox of a session as a Supervisor does that then
	// goes, before it has told the session.
	stray := func(session uuid.UUID) string {
		if err := i.service.Create(ctx, sandbox.Request{Session: session}); err != nil {
			t.Fatal(err)
		}
		return sandbox.Name(session)
	}
	left, young := stray(i.session(store.Agent{}).ID), stray(i.session(store.Agent{}).ID)
	// The sandbox of a session that these records do not know.
	foreign := stray(uuid.New())
	for _, name := range []string{given.Sandbox, left, foreign} {
		i.service.Age(name, time.Hour)
	}
	i.eventually("the sandbox that its session does not have is deleted", func() bool {
		_, there := i.service.Made()[left]
		return !there
	})
	made := i.service.Made()
	for what, name := range map[string]string{"of the session": given.Sandbox, "that is young": young, "of an unknown session": foreign} {
		if _, there := made[name]; !there {
			t.Errorf("the sandbox %s was deleted", what)
		}
	}
}

func TestATurnWhoseSandboxIsPutToSleepFails(t *testing.T) {
	i := install(t)
	driver, _ := i.supervisor()
	session := i.session(store.Agent{})
	turn := i.send(driver, session, "hello")
	given := i.service.Turn(t)
	given.Tell("started", started)
	i.eventually("the turn has started", func() bool { return i.turn(turn.ID).StartedAt != nil })

	// What ran in the sandbox stops with its pod.
	if err := i.service.Suspend(ctx, given.Sandbox, ""); err != nil {
		t.Fatal(err)
	}
	ended := i.ended(turn.ID)
	if !failed(ended, store.FailureRunner) || !strings.Contains(ended.Failure.Message, "put to sleep") {
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	// The session goes on in its sandbox, with its volume.
	next := i.send(driver, session, "and then")
	if again := i.service.Turn(t); again.ID != next.ID.String() || again.Sandbox != given.Sandbox {
		t.Errorf("the next turn was given as %+v", again)
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
	i.service.Lose(given.Sandbox)
	second := i.send(driver, session, "are you there")
	again := i.service.Turn(t)
	if again.ID != second.ID.String() {
		t.Errorf("the turn was given as %+v", again)
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
		t.Errorf("the turn ended as %+v, %+v", ended, ended.Failure)
	}
	i.eventually("the session has ended", func() bool { return i.sessionOf(session.ID).EndedAt != nil })
	if _, err := driver.Accept(ctx, session.ID, uuid.Nil, "once more"); !errors.Is(err, store.ErrSessionEnded) {
		t.Errorf("a message to the session that has ended: %v", err)
	}
	if len(i.service.Made()) != 0 || len(i.service.Given()) != 2 {
		t.Errorf("the sandboxes are %v, and the runners were given %v", i.service.Made(), i.service.Given())
	}
}

func TestAHarnessThatAnInstallationHas(t *testing.T) {
	i := install(t)
	driver := New(i.config, i.service, i.store, i.events, metrics.New())
	if !driver.HasHarness("opencode") || driver.HasHarness("codex") {
		t.Error("the driver has other harnesses than the installation")
	}
}
