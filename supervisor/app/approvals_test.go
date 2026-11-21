package app

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/api"
	"aviary/supervisor/internal/driver"
)

// waiting returns the IDs of the pending approval requests of the turn.
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

// approve answers an approval request and returns the response status and
// body.
func (s *supervisor) approve(turn, request, body string) (int, map[string]string) {
	s.t.Helper()
	var reply map[string]string
	status, _ := s.request("POST", "/v1/turns/"+turn+"/approvals/"+request, body, &reply)
	return status, reply
}

// An approval request can be answered through any Supervisor.
func TestApproval(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	following := aviary.supervisor()
	session := following.session(`{"harness": "opencode", "approval": "ask"}`)
	accepted := following.send(session, "hello")
	runner := aviary.given(accepted.ID)
	if runner.Approval != "ask" || runner.Environment["AVIARY_APPROVAL"] != "ask" {
		t.Errorf("runner turn = %+v, want approval ask", runner.Turn)
	}
	// Started after the dispatch, so that the other Supervisor follows the
	// turn.
	asked := aviary.supervisor()
	runner.report(started("harness-session"))
	watching := asked.watch(accepted.ID)
	if waiting := asked.waiting(accepted.ID); len(waiting) != 0 {
		t.Errorf("pending approvals = %v, want none", waiting)
	}

	runner.report(asks("1", "rm -r build"))
	runner.report(asks("2", "git push"))
	if waiting := asked.waiting(accepted.ID); !slices.Equal(waiting, []string{"1", "2"}) {
		t.Errorf("pending approvals = %v, want [1 2]", waiting)
	}

	// The answer reaches the runner through the Supervisor that follows the
	// turn.
	status, reply := asked.approve(accepted.ID, "2", `{"option_id": "once"}`)
	if status != http.StatusAccepted || reply["id"] != "2" || reply["option_id"] != "once" {
		t.Errorf("approve = %d %v, want 202", status, reply)
	}
	if id, option := runner.answer(); id != "2" || option != "once" {
		t.Errorf("runner got option %q for request %q", option, id)
	}
	// The request is no longer pending, and a second answer is refused.
	if waiting := following.waiting(accepted.ID); !slices.Equal(waiting, []string{"1"}) {
		t.Errorf("pending approvals after an answer = %v, want [1]", waiting)
	}
	if status, reply := following.approve(accepted.ID, "2", `{"option_id": "never"}`); status != http.StatusConflict || reply["code"] != "approval_answered" {
		t.Errorf("second answer = %d %v, want 409 approval_answered", status, reply)
	}
	runner.report(answered("2", "selected", "once"))

	// The harness cancels the other request, as when the turn is stopped.
	runner.report(answered("1", "cancelled", ""))
	if status, reply := asked.approve(accepted.ID, "1", `{"option_id": "once"}`); status != http.StatusConflict || reply["code"] != "approval_answered" {
		t.Errorf("answer to a cancelled request = %d %v, want 409 approval_answered", status, reply)
	}
	runner.report(finished("end_turn", nil))

	events := watching()
	expected := []string{
		"turn.started", "approval.requested", "approval.requested", "approval.resolved", "approval.resolved", "turn.completed",
	}
	if !slices.Equal(types(events), expected) {
		t.Fatalf("event types = %v, want %v", types(events), expected)
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
		t.Errorf("unexpected approval.requested data %s: %v", first, err)
	}
	var resolved map[string]string
	selected := of("approval.resolved")[0].Data
	// Without an identity provider, the answer has no answered_by.
	if err := json.Unmarshal([]byte(selected), &resolved); err != nil || len(resolved) != 3 ||
		resolved["id"] != "2" || resolved["outcome"] != "selected" || resolved["option_id"] != "once" {
		t.Errorf("unexpected approval.resolved data %s: %v", selected, err)
	}

	// The record keeps each request with its outcome, and an ended turn has
	// no pending requests.
	record := asked.turn(accepted.ID)
	kept := items(t, record)
	if len(kept) != 2 || kept[0]["type"] != "approval" || kept[0]["id"] != "1" || kept[0]["outcome"] != "cancelled" ||
		kept[1]["id"] != "2" || kept[1]["outcome"] != "selected" || kept[1]["option_id"] != "once" || record.Approvals != nil {
		t.Errorf("turn items = %v, approvals = %s", kept, record.Approvals)
	}
}

func TestApprovalRefusals(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session := s.session()
	accepted := s.send(session, "hello")
	runner := aviary.given(accepted.ID)
	runner.report(started("harness-session"))
	runner.report(asks("1", "rm -r build"))

	for name, test := range map[string]struct {
		turn, request, body string
		status              int
		code                string
	}{
		"unknown option":  {accepted.ID, "1", `{"option_id": "always"}`, http.StatusBadRequest, "invalid_request"},
		"no option":       {accepted.ID, "1", `{}`, http.StatusBadRequest, "invalid_request"},
		"unknown field":   {accepted.ID, "1", `{"option_id": "once", "id": "2"}`, http.StatusBadRequest, "invalid_request"},
		"no body":         {accepted.ID, "1", ``, http.StatusBadRequest, "invalid_request"},
		"unknown request": {accepted.ID, "2", `{"option_id": "once"}`, http.StatusNotFound, "not_found"},
		"unknown turn":    {"01a1e000-0000-7000-8000-000000000000", "1", `{"option_id": "once"}`, http.StatusNotFound, "not_found"},
		"invalid turn ID": {"turn", "1", `{"option_id": "once"}`, http.StatusNotFound, "not_found"},
	} {
		if status, reply := s.approve(test.turn, test.request, test.body); status != test.status || reply["code"] != test.code {
			t.Errorf("%s: got %d %v, want %d %s", name, status, reply, test.status, test.code)
		}
	}
	// None of them was accepted.
	if waiting := s.waiting(accepted.ID); !slices.Equal(waiting, []string{"1"}) {
		t.Errorf("pending approvals = %v, want [1]", waiting)
	}

	// An ended turn takes no answers.
	runner.lose()
	s.follow(accepted.ID)
	if status, reply := s.approve(accepted.ID, "1", `{"option_id": "once"}`); status != http.StatusConflict || reply["code"] != "turn_ended" {
		t.Errorf("answer after the turn ended = %d %v, want 409 turn_ended", status, reply)
	}
	if waiting := s.waiting(accepted.ID); len(waiting) != 0 {
		t.Errorf("pending approvals of an ended turn = %v, want none", waiting)
	}
}

// If the announcement of an answer is lost, the Supervisor that follows the
// turn finds the answer when it sweeps.
func TestApprovalWithoutAnnouncement(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) { config.Sweep = 100 * time.Millisecond })
	s := aviary.supervisor()
	accepted := s.send(s.session(), "hello")
	runner := aviary.given(accepted.ID)
	runner.report(started("harness-session"))
	runner.report(asks("1", "rm -r build"))
	// Record the answer as the API does, without announcing it.
	answered, err := aviary.records.AnswerApproval(context.Background(), uuid.MustParse(accepted.ID), "1", "never", uuid.Nil)
	if err != nil || !answered {
		t.Fatalf("AnswerApproval = %t, %v", answered, err)
	}
	if id, option := runner.answer(); id != "1" || option != "never" {
		t.Errorf(`runner got option %q for request %q, want "never" for "1"`, option, id)
	}
	// The answer is delivered again on every sweep until the runner reports
	// it resolved.
	if id, option := runner.answer(); id != "1" || option != "never" {
		t.Errorf(`redelivered option %q for request %q, want "never" for "1"`, option, id)
	}
}

// The approval.resolved event and the turn's record name the user who
// answered.
func TestApprovalIsAnsweredBySomeone(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	aviary.callers = api.Callers{
		Provider: tokens{
			"an-administrator": {Subject: "1", Groups: []string{"platform"}},
			"token-of-alice":   {Subject: "2", Username: "alice@example.com"},
			"token-of-carol":   {Subject: "3", Username: "carol@example.com"},
		},
		Admins: []string{"platform"},
	}
	s := aviary.supervisor()
	s.token = "an-administrator"
	project := s.project()
	agent := s.agent(project, `{"harness": "opencode", "approval": "ask"}`)
	s.create("/v1/projects/"+project+"/members", `{"user": "alice@example.com", "role": "user"}`)
	s.create("/v1/projects/"+project+"/members", `{"user": "carol@example.com", "role": "owner"}`)

	s.token = "token-of-carol"
	var carol me
	s.request(http.MethodGet, "/v1/me", "", &carol)
	s.token = "token-of-alice"
	accepted := s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello")
	runner := aviary.given(accepted.ID)
	runner.report(started("harness-session"))
	watching := s.watch(accepted.ID)
	runner.report(asks("1", "rm -r build"))
	runner.report(asks("2", "git push"))

	// Carol, an owner, answers both requests. The harness resolves the first
	// with Carol's answer and cancels the second, which then has no
	// answered_by.
	s.token = "token-of-carol"
	for _, request := range []string{"1", "2"} {
		if status, reply := s.approve(accepted.ID, request, `{"option_id": "once"}`); status != http.StatusAccepted {
			t.Fatalf("approve = %d %v, want 202", status, reply)
		}
		runner.answer()
	}
	runner.report(answered("1", "selected", "once"))
	runner.report(answered("2", "cancelled", ""))
	runner.report(finished("end_turn", nil))

	var resolved []map[string]string
	for _, event := range watching() {
		if event.Type == "approval.resolved" {
			var data map[string]string
			if err := json.Unmarshal([]byte(event.Data), &data); err != nil {
				t.Fatal(err)
			}
			resolved = append(resolved, data)
		}
	}
	if len(resolved) != 2 || resolved[0]["id"] != "1" || resolved[0]["outcome"] != "selected" || resolved[0]["option_id"] != "once" ||
		resolved[0]["answered_by"] != carol.ID || carol.ID == "" {
		t.Errorf("answers by user %s: approval.resolved events = %v", carol.ID, resolved)
	}
	if by, told := resolved[len(resolved)-1]["answered_by"]; told {
		t.Errorf("cancelled request has answered_by %v", by)
	}
	kept := items(t, s.turn(accepted.ID))
	if len(kept) != 2 || kept[0]["id"] != "1" || kept[0]["answered_by"] != carol.ID || kept[1]["answered_by"] != nil {
		t.Errorf("turn items = %v", kept)
	}
}
