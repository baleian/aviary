// Package runnertest has runners for tests, which the test plays: a runner
// takes the turns of a session over HTTP, as the runner in a sandbox does,
// and the test tells what happens in each.
package runnertest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// patience is how long a test waits for what a runner is to be told.
const patience = 5 * time.Second

// Runners are the runners of a test, and the turns that they were given.
type Runners struct {
	closed chan struct{}

	mu sync.Mutex
	// given are the turns that the runners were given, in their order, and
	// taken how many of them Turn has returned.
	given []*Turn
	taken int
}

// New returns the runners of the test. Those who read the events of a turn
// are let go when the test ends.
func New(t testing.TB) *Runners {
	rs := &Runners{closed: make(chan struct{})}
	t.Cleanup(func() { close(rs.closed) })
	return rs
}

// Runner returns a runner of the sandbox, which has no turn yet.
func (rs *Runners) Runner(sandbox string) *Runner {
	return &Runner{runners: rs, sandbox: sandbox}
}

// Turn waits for the next turn that a runner is given.
func (rs *Runners) Turn(t testing.TB) *Turn {
	t.Helper()
	for end := time.Now().Add(patience); ; time.Sleep(5 * time.Millisecond) {
		rs.mu.Lock()
		if rs.taken < len(rs.given) {
			rs.taken++
			turn := rs.given[rs.taken-1]
			rs.mu.Unlock()
			return turn
		}
		rs.mu.Unlock()
		if time.Now().After(end) {
			t.Fatal("no runner was given a turn")
		}
	}
}

// TurnWith waits for a runner to be given the turn with the ID, and
// returns it.
func (rs *Runners) TurnWith(t testing.TB, id string) *Turn {
	t.Helper()
	for end := time.Now().Add(patience); ; time.Sleep(5 * time.Millisecond) {
		rs.mu.Lock()
		for _, turn := range rs.given {
			if turn.ID == id {
				rs.mu.Unlock()
				return turn
			}
		}
		rs.mu.Unlock()
		if time.Now().After(end) {
			t.Fatalf("no runner was given the turn %s", id)
		}
	}
}

// Given returns the IDs of the turns that the runners were given, in their
// order.
func (rs *Runners) Given() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	ids := []string{}
	for _, turn := range rs.given {
		ids = append(ids, turn.ID)
	}
	return ids
}

// Runner is the runner of a sandbox.
type Runner struct {
	runners *Runners
	sandbox string
	mu      sync.Mutex
	// turn is the turn that the runner has: the last that it was given.
	turn *Turn
}

// Gone has the runner go, as with the pod that it runs in: those who read
// the events of its turn are cut off.
func (rn *Runner) Gone() {
	rn.mu.Lock()
	turn := rn.turn
	rn.mu.Unlock()
	if turn != nil {
		turn.Cut()
	}
}

// Turn is a turn that a runner was given, which the test runs.
type Turn struct {
	// Sandbox is the sandbox of the runner.
	Sandbox string `json:"-"`
	ID      string `json:"-"`
	// What the turn was started with.
	Text         string            `json:"text"`
	Instructions string            `json:"instructions"`
	Approval     string            `json:"approval"`
	Environment  map[string]string `json:"environment"`

	closed chan struct{}

	mu     sync.Mutex
	events []event
	ended  bool
	// stops is how often the turn was told to stop, and answers are the
	// answers that it was given: the request and the option of each. The
	// test has seen so many of them.
	stops, seenStops int
	answers          [][2]string
	seenAnswers      int
	// cuts counts how often those who read the events were cut off.
	cuts int
	// changed is closed when there is more to read.
	changed chan struct{}
}

type event struct {
	name string
	data []byte
}

func (rn *Runner) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /turns/{turn}", rn.start)
	mux.HandleFunc("GET /turns/{turn}/events", rn.events)
	mux.HandleFunc("POST /turns/{turn}/cancel", func(w http.ResponseWriter, r *http.Request) {
		rn.tell(w, r, func(turn *Turn) {
			turn.mu.Lock()
			defer turn.mu.Unlock()
			turn.stops++
		})
	})
	mux.HandleFunc("PUT /turns/{turn}/approvals/{request}", func(w http.ResponseWriter, r *http.Request) {
		var answer struct {
			Option string `json:"option_id"`
		}
		if json.NewDecoder(r.Body).Decode(&answer) != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		rn.tell(w, r, func(turn *Turn) {
			turn.mu.Lock()
			defer turn.mu.Unlock()
			turn.answers = append(turn.answers, [2]string{r.PathValue("request"), answer.Option})
		})
	})
	mux.ServeHTTP(w, r)
}

// has returns the turn that the request is about, if the runner has it.
func (rn *Runner) has(r *http.Request) *Turn {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if rn.turn == nil || rn.turn.ID != r.PathValue("turn") {
		return nil
	}
	return rn.turn
}

func (rn *Runner) start(w http.ResponseWriter, r *http.Request) {
	turn := &Turn{
		Sandbox: rn.sandbox,
		ID:      r.PathValue("turn"),
		closed:  rn.runners.closed,
		changed: make(chan struct{}),
	}
	if json.NewDecoder(r.Body).Decode(turn) != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	rn.mu.Lock()
	defer rn.mu.Unlock()
	switch {
	case rn.turn != nil && rn.turn.ID == turn.ID:
	case rn.turn != nil && !rn.turn.over():
		w.WriteHeader(http.StatusConflict)
		return
	default:
		rn.turn = turn
		rn.runners.mu.Lock()
		rn.runners.given = append(rn.runners.given, turn)
		rn.runners.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (rn *Runner) tell(w http.ResponseWriter, r *http.Request, tell func(*Turn)) {
	turn := rn.has(r)
	if turn == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	tell(turn)
	w.WriteHeader(http.StatusNoContent)
}

// events sends the events of the turn as server-sent events, from the one
// after Last-Event-ID, until the turn ends.
func (rn *Runner) events(w http.ResponseWriter, r *http.Request) {
	turn := rn.has(r)
	if turn == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	after, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	turn.mu.Lock()
	cuts := turn.cuts
	turn.mu.Unlock()
	for {
		turn.mu.Lock()
		events := turn.events[min(after, len(turn.events)):]
		ended, changed, cut := turn.ended, turn.changed, turn.cuts != cuts
		turn.mu.Unlock()
		if cut {
			// The connection is dropped, and the answer has no end.
			panic(http.ErrAbortHandler)
		}
		for _, event := range events {
			after++
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", after, event.name, event.data)
		}
		w.(http.Flusher).Flush()
		if ended {
			return
		}
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		case <-turn.closed:
			return
		}
	}
}

func (t *Turn) over() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ended
}

// change lets those who read the events of the turn look again.
func (t *Turn) change() {
	close(t.changed)
	t.changed = make(chan struct{})
}

// Tell adds an event to the turn. The events finished and failed end it:
// what is told after them is not heard.
func (t *Turn) Tell(name string, data any) {
	encoded, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended {
		return
	}
	t.events = append(t.events, event{name, encoded})
	t.ended = name == "finished" || name == "failed"
	t.change()
}

// Cut cuts off those who read the events of the turn.
func (t *Turn) Cut() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cuts++
	t.change()
}

// Stopped waits until the turn was told to stop once more.
func (t *Turn) Stopped(tb testing.TB) {
	tb.Helper()
	for end := time.Now().Add(patience); ; time.Sleep(5 * time.Millisecond) {
		t.mu.Lock()
		if t.seenStops < t.stops {
			t.seenStops++
			t.mu.Unlock()
			return
		}
		t.mu.Unlock()
		if time.Now().After(end) {
			tb.Fatalf("the turn %s was not told to stop", t.ID)
		}
	}
}

// Answered waits until a request of the turn was answered once more, and
// returns the request and the option that was chosen.
func (t *Turn) Answered(tb testing.TB) (request, option string) {
	tb.Helper()
	for end := time.Now().Add(patience); ; time.Sleep(5 * time.Millisecond) {
		t.mu.Lock()
		if t.seenAnswers < len(t.answers) {
			t.seenAnswers++
			answer := t.answers[t.seenAnswers-1]
			t.mu.Unlock()
			return answer[0], answer[1]
		}
		t.mu.Unlock()
		if time.Now().After(end) {
			tb.Fatalf("no request of the turn %s was answered", t.ID)
		}
	}
}
