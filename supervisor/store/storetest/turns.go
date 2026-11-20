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

// supervisor is the store of a Supervisor in a test. Its methods fail the
// test if the store does not do what they ask of it.
type supervisor struct {
	store.Store
	t        *testing.T
	database Database
}

// open returns a Supervisor's store on a database of its own.
func (suite suite) open(t *testing.T) supervisor {
	t.Helper()
	database := suite.create(t)
	return supervisor{Store: database.Open(t), t: t, database: database}
}

// another returns the store of another Supervisor on the same database.
func (s supervisor) another() supervisor {
	s.t.Helper()
	return supervisor{Store: s.database.Open(s.t), t: s.t, database: s.database}
}

// session creates a session of an agent of its own.
func (s supervisor) session() uuid.UUID {
	s.t.Helper()
	project, err := s.CreateProject(ctx, "test")
	if err != nil {
		s.t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, project.ID, store.Agent{Harness: "opencode", Instructions: "Be brief.", Approval: "ask"})
	if err != nil {
		s.t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, agent.ID, uuid.Nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return session.ID
}

// queue accepts a message to the session as a turn, with no limit on the
// turns that wait.
func (s supervisor) queue(session uuid.UUID) store.Turn {
	s.t.Helper()
	turn, _, err := s.CreateTurn(ctx, session, uuid.Nil, "hello", 0, 50)
	if err != nil {
		s.t.Fatal(err)
	}
	return turn
}

// dispatch dispatches the one turn that waits, and returns what runs it.
func (s supervisor) dispatch() store.Dispatch {
	s.t.Helper()
	dispatches, err := s.DispatchTurns(ctx, 50)
	if err != nil {
		s.t.Fatal(err)
	}
	if len(dispatches) != 1 {
		s.t.Fatalf("%d turns were dispatched, not one", len(dispatches))
	}
	return dispatches[0]
}

// running queues a message to the session and has a Supervisor follow its
// turn, which then runs. It returns the turn's dispatch.
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

// end ends the turn as the turn of a pod that succeeded or failed, and
// returns it.
func (s supervisor) end(id uuid.UUID, succeeded bool, failure store.Failure) store.Turn {
	s.t.Helper()
	turn, changed, err := s.EndTurn(ctx, id, succeeded, failure, nothing)
	if err != nil || !changed {
		s.t.Fatalf("the turn was not ended: %v", err)
	}
	return turn
}

// nothing is what a turn said in which the agent said nothing.
func nothing(context.Context) ([]json.RawMessage, error) { return nil, nil }

func (suite suite) testCreateTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	first := st.queue(session)
	if first.Seq != 1 || first.State != store.TurnQueued || first.Text != "hello" {
		t.Errorf("the first turn of a session is %+v", first)
	}
	if _, _, err := st.CreateTurn(ctx, session, uuid.Nil, "again", 0, 50); !errors.Is(err, store.ErrSessionBusy) {
		t.Errorf("a session with a turn that has not ended took a message: %v", err)
	}
	if _, _, err := st.CancelTurn(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if second := st.queue(session); second.Seq != 2 {
		t.Errorf("the second turn of a session has the number %d", second.Seq)
	}
	if _, _, err := st.CreateTurn(ctx, uuid.New(), uuid.Nil, "hello", 0, 50); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a session that does not exist took a message: %v", err)
	}
}

// atOnce calls the function so many times at once, each time with another
// number from 0, and returns when all the calls have.
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

// supervisors returns the Stores of so many Supervisors on the database.
func (s supervisor) supervisors(count int) []supervisor {
	s.t.Helper()
	supervisors := make([]supervisor, count)
	for i := range supervisors {
		supervisors[i] = s.another()
	}
	return supervisors
}

func (suite suite) testCreateTurnTakesOneOfTheMessagesSentAtOnce(t *testing.T) {
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
		t.Errorf("of 20 messages sent to a session at once, %d were accepted and %d refused", accepted.Load(), busy.Load())
	}
}

func (suite suite) testCreateTurnKeepsToTheMostThatMayWait(t *testing.T) {
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
			t.Fatalf("%d turns wait, not %d: %v", waiting, expected, err)
		}
	}
	refused := st.session()
	if _, err := send(refused); !errors.Is(err, store.ErrQueueFull) {
		t.Fatalf("a message beyond the most that may wait was accepted: %v", err)
	}
	if turns, err := st.Turns(ctx, refused, math.MaxInt32, 10); err != nil || len(turns) != 0 {
		t.Errorf("the refused message left a turn behind: %v, %v", turns, err)
	}

	// The two that run make no room in the queue until one of them ends.
	dispatches, err := st.DispatchTurns(ctx, maxRunning)
	if err != nil || len(dispatches) != maxRunning {
		t.Fatalf("%d turns were dispatched: %v", len(dispatches), err)
	}
	if _, err := send(refused); !errors.Is(err, store.ErrQueueFull) {
		t.Errorf("a message beyond the most that may wait was accepted: %v", err)
	}
	st.end(dispatches[0].TurnID, false, store.Failure{Reason: store.FailureSandboxLost})
	if waiting, err := send(refused); err != nil || waiting != 2 {
		t.Errorf("after a turn ended, %d turns wait: %v", waiting, err)
	}
}

func (suite suite) testSupervisorsKeepToTheMostThatMayWaitTogether(t *testing.T) {
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
		t.Errorf("of 12 messages sent at once, %d were accepted and %d refused", accepted.Load(), refused.Load())
	}
}

// Messages that come in together are turns that wait only until they are
// dispatched. With room for all of them to run, none counts as waiting.
func (suite suite) testCreateTurnLetsThoseInThatHaveRoomToRun(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	sessions := make([]uuid.UUID, 12)
	for i := range sessions {
		sessions[i] = st.session()
	}
	atOnce(len(sessions), func(i int) {
		if _, waiting, err := st.CreateTurn(ctx, sessions[i], uuid.Nil, "hello", 2, 50); err != nil || waiting != 0 {
			t.Errorf("%d turns wait: %v", waiting, err)
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
		t.Fatalf("the turns %v were dispatched, not the two oldest of %v", dispatches, queued)
	}
	first := dispatches[0]
	if first.Harness != "opencode" {
		t.Errorf("the dispatch is %+v", first)
	}
	if state := st.turn(first.TurnID).State; state != store.TurnDispatching {
		t.Errorf("a dispatched turn is %s", state)
	}

	// A turn counts against the limit from its dispatch to its end.
	if _, err := st.FollowTurn(ctx, first.TurnID); err != nil {
		t.Fatal(err)
	}
	if more, err := st.DispatchTurns(ctx, 2); err != nil || len(more) != 0 {
		t.Fatalf("%d turns were dispatched beyond the limit: %v", len(more), err)
	}
	st.end(first.TurnID, true, store.Failure{})
	if last := st.dispatch(); last.TurnID != queued[2].ID {
		t.Errorf("the turn %s was dispatched, not the one that waited", last.TurnID)
	}
}

func (suite suite) testSupervisorsDispatchWithinTheLimitTogether(t *testing.T) {
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
		t.Errorf("eight Supervisors with a limit of 5 dispatched the turns %v", dispatched)
	}
}

// The limit holds also while messages come. A Supervisor that counts the
// turns that run and then picks the next may find other turns to pick than
// another that counted at the same time.
func (suite suite) testSupervisorsDispatchWithinTheLimitWhileTurnsCome(t *testing.T) {
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
			t.Fatalf("four Supervisors with a limit of 1 dispatched %d turns", len(dispatched))
		}
		// The turns end, for the next to come to a session with none.
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

// A turn that is cancelled while the turns are dispatched has ended and is
// not dispatched, or is dispatched and asked to stop: never both.
func (suite suite) testDispatchTurnsLeavesATurnThatWasCancelledMeanwhile(t *testing.T) {
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
			t.Errorf("a turn that was cancelled while the turns were dispatched is %+v, dispatched %t", turn, dispatched[turn.ID])
		}
	}
}

// A turn that waits for a person takes no place among the turns that run:
// neither when turns are dispatched, nor when those that wait are counted.
func (suite suite) testDispatchTurnsCountsNoTurnThatWaitsForAPerson(t *testing.T) {
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
	dispatch := func() []uuid.UUID {
		t.Helper()
		dispatches, err := st.DispatchTurns(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		turns := []uuid.UUID{}
		for _, dispatch := range dispatches {
			turns = append(turns, dispatch.TurnID)
		}
		return turns
	}
	first, _ := send()
	if turns := dispatch(); !slices.Equal(turns, []uuid.UUID{first}) {
		t.Fatalf("the turns %v were dispatched, not the one that waited", turns)
	}
	second, waiting := send()
	if turns := dispatch(); waiting != 1 || len(turns) != 0 {
		t.Errorf("while a turn runs, %d turns wait and the turns %v were dispatched beyond the limit", waiting, turns)
	}

	if err := st.AddApproval(ctx, first, "1", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if turns := dispatch(); !slices.Equal(turns, []uuid.UUID{second}) {
		t.Fatalf("while the turn that runs waits for a person, the turns %v were dispatched, not the one that waited", turns)
	}
	st.end(second, false, store.Failure{Reason: store.FailureSandboxLost})
	// The turn waits also when a person has answered, until its harness has
	// the answer.
	if answered, err := st.AnswerApproval(ctx, first, "1", "once", uuid.Nil); err != nil || !answered {
		t.Fatalf("the request was answered %t: %v", answered, err)
	}
	if _, waiting := send(); waiting != 0 {
		t.Errorf("%d turns wait while the only turn that runs waits for a person", waiting)
	}

	// It takes its place again when its harness has the answer.
	if _, _, _, err := st.ResolveApproval(ctx, first, "1", "selected"); err != nil {
		t.Fatal(err)
	}
	if turns := dispatch(); len(turns) != 0 {
		t.Errorf("the turns %v were dispatched beyond the limit, while a turn runs that waits for a person no longer", turns)
	}
}

func (suite suite) testRequeueTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	first, second := st.queue(st.session()), st.queue(st.session())
	dispatches, err := st.DispatchTurns(ctx, 1)
	if err != nil || len(dispatches) != 1 || dispatches[0].TurnID != first.ID {
		t.Fatalf("the turns %v were dispatched, not the oldest: %v", dispatches, err)
	}
	if _, err := st.FollowTurn(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if requeued, err := st.RequeueTurn(ctx, first.ID); err != nil || !requeued {
		t.Fatalf("a dispatched turn was put back %t: %v", requeued, err)
	}
	if state := st.turn(first.ID).State; state != store.TurnQueued {
		t.Errorf("a turn that was put back is %s", state)
	}
	// It waits in the place that it had: before the turn that was accepted
	// after it.
	err = st.AnnounceQueue(ctx, func(queued []uuid.UUID) error {
		if !slices.Equal(queued, []uuid.UUID{first.ID, second.ID}) {
			t.Errorf("the queue is %v, not %v", queued, []uuid.UUID{first.ID, second.ID})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.FollowTurn(ctx, first.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a turn that was put back was followed: %v", err)
	}
	if dispatched, err := st.DispatchedTurns(ctx); err != nil || len(dispatched) != 0 {
		t.Errorf("the dispatched turns are %+v: %v", dispatched, err)
	}
	// What comes late of the dispatch before does nothing to the turn.
	if _, started, err := st.StartTurn(ctx, first.ID); err != nil || started || st.turn(first.ID).StartedAt != nil {
		t.Errorf("a turn that waits to run started %t: %v", started, err)
	}

	// It is dispatched again when it is the oldest.
	again, err := st.DispatchTurns(ctx, 1)
	if err != nil || len(again) != 1 || again[0].TurnID != first.ID {
		t.Fatalf("after the turn was put back, %+v was dispatched: %v", again, err)
	}
	if _, err := st.FollowTurn(ctx, first.ID); err != nil {
		t.Fatal(err)
	}

	// Only a turn that was dispatched, and of which nothing has come yet, is
	// put back. Any other stays as it is.
	refused := func(what string, id uuid.UUID) {
		t.Helper()
		before, _ := st.Turn(ctx, id)
		if requeued, err := st.RequeueTurn(ctx, id); err != nil || requeued {
			t.Errorf("%s was put back %t: %v", what, requeued, err)
		}
		if after, _ := st.Turn(ctx, id); after.State != before.State {
			t.Errorf("%s was %s, and is %s after it was not put back", what, before.State, after.State)
		}
	}
	refused("a turn that waits to run", second.ID)
	refused("a turn that there is not", uuid.New())
	if _, _, err := st.StartTurn(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	refused("a turn whose harness has been given the message", first.ID)
	st.end(first.ID, true, store.Failure{})
	refused("a turn that has ended", first.ID)
	asked := st.dispatch()
	if _, _, err := st.CancelTurn(ctx, asked.TurnID); err != nil {
		t.Fatal(err)
	}
	refused("a turn that was asked to stop", asked.TurnID)
}

// A turn that was put back waits as one that was never dispatched: it is
// cancelled at once, and it expires by the time since it was accepted.
func (suite suite) testRequeuedTurnWaitsAsOneThatWasNeverDispatched(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	requeue := func(turn uuid.UUID) {
		t.Helper()
		if requeued, err := st.RequeueTurn(ctx, turn); err != nil || !requeued {
			t.Fatalf("a dispatched turn was put back %t: %v", requeued, err)
		}
	}

	st.queue(st.session())
	cancelled := st.dispatch().TurnID
	requeue(cancelled)
	if turn, unended, err := st.CancelTurn(ctx, cancelled); err != nil || !unended || turn.State != store.TurnCancelled || turn.EndedAt == nil {
		t.Errorf("a turn that was put back and cancelled is %+v, had not ended %t: %v", turn, unended, err)
	}

	st.queue(st.session())
	old := st.dispatch().TurnID
	requeue(old)
	st.database.Age(t, time.Minute, old)
	if expired, err := st.ExpireTurns(ctx, 30*time.Second); err != nil || len(expired) != 1 || expired[0].ID != old || expired[0].State != store.TurnExpired {
		t.Errorf("the turns %+v expired, not the one that was put back and has waited for a minute: %v", expired, err)
	}
}

// A turn is put back or it starts, also when one Supervisor puts it back
// while another records its start: it never waits to run and has started.
func (suite suite) testSupervisorsRequeueAndStartATurnAtOnce(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	requeuer, starter, session := st.another(), st.another(), st.session()
	for range 40 {
		turn := st.queue(session).ID
		st.dispatch()
		var requeued, started bool
		atOnce(2, func(i int) {
			var err error
			if i == 0 {
				requeued, err = requeuer.RequeueTurn(ctx, turn)
			} else {
				_, started, err = starter.StartTurn(ctx, turn)
			}
			if err != nil {
				t.Error(err)
			}
		})
		kept := st.turn(turn)
		if requeued == started || (kept.State == store.TurnQueued) != requeued || (kept.StartedAt != nil) != started {
			t.Fatalf("a turn that was put back %t and started %t at once is %s, and started at %v", requeued, started, kept.State, kept.StartedAt)
		}
		// The turn ends, for the next to come to a session with none.
		if _, _, err := st.CancelTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
		if started {
			st.end(turn, false, store.Failure{Reason: store.FailureSandboxLost})
		}
	}
}

func (suite suite) testAnnounceQueue(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	first, gone, last := st.queue(st.session()), st.queue(st.session()), st.queue(st.session())
	if _, _, err := st.CancelTurn(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	// One announcement at a time, also from several Supervisors. Two at once
	// could tell a turn an older position after a newer one.
	supervisors := st.supervisors(4)
	var announcing atomic.Int32
	atOnce(len(supervisors), func(i int) {
		err := supervisors[i].AnnounceQueue(ctx, func(queued []uuid.UUID) error {
			if announcing.Add(1) != 1 {
				t.Error("two announcements of the queue ran at once")
			}
			defer announcing.Add(-1)
			time.Sleep(20 * time.Millisecond)
			if !slices.Equal(queued, []uuid.UUID{first.ID, last.ID}) {
				t.Errorf("the queue is %v, not %v", queued, []uuid.UUID{first.ID, last.ID})
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	})
}

// A Supervisor that follows a turn is given what it takes to run the turn,
// as it is now.
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
		t.Errorf("a turn that waits to run was followed: %v", err)
	}
	if _, err := st.FollowTurn(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a turn that there is not was followed: %v", err)
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
		followed.ProviderURL != "" || followed.Model != "" || followed.Sandbox != "" ||
		followed.Dispatched < 0 || followed.Dispatched > time.Minute {
		t.Errorf("the Supervisor was given %+v", followed)
	}
	if state := st.turn(followed.ID).State; state != store.TurnRunning {
		t.Errorf("a turn that a Supervisor follows is %s", state)
	}

	// The sandbox that the session has got meanwhile, and that the turn was
	// asked to stop, are told to who follows it then: the same Supervisor,
	// or the one that follows the turn after it.
	if set, err := st.SetSandbox(ctx, session, "", "sandbox-1"); err != nil || !set {
		t.Fatalf("the session was not given its sandbox: %v", err)
	}
	if _, _, err := st.CancelTurn(ctx, dispatch.TurnID); err != nil {
		t.Fatal(err)
	}
	again, err := st.another().FollowTurn(ctx, dispatch.TurnID)
	if err != nil || again.ID != followed.ID || again.Sandbox != "sandbox-1" || again.CancelRequestedAt == nil {
		t.Errorf("the Supervisor that follows the turn then was given %+v: %v", again, err)
	}

	st.end(dispatch.TurnID, false, store.Failure{Reason: store.FailureRunner})
	if _, err := st.FollowTurn(ctx, dispatch.TurnID); !errors.Is(err, store.ErrTurnEnded) {
		t.Errorf("a turn that has ended was followed: %v", err)
	}
}

func (suite suite) testStartTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	dispatch := st.running(st.session())
	if started := st.turn(dispatch.TurnID).StartedAt; started != nil {
		t.Fatalf("a turn whose harness has no message yet started at %v", started)
	}
	startup, first, err := st.StartTurn(ctx, dispatch.TurnID)
	if err != nil || !first || startup < 0 || startup > time.Minute {
		t.Fatalf("the turn started after %s, for the first time %t: %v", startup, first, err)
	}
	started := st.turn(dispatch.TurnID).StartedAt
	if started == nil {
		t.Fatal("the turn has not started")
	}

	// Another Supervisor that follows the turn reads the start again.
	if _, first, err := st.StartTurn(ctx, dispatch.TurnID); err != nil || first {
		t.Errorf("the turn started a second time: %v", err)
	}
	if again := st.turn(dispatch.TurnID).StartedAt; !again.Equal(*started) {
		t.Errorf("the turn started at %v and then at %v", started, again)
	}
}

func (suite suite) testCancelTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)

	// A turn that waits ends at once.
	session := st.session()
	queued := st.queue(session)
	cancelled, unended, err := st.CancelTurn(ctx, queued.ID)
	if err != nil || !unended || cancelled.State != store.TurnCancelled || cancelled.EndedAt == nil {
		t.Errorf("a waiting turn that was cancelled is %+v, had not ended %t: %v", cancelled, unended, err)
	}
	if again, unended, err := st.CancelTurn(ctx, queued.ID); err != nil || unended || again.State != store.TurnCancelled {
		t.Errorf("a cancelled turn that was cancelled again is %+v, had not ended %t: %v", again, unended, err)
	}
	if dispatches, err := st.DispatchTurns(ctx, 50); err != nil || len(dispatches) != 0 {
		t.Errorf("a cancelled turn was dispatched: %v, %v", dispatches, err)
	}

	// A turn that was dispatched is asked to stop, and ends with its pod.
	dispatch := st.running(session)
	asked, unended, err := st.CancelTurn(ctx, dispatch.TurnID)
	if err != nil || !unended || asked.State != store.TurnRunning || asked.CancelRequestedAt == nil || asked.EndedAt != nil {
		t.Fatalf("a running turn that was cancelled is %+v, had not ended %t: %v", asked, unended, err)
	}
	if again, unended, err := st.CancelTurn(ctx, dispatch.TurnID); err != nil || !unended ||
		!again.CancelRequestedAt.Equal(*asked.CancelRequestedAt) {
		t.Errorf("a turn that was asked to stop again is %+v, had not ended %t: %v", again, unended, err)
	}
	if cancelling, err := st.CancellingTurns(ctx); err != nil || !slices.Equal(cancelling, []uuid.UUID{dispatch.TurnID}) {
		t.Errorf("the turns that were asked to stop are %v: %v", cancelling, err)
	}
	st.end(dispatch.TurnID, false, store.Failure{Reason: store.FailureSandboxLost})
	if cancelling, err := st.CancellingTurns(ctx); err != nil || len(cancelling) != 0 {
		t.Errorf("the turns that were asked to stop are %v: %v", cancelling, err)
	}

	// A turn that has ended stays as it ended.
	completed := st.running(session)
	if err := st.SetFinished(ctx, completed.TurnID, "end_turn", nil); err != nil {
		t.Fatal(err)
	}
	st.end(completed.TurnID, true, store.Failure{})
	if late, unended, err := st.CancelTurn(ctx, completed.TurnID); err != nil || unended ||
		late.State != store.TurnCompleted || late.CancelRequestedAt != nil {
		t.Errorf("a completed turn that was cancelled is %+v, had not ended %t: %v", late, unended, err)
	}
	if _, _, err := st.CancelTurn(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a turn that does not exist was cancelled: %v", err)
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
		t.Errorf("the turns %+v expired, not the one that waited for a minute", expired)
	}
	if state := st.turn(fresh.ID).State; state != store.TurnQueued {
		t.Errorf("a turn that has just been queued is %s", state)
	}
	if state := st.turn(dispatched.TurnID).State; state != store.TurnRunning {
		t.Errorf("a turn that runs is %s after the turns that waited too long expired", state)
	}
}

func (suite suite) testEndTurn(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	lost := store.Failure{Reason: store.FailureSandboxLost, Message: "the sandbox of the session is gone"}
	tests := []struct {
		name string
		// What the turn went through before its pod ended: whether it was
		// asked to stop, and why the harness said that it stopped, if it did.
		cancelled  bool
		stopReason string
		succeeded  bool
		state      string
		failure    *store.Failure
	}{
		{name: "the pod succeeded", stopReason: "end_turn", succeeded: true, state: store.TurnCompleted},
		{name: "the pod succeeded and the runner reported no end", succeeded: true, state: store.TurnFailed, failure: &lost},
		{name: "the pod failed", state: store.TurnFailed, failure: &lost},
		{name: "the pod failed after the harness had ended the turn", stopReason: "end_turn", state: store.TurnFailed, failure: &lost},
		{name: "the turn was cancelled and its pod failed", cancelled: true, state: store.TurnCancelled},
		{
			name:      "the turn was cancelled and the harness stopped it",
			cancelled: true, stopReason: "cancelled", succeeded: true, state: store.TurnCancelled,
		},
		{
			name:      "the turn was cancelled and its pod succeeded without an end from the runner",
			cancelled: true, succeeded: true, state: store.TurnCancelled,
		},
		{
			name:      "the turn was cancelled when the harness had ended it by itself",
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
				t.Errorf("the turn ended as %s, not as %s", ended.State, test.state)
			}
			if (ended.Failure == nil) != (test.failure == nil) || ended.Failure != nil && *ended.Failure != *test.failure {
				t.Errorf("the turn failed for %+v, not for %+v", ended.Failure, test.failure)
			}
			if kept := st.turn(dispatch.TurnID); kept.State != ended.State {
				t.Errorf("the turn is kept as %s", kept.State)
			}

			// A turn ends once. The Supervisors that come later change nothing.
			again, changed, err := st.another().EndTurn(ctx, dispatch.TurnID, !test.succeeded, store.Failure{Reason: store.FailureRunner},
				func(context.Context) ([]json.RawMessage, error) {
					t.Error("what the turn said was read for a turn that had ended")
					return nil, nil
				})
			if err != nil || changed || again.ID != uuid.Nil {
				t.Errorf("the turn ended a second time, as %+v: %v", again, err)
			}
			if kept := st.turn(dispatch.TurnID); kept.State != ended.State {
				t.Errorf("the turn is %s after it was ended a second time", kept.State)
			}
			// The session takes its next message.
			next := st.queue(session)
			if next.Seq != 2 {
				t.Errorf("the next turn of the session has the number %d", next.Seq)
			}
			if _, _, err := st.CancelTurn(ctx, next.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, changed, err := st.EndTurn(ctx, uuid.New(), true, store.Failure{}, nothing); err != nil || changed {
		t.Errorf("a turn that does not exist was ended: %v", err)
	}
}

// A turn ends once, also when several Supervisors end it at once: one of
// them reads what the turn said and keeps it.
func (suite suite) testSupervisorsEndATurnOnceTogether(t *testing.T) {
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
		t.Errorf("of eight Supervisors that ended a turn at once, %d ended it and %d read what it said", ended.Load(), read.Load())
	}
	if items, err := st.TurnItems(ctx, []uuid.UUID{dispatch.TurnID}); err != nil || len(items[dispatch.TurnID]) != 1 {
		t.Errorf("the items of the turn are %s: %v", items, err)
	}
}

func (suite suite) testEndTurnKeepsWhatTheTurnSaid(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	// What a tool gave may have the character U+0000 in it, which a database
	// may be unable to keep.
	said := []json.RawMessage{
		json.RawMessage(`{"type":"message","text":"one"}`),
		json.RawMessage(`{"type":"tool_call","output":"a\u0000b \\u0000"}`),
		json.RawMessage(`{"type":"message","text":"two"}`),
	}

	// A turn whose items cannot be read has not ended, and is ended later.
	dispatch := st.running(session)
	_, _, err := st.EndTurn(ctx, dispatch.TurnID, true, store.Failure{}, func(context.Context) ([]json.RawMessage, error) {
		return nil, errors.New("the events cannot be read")
	})
	if err == nil {
		t.Fatal("a turn ended whose items could not be read")
	}
	if kept := st.turn(dispatch.TurnID); kept.State != store.TurnRunning || kept.EndedAt != nil {
		t.Fatalf("the turn whose items could not be read is %+v", kept)
	}
	if _, changed, err := st.EndTurn(ctx, dispatch.TurnID, true, store.Failure{}, func(context.Context) ([]json.RawMessage, error) {
		return said, nil
	}); err != nil || !changed {
		t.Fatalf("the turn was not ended: %v", err)
	}

	silent := st.running(st.session())
	st.end(silent.TurnID, true, store.Failure{})
	items, err := st.TurnItems(ctx, []uuid.UUID{silent.TurnID, dispatch.TurnID, uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[dispatch.TurnID]) != len(said) {
		t.Fatalf("the items of the turns are %s", items)
	}
	for i, given := range said {
		if !keeps(t, items[dispatch.TurnID][i], string(given)) {
			t.Errorf("item %d is %s, not %s", i+1, items[dispatch.TurnID][i], given)
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
		t.Errorf("the turn ended with the stop reason %v and the usage %+v", ended.StopReason, ended.Usage)
	}

	silent := st.running(st.session())
	if err := st.SetFinished(ctx, silent.TurnID, "max_tokens", nil); err != nil {
		t.Fatal(err)
	}
	if ended := st.end(silent.TurnID, true, store.Failure{}); *ended.StopReason != "max_tokens" || ended.Usage != nil {
		t.Errorf("a turn whose harness reported no usage has %+v", ended.Usage)
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
			t.Errorf("the %d turns before turn %d are %v, not %v", test.limit, test.before, seqs, test.seqs)
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
		t.Fatalf("the dispatched turns are %+v", dispatched)
	}
	for _, turn := range dispatched {
		switch turn.ID {
		case waiting.TurnID:
			if turn.Cancelling != 0 {
				t.Errorf("the turn that waits to be followed is %+v", turn)
			}
		case running.TurnID:
			if turn.Cancelling < time.Minute {
				t.Errorf("the turn that runs and was asked to stop is %+v", turn)
			}
		default:
			t.Errorf("the turn %s is not dispatched", turn.ID)
		}
	}
}

// An operation whose caller waits for it no longer fails.
func (suite suite) testCallerThatWaitsNoLonger(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	session := st.session()
	gone, leave := context.WithCancel(ctx)
	leave()
	if _, _, err := st.CreateTurn(gone, session, uuid.Nil, "hello", 0, 50); err == nil {
		t.Error("a message was accepted whose sender waits no longer")
	}
	if turns, err := st.Turns(ctx, session, math.MaxInt32, 10); err != nil || len(turns) != 0 {
		t.Errorf("the message left a turn behind: %v, %v", turns, err)
	}
	if _, err := st.Session(gone, session); err == nil {
		t.Error("a session was read for a caller that waits no longer")
	}
}

func (suite suite) testNotFound(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	none := uuid.New()
	for name, get := range map[string]func() error{
		"project":         func() error { _, err := st.Project(ctx, none); return err },
		"agent":           func() error { _, err := st.Agent(ctx, none); return err },
		"session":         func() error { _, err := st.Session(ctx, none); return err },
		"turn":            func() error { _, err := st.Turn(ctx, none); return err },
		"harness of none": func() error { _, err := st.TurnHarness(ctx, none); return err },
		"agent of none":   func() error { _, err := st.CreateAgent(ctx, none, store.Agent{Harness: "opencode"}); return err },
		"session of none": func() error { _, err := st.CreateSession(ctx, none, uuid.Nil); return err },
	} {
		if err := get(); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
