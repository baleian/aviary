package app

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"aviary/supervisor/internal/driver"
)

// A request to cancel a turn reaches any Supervisor, and the runner of the
// turn is told by the one that follows the turn.
func TestCancel(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	following := aviary.supervisor()
	session := following.session()
	accepted := following.send(session, "hello")
	runner := aviary.given(accepted.ID)
	// The Supervisor that is asked starts after the turn was dispatched, so
	// the other one follows the turn.
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
		t.Errorf("cancelling a turn that runs was answered with %d and the turn %+v", status, cancelling)
	}
	runner.cancelled()
	if status, again := cancel(following); status != http.StatusAccepted || !again.CancelRequestedAt.Equal(*cancelling.CancelRequestedAt) {
		t.Errorf("cancelling the turn again was answered with %d and the turn %+v", status, again)
	}
	// The harness ends the turn, which its runner tells.
	runner.report(finished("cancelled", nil))

	expected := []string{"turn.started", "message.delta", "turn.cancelled"}
	if events := watching(); !slices.Equal(types(events), expected) {
		t.Errorf("the events of the cancelled turn are %v, not %v", types(events), expected)
	}
	// The record keeps what the agent said until it was stopped.
	record := asked.turn(accepted.ID)
	if said := items(t, record); record.State != "cancelled" || record.StopReason != "cancelled" || record.Failure != nil ||
		len(said) != 1 || said[0]["text"] != "one " {
		t.Errorf("the record of the cancelled turn is %+v, and has the items %v", record, said)
	}
	if status, ended := cancel(asked); status != http.StatusOK || ended.State != "cancelled" {
		t.Errorf("cancelling the cancelled turn was answered with %d and the turn %+v", status, ended)
	}
	if next := asked.send(session, "again"); next.Seq != 2 {
		t.Errorf("the next turn of the session has the number %d", next.Seq)
	}
}

// A turn that is cancelled between its dispatch and the moment at which a
// runner has it is not given to a runner.
func TestCancelBeforeTheRunnerHasTheTurn(t *testing.T) {
	t.Parallel()
	// The turn waits for a template that no release has had built.
	aviary := install(t, func(config *driver.Config) { config.Templates.Store = "s3://elsewhere/templates" })
	s := aviary.supervisor()
	accepted := s.send(s.session(), "hello")
	eventually(t, soon, "the turn is not followed", func() bool { return s.turn(accepted.ID).State == "running" })
	var cancelling turn
	if status, _ := s.request("POST", "/v1/turns/"+accepted.ID+"/cancel", "", &cancelling); status != http.StatusAccepted {
		t.Errorf("cancelling a turn that no runner has yet was answered with %d", status)
	}
	if events := s.follow(accepted.ID); !slices.Equal(types(events), []string{"turn.cancelled"}) {
		t.Errorf("the events of the turn that was cancelled before it began are %v", events)
	}
	if record := s.turn(accepted.ID); record.State != "cancelled" || record.Failure != nil || record.StartedAt != nil {
		t.Errorf("the record of the turn that was cancelled before it began is %+v", record)
	}
	if given := aviary.service.Given(); len(given) != 0 {
		t.Errorf("the runners were given %v", given)
	}
}

// A Supervisor that is told to stop does not wait for its turns. It follows
// them no longer, and another Supervisor takes them up.
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
	// The other Supervisor starts after the turn was dispatched, so the one
	// that is told to stop follows the turn.
	other := aviary.supervisor()

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		stopping.stop()
	}()
	select {
	case <-stopped:
	case <-time.After(soon):
		t.Fatal("the Supervisor did not stop while a turn ran")
	}
	// A client that read along is let go, and reads on from another.
	if more := client.rest(soon); !slices.Equal(types(read), []string{"turn.started", "message.delta"}) || len(more) != 0 {
		t.Errorf("the client of the Supervisor that stopped read the events %v and then %v", read, more)
	}
	if state := other.turn(accepted.ID).State; state != "running" {
		t.Errorf("the turn of the Supervisor that stopped is %s", state)
	}

	// The other Supervisor goes on from what is recorded of the turn.
	runner.report(said("two"))
	runner.report(finished("end_turn", nil))
	rest := other.read(accepted.ID, read[len(read)-1].ID).rest(patience)
	if !slices.Equal(types(rest), []string{"message.delta", "turn.completed"}) {
		t.Errorf("the client read on from the other Supervisor the events %v", rest)
	}
	if said := items(t, other.turn(accepted.ID)); len(said) != 1 || said[0]["text"] != "one two" {
		t.Errorf("the record of the turn has the items %v", said)
	}
	if given := aviary.service.Given(); len(given) != 1 {
		t.Errorf("the runners were given %v, not the turn once", given)
	}
}
