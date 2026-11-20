package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/sandbox/runnertest"
)

// running starts a runner, and returns it with the runners of the test.
func running(t *testing.T) (*runnertest.Runners, *Runner) {
	t.Helper()
	runners := runnertest.New(t)
	server := httptest.NewServer(runners.Runner("a-sandbox"))
	t.Cleanup(server.Close)
	return runners, &Runner{address: server.URL, http: server.Client(), quiet: time.Minute}
}

// read reads the events of the turn that follow the one with the number,
// and returns them as "number name data".
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

// outcome waits for what is done to end, and returns how it ended.
func outcome(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("it did not end")
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
	// Giving it again is no error: the one who gave it may not have heard
	// the answer.
	for range 2 {
		if err := runner.Start(ctx, first, start); err != nil {
			t.Fatal(err)
		}
	}
	turn := s.Turn(t)
	if turn.Sandbox != "a-sandbox" || turn.ID != first.String() || turn.Text != "hello" || turn.Instructions != "be kind" ||
		turn.Approval != "ask" || turn.Environment["AVIARY_LLM_MODEL"] != "a-model" {
		t.Errorf("the runner was given %+v", turn)
	}
	// It runs one turn at a time.
	second := uuid.New()
	if err := runner.Start(ctx, second, Turn{Text: "and then"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("a turn while another runs: %v", err)
	}
	turn.Tell("finished", map[string]string{"stop_reason": "end_turn"})
	if err := runner.Start(ctx, second, Turn{Text: "and then"}); err != nil {
		t.Fatal(err)
	}
	if next := s.Turn(t); next.ID != second.String() || next.Text != "and then" {
		t.Errorf("the runner was given %+v", next)
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
	// What is told while it is read comes as it is told.
	if event := <-heard; event.Seq != 1 || event.Name != "started" || string(event.Data) != `{"harness_session_id":"s-1"}` {
		t.Fatalf("the first event is %+v", event)
	}
	turn.Tell("message.delta", map[string]string{"text": "hi"})
	if event := <-heard; event.Seq != 2 || event.Name != "message.delta" {
		t.Fatalf("the second event is %+v", event)
	}
	turn.Tell("finished", map[string]string{"stop_reason": "end_turn"})
	turn.Tell("message.delta", map[string]string{"text": "too late"})
	if err := outcome(t, done); err != nil {
		t.Fatal(err)
	}
	if event := <-heard; event.Seq != 3 || event.Name != "finished" || len(heard) != 0 {
		t.Fatalf("the last event is %+v, with %d after it", event, len(heard))
	}
	// One who has some of them reads the ones after.
	events, err := read(runner, id, 1)
	if err != nil || !same(events, `2 message.delta {"text":"hi"}`, `3 finished {"stop_reason":"end_turn"}`) {
		t.Errorf("after the first event come %q: %v", events, err)
	}
	if events, err := read(runner, id, 3); err != nil || len(events) != 0 {
		t.Errorf("after the last event come %q: %v", events, err)
	}
	// The one who reads may stop.
	enough := errors.New("enough")
	err = runner.Events(ctx, id, 0, func(Event) error { return enough })
	if !errors.Is(err, enough) {
		t.Errorf("the reading that was stopped ended with %v", err)
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
		t.Fatal("the reading that was cut off ended as one that was not")
	}
	turn.Tell("message.delta", map[string]string{"text": "two"})
	turn.Tell("finished", map[string]string{"stop_reason": "end_turn"})
	events, err := read(runner, id, last.Seq)
	if err != nil || !same(events, `3 message.delta {"text":"two"}`, `4 finished {"stop_reason":"end_turn"}`) {
		t.Errorf("the reading went on with %q: %v", events, err)
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
		t.Errorf("the request %q was answered with %q", request, option)
	}
	if err := runner.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	turn.Stopped(t)

	// A turn that the runner does not have: it has another by now, or it
	// was started again.
	other := uuid.New()
	if err := runner.Cancel(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Errorf("stopping a turn that the runner does not have: %v", err)
	}
	if err := runner.Answer(ctx, other, "request/1", "allow-once"); !errors.Is(err, ErrNotFound) {
		t.Errorf("answering a turn that the runner does not have: %v", err)
	}
	if _, err := read(runner, other, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading a turn that the runner does not have: %v", err)
	}
}

// saying is a runner that answers one who reads the events of a turn with
// what say writes.
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
		t.Errorf("the events are %q: %v", events, err)
	}
}

func TestWhatARunnerTellsWrongIsNotTaken(t *testing.T) {
	wrong := map[string]string{
		"an event that is not the next":  "id: 2\nevent: started\ndata: {}\n\n",
		"an event that was told already": "id: 1\nevent: started\ndata: {}\n\nid: 1\nevent: started\ndata: {}\n\n",
		"an event without a number":      "event: started\ndata: {}\n\n",
		"an event that tells no JSON":    "id: 1\nevent: started\ndata: {\n\n",
		"an event that tells nothing":    "id: 1\nevent: started\n\n",
		"an event that is too long":      "id: 1\nevent: started\ndata: \"" + strings.Repeat("a", eventLimit) + "\"\n\n",
	}
	for what, said := range wrong {
		runner := saying(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, said) })
		events, err := read(runner, uuid.New(), 0)
		if err == nil || (len(events) != 0 && what != "an event that was told already") {
			t.Errorf("%s was taken: %q", what, events)
		}
	}
}

func TestARunnerThatSaysNothingIsGivenUp(t *testing.T) {
	// One that says that it is there, and then tells the end of the turn.
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
		t.Errorf("the events of a runner that is there are %q: %v", events, err)
	}
	// One that says nothing, and one that does not answer at all.
	silent := saying(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	silent.quiet = 50 * time.Millisecond
	if _, err := read(silent, uuid.New(), 0); !errors.Is(err, errSilent) {
		t.Errorf("the reading of a runner that says nothing ended with %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(server.Close)
	deaf := &Runner{address: server.URL, http: server.Client(), quiet: 50 * time.Millisecond}
	if _, err := read(deaf, uuid.New(), 0); !errors.Is(err, errSilent) {
		t.Errorf("the reading of a runner that does not answer ended with %v", err)
	}
	// The one who reads stops, which is not the runner saying nothing.
	ctx, stop := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, stop)
	silent.quiet = time.Minute
	err := silent.Events(ctx, uuid.New(), 0, func(Event) error { return nil })
	if !errors.Is(err, context.Canceled) || errors.Is(err, errSilent) {
		t.Errorf("the reading that was stopped ended with %v", err)
	}
}
