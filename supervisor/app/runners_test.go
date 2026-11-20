package app

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"aviary/supervisor/internal/driver"
)

// A cancel request can go to any Supervisor. The one that follows the turn
// tells the runner.
func TestCancel(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	following := aviary.supervisor()
	session := following.session()
	accepted := following.send(session, "hello")
	runner := aviary.given(accepted.ID)
	// Started after the dispatch, so that the other Supervisor follows the
	// turn.
	asked := aviary.supervisor()
	runner.report(started("harness-session"))
	runner.report(said("one "))
	watching := asked.watch(accepted.ID)

	cancel := func(s *supervisor) (int, turn) {
		t.Helper()
		var answer turn
		status, _ := s.request("POST", "/v1/turns/"+accepted.ID+"/cancel", "", &answer)
		return status, answer
	}
	status, cancelling := cancel(asked)
	if status != http.StatusAccepted || cancelling.State != "running" || cancelling.CancelRequestedAt == nil {
		t.Errorf("cancel running turn = %d %+v, want 202 running with cancel_requested_at", status, cancelling)
	}
	runner.cancelled()
	if status, again := cancel(following); status != http.StatusAccepted || !again.CancelRequestedAt.Equal(*cancelling.CancelRequestedAt) {
		t.Errorf("cancel again = %d %+v, want 202 with the same cancel_requested_at", status, again)
	}
	runner.report(finished("cancelled", nil))

	expected := []string{"turn.started", "message.delta", "turn.cancelled"}
	if events := watching(); !slices.Equal(types(events), expected) {
		t.Errorf("event types = %v, want %v", types(events), expected)
	}
	// The record keeps what the agent said before it was stopped.
	record := asked.turn(accepted.ID)
	if said := items(t, record); record.State != "cancelled" || record.StopReason != "cancelled" || record.Failure != nil ||
		len(said) != 1 || said[0]["text"] != "one " {
		t.Errorf("cancelled turn = %+v, items %v", record, said)
	}
	if status, ended := cancel(asked); status != http.StatusOK || ended.State != "cancelled" {
		t.Errorf("cancel ended turn = %d %+v, want 200 cancelled", status, ended)
	}
	if next := asked.send(session, "again"); next.Seq != 2 {
		t.Errorf("next turn seq = %d, want 2", next.Seq)
	}
}

// A turn that is cancelled after its dispatch, but before a runner has it, is
// never given to a runner.
func TestCancelBeforeTheRunnerHasTheTurn(t *testing.T) {
	t.Parallel()
	// The sandbox stays starting, so that the turn waits for it.
	aviary := install(t)
	aviary.service.Slow(true)
	s := aviary.supervisor()
	accepted := s.send(s.session(), "hello")
	eventually(t, soon, "turn is not running", func() bool { return s.turn(accepted.ID).State == "running" })
	var cancelling turn
	if status, _ := s.request("POST", "/v1/turns/"+accepted.ID+"/cancel", "", &cancelling); status != http.StatusAccepted {
		t.Errorf("cancel before the runner has the turn: status %d, want 202", status)
	}
	if events := s.follow(accepted.ID); !slices.Equal(types(events), []string{"turn.cancelled"}) {
		t.Errorf("events = %v, want only turn.cancelled", events)
	}
	if record := s.turn(accepted.ID); record.State != "cancelled" || record.Failure != nil || record.StartedAt != nil {
		t.Errorf("turn = %+v, want cancelled and never started", record)
	}
	if given := aviary.service.Given(); len(given) != 0 {
		t.Errorf("runners were given %v, want none", given)
	}
}

// A stopping Supervisor does not wait for its turns: it leaves them, and
// another Supervisor takes them over.
func TestStop(t *testing.T) {
	t.Parallel()
	// The other Supervisor finds the turn when it sweeps.
	aviary := install(t, func(config *driver.Config) { config.Sweep = 100 * time.Millisecond })
	stopping := aviary.supervisor()
	accepted := stopping.send(stopping.session(), "hello")
	runner := aviary.given(accepted.ID)
	runner.report(started("harness-session"))
	runner.report(said("one "))
	client := stopping.read(accepted.ID, "")
	read := []event{client.next(), client.next()}
	// Started after the dispatch, so that the stopping Supervisor follows
	// the turn.
	other := aviary.supervisor()

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		stopping.stop()
	}()
	select {
	case <-stopped:
	case <-time.After(soon):
		t.Fatal("Supervisor did not stop while a turn was running")
	}
	// The stopping Supervisor ends the event stream, and the client resumes
	// it from another.
	if more := client.rest(soon); !slices.Equal(types(read), []string{"turn.started", "message.delta"}) || len(more) != 0 {
		t.Errorf("stream of the stopped Supervisor: read %v, then %v", read, more)
	}
	if state := other.turn(accepted.ID).State; state != "running" {
		t.Errorf("turn state after stop = %s, want running", state)
	}

	// The other Supervisor continues from the recorded events.
	runner.report(said("two"))
	runner.report(finished("end_turn", nil))
	rest := other.read(accepted.ID, read[len(read)-1].ID).rest(patience)
	if !slices.Equal(types(rest), []string{"message.delta", "turn.completed"}) {
		t.Errorf("events resumed from the other Supervisor = %v", rest)
	}
	if said := items(t, other.turn(accepted.ID)); len(said) != 1 || said[0]["text"] != "one two" {
		t.Errorf("turn items = %v", said)
	}
	if given := aviary.service.Given(); len(given) != 1 {
		t.Errorf("runners were given %v, want the turn once", given)
	}
}
