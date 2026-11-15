package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	batchv1 "k8s.io/api/batch/v1"

	"aviary/supervisor/internal/driver"
	runnerv1 "aviary/supervisor/internal/gen/aviary/runner/v1"
)

// items returns the items of a turn's record, each as the fields of its
// object.
func items(t *testing.T, record turn) []map[string]any {
	t.Helper()
	items := []map[string]any{}
	for _, encoded := range record.Items {
		var item map[string]any
		if err := json.Unmarshal(encoded, &item); err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	return items
}

// A message runs as a turn: the Supervisor asks the cluster for a pod, gives
// the turn to the runner in it, passes on and keeps what the runner reports,
// and ends the turn when the pod has stopped.
func TestTurn(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session := s.session(`{"harness": "codex", "instructions": "Be brief."}`)

	accepted := s.send(session, "hello")
	watching := s.watch(accepted.ID)
	if accepted.State != "queued" || accepted.Seq != 1 || accepted.Text != "hello" || accepted.SessionID != session {
		t.Errorf("the message was accepted as the turn %+v", accepted)
	}
	job := aviary.job(accepted.ID)
	if image := job.Spec.Template.Spec.Containers[0].Image; image != "codex" {
		t.Errorf("the pod of the turn runs the image %s, not that of the agent's harness", image)
	}
	// The token is good for the model endpoint and the forward proxy while
	// its turn runs, which is from when the runner has taken the turn.
	if origin := s.origin(token(job), false); origin != nil {
		t.Errorf("the token of a turn that no runner has taken is that of %v", origin)
	}

	runner := aviary.runner(token(job))
	start, err := runner.attach(s)
	if err != nil {
		t.Fatal(err)
	}
	if start.GetText() != "hello" || start.GetInstructions() != "Be brief." || start.HarnessSessionId != nil ||
		start.Received == nil || start.GetReceived() != 0 {
		t.Errorf("the runner was started with %v", start)
	}
	if state := s.turn(accepted.ID).State; state != "running" {
		t.Errorf("a turn that its runner has taken is %s", state)
	}
	for _, forward := range []bool{false, true} {
		origin := s.origin(token(job), forward)
		if origin["turn"] != accepted.ID || origin["session"] != session || origin["agent"] == "" || origin["project"] == "" {
			t.Errorf("the token of the running turn is that of %v", origin)
		}
	}
	if origin := s.origin("another-token", false); origin != nil {
		t.Errorf("a token of no turn is that of %v", origin)
	}

	runner.report(started("harness-session-1"))
	runner.report(said("Hello "))
	runner.report(said("there"))
	output, err := structpb.NewValue("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	runner.report(&runnerv1.RunTurnRequest{Message: &runnerv1.RunTurnRequest_ToolCall{
		ToolCall: &runnerv1.ToolCall{Id: "t1", Title: "ls", Kind: "execute", Status: "pending"},
	}})
	status := "completed"
	runner.report(&runnerv1.RunTurnRequest{Message: &runnerv1.RunTurnRequest_ToolUpdate{
		ToolUpdate: &runnerv1.ToolUpdate{Id: "t1", Status: &status, Output: output},
	}})
	runner.report(finished("end_turn", &runnerv1.Usage{TotalTokens: 18, InputTokens: 11, OutputTokens: 7}))
	runner.leave()

	// The turn has not ended before its pod has: until then the session
	// takes no message.
	if state := s.turn(accepted.ID).State; state != "running" {
		t.Errorf("a turn whose pod has not stopped is %s", state)
	}
	var refusal struct{ Code string }
	if status, _ := s.request("POST", "/v1/sessions/"+session+"/messages", `{"text": "again"}`, &refusal); status != http.StatusConflict || refusal.Code != "session_busy" {
		t.Errorf("a message to a session whose turn has not ended was answered with %d %s", status, refusal.Code)
	}
	aviary.end(job, batchv1.JobComplete, nil)

	events := watching()
	expected := []string{"turn.started", "message.delta", "message.delta", "tool.call", "tool.update", "turn.completed"}
	if !slices.Equal(types(events), expected) {
		t.Fatalf("the events of the turn are %v, not %v", types(events), expected)
	}
	var last struct {
		StopReason string           `json:"stop_reason"`
		Usage      map[string]int64 `json:"usage"`
	}
	if err := json.Unmarshal([]byte(events[len(events)-1].Data), &last); err != nil {
		t.Fatal(err)
	}
	usage := map[string]int64{"total_tokens": 18, "input_tokens": 11, "output_tokens": 7}
	if last.StopReason != "end_turn" || !maps.Equal(last.Usage, usage) {
		t.Errorf("the last event of the turn is %s", events[len(events)-1].Data)
	}

	record := s.turn(accepted.ID)
	if record.State != "completed" || record.StartedAt == nil || record.EndedAt == nil || record.StopReason != "end_turn" ||
		!maps.Equal(record.Usage, usage) || record.Failure != nil {
		t.Errorf("the record of the turn is %+v", record)
	}
	said := items(t, record)
	if len(said) != 2 || said[0]["type"] != "message" || said[0]["text"] != "Hello there" ||
		said[1]["type"] != "tool_call" || said[1]["title"] != "ls" || said[1]["status"] != "completed" || said[1]["output"] != "a.txt" {
		t.Errorf("the record of the turn has the items %v", said)
	}
	eventually(t, soon, "the Job of the turn that ended is still there", func() bool { return len(aviary.jobs()) == 0 })
	if origin := s.origin(token(job), true); origin != nil {
		t.Errorf("the token of a turn that has ended is that of %v", origin)
	}

	// The next turn of the session continues the harness session.
	next := s.send(session, "and again")
	if next.Seq != 2 {
		t.Errorf("the second turn of the session has the number %d", next.Seq)
	}
	again, err := aviary.runner(token(aviary.job(next.ID))).attach(s)
	if err != nil || again.GetText() != "and again" || again.GetHarnessSessionId() != "harness-session-1" {
		t.Errorf("the runner of the next turn was started with %v: %v", again, err)
	}
}

// positions returns the places in the queue that the events told a turn.
func positions(t *testing.T, events []event) []int {
	t.Helper()
	positions := []int{}
	for _, event := range events {
		if event.Type != "turn.queued" {
			continue
		}
		var data struct{ Position int }
		if err := json.Unmarshal([]byte(event.Data), &data); err != nil {
			t.Fatal(err)
		}
		positions = append(positions, data.Position)
	}
	return positions
}

// Turns beyond the limit wait in the order in which they were sent, and are
// told their places as the queue moves.
func TestQueue(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) { config.MaxConcurrentTurns = 1 })
	s := aviary.supervisor()
	// told returns the places that the turn was told, each once.
	told := func(events []event) []int { return slices.Compact(positions(t, events)) }
	ending := func(events []event) string { return events[len(events)-1].Type }

	lost := s.session()
	first := s.send(lost, "one")
	running := s.take(aviary.job(first.ID))
	running.report(started("harness-session"))
	second, third, fourth := s.send(s.session(), "two"), s.send(s.session(), "three"), s.send(s.session(), "four")
	for _, waiting := range []turn{second, third, fourth} {
		if state := s.turn(waiting.ID).State; state != "queued" {
			t.Errorf("a turn beyond the limit is %s", state)
		}
	}

	// A turn that waits is cancelled at once, and never runs.
	var cancelled turn
	if status, _ := s.request("POST", "/v1/turns/"+second.ID+"/cancel", "", &cancelled); status != http.StatusOK || cancelled.State != "cancelled" {
		t.Errorf("cancelling a turn that waits was answered with %d and the turn %+v", status, cancelled)
	}
	if events := s.follow(second.ID); ending(events) != "turn.cancelled" || len(types(events)) != 1 || !slices.Equal(told(events), []int{1}) {
		t.Errorf("the events of the turn that was cancelled while it waited are %v", events)
	}
	if record := s.turn(second.ID); record.StartedAt != nil || record.EndedAt == nil || len(record.Items) != 0 || record.Failure != nil {
		t.Errorf("the record of the turn that never ran is %+v", record)
	}

	// The turn that runs loses its pod. It fails, and the next one runs.
	aviary.end(aviary.job(first.ID), batchv1.JobFailed, nil)
	if events := s.follow(first.ID); ending(events) != "turn.failed" {
		t.Errorf("the turn that lost its pod ended with %s", ending(events))
	}
	if record := s.turn(first.ID); record.State != "failed" || record.Failure["reason"] != "pod_lost" {
		t.Errorf("the record of the turn that lost its pod is %+v", record)
	}
	s.run(aviary.job(third.ID), "done")
	s.run(aviary.job(fourth.ID), "done")

	// The third turn was behind one turn and the fourth behind two, until
	// the turn before them left the queue and then the one that ran ended.
	for _, waited := range []struct {
		turn   turn
		places []int
	}{{third, []int{2, 1}}, {fourth, []int{3, 2, 1}}} {
		events := s.follow(waited.turn.ID)
		if ending(events) != "turn.completed" || !slices.Equal(told(events), waited.places) {
			t.Errorf("the turn %q ended with %s and was told the places %v in the queue, not %v",
				waited.turn.Text, ending(events), told(events), waited.places)
		}
	}
	eventually(t, soon, "the cluster still runs Jobs", func() bool { return len(aviary.jobs()) == 0 })

	// The session of the turn that failed goes on.
	next := s.send(lost, "again")
	s.run(aviary.job(next.ID), "done")
	if events := s.follow(next.ID); ending(events) != "turn.completed" || next.Seq != 2 {
		t.Errorf("turn %d of the session whose turn failed ended with %s", next.Seq, ending(events))
	}
}

// No more turns wait than may, and none waits for longer than it may.
func TestQueueLimits(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) {
		config.MaxConcurrentTurns = 1
		config.MaxQueuedTurns = 1
		config.MaxQueueWait = time.Second
	})
	s := aviary.supervisor()

	running := s.send(s.session(), "one")
	aviary.job(running.ID)
	waiting := s.send(s.session(), "two")
	var refusal struct{ Code string }
	status, header := s.request("POST", "/v1/sessions/"+s.session()+"/messages", `{"text": "three"}`, &refusal)
	if status != http.StatusTooManyRequests || refusal.Code != "queue_full" || header.Get("Retry-After") == "" {
		t.Errorf("a message beyond the most that may wait was answered with %d %s, to try again after %q",
			status, refusal.Code, header.Get("Retry-After"))
	}

	events := s.follow(waiting.ID)
	if !slices.Equal(types(events), []string{"turn.expired"}) {
		t.Errorf("the events of the turn that waited too long are %v", events)
	}
	if record := s.turn(waiting.ID); record.State != "expired" || record.EndedAt == nil || record.StartedAt != nil {
		t.Errorf("the record of the turn that waited too long is %+v", record)
	}
	if status, _ := s.request("POST", "/v1/turns/"+waiting.ID+"/cancel", "", &refusal); status != http.StatusConflict || refusal.Code != "turn_ended" {
		t.Errorf("cancelling a turn that has expired was answered with %d %s", status, refusal.Code)
	}
	if jobs := aviary.jobs(); len(jobs) != 1 {
		t.Errorf("the cluster runs %v, not only the turn that did not wait", jobs)
	}
}
