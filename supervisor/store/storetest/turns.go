package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

var ctx = context.Background()

// supervisor is the Store of one Supervisor in a test, with helpers that fail
// the test on errors.
type supervisor struct {
	store.Store
	t        *testing.T
	database Database
}

// open returns the Store of a Supervisor on a new database.
func (suite suite) open(t *testing.T) supervisor {
	t.Helper()
	database := suite.create(t)
	return supervisor{Store: database.Open(t), t: t, database: database}
}

// another returns the Store of another Supervisor on the same database.
func (s supervisor) another() supervisor {
	s.t.Helper()
	return supervisor{Store: s.database.Open(s.t), t: s.t, database: s.database}
}

// project creates a project with a slug of its own.
func (s supervisor) project() store.Project {
	s.t.Helper()
	project, err := s.CreateProject(ctx, store.Project{Slug: unique("project")})
	if err != nil {
		s.t.Fatal(err)
	}
	return project
}

// session creates a session of a new agent in a new project.
func (s supervisor) session() uuid.UUID {
	s.t.Helper()
	project := s.project()
	provider, err := s.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1"})
	if err != nil {
		s.t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, project.ID, store.Agent{
		Slug: unique("agent"), Harness: "opencode", Instructions: "Be brief.", Approval: "ask",
		Model: store.Model{Provider: provider.ID, Name: "a-model"},
	})
	if err != nil {
		s.t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, agent.ID, uuid.Nil, "")
	if err != nil {
		s.t.Fatal(err)
	}
	return session.ID
}

// queue queues a message to the session, with no queue limit.
func (s supervisor) queue(session uuid.UUID) store.Turn {
	s.t.Helper()
	turn, _, err := s.CreateTurn(ctx, session, uuid.Nil, "hello", 0, 50)
	if err != nil {
		s.t.Fatal(err)
	}
	return turn
}

// dispatch dispatches the only queued turn.
func (s supervisor) dispatch() store.Dispatch {
	s.t.Helper()
	dispatches, err := s.DispatchTurns(ctx, 50)
	if err != nil {
		s.t.Fatal(err)
	}
	if len(dispatches) != 1 {
		s.t.Fatalf("dispatched %d turns, want 1", len(dispatches))
	}
	return dispatches[0]
}

// running queues a message to the session, and dispatches and follows its
// turn, which then runs.
func (s supervisor) running(session uuid.UUID) store.Dispatch {
	s.t.Helper()
	s.queue(session)
	dispatch := s.dispatch()
	if _, err := s.FollowTurn(ctx, dispatch.TurnID); err != nil {
		s.t.Fatal(err)
	}
	return dispatch
}

func (s supervisor) turn(id uuid.UUID) store.Turn {
	s.t.Helper()
	turn, err := s.Turn(ctx, id)
	if err != nil {
		s.t.Fatal(err)
	}
	return turn
}

// end ends the turn and returns it.
func (s supervisor) end(id uuid.UUID, succeeded bool, failure store.Failure) store.Turn {
	s.t.Helper()
	turn, changed, err := s.EndTurn(ctx, id, succeeded, failure, nothing)
	if err != nil || !changed {
		s.t.Fatalf("EndTurn did not end the turn: %v", err)
	}
	return turn
}

// nothing returns the items of a turn without any.
func nothing(context.Context) ([]json.RawMessage, error) { return nil, nil }

func (suite suite) testCreateTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	first := st.queue(session)
	if first.Seq != 1 || first.State != store.TurnQueued || first.Text != "hello" {
		t.Errorf("first turn = %+v", first)
	}
	if _, _, err := st.CreateTurn(ctx, session, uuid.Nil, "again", 0, 50); !errors.Is(err, store.ErrSessionBusy) {
		t.Errorf("CreateTurn with an unfinished turn = %v, want ErrSessionBusy", err)
	}
	if _, _, err := st.CancelTurn(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if second := st.queue(session); second.Seq != 2 {
		t.Errorf("second turn Seq = %d, want 2", second.Seq)
	}
	if _, _, err := st.CreateTurn(ctx, uuid.New(), uuid.Nil, "hello", 0, 50); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("CreateTurn on a nonexistent session = %v, want ErrNotFound", err)
	}
}

// atOnce calls call concurrently with i from 0 to times-1, and waits for the
// calls to return.
func atOnce(times int, call func(i int)) {
	start := make(chan struct{})
	var calls sync.WaitGroup
	for i := range times {
		calls.Go(func() {
			<-start
			call(i)
		})
	}
	close(start)
	calls.Wait()
}

// supervisors returns the Stores of count other Supervisors on the database.
func (s supervisor) supervisors(count int) []supervisor {
	s.t.Helper()
	supervisors := make([]supervisor, count)
	for i := range supervisors {
		supervisors[i] = s.another()
	}
	return supervisors
}

func (suite suite) testConcurrentCreateTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	var accepted, busy atomic.Int32
	atOnce(20, func(int) {
		_, _, err := st.CreateTurn(ctx, session, uuid.Nil, "hello", 0, 50)
		switch {
		case err == nil:
			accepted.Add(1)
		case errors.Is(err, store.ErrSessionBusy):
			busy.Add(1)
		default:
			t.Error(err)
		}
	})
	if accepted.Load() != 1 || busy.Load() != 19 {
		t.Errorf("of 20 concurrent messages, %d accepted and %d busy, want 1 and 19", accepted.Load(), busy.Load())
	}
}

func (suite suite) testCreateTurnQueueLimit(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	const maxWaiting, maxRunning = 2, 2
	send := func(session uuid.UUID) (int, error) {
		_, waiting, err := st.CreateTurn(ctx, session, uuid.Nil, "hello", maxWaiting, maxRunning)
		return waiting, err
	}
	// The first two have room to run, so they do not wait.
	for _, expected := range []int{0, 0, 1, 2} {
		if waiting, err := send(st.session()); err != nil || waiting != expected {
			t.Fatalf("waiting = %d, want %d: %v", waiting, expected, err)
		}
	}
	refused := st.session()
	if _, err := send(refused); !errors.Is(err, store.ErrQueueFull) {
		t.Fatalf("CreateTurn beyond the queue limit = %v, want ErrQueueFull", err)
	}
	if turns, err := st.Turns(ctx, refused, math.MaxInt32, 10); err != nil || len(turns) != 0 {
		t.Errorf("refused message left turns %v: %v", turns, err)
	}

	// Dispatched turns make no room in the queue until one of them ends.
	dispatches, err := st.DispatchTurns(ctx, maxRunning)
	if err != nil || len(dispatches) != maxRunning {
		t.Fatalf("dispatched %d turns: %v", len(dispatches), err)
	}
	if _, err := send(refused); !errors.Is(err, store.ErrQueueFull) {
		t.Errorf("CreateTurn beyond the queue limit = %v, want ErrQueueFull", err)
	}
	st.end(dispatches[0].TurnID, false, store.Failure{Reason: store.FailureSandboxLost})
	if waiting, err := send(refused); err != nil || waiting != 2 {
		t.Errorf("waiting after a turn ended = %d, want 2: %v", waiting, err)
	}
}

func (suite suite) testConcurrentCreateTurnQueueLimit(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	supervisors, sessions := st.supervisors(12), make([]uuid.UUID, 12)
	for i := range sessions {
		sessions[i] = st.session()
	}
	var accepted, refused atomic.Int32
	atOnce(12, func(i int) {
		// One turn has room to run and three may wait.
		_, _, err := supervisors[i].CreateTurn(ctx, sessions[i], uuid.Nil, "hello", 3, 1)
		switch {
		case err == nil:
			accepted.Add(1)
		case errors.Is(err, store.ErrQueueFull):
			refused.Add(1)
		default:
			t.Error(err)
		}
	})
	if accepted.Load() != 4 || refused.Load() != 8 {
		t.Errorf("of 12 concurrent messages, %d accepted and %d refused, want 4 and 8", accepted.Load(), refused.Load())
	}
}

// Concurrent messages that all have room to run do not count as waiting.
func (suite suite) testConcurrentCreateTurnWithRoomToRun(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	sessions := make([]uuid.UUID, 12)
	for i := range sessions {
		sessions[i] = st.session()
	}
	atOnce(len(sessions), func(i int) {
		if _, waiting, err := st.CreateTurn(ctx, sessions[i], uuid.Nil, "hello", 2, 50); err != nil || waiting != 0 {
			t.Errorf("waiting = %d, want 0: %v", waiting, err)
		}
	})
}

func (suite suite) testDispatchTurns(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	var queued []store.Turn
	for range 3 {
		queued = append(queued, st.queue(st.session()))
	}
	dispatches, err := st.DispatchTurns(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatches) != 2 || dispatches[0].TurnID != queued[0].ID || dispatches[1].TurnID != queued[1].ID {
		t.Fatalf("DispatchTurns = %v, want the two oldest of %v", dispatches, queued)
	}
	first := dispatches[0]
	if first.Harness != "opencode" {
		t.Errorf("dispatch = %+v, want harness opencode", first)
	}
	if state := st.turn(first.TurnID).State; state != store.TurnDispatching {
		t.Errorf("dispatched turn state = %s, want dispatching", state)
	}

	// A turn counts against the limit from its dispatch to its end.
	if _, err := st.FollowTurn(ctx, first.TurnID); err != nil {
		t.Fatal(err)
	}
	if more, err := st.DispatchTurns(ctx, 2); err != nil || len(more) != 0 {
		t.Fatalf("dispatched %d turns beyond the limit: %v", len(more), err)
	}
	st.end(first.TurnID, true, store.Failure{})
	if last := st.dispatch(); last.TurnID != queued[2].ID {
		t.Errorf("dispatched turn %s, want the queued one", last.TurnID)
	}
}

func (suite suite) testConcurrentDispatchTurns(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	for range 20 {
		st.queue(st.session())
	}
	supervisors := st.supervisors(8)
	var mu sync.Mutex
	var dispatched []uuid.UUID
	atOnce(len(supervisors), func(i int) {
		dispatches, err := supervisors[i].DispatchTurns(ctx, 5)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		defer mu.Unlock()
		for _, dispatch := range dispatches {
			dispatched = append(dispatched, dispatch.TurnID)
		}
	})
	slices.SortFunc(dispatched, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	if len(dispatched) != 5 || len(slices.Compact(dispatched)) != 5 {
		t.Errorf("8 Supervisors with a limit of 5 dispatched %v", dispatched)
	}
}

// The limit also holds while messages arrive, when Supervisors that count the
// running turns at the same time could pick different turns.
func (suite suite) testConcurrentDispatchTurnsAndCreateTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	supervisors := st.supervisors(4)
	sessions := make([]uuid.UUID, 8)
	for i := range sessions {
		sessions[i] = st.session()
	}
	for range 20 {
		var coming atomic.Bool
		coming.Store(true)
		var dispatching sync.WaitGroup
		for _, supervisor := range supervisors {
			dispatching.Go(func() {
				for coming.Load() {
					if _, err := supervisor.DispatchTurns(ctx, 1); err != nil {
						t.Error(err)
						return
					}
				}
			})
		}
		turns := make([]uuid.UUID, len(sessions))
		atOnce(len(sessions), func(i int) {
			turn, _, err := st.CreateTurn(ctx, sessions[i], uuid.Nil, "hello", 0, 1)
			if err != nil {
				t.Error(err)
			}
			turns[i] = turn.ID
		})
		coming.Store(false)
		dispatching.Wait()
		dispatched, err := st.DispatchedTurns(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(dispatched) > 1 {
			t.Fatalf("4 Supervisors with a limit of 1 dispatched %d turns", len(dispatched))
		}
		// End the turns so that each session can take the next message.
		for _, turn := range turns {
			if _, _, err := st.CancelTurn(ctx, turn); err != nil {
				t.Fatal(err)
			}
		}
		for _, turn := range dispatched {
			st.end(turn.ID, false, store.Failure{Reason: store.FailureSandboxLost})
		}
	}
}

// A turn cancelled during a dispatch either ends without being dispatched, or
// is dispatched with its cancellation requested, never both.
func (suite suite) testConcurrentDispatchTurnsAndCancelTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	dispatcher := st.another()
	turns := make([]store.Turn, 40)
	for i := range turns {
		turns[i] = st.queue(st.session())
	}
	var dispatches []store.Dispatch
	atOnce(len(turns)+1, func(i int) {
		if i == len(turns) {
			var err error
			if dispatches, err = dispatcher.DispatchTurns(ctx, len(turns)); err != nil {
				t.Error(err)
			}
			return
		}
		if _, _, err := st.CancelTurn(ctx, turns[i].ID); err != nil {
			t.Error(err)
		}
	})
	dispatched := map[uuid.UUID]bool{}
	for _, dispatch := range dispatches {
		dispatched[dispatch.TurnID] = true
	}
	for _, queued := range turns {
		turn := st.turn(queued.ID)
		ended := turn.State == store.TurnCancelled && turn.EndedAt != nil
		asked := turn.State == store.TurnDispatching && turn.EndedAt == nil && turn.CancelRequestedAt != nil
		if (dispatched[turn.ID] && !asked) || (!dispatched[turn.ID] && !ended) {
			t.Errorf("turn cancelled during DispatchTurns = %+v, dispatched %t", turn, dispatched[turn.ID])
		}
	}
}

// A turn that waits for approval counts against the limit, since its sandbox
// keeps running.
func (suite suite) testDispatchTurnsCountsAwaitingApproval(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	// One turn has room to run.
	send := func() (uuid.UUID, int) {
		t.Helper()
		turn, waiting, err := st.CreateTurn(ctx, st.session(), uuid.Nil, "hello", 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		return turn.ID, waiting
	}
	first, _ := send()
	dispatches, err := st.DispatchTurns(ctx, 1)
	if err != nil || len(dispatches) != 1 || dispatches[0].TurnID != first {
		t.Fatalf("DispatchTurns = %+v, want the first turn: %v", dispatches, err)
	}
	if err := st.AddApproval(ctx, first, "1", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, waiting := send(); waiting != 1 {
		t.Errorf("waiting = %d while the running turn awaits approval, want 1", waiting)
	}
	if dispatches, err := st.DispatchTurns(ctx, 1); err != nil || len(dispatches) != 0 {
		t.Errorf("DispatchTurns while the running turn awaits approval = %+v, want none: %v", dispatches, err)
	}
}

func (suite suite) testAnnounceQueue(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	first, gone, last := st.queue(st.session()), st.queue(st.session()), st.queue(st.session())
	if _, _, err := st.CancelTurn(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	// Announcements are serialized across Supervisors. Concurrent ones could
	// announce an older position after a newer one.
	supervisors := st.supervisors(4)
	var announcing atomic.Int32
	atOnce(len(supervisors), func(i int) {
		err := supervisors[i].AnnounceQueue(ctx, func(queued []uuid.UUID) error {
			if announcing.Add(1) != 1 {
				t.Error("concurrent AnnounceQueue calls overlapped")
			}
			defer announcing.Add(-1)
			time.Sleep(20 * time.Millisecond)
			if !slices.Equal(queued, []uuid.UUID{first.ID, last.ID}) {
				t.Errorf("queue = %v, want %v", queued, []uuid.UUID{first.ID, last.ID})
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	})
}

// FollowTurn returns what it takes to run the turn, as it is now.
func (suite suite) testFollowTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	kept, err := st.Session(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.Agent(ctx, kept.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	queued := st.queue(session)
	if _, err := st.FollowTurn(ctx, queued.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FollowTurn of a queued turn = %v, want ErrNotFound", err)
	}
	if _, err := st.FollowTurn(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FollowTurn of a nonexistent turn = %v, want ErrNotFound", err)
	}

	dispatch := st.dispatch()
	followed, err := st.FollowTurn(ctx, dispatch.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	if followed.ID != dispatch.TurnID || followed.SessionID != session || followed.Seq != 1 || followed.Text != "hello" ||
		followed.State != store.TurnRunning || followed.CancelRequestedAt != nil ||
		followed.AgentID != agent.ID || followed.ProjectID != agent.ProjectID ||
		followed.Harness != "opencode" || followed.Instructions != "Be brief." || followed.Approval != "ask" ||
		followed.ProviderURL != "https://api.example.com/v1" || followed.Model != "a-model" || followed.Sandbox != "" ||
		followed.Dispatched < 0 || followed.Dispatched > time.Minute {
		t.Errorf("FollowTurn = %+v", followed)
	}
	if state := st.turn(followed.ID).State; state != store.TurnRunning {
		t.Errorf("followed turn state = %s, want running", state)
	}

	// A later FollowTurn, by the same or another Supervisor, returns the
	// session's new sandbox and the cancellation request.
	if set, err := st.SetSandbox(ctx, session, "", "sandbox-1"); err != nil || !set {
		t.Fatalf("SetSandbox did not set the sandbox: %v", err)
	}
	if _, _, err := st.CancelTurn(ctx, dispatch.TurnID); err != nil {
		t.Fatal(err)
	}
	again, err := st.another().FollowTurn(ctx, dispatch.TurnID)
	if err != nil || again.ID != followed.ID || again.Sandbox != "sandbox-1" || again.CancelRequestedAt == nil {
		t.Errorf("second FollowTurn = %+v, want sandbox-1 and a cancellation request: %v", again, err)
	}

	st.end(dispatch.TurnID, false, store.Failure{Reason: store.FailureRunner})
	if _, err := st.FollowTurn(ctx, dispatch.TurnID); !errors.Is(err, store.ErrTurnEnded) {
		t.Errorf("FollowTurn of an ended turn = %v, want ErrTurnEnded", err)
	}
}

func (suite suite) testStartTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	dispatch := st.running(st.session())
	if started := st.turn(dispatch.TurnID).StartedAt; started != nil {
		t.Fatalf("StartedAt = %v before StartTurn, want nil", started)
	}
	startup, first, err := st.StartTurn(ctx, dispatch.TurnID)
	if err != nil || !first || startup < 0 || startup > time.Minute {
		t.Fatalf("StartTurn = %s, first %t: %v", startup, first, err)
	}
	started := st.turn(dispatch.TurnID).StartedAt
	if started == nil {
		t.Fatal("StartedAt not set after StartTurn")
	}

	// Starting the turn again, as another Supervisor that follows it does,
	// changes nothing.
	if _, first, err := st.StartTurn(ctx, dispatch.TurnID); err != nil || first {
		t.Errorf("second StartTurn: %v, want first = false", err)
	}
	if again := st.turn(dispatch.TurnID).StartedAt; !again.Equal(*started) {
		t.Errorf("StartedAt changed from %v to %v", started, again)
	}
}

func (suite suite) testCancelTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)

	// A queued turn ends at once.
	session := st.session()
	queued := st.queue(session)
	cancelled, unended, err := st.CancelTurn(ctx, queued.ID)
	if err != nil || !unended || cancelled.State != store.TurnCancelled || cancelled.EndedAt == nil {
		t.Errorf("CancelTurn of a queued turn = %+v, %t: %v", cancelled, unended, err)
	}
	if again, unended, err := st.CancelTurn(ctx, queued.ID); err != nil || unended || again.State != store.TurnCancelled {
		t.Errorf("second CancelTurn of a queued turn = %+v, %t: %v", again, unended, err)
	}
	if dispatches, err := st.DispatchTurns(ctx, 50); err != nil || len(dispatches) != 0 {
		t.Errorf("DispatchTurns after CancelTurn = %v, want none: %v", dispatches, err)
	}

	// A dispatched turn is only marked, and ends when its harness has stopped.
	dispatch := st.running(session)
	asked, unended, err := st.CancelTurn(ctx, dispatch.TurnID)
	if err != nil || !unended || asked.State != store.TurnRunning || asked.CancelRequestedAt == nil || asked.EndedAt != nil {
		t.Fatalf("CancelTurn of a running turn = %+v, %t: %v", asked, unended, err)
	}
	if again, unended, err := st.CancelTurn(ctx, dispatch.TurnID); err != nil || !unended ||
		!again.CancelRequestedAt.Equal(*asked.CancelRequestedAt) {
		t.Errorf("second CancelTurn of a running turn = %+v, %t: %v", again, unended, err)
	}
	if cancelling, err := st.CancellingTurns(ctx); err != nil || !slices.Equal(cancelling, []uuid.UUID{dispatch.TurnID}) {
		t.Errorf("CancellingTurns = %v, want the running turn: %v", cancelling, err)
	}
	st.end(dispatch.TurnID, false, store.Failure{Reason: store.FailureSandboxLost})
	if cancelling, err := st.CancellingTurns(ctx); err != nil || len(cancelling) != 0 {
		t.Errorf("CancellingTurns after the turn ended = %v, want none: %v", cancelling, err)
	}

	// Cancelling a turn that has ended changes nothing.
	completed := st.running(session)
	if err := st.SetFinished(ctx, completed.TurnID, "end_turn", nil); err != nil {
		t.Fatal(err)
	}
	st.end(completed.TurnID, true, store.Failure{})
	if late, unended, err := st.CancelTurn(ctx, completed.TurnID); err != nil || unended ||
		late.State != store.TurnCompleted || late.CancelRequestedAt != nil {
		t.Errorf("CancelTurn of a completed turn = %+v, %t: %v", late, unended, err)
	}
	if _, _, err := st.CancelTurn(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("CancelTurn of a nonexistent turn = %v, want ErrNotFound", err)
	}
}

func (suite suite) testExpireTurns(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	dispatched := st.running(st.session())
	old, fresh := st.queue(st.session()), st.queue(st.session())
	st.database.Age(t, time.Minute, old.ID, dispatched.TurnID)

	expired, err := st.ExpireTurns(ctx, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != old.ID || expired[0].State != store.TurnExpired || expired[0].EndedAt == nil {
		t.Errorf("ExpireTurns = %+v, want only the old queued turn", expired)
	}
	if state := st.turn(fresh.ID).State; state != store.TurnQueued {
		t.Errorf("new queued turn state = %s, want queued", state)
	}
	if state := st.turn(dispatched.TurnID).State; state != store.TurnRunning {
		t.Errorf("running turn state after ExpireTurns = %s, want running", state)
	}
}

func (suite suite) testEndTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	lost := store.Failure{Reason: store.FailureSandboxLost, Message: "the session's sandbox is gone"}
	tests := []struct {
		name string
		// Whether cancellation was requested, and the stop reason, if any,
		// before the turn ends.
		cancelled  bool
		stopReason string
		succeeded  bool
		state      string
		failure    *store.Failure
	}{
		{name: "succeeded", stopReason: "end_turn", succeeded: true, state: store.TurnCompleted},
		{name: "succeeded without stop reason", succeeded: true, state: store.TurnFailed, failure: &lost},
		{name: "failed", state: store.TurnFailed, failure: &lost},
		{name: "failed after stop reason", stopReason: "end_turn", state: store.TurnFailed, failure: &lost},
		{name: "cancelled and failed", cancelled: true, state: store.TurnCancelled},
		{
			name:      "cancelled and stopped by harness",
			cancelled: true, stopReason: "cancelled", succeeded: true, state: store.TurnCancelled,
		},
		{
			name:      "cancelled and succeeded without stop reason",
			cancelled: true, succeeded: true, state: store.TurnCancelled,
		},
		{
			name:      "cancelled after harness ended turn",
			cancelled: true, stopReason: "end_turn", succeeded: true, state: store.TurnCompleted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := st.session()
			dispatch := st.running(session)
			if test.cancelled {
				if _, _, err := st.CancelTurn(ctx, dispatch.TurnID); err != nil {
					t.Fatal(err)
				}
			}
			if test.stopReason != "" {
				if err := st.SetFinished(ctx, dispatch.TurnID, test.stopReason, nil); err != nil {
					t.Fatal(err)
				}
			}
			ended := st.end(dispatch.TurnID, test.succeeded, lost)
			if ended.State != test.state || ended.EndedAt == nil {
				t.Errorf("state = %s, want %s", ended.State, test.state)
			}
			if (ended.Failure == nil) != (test.failure == nil) || ended.Failure != nil && *ended.Failure != *test.failure {
				t.Errorf("failure = %+v, want %+v", ended.Failure, test.failure)
			}
			if kept := st.turn(dispatch.TurnID); kept.State != ended.State {
				t.Errorf("Turn state = %s, want the state EndTurn returned", kept.State)
			}

			// A turn ends once. Later calls change nothing.
			again, changed, err := st.another().EndTurn(ctx, dispatch.TurnID, !test.succeeded, store.Failure{Reason: store.FailureRunner},
				func(context.Context) ([]json.RawMessage, error) {
					t.Error("items called for an ended turn")
					return nil, nil
				})
			if err != nil || changed || again.ID != uuid.Nil {
				t.Errorf("second EndTurn = %+v: %v", again, err)
			}
			if kept := st.turn(dispatch.TurnID); kept.State != ended.State {
				t.Errorf("Turn state after second EndTurn = %s, want unchanged", kept.State)
			}
			// The session accepts its next message.
			next := st.queue(session)
			if next.Seq != 2 {
				t.Errorf("next turn Seq = %d, want 2", next.Seq)
			}
			if _, _, err := st.CancelTurn(ctx, next.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, changed, err := st.EndTurn(ctx, uuid.New(), true, store.Failure{}, nothing); err != nil || changed {
		t.Errorf("EndTurn of a nonexistent turn: %v, want no change", err)
	}
}

// Of concurrent EndTurn calls for a turn, one ends it and stores its items.
func (suite suite) testConcurrentEndTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	dispatch := st.running(st.session())
	supervisors := st.supervisors(8)
	var ended, read atomic.Int32
	atOnce(len(supervisors), func(i int) {
		_, changed, err := supervisors[i].EndTurn(ctx, dispatch.TurnID, i%2 == 0, store.Failure{Reason: store.FailureSandboxLost},
			func(context.Context) ([]json.RawMessage, error) {
				read.Add(1)
				return []json.RawMessage{json.RawMessage(`{"type":"message","text":"one"}`)}, nil
			})
		if err != nil {
			t.Error(err)
		}
		if changed {
			ended.Add(1)
		}
	})
	if ended.Load() != 1 || read.Load() != 1 {
		t.Errorf("of 8 concurrent EndTurn calls, %d ended the turn and %d read its items, want 1 and 1", ended.Load(), read.Load())
	}
	if items, err := st.TurnItems(ctx, []uuid.UUID{dispatch.TurnID}); err != nil || len(items[dispatch.TurnID]) != 1 {
		t.Errorf("TurnItems = %s: %v", items, err)
	}
}

func (suite suite) testEndTurnStoresItems(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	// The output of a tool call may contain U+0000, which a database may be
	// unable to store.
	said := []json.RawMessage{
		json.RawMessage(`{"type":"message","text":"one"}`),
		json.RawMessage(`{"type":"tool_call","output":"a\u0000b \\u0000"}`),
		json.RawMessage(`{"type":"message","text":"two"}`),
	}

	// If items fails, the turn does not end, and can be ended later.
	dispatch := st.running(session)
	_, _, err := st.EndTurn(ctx, dispatch.TurnID, true, store.Failure{}, func(context.Context) ([]json.RawMessage, error) {
		return nil, errors.New("the events cannot be read")
	})
	if err == nil {
		t.Fatal("EndTurn succeeded although items failed")
	}
	if kept := st.turn(dispatch.TurnID); kept.State != store.TurnRunning || kept.EndedAt != nil {
		t.Fatalf("turn after items failed = %+v, want running", kept)
	}
	if _, changed, err := st.EndTurn(ctx, dispatch.TurnID, true, store.Failure{}, func(context.Context) ([]json.RawMessage, error) {
		return said, nil
	}); err != nil || !changed {
		t.Fatalf("EndTurn did not end the turn: %v", err)
	}

	silent := st.running(st.session())
	st.end(silent.TurnID, true, store.Failure{})
	items, err := st.TurnItems(ctx, []uuid.UUID{silent.TurnID, dispatch.TurnID, uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[dispatch.TurnID]) != len(said) {
		t.Fatalf("TurnItems = %s", items)
	}
	for i, given := range said {
		if !keeps(t, items[dispatch.TurnID][i], string(given)) {
			t.Errorf("item %d = %s, want %s", i+1, items[dispatch.TurnID][i], given)
		}
	}
}

func (suite suite) testSetFinished(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	thought := int64(3)
	usage := &store.Usage{TotalTokens: 18, InputTokens: 11, OutputTokens: 7, ThoughtTokens: &thought}

	dispatch := st.running(st.session())
	if err := st.SetFinished(ctx, dispatch.TurnID, "end_turn", usage); err != nil {
		t.Fatal(err)
	}
	ended := st.end(dispatch.TurnID, true, store.Failure{})
	if ended.StopReason == nil || *ended.StopReason != "end_turn" || ended.Usage == nil ||
		ended.Usage.TotalTokens != 18 || ended.Usage.InputTokens != 11 || ended.Usage.OutputTokens != 7 ||
		ended.Usage.ThoughtTokens == nil || *ended.Usage.ThoughtTokens != 3 || ended.Usage.CachedReadTokens != nil {
		t.Errorf("stop reason = %v, usage = %+v", ended.StopReason, ended.Usage)
	}

	silent := st.running(st.session())
	if err := st.SetFinished(ctx, silent.TurnID, "max_tokens", nil); err != nil {
		t.Fatal(err)
	}
	if ended := st.end(silent.TurnID, true, store.Failure{}); *ended.StopReason != "max_tokens" || ended.Usage != nil {
		t.Errorf("usage of a turn without reported usage = %+v, want nil", ended.Usage)
	}
}

func (suite suite) testTurns(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	for range 3 {
		dispatch := st.running(session)
		st.end(dispatch.TurnID, false, store.Failure{Reason: store.FailureSandboxLost})
	}
	st.queue(st.session())
	for _, test := range []struct {
		before, limit int
		seqs          []int
	}{
		{before: math.MaxInt32, limit: 20, seqs: []int{3, 2, 1}},
		{before: math.MaxInt32, limit: 2, seqs: []int{3, 2}},
		{before: 3, limit: 2, seqs: []int{2, 1}},
		{before: 2, limit: 2, seqs: []int{1}},
		{before: 1, limit: 2, seqs: []int{}},
	} {
		turns, err := st.Turns(ctx, session, test.before, test.limit)
		if err != nil {
			t.Fatal(err)
		}
		seqs := []int{}
		for _, turn := range turns {
			seqs = append(seqs, turn.Seq)
		}
		if !slices.Equal(seqs, test.seqs) {
			t.Errorf("Turns(limit %d, before %d) = %v, want %v", test.limit, test.before, seqs, test.seqs)
		}
	}
}

func (suite suite) testDispatchedTurns(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	st.queue(st.session())
	waiting := st.dispatch()
	running := st.running(st.session())
	ended := st.running(st.session())
	st.end(ended.TurnID, true, store.Failure{})
	if _, _, err := st.CancelTurn(ctx, running.TurnID); err != nil {
		t.Fatal(err)
	}
	st.database.Age(t, 2*time.Minute, running.TurnID)

	dispatched, err := st.DispatchedTurns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatched) != 2 {
		t.Fatalf("DispatchedTurns = %+v, want 2 turns", dispatched)
	}
	for _, turn := range dispatched {
		switch turn.ID {
		case waiting.TurnID:
			if turn.Cancelling != 0 {
				t.Errorf("unfollowed dispatched turn = %+v, want no cancellation", turn)
			}
		case running.TurnID:
			if turn.Cancelling < time.Minute {
				t.Errorf("cancelling running turn = %+v, want Cancelling of at least a minute", turn)
			}
		default:
			t.Errorf("unexpected turn %s in DispatchedTurns", turn.ID)
		}
	}
}

// An operation whose context is done fails without effect.
func (suite suite) testCancelledContext(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	gone, leave := context.WithCancel(ctx)
	leave()
	if _, _, err := st.CreateTurn(gone, session, uuid.Nil, "hello", 0, 50); err == nil {
		t.Error("CreateTurn succeeded with a cancelled context")
	}
	if turns, err := st.Turns(ctx, session, math.MaxInt32, 10); err != nil || len(turns) != 0 {
		t.Errorf("CreateTurn with a cancelled context left turns %v: %v", turns, err)
	}
	if _, err := st.Session(gone, session); err == nil {
		t.Error("Session succeeded with a cancelled context")
	}
}

func (suite suite) testNotFound(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	none := uuid.New()
	agent := store.Agent{Slug: unique("agent"), Harness: "opencode"}
	for name, get := range map[string]func() error{
		"Project":         func() error { _, err := st.Project(ctx, none); return err },
		"Agent":           func() error { _, err := st.Agent(ctx, none); return err },
		"Session":         func() error { _, err := st.Session(ctx, none); return err },
		"Turn":            func() error { _, err := st.Turn(ctx, none); return err },
		"TurnHarness":     func() error { _, err := st.TurnHarness(ctx, none); return err },
		"CreateAgent":     func() error { _, err := st.CreateAgent(ctx, none, agent); return err },
		"CreateSession":   func() error { _, err := st.CreateSession(ctx, none, uuid.Nil, ""); return err },
		"SetSessionTitle": func() error { _, err := st.SetSessionTitle(ctx, none, "a title"); return err },
	} {
		if err := get(); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s = %v, want ErrNotFound", name, err)
		}
	}
}
