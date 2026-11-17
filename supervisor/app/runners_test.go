package app

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	runnerv1 "aviary/supervisor/internal/gen/aviary/runner/v1"
)

// final tells whether a runner takes the error that ended its call as the
// end of its turn. After any other error it calls again.
func final(err error) bool {
	switch status.Code(err) {
	case codes.Unauthenticated, codes.FailedPrecondition, codes.InvalidArgument, codes.DataLoss, codes.ResourceExhausted:
		return true
	}
	return false
}

// A runner that lost its Supervisor calls again, any Supervisor, and sends
// again what it has no answer for. The turn is recorded whole and once.
func TestRunnerComesBack(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	first, second := aviary.supervisor(), aviary.supervisor()
	accepted := first.send(first.session(), "hello")
	job := aviary.job(accepted.ID)
	runner := first.take(job)
	reports := []*runnerv1.RunTurnRequest{started("harness-session"), said("one "), said("two ")}
	for _, report := range reports {
		runner.report(report)
	}
	began := first.turn(accepted.ID).StartedAt
	if began == nil {
		t.Fatal("the turn has not started")
	}

	runner.call.hang()
	start, err := runner.attach(second)
	if err != nil || start.GetText() != "hello" || start.GetReceived() != 3 {
		t.Fatalf("the runner that came back was started with %v: %v", start, err)
	}
	for i, report := range reports {
		if received, err := runner.send(uint64(i+1), report); err != nil || received != uint64(i+1) {
			t.Errorf("message %d, sent again, was answered with %d: %v", i+1, received, err)
		}
	}
	if again := second.turn(accepted.ID).StartedAt; !again.Equal(*began) {
		t.Errorf("the turn started at %v, and again at %v", began, again)
	}
	runner.report(said("three"))
	// A message that this Supervisor does not know, from a newer runner.
	runner.report(&runnerv1.RunTurnRequest{})

	// A message that follows one that is not recorded ends the call for good:
	// the runner has let go of the messages before it, so they are lost.
	if _, err := runner.send(7, said("seven")); status.Code(err) != codes.DataLoss {
		t.Fatalf("a message after one that is not recorded was answered with: %v", err)
	}
	if start, err := runner.attach(first); err != nil || start.GetReceived() != 5 {
		t.Fatalf("the runner that came back again was started with %v: %v", start, err)
	}
	runner.report(finished("end_turn", nil))
	runner.leave()
	aviary.end(job, batchv1.JobComplete, nil)

	expected := []string{"turn.started", "message.delta", "message.delta", "message.delta", "turn.completed"}
	if events := first.follow(accepted.ID); !slices.Equal(types(events), expected) {
		t.Errorf("the events of the turn are %v, not %v", types(events), expected)
	}
	record := second.turn(accepted.ID)
	if said := items(t, record); record.State != "completed" || len(said) != 1 || said[0]["text"] != "one two three" {
		t.Errorf("the record of the turn is %+v, and has the items %v", record, said)
	}
}

// The events of a turn are in the event log while the turn runs. A turn
// whose events the log has lost cannot be recorded whole, and its runner is
// told.
func TestRunnerIsToldOfLostReports(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	accepted := s.send(s.session(), "hello")
	runner := s.take(aviary.job(accepted.ID))
	runner.report(started("harness-session"))
	runner.report(said("one "))

	if err := aviary.log.Expire(context.Background(), uuid.MustParse(accepted.ID), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.send(3, said("two")); status.Code(err) != codes.DataLoss {
		t.Errorf("a report after reports that the event log lost was answered with: %v", err)
	}
}

// A second pod of a turn, which Kubernetes may start, must not run the turn
// again: the agent of the first may have acted.
func TestTurnIsGivenToOneRunner(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	accepted := s.send(s.session(), "hello")
	job := aviary.job(accepted.ID)
	refusal := func(r *runner) codes.Code {
		t.Helper()
		start, err := r.attach(s)
		if err == nil {
			t.Errorf("the runner was started with %v", start)
		}
		return status.Code(err)
	}

	if code := refusal(aviary.runner("another-token")); code != codes.Unauthenticated {
		t.Errorf("a runner with a token of no turn was refused with %s", code)
	}
	if code := refusal(aviary.runner("")); code != codes.Unauthenticated {
		t.Errorf("a runner without a token was refused with %s", code)
	}
	if code := refusal(&runner{t: t, token: token(job), id: "runner-1"}); code != codes.InvalidArgument {
		t.Errorf("a runner whose name is not a UUID was refused with %s", code)
	}

	owner := s.take(job)
	if code := refusal(aviary.runner(token(job))); code != codes.FailedPrecondition {
		t.Errorf("a second runner of the turn was refused with %s", code)
	}
	// A runner of an older release has no name. It cannot be told apart
	// from another, so it is not given a turn again.
	if code := refusal(&runner{t: t, token: token(job)}); code != codes.FailedPrecondition {
		t.Errorf("a runner without a name was refused a turn that was taken with %s", code)
	}

	// The turn is still that of the runner that took it.
	owner.report(started("harness-session"))
	owner.report(finished("end_turn", nil))
	owner.leave()
	aviary.end(job, batchv1.JobComplete, nil)
	if events := s.follow(accepted.ID); events[len(events)-1].Type != "turn.completed" {
		t.Errorf("the turn ended with %v", events[len(events)-1])
	}
	if code := refusal(owner); code != codes.FailedPrecondition {
		t.Errorf("the runner of a turn that has ended was refused with %s", code)
	}
}

// A runner of an older release does not number its messages and expects no
// answers to them. During an upgrade its turns go on.
func TestRunnerOfAnOlderRelease(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	accepted := s.send(s.session(), "hello")
	job := aviary.job(accepted.ID)
	old := &runner{t: t, token: token(job)}
	if _, err := old.attach(s); err != nil {
		t.Fatal(err)
	}
	for _, report := range []*runnerv1.RunTurnRequest{started("harness-session"), said("hello"), finished("end_turn", nil)} {
		if err := old.call.stream.Send(report); err != nil {
			t.Fatal(err)
		}
	}
	old.leave()
	select {
	case seq := <-old.call.received:
		t.Errorf("the runner was answered for message %d: it would take the answer for the end of its turn", seq)
	default:
	}
	aviary.end(job, batchv1.JobComplete, nil)

	expected := []string{"turn.started", "message.delta", "turn.completed"}
	if events := s.follow(accepted.ID); !slices.Equal(types(events), expected) {
		t.Errorf("the events of the turn are %v, not %v", types(events), expected)
	}
	if record := s.turn(accepted.ID); record.State != "completed" || record.StopReason != "end_turn" || len(record.Items) != 1 {
		t.Errorf("the record of the turn is %+v", record)
	}
}

// A request to cancel a turn reaches any Supervisor, and the runner of the
// turn is told by the one that it is connected to.
func TestCancel(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	connected, asked := aviary.supervisor(), aviary.supervisor()
	session := asked.session()
	accepted := asked.send(session, "hello")
	job := aviary.job(accepted.ID)
	runner := connected.take(job)
	runner.report(started("harness-session"))
	runner.report(said("one "))
	watching := asked.watch(accepted.ID)

	// The runner calls its Supervisor again while its first call is still
	// open, and the first call ends after that. The order to cancel has to
	// reach the call that is left.
	first := runner.call
	if _, err := runner.attach(connected); err != nil {
		t.Fatal(err)
	}
	first.hang()
	eventually(t, soon, "the Supervisor has not noticed that the first call of the runner ended", func() bool {
		return connected.metric("aviary_turns_running") == 1
	})

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
	if status, again := cancel(connected); status != http.StatusAccepted || !again.CancelRequestedAt.Equal(*cancelling.CancelRequestedAt) {
		t.Errorf("cancelling the turn again was answered with %d and the turn %+v", status, again)
	}

	// A runner that loses its Supervisor after that is told again by the
	// one that it calls.
	runner.call.hang()
	if _, err := runner.attach(asked); err != nil {
		t.Fatal(err)
	}
	runner.cancelled()
	runner.report(finished("cancelled", nil))
	runner.leave()
	aviary.end(job, batchv1.JobComplete, nil)

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

// A turn that is cancelled between its dispatch and the start of its runner
// is not given to the runner.
func TestCancelBeforeTheRunnerComes(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	accepted := s.send(s.session(), "hello")
	job := aviary.job(accepted.ID)
	var cancelling turn
	if status, _ := s.request("POST", "/v1/turns/"+accepted.ID+"/cancel", "", &cancelling); status != http.StatusAccepted {
		t.Errorf("cancelling a turn whose pod starts was answered with %d", status)
	}
	if start, err := aviary.runner(token(job)).attach(s); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("the runner of a cancelled turn was started with %v: %v", start, err)
	}
	// The runner gives up, and its pod fails.
	aviary.end(job, batchv1.JobFailed, stopped(corev1.ContainerStateTerminated{
		ExitCode: 1, Message: `{"reason": "supervisor_lost", "message": "the turn was cancelled"}`,
	}))
	if events := s.follow(accepted.ID); !slices.Equal(types(events), []string{"turn.cancelled"}) {
		t.Errorf("the events of the turn that was cancelled before it began are %v", events)
	}
	if record := s.turn(accepted.ID); record.State != "cancelled" || record.Failure != nil || record.StartedAt != nil {
		t.Errorf("the record of the turn that was cancelled before it began is %+v", record)
	}
}

// A Supervisor that is told to stop does not wait for its turns. It ends the
// calls of their runners, which go on with another Supervisor.
func TestStop(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	stopping, other := aviary.supervisor(), aviary.supervisor()
	accepted := stopping.send(stopping.session(), "hello")
	job := aviary.job(accepted.ID)
	runner := stopping.take(job)
	runner.report(started("harness-session"))
	runner.report(said("one "))
	client := stopping.read(accepted.ID, "")
	read := []event{client.next(), client.next()}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		stopping.stop()
	}()
	select {
	case err := <-runner.call.ended:
		if final(err) {
			t.Errorf("the Supervisor that stopped ended the call of the runner for good: %v", err)
		}
	case <-time.After(soon):
		t.Fatal("the Supervisor that stopped did not end the call of the runner")
	}
	select {
	case <-stopped:
	case <-time.After(soon):
		t.Fatal("the Supervisor did not stop while a turn ran")
	}
	// A client that read along is let go, and reads on from another.
	if more := client.rest(soon); !slices.Equal(types(read), []string{"turn.started", "message.delta"}) || len(more) != 0 {
		t.Errorf("the client of the Supervisor that stopped read the events %v and then %v", read, more)
	}

	if start, err := runner.attach(other); err != nil || start.GetReceived() != 2 {
		t.Fatalf("the runner was started by the other Supervisor with %v: %v", start, err)
	}
	runner.report(said("two"))
	runner.report(finished("end_turn", nil))
	runner.leave()
	aviary.end(job, batchv1.JobComplete, nil)
	rest := other.read(accepted.ID, read[len(read)-1].ID).rest(patience)
	if !slices.Equal(types(rest), []string{"message.delta", "turn.completed"}) {
		t.Errorf("the client read on from the other Supervisor the events %v", rest)
	}
	if said := items(t, other.turn(accepted.ID)); len(said) != 1 || said[0]["text"] != "one two" {
		t.Errorf("the record of the turn has the items %v", said)
	}
}
