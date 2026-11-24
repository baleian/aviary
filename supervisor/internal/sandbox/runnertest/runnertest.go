// Package runnertest provides fake runners that serve the runner's HTTP API,
// with turns that the test drives.
package runnertest

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// patience is how long the wait helpers wait before they fail the test.
const patience = 5 * time.Second

// Runners is a set of fake runners. It records the turns given to them.
type Runners struct {
	closed chan struct{}

	mu sync.Mutex
	// given holds the turns in the order in which they were given, and taken
	// counts those that Turn has returned.
	given []*Turn
	taken int
}

// New returns an empty set of runners. Open event streams end when the test
// ends.
func New(t testing.TB) *Runners {
	rs := &Runners{closed: make(chan struct{})}
	t.Cleanup(func() { close(rs.closed) })
	return rs
}

// Runner returns a new runner for the named sandbox, whose home is home.
func (rs *Runners) Runner(sandbox string, home *Home) *Runner {
	return &Runner{runners: rs, sandbox: sandbox, home: home}
}

// Home is the home of a sandbox. It outlives the runners of the sandbox, and
// its archive is its content.
type Home struct {
	mu      sync.Mutex
	content string
	// files are the files that messages carried, by their paths below the
	// attachments of the home. full makes the home refuse more.
	files map[string]string
	full  bool
	// unpacked counts the archives that were unpacked into the home.
	unpacked int
	// cut makes the runner end the next archive that it packs halfway, and
	// refuse makes it refuse the next one that it is to unpack.
	cut, refuse bool
	// held, if not nil, holds an archive halfway until it is closed.
	held chan struct{}
}

// Write replaces the content of the home.
func (h *Home) Write(content string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.content = content
}

// Content returns the content of the home, and how many archives were
// unpacked into it.
func (h *Home) Content() (content string, unpacked int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.content, h.unpacked
}

// Files returns the files that messages carried into the home, by their
// paths below its attachments.
func (h *Home) Files() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return maps.Clone(h.files)
}

// Fill leaves the home without room for the files of messages.
func (h *Home) Fill() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.full = true
}

// Cut makes the runner end the next archive of the home halfway.
func (h *Home) Cut() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cut = true
}

// Refuse makes the runner fail to unpack the next archive.
func (h *Home) Refuse() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.refuse = true
}

// Hold makes the runner send the archives of the home only halfway, until
// the returned function is called.
func (h *Home) Hold() (release func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	held := make(chan struct{})
	h.held = held
	return sync.OnceFunc(func() { close(held) })
}

// Turn waits for the next turn given to any of the runners and returns it.
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
			t.Fatal("timed out waiting for a turn")
		}
	}
}

// TurnWith waits until a runner is given the turn with the ID and returns
// it.
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
			t.Fatalf("timed out waiting for turn %s", id)
		}
	}
}

// Given returns the IDs of the turns given to the runners, in order.
func (rs *Runners) Given() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	ids := []string{}
	for _, turn := range rs.given {
		ids = append(ids, turn.ID)
	}
	return ids
}

// Runner is a fake runner, an http.Handler that serves the runner's API.
type Runner struct {
	runners *Runners
	sandbox string
	home    *Home
	mu      sync.Mutex
	// turn is the last turn given to the runner.
	turn *Turn
}

// Gone simulates the loss of the runner's pod: the event streams of its turn
// are cut off.
func (rn *Runner) Gone() {
	rn.mu.Lock()
	turn := rn.turn
	rn.mu.Unlock()
	if turn != nil {
		turn.Cut()
	}
}

// Turn is a turn given to a fake runner, which the test drives.
type Turn struct {
	// Sandbox is the name of the runner's sandbox.
	Sandbox string `json:"-"`
	ID      string `json:"-"`
	// The request that started the turn.
	Text         string            `json:"text"`
	Instructions string            `json:"instructions"`
	Approval     string            `json:"approval"`
	Environment  map[string]string `json:"environment"`
	McpServers   []McpServer       `json:"mcp_servers"`

	closed chan struct{}

	mu     sync.Mutex
	events []event
	ended  bool
	// stops counts the cancellations, and answers holds the request and the
	// option of each answer. seenStops and seenAnswers count those that the
	// test has waited for.
	stops, seenStops int
	answers          [][2]string
	seenAnswers      int
	// cuts counts the times the event streams were cut off.
	cuts int
	// changed is closed when there is more to read.
	changed chan struct{}
}

// McpServer is an MCP server of a turn.
type McpServer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Transport string `json:"transport"`
	URL       string `json:"url"`
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
	mux.HandleFunc("GET /home", rn.pack)
	mux.HandleFunc("PUT /home", rn.unpack)
	mux.HandleFunc("PUT /files/{path...}", rn.file)
	mux.ServeHTTP(w, r)
}

// file writes the body as the file of the path, unless the home is full.
func (rn *Runner) file(w http.ResponseWriter, r *http.Request) {
	content, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	home := rn.home
	home.mu.Lock()
	defer home.mu.Unlock()
	if home.full {
		http.Error(w, "no space left on device", http.StatusInsufficientStorage)
		return
	}
	if home.files == nil {
		home.files = map[string]string{}
	}
	home.files[r.PathValue("path")] = string(content)
	w.WriteHeader(http.StatusNoContent)
}

// pack sends the content of the home as its archive, unless a turn runs.
func (rn *Runner) pack(w http.ResponseWriter, r *http.Request) {
	rn.mu.Lock()
	running := rn.turn != nil && !rn.turn.over()
	rn.mu.Unlock()
	if running {
		http.Error(w, "a turn is running in the home", http.StatusConflict)
		return
	}
	home := rn.home
	home.mu.Lock()
	content, cut, held := home.content, home.cut, home.held
	home.cut = false
	home.mu.Unlock()
	half := len(content) / 2
	io.WriteString(w, content[:half])
	w.(http.Flusher).Flush()
	if held != nil {
		select {
		case <-held:
		case <-r.Context().Done():
			return
		case <-rn.runners.closed:
			return
		}
	}
	if cut {
		// Abort the response, as a runner that cannot pack the home does.
		panic(http.ErrAbortHandler)
	}
	io.WriteString(w, content[half:])
}

// unpack takes the archive as the content of the home, unless the runner has
// had a turn.
func (rn *Runner) unpack(w http.ResponseWriter, r *http.Request) {
	rn.mu.Lock()
	used := rn.turn != nil
	rn.mu.Unlock()
	if used {
		http.Error(w, "a turn has run in the home", http.StatusConflict)
		return
	}
	archive, err := io.ReadAll(r.Body)
	home := rn.home
	home.mu.Lock()
	defer home.mu.Unlock()
	if err != nil || home.refuse {
		home.refuse = false
		http.Error(w, "the archive cannot be unpacked", http.StatusInternalServerError)
		return
	}
	home.content = string(archive)
	home.unpacked++
	w.WriteHeader(http.StatusNoContent)
}

// has returns the turn named in the request path, or nil if the runner does
// not have it.
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

// events streams the turn's events after Last-Event-ID until the turn ends.
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
			// Abort the response, as a lost pod would.
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

// change wakes the event streams of the turn. t.mu must be held.
func (t *Turn) change() {
	close(t.changed)
	t.changed = make(chan struct{})
}

// Tell adds an event to the turn. A finished or failed event ends the turn,
// and later events are dropped.
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

// Cut breaks the open event streams of the turn.
func (t *Turn) Cut() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cuts++
	t.change()
}

// Stopped waits for the next cancellation of the turn.
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
			tb.Fatalf("turn %s was not cancelled", t.ID)
		}
	}
}

// Answered waits for the next answer to an approval request of the turn, and
// returns the request and the chosen option.
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
			tb.Fatalf("no approval request of turn %s was answered", t.ID)
		}
	}
}
