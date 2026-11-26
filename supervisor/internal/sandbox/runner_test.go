package sandbox

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/sandbox/runnertest"
)

var ctx = context.Background()

// running starts a fake runner and returns its controls and a client of it.
func running(t *testing.T) (*runnertest.Runners, *Runner) {
	t.Helper()
	runners := runnertest.New(t)
	server := httptest.NewServer(runners.Runner("a-sandbox", &runnertest.Home{}))
	t.Cleanup(server.Close)
	return runners, &Runner{address: server.URL, http: server.Client(), quiet: time.Minute}
}

// read returns the events of the turn after the one numbered after, each as
// "seq name data".
func read(runner *Runner, turn uuid.UUID, after uint64) ([]string, error) {
	var events []string
	err := runner.Events(context.Background(), turn, after, func(event Event) error {
		events = append(events, fmt.Sprintf("%d %s %s", event.Seq, event.Name, event.Data))
		return nil
	})
	return events, err
}

func same(got []string, want ...string) bool {
	return strings.Join(got, "\n") == strings.Join(want, "\n")
}

// outcome waits for a result on done.
func outcome(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the result")
		return nil
	}
}

func TestARunnerIsGivenATurn(t *testing.T) {
	ctx := context.Background()
	s, runner := running(t)
	first := uuid.New()
	start := Turn{
		Text:         "hello",
		Instructions: "be kind",
		Approval:     "ask",
		Environment:  map[string]string{"AVIARY_LLM_MODEL": "a-model"},
	}
	// Start is idempotent, since the caller may have missed the response.
	for range 2 {
		if err := runner.Start(ctx, first, start); err != nil {
			t.Fatal(err)
		}
	}
	turn := s.Turn(t)
	if turn.Sandbox != "a-sandbox" || turn.ID != first.String() || turn.Text != "hello" || turn.Instructions != "be kind" ||
		turn.Approval != "ask" || turn.Environment["AVIARY_LLM_MODEL"] != "a-model" {
		t.Errorf("runner got %+v", turn)
	}
	// The runner runs one turn at a time.
	second := uuid.New()
	if err := runner.Start(ctx, second, Turn{Text: "and then"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Start during another turn = %v, want ErrBusy", err)
	}
	turn.Tell("finished", map[string]string{"stop_reason": "end_turn"})
	if err := runner.Start(ctx, second, Turn{Text: "and then"}); err != nil {
		t.Fatal(err)
	}
	if next := s.Turn(t); next.ID != second.String() || next.Text != "and then" {
		t.Errorf("runner got %+v", next)
	}
}

func TestTheEventsOfATurnAreReadInTheirOrderUntilItEnds(t *testing.T) {
	ctx := context.Background()
	s, runner := running(t)
	id := uuid.New()
	if err := runner.Start(ctx, id, Turn{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	turn := s.Turn(t)
	turn.Tell("started", map[string]string{"harness_session_id": "s-1"})
	heard := make(chan Event, 16)
	done := make(chan error, 1)
	go func() {
		done <- runner.Events(ctx, id, 0, func(event Event) error {
			heard <- event
			return nil
		})
	}()
	// Events arrive as they are sent.
	if event := <-heard; event.Seq != 1 || event.Name != "started" || string(event.Data) != `{"harness_session_id":"s-1"}` {
		t.Fatalf("first event = %+v", event)
	}
	turn.Tell("message.delta", map[string]string{"text": "hi"})
	if event := <-heard; event.Seq != 2 || event.Name != "message.delta" {
		t.Fatalf("second event = %+v", event)
	}
	turn.Tell("finished", map[string]string{"stop_reason": "end_turn"})
	turn.Tell("message.delta", map[string]string{"text": "too late"})
	if err := outcome(t, done); err != nil {
		t.Fatal(err)
	}
	if event := <-heard; event.Seq != 3 || event.Name != "finished" || len(heard) != 0 {
		t.Fatalf("last event = %+v, followed by %d more", event, len(heard))
	}
	// Reading can start after any event.
	events, err := read(runner, id, 1)
	if err != nil || !same(events, `2 message.delta {"text":"hi"}`, `3 finished {"stop_reason":"end_turn"}`) {
		t.Errorf("events after 1 = %q, %v", events, err)
	}
	if events, err := read(runner, id, 3); err != nil || len(events) != 0 {
		t.Errorf("events after 3 = %q, %v", events, err)
	}
	// each can stop the reading.
	enough := errors.New("enough")
	err = runner.Events(ctx, id, 0, func(Event) error { return enough })
	if !errors.Is(err, enough) {
		t.Errorf("Events = %v, want the error from each", err)
	}
}

func TestAReadingThatWasCutOffIsReadOnFromWhereItWas(t *testing.T) {
	ctx := context.Background()
	s, runner := running(t)
	id := uuid.New()
	if err := runner.Start(ctx, id, Turn{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	turn := s.Turn(t)
	turn.Tell("started", map[string]string{"harness_session_id": "s-1"})
	turn.Tell("message.delta", map[string]string{"text": "one"})
	heard := make(chan Event, 16)
	done := make(chan error, 1)
	go func() {
		done <- runner.Events(ctx, id, 0, func(event Event) error {
			heard <- event
			return nil
		})
	}()
	<-heard
	last := <-heard
	turn.Cut()
	if err := outcome(t, done); err == nil {
		t.Fatal("Events on a cut stream = nil, want an error")
	}
	turn.Tell("message.delta", map[string]string{"text": "two"})
	turn.Tell("finished", map[string]string{"stop_reason": "end_turn"})
	events, err := read(runner, id, last.Seq)
	if err != nil || !same(events, `3 message.delta {"text":"two"}`, `4 finished {"stop_reason":"end_turn"}`) {
		t.Errorf("resumed events = %q, %v", events, err)
	}
}

func TestWhatATurnIsToldReachesItsRunner(t *testing.T) {
	ctx := context.Background()
	s, runner := running(t)
	id := uuid.New()
	if err := runner.Start(ctx, id, Turn{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	turn := s.Turn(t)
	if err := runner.Answer(ctx, id, "request/1", "allow-once"); err != nil {
		t.Fatal(err)
	}
	if request, option := turn.Answered(t); request != "request/1" || option != "allow-once" {
		t.Errorf("answered request %q with option %q", request, option)
	}
	if err := runner.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	turn.Stopped(t)

	// A turn that the runner does not have, because it has moved on to
	// another or restarted.
	other := uuid.New()
	if err := runner.Cancel(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Errorf("Cancel of an unknown turn = %v, want ErrNotFound", err)
	}
	if err := runner.Answer(ctx, other, "request/1", "allow-once"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Answer for an unknown turn = %v, want ErrNotFound", err)
	}
	if _, err := read(runner, other, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("Events of an unknown turn = %v, want ErrNotFound", err)
	}
}

// saying returns a client of a server that answers event stream requests
// with what say writes.
func saying(t *testing.T, say func(w http.ResponseWriter, r *http.Request)) *Runner {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		say(w, r)
	}))
	t.Cleanup(server.Close)
	return &Runner{address: server.URL, http: server.Client(), quiet: time.Minute}
}

func TestTheLinesBetweenTheEventsOfATurnAreNoEvents(t *testing.T) {
	runner := saying(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, ": the runner is there\n\n")
		fmt.Fprint(w, "event: started\ndata: {\"harness_session_id\":\ndata: \"s-1\"}\nid: 1\n\n")
		fmt.Fprint(w, ":\n\nretry: 10\n\n")
		fmt.Fprint(w, "id:2\r\nevent:finished\r\ndata:{}\r\n\r\n")
	})
	events, err := read(runner, uuid.New(), 0)
	if err != nil || !same(events, "1 started {\"harness_session_id\":\n\"s-1\"}", "2 finished {}") {
		t.Errorf("events = %q, %v", events, err)
	}
}

func TestWhatARunnerTellsWrongIsNotTaken(t *testing.T) {
	wrong := map[string]string{
		"out-of-order event":      "id: 2\nevent: started\ndata: {}\n\n",
		"repeated event":          "id: 1\nevent: started\ndata: {}\n\nid: 1\nevent: started\ndata: {}\n\n",
		"event without ID":        "event: started\ndata: {}\n\n",
		"event with invalid JSON": "id: 1\nevent: started\ndata: {\n\n",
		"event without data":      "id: 1\nevent: started\n\n",
		"oversized event":         "id: 1\nevent: started\ndata: \"" + strings.Repeat("a", eventLimit) + "\"\n\n",
	}
	for what, said := range wrong {
		runner := saying(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, said) })
		events, err := read(runner, uuid.New(), 0)
		if err == nil || (len(events) != 0 && what != "repeated event") {
			t.Errorf("%s accepted: %q", what, events)
		}
	}
}

func TestARunnerThatSaysNothingIsGivenUp(t *testing.T) {
	// A runner that sends keepalives and then ends the turn.
	patient := saying(t, func(w http.ResponseWriter, _ *http.Request) {
		for range 10 {
			time.Sleep(20 * time.Millisecond)
			fmt.Fprint(w, ": the runner is there\n\n")
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "id: 1\nevent: finished\ndata: {}\n\n")
	})
	patient.quiet = 100 * time.Millisecond
	if events, err := read(patient, uuid.New(), 0); err != nil || !same(events, "1 finished {}") {
		t.Errorf("events = %q, %v", events, err)
	}
	// A runner that sends nothing, and one that does not respond at all.
	silent := saying(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	silent.quiet = 50 * time.Millisecond
	if _, err := read(silent, uuid.New(), 0); !errors.Is(err, errSilent) {
		t.Errorf("Events from a silent runner = %v, want errSilent", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(server.Close)
	deaf := &Runner{address: server.URL, http: server.Client(), quiet: 50 * time.Millisecond}
	if _, err := read(deaf, uuid.New(), 0); !errors.Is(err, errSilent) {
		t.Errorf("Events from an unresponsive runner = %v, want errSilent", err)
	}
	// Cancelling ctx is not reported as silence.
	ctx, stop := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, stop)
	silent.quiet = time.Minute
	err := silent.Events(ctx, uuid.New(), 0, func(Event) error { return nil })
	if !errors.Is(err, context.Canceled) || errors.Is(err, errSilent) {
		t.Errorf("Events after cancel = %v, want context.Canceled", err)
	}
}

// A runner writes a file that a message carries at the path that it is given
// below the attachments of the home, whatever the name of the file, and says
// so if the home has no room for it.
func TestARunnerWritesTheFilesOfAMessage(t *testing.T) {
	runners := runnertest.New(t)
	home := &runnertest.Home{}
	server := httptest.NewServer(runners.Runner("a-sandbox", home))
	t.Cleanup(server.Close)
	runner := &Runner{address: server.URL, http: server.Client(), quiet: time.Minute}

	want := map[string]string{"3/notes.txt": "to do", "3/보고서 2026 (2).pdf": "%PDF", "3/50% off?.txt": "sale", "4/notes.txt": "done"}
	for path, content := range want {
		if err := runner.PutFile(ctx, path, strings.NewReader(content), int64(len(content))); err != nil {
			t.Fatalf("PutFile(%q): %v", path, err)
		}
	}
	if got := home.Files(); !maps.Equal(got, want) {
		t.Errorf("files of the home = %q, want %q", got, want)
	}
	// Sent again, a file replaces the one that is there.
	if err := runner.PutFile(ctx, "3/notes.txt", strings.NewReader("done"), 4); err != nil || home.Files()["3/notes.txt"] != "done" {
		t.Errorf("PutFile again: %v, the file is %q; want it replaced", err, home.Files()["3/notes.txt"])
	}
	home.Fill()
	if err := runner.PutFile(ctx, "5/large.bin", strings.NewReader("a"), 1); !errors.Is(err, ErrNoRoom) {
		t.Errorf("PutFile into a full home: %v, want ErrNoRoom", err)
	}
	server.Close()
	if err := runner.PutFile(ctx, "5/notes.txt", strings.NewReader("a"), 1); err == nil || errors.Is(err, ErrNoRoom) {
		t.Errorf("PutFile to a runner that is gone: %v, want another error", err)
	}
}
