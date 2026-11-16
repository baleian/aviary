package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"

	"aviary/supervisor/internal/testenv"
)

// waiting returns the IDs of the requests for approval that the turn shows
// as waiting for a person.
func (s *supervisor) waiting(turn string) []string {
	s.t.Helper()
	ids := []string{}
	for _, encoded := range s.turn(turn).Approvals {
		var request struct{ ID string }
		if err := json.Unmarshal(encoded, &request); err != nil {
			s.t.Fatal(err)
		}
		ids = append(ids, request.ID)
	}
	return ids
}

// approve sends the answer of a person to a request for approval. It
// returns the status of the reply and its body.
func (s *supervisor) approve(turn, request, body string) (int, map[string]string) {
	s.t.Helper()
	var reply map[string]string
	status, _ := s.request("POST", "/v1/turns/"+turn+"/approvals/"+request, body, &reply)
	return status, reply
}

// A harness that asks before it acts is answered by a person, through any
// Supervisor. The turn waits for the answer.
func TestApproval(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	connected, asked := aviary.supervisor(), aviary.supervisor()
	session := asked.session(`{"harness": "opencode", "approval": "ask"}`)
	accepted := asked.send(session, "hello")
	job := aviary.job(accepted.ID)
	if approval := told(job, "AVIARY_APPROVAL"); approval != "ask" {
		t.Errorf("the pod of a turn of an agent with approval is told %q", approval)
	}
	runner := connected.take(job)
	runner.report(started("harness-session"))
	watching := asked.watch(accepted.ID)
	if waiting := asked.waiting(accepted.ID); len(waiting) != 0 {
		t.Errorf("a turn whose harness has asked nothing shows the requests %v", waiting)
	}

	runner.report(asks("1", "rm -r build"))
	runner.report(asks("2", "git push"))
	// A runner that lost its Supervisor reports a request again.
	if received, err := runner.send(runner.seq, asks("2", "git push")); err != nil || received != runner.seq {
		t.Fatalf("a request that was reported again was answered with %d: %v", received, err)
	}
	if waiting := asked.waiting(accepted.ID); !slices.Equal(waiting, []string{"1", "2"}) {
		t.Errorf("the turn shows the requests %v as waiting", waiting)
	}

	// The answer of a person reaches the runner through the Supervisor that
	// the runner is connected to, at once.
	status, reply := asked.approve(accepted.ID, "2", `{"option_id": "once"}`)
	if status != http.StatusAccepted || reply["id"] != "2" || reply["option_id"] != "once" {
		t.Errorf("the answer to a request was answered with %d %v", status, reply)
	}
	if id, option := runner.answer(); id != "2" || option != "once" {
		t.Errorf("the runner was given the answer %q to the request %q", option, id)
	}
	// The request waits for a person no longer, and the first answer stands.
	if waiting := connected.waiting(accepted.ID); !slices.Equal(waiting, []string{"1"}) {
		t.Errorf("after an answer the turn shows the requests %v as waiting", waiting)
	}
	if status, reply := connected.approve(accepted.ID, "2", `{"option_id": "never"}`); status != http.StatusConflict || reply["code"] != "approval_answered" {
		t.Errorf("a second answer to a request was answered with %d %v", status, reply)
	}

	// A runner that loses its Supervisor before the harness has the answer
	// is given it again by the one that it calls.
	runner.call.hang()
	if _, err := runner.attach(asked); err != nil {
		t.Fatal(err)
	}
	if id, option := runner.answer(); id != "2" || option != "once" {
		t.Errorf("the runner that came back was given the answer %q to the request %q", option, id)
	}
	runner.report(answered("2", "selected", "once"))
	// An answer that the harness has is not passed on again.
	runner.call.hang()
	if _, err := runner.attach(connected); err != nil {
		t.Fatal(err)
	}
	select {
	case again := <-runner.call.answers:
		t.Errorf("the runner was given the answer %v that its harness has", again)
	case <-time.After(300 * time.Millisecond):
	}

	// The other request is still open when the turn is told to stop.
	runner.report(answered("1", "cancelled", ""))
	if status, reply := asked.approve(accepted.ID, "1", `{"option_id": "once"}`); status != http.StatusConflict || reply["code"] != "approval_answered" {
		t.Errorf("an answer to a request that the harness waits for no longer was answered with %d %v", status, reply)
	}
	runner.report(finished("end_turn", nil))
	runner.leave()
	aviary.end(job, batchv1.JobComplete, nil)

	events := watching()
	expected := []string{
		"turn.started", "approval.requested", "approval.requested", "approval.resolved", "approval.resolved", "turn.completed",
	}
	if !slices.Equal(types(events), expected) {
		t.Fatalf("the events of the turn are %v, not %v", types(events), expected)
	}
	of := func(kind string) []event {
		var of []event
		for _, event := range events {
			if event.Type == kind {
				of = append(of, event)
			}
		}
		return of
	}
	var request struct {
		ID       string
		ToolCall struct{ ID, Title string } `json:"tool_call"`
		Options  []struct{ ID, Kind, Name string }
	}
	first := of("approval.requested")[0].Data
	if err := json.Unmarshal([]byte(first), &request); err != nil || request.ID != "1" ||
		request.ToolCall.ID != "call-1" || request.ToolCall.Title != "rm -r build" ||
		len(request.Options) != 2 || request.Options[0].ID != "once" || request.Options[0].Kind != "allow_once" || request.Options[0].Name != "Once" {
		t.Errorf("the request for approval is told as %s: %v", first, err)
	}
	var resolved map[string]string
	selected := of("approval.resolved")[0].Data
	if err := json.Unmarshal([]byte(selected), &resolved); err != nil ||
		resolved["id"] != "2" || resolved["outcome"] != "selected" || resolved["option_id"] != "once" {
		t.Errorf("the answer to a request is told as %s: %v", selected, err)
	}

	// The record keeps what was asked and how it was answered. A turn that
	// has ended shows no requests as waiting, and takes no answers.
	record := asked.turn(accepted.ID)
	kept := items(t, record)
	if len(kept) != 2 || kept[0]["type"] != "approval" || kept[0]["id"] != "1" || kept[0]["outcome"] != "cancelled" ||
		kept[1]["id"] != "2" || kept[1]["outcome"] != "selected" || kept[1]["option_id"] != "once" || record.Approvals != nil {
		t.Errorf("the record of the turn has the items %v and the requests %s", kept, record.Approvals)
	}
}

func TestApprovalRefusals(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session := s.session()
	accepted := s.send(session, "hello")
	job := aviary.job(accepted.ID)
	runner := s.take(job)
	runner.report(started("harness-session"))
	runner.report(asks("1", "rm -r build"))

	for name, test := range map[string]struct {
		turn, request, body string
		status              int
		code                string
	}{
		"an option that the harness does not take": {accepted.ID, "1", `{"option_id": "always"}`, http.StatusBadRequest, "invalid_request"},
		"no option":                   {accepted.ID, "1", `{}`, http.StatusBadRequest, "invalid_request"},
		"a field that there is not":   {accepted.ID, "1", `{"option_id": "once", "id": "2"}`, http.StatusBadRequest, "invalid_request"},
		"no body":                     {accepted.ID, "1", ``, http.StatusBadRequest, "invalid_request"},
		"a request that there is not": {accepted.ID, "2", `{"option_id": "once"}`, http.StatusNotFound, "not_found"},
		"a turn that there is not":    {"01a1e000-0000-7000-8000-000000000000", "1", `{"option_id": "once"}`, http.StatusNotFound, "not_found"},
		"a turn ID that is none":      {"turn", "1", `{"option_id": "once"}`, http.StatusNotFound, "not_found"},
	} {
		if status, reply := s.approve(test.turn, test.request, test.body); status != test.status || reply["code"] != test.code {
			t.Errorf("%s: answered with %d %v, not %d %s", name, status, reply, test.status, test.code)
		}
	}
	// None of them was taken for an answer.
	if waiting := s.waiting(accepted.ID); !slices.Equal(waiting, []string{"1"}) {
		t.Errorf("the turn shows the requests %v as waiting", waiting)
	}

	// A turn that has ended takes no answer to what its harness asked.
	runner.call.hang()
	aviary.end(job, batchv1.JobFailed, nil)
	s.follow(accepted.ID)
	if status, reply := s.approve(accepted.ID, "1", `{"option_id": "once"}`); status != http.StatusConflict || reply["code"] != "turn_ended" {
		t.Errorf("an answer to a request of a turn that has ended was answered with %d %v", status, reply)
	}
	if waiting := s.waiting(accepted.ID); len(waiting) != 0 {
		t.Errorf("a turn that has ended shows the requests %v as waiting", waiting)
	}
}

// An answer whose announcement is lost is found a little later by the
// Supervisor that the runner is connected to.
func TestApprovalWithoutAnnouncement(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	aviary.sweep = 100 * time.Millisecond
	s := aviary.supervisor()
	accepted := s.send(s.session(), "hello")
	runner := s.take(aviary.job(accepted.ID))
	runner.report(started("harness-session"))
	runner.report(asks("1", "rm -r build"))
	// The answer is recorded as the API records it, and nothing is announced.
	testenv.Exec(t, aviary.database,
		`update approvals set option_id = 'never', answered_at = now() where turn_id = $1 and id = '1'`, accepted.ID)
	if id, option := runner.answer(); id != "1" || option != "never" {
		t.Errorf("the runner was given the answer %q to the request %q", option, id)
	}
}
