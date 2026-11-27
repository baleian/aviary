package app

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The name, description, model and instructions of an agent are replaced
// together, and the next turn of its sessions runs with them. Its slug and
// its harness stay.
func TestAgentIsReplaced(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project, other := s.project(), s.project()
	provider := func(project string) string {
		return s.create("/v1/projects/"+project+"/providers", `{"name": "a provider", "url": "https://api.example.com/v1", "api_key": {"value": "sk-a-key"}}`)
	}
	first, second, theirs := provider(project), provider(project), provider(other)
	model := func(provider, name string) string {
		return fmt.Sprintf(`"model": {"provider": %q, "name": %q}`, provider, name)
	}
	path := "/v1/agents/" + s.create("/v1/projects/"+project+"/agents", `{"slug": "reviewer", "name": "Code reviewer", `+
		`"description": "Reviews a branch.", "harness": "codex", "instructions": "Be brief.", `+model(first, "a-model")+`}`)
	read := func() map[string]any {
		t.Helper()
		var agent map[string]any
		if status, _ := s.request("GET", path, "", &agent); status != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, status)
		}
		return agent
	}
	created := read()
	if created["updated_at"] == nil || created["updated_at"] != created["created_at"] {
		t.Errorf("new agent has updated_at %v, want its created_at %v", created["updated_at"], created["created_at"])
	}
	// given returns what the runner of the session's next turn is given, and
	// ends the turn.
	session := s.create(path+"/sessions", "")
	given := func() []string {
		t.Helper()
		runner := aviary.given(s.send(session, "hello").ID)
		runner.report(started("harness-session"))
		runner.report(finished("end_turn", nil))
		return []string{runner.Instructions, runner.Environment["AVIARY_LLM_MODEL"]}
	}
	if settings := given(); !reflect.DeepEqual(settings, []string{"Be brief.", "a-model"}) {
		t.Errorf("runner of the agent's turn was given %q", settings)
	}

	// What the body leaves out is empty afterwards.
	var replaced map[string]any
	body := `{"name": "Reviewer", "instructions": "Be thorough.", ` + model(second, "another-model") + `}`
	if status, _ := s.request("PUT", path, body, &replaced); status != http.StatusOK {
		t.Fatalf("PUT %s %s: status %d, want 200", path, body, status)
	}
	described, present := replaced["description"]
	if replaced["id"] != created["id"] || replaced["project_id"] != project || replaced["slug"] != "reviewer" || replaced["harness"] != "codex" ||
		replaced["name"] != "Reviewer" || !present || described != "" || replaced["instructions"] != "Be thorough." ||
		!reflect.DeepEqual(replaced["model"], map[string]any{"provider": second, "name": "another-model"}) || replaced["created_at"] != created["created_at"] {
		t.Errorf("replaced agent = %v", replaced)
	}
	at := func(agent map[string]any, field string) time.Time {
		t.Helper()
		value, _ := agent[field].(string)
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			t.Fatalf("%s of the agent = %v: %v", field, agent[field], err)
		}
		return parsed
	}
	if !at(replaced, "updated_at").After(at(created, "updated_at")) {
		t.Errorf("updated_at of the replaced agent = %v, want it after %v", replaced["updated_at"], created["updated_at"])
	}
	if again := read(); !reflect.DeepEqual(again, replaced) {
		t.Errorf("agent read after it was replaced = %v, want %v", again, replaced)
	}
	if settings := given(); !reflect.DeepEqual(settings, []string{"Be thorough.", "another-model"}) {
		t.Errorf("runner of the replaced agent's turn was given %q", settings)
	}

	// The slug and the harness may be given, as they are.
	var plain map[string]any
	body = `{"slug": "reviewer", "harness": "codex", ` + model(first, "a-model") + `}`
	if status, _ := s.request("PUT", path, body, &plain); status != http.StatusOK || plain["slug"] != "reviewer" || plain["harness"] != "codex" ||
		plain["name"] != "" || plain["instructions"] != nil {
		t.Errorf("PUT %s %s = %d %v, want 200 with an agent that has only a model", path, body, status, plain)
	}
	last := read()
	for _, test := range []struct {
		path, body string
		status     int
		code       string
	}{
		{path, ``, http.StatusBadRequest, "invalid_request"},
		{path, `{}`, http.StatusBadRequest, "invalid_request"},
		{path, `{"name": "Reviewer"}`, http.StatusBadRequest, "invalid_request"},
		{path, `{"slug": "renamed", ` + model(first, "a-model") + `}`, http.StatusBadRequest, "invalid_request"},
		{path, `{"harness": "opencode", ` + model(first, "a-model") + `}`, http.StatusBadRequest, "invalid_request"},
		{path, `{"id": "` + uuid.NewString() + `", ` + model(first, "a-model") + `}`, http.StatusBadRequest, "invalid_request"},
		{path, `{` + model(first, "") + `}`, http.StatusBadRequest, "invalid_request"},
		{path, `{` + model(theirs, "a-model") + `}`, http.StatusBadRequest, "invalid_request"},
		{path, `{` + model(uuid.NewString(), "a-model") + `}`, http.StatusBadRequest, "invalid_request"},
		{"/v1/agents/" + uuid.NewString(), `{` + model(first, "a-model") + `}`, http.StatusNotFound, "not_found"},
	} {
		var refusal struct{ Code, Message string }
		if status, _ := s.request("PUT", test.path, test.body, &refusal); status != test.status || refusal.Code != test.code || refusal.Message == "" {
			t.Errorf("PUT %s %s = %d %s %q, want %d %s", test.path, test.body, status, refusal.Code, refusal.Message, test.status, test.code)
		}
	}
	if after := read(); !reflect.DeepEqual(after, last) {
		t.Errorf("agent after refused replacements = %v, want %v", after, last)
	}
}

// Deleting an agent ends its sessions, deletes their sandboxes and frees its
// slug. The agent and its sessions stay readable.
func TestAgentIsDeleted(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	s.hears()
	project := s.project()
	stream := s.changes(project)
	agents := "/v1/projects/" + project + "/agents"
	agent := s.agent(project, `{"slug": "reviewer", "harness": "opencode"}`)
	path := "/v1/agents/" + agent
	other := s.create("/v1/agents/"+s.agent(project, `{"harness": "opencode"}`)+"/sessions", "")
	// A session that was never used, and one with a sandbox and a turn that
	// is still running.
	idle, used := s.create(path+"/sessions", ""), s.create(path+"/sessions", "")
	runner := aviary.given(s.send(used, "hello").ID)
	runner.report(started("harness-session"))
	read := func(path string) map[string]any {
		t.Helper()
		var record map[string]any
		if status, _ := s.request("GET", path, "", &record); status != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, status)
		}
		return record
	}
	// listed returns the IDs of the project's agents.
	listed := func() []string {
		t.Helper()
		ids := []string{}
		for _, listed := range read(agents)["agents"].([]any) {
			ids = append(ids, listed.(map[string]any)["id"].(string))
		}
		return ids
	}

	// A turn that has not ended keeps the agent.
	var refusal struct{ Code string }
	if status, _ := s.request("DELETE", path, "", &refusal); status != http.StatusConflict || refusal.Code != "session_busy" {
		t.Fatalf("delete an agent with a running turn = %d %s, want 409 session_busy", status, refusal.Code)
	}
	if kept := read(path); kept["deleted_at"] != nil || len(listed()) != 2 || read("/v1/sessions/" + idle)["ended_at"] != nil {
		t.Errorf("after the refused deletion the agent is %v, among %d listed", kept, len(listed()))
	}
	runner.report(finished("end_turn", nil))
	if sandboxes := aviary.service.Made(); len(sandboxes) != 1 {
		t.Fatalf("sandboxes before the agent is deleted = %v, want one", sandboxes)
	}
	// Every change so far has been sent.
	stream.sent("the session with its completed turn", func(session map[string]any) bool {
		last, _ := session["last_turn"].(map[string]any)
		return session["id"] == used && last["state"] == "completed"
	})

	var deleted map[string]any
	if status, _ := s.request("DELETE", path, "", &deleted); status != http.StatusOK || deleted["id"] != agent || deleted["deleted_at"] == nil ||
		deleted["slug"] != "reviewer" {
		t.Fatalf("delete agent = %d %v, want 200 with the agent and deleted_at", status, deleted)
	}
	if again := read(path); !reflect.DeepEqual(again, deleted) {
		t.Errorf("deleted agent read as %v, want %v", again, deleted)
	}
	if ids := listed(); len(ids) != 1 || ids[0] == agent {
		t.Errorf("agents listed after the deletion = %v, want only the other agent", ids)
	}
	// Its sessions have ended, and say why. Those who watch the project are
	// sent both.
	ended := map[string]bool{}
	for len(ended) < 2 {
		session := stream.sent("a session that ended with its agent", func(session map[string]any) bool {
			return session["ended_at"] != nil && session["end_reason"] == "agent_deleted"
		})
		ended[session["id"].(string)] = true
	}
	if !ended[idle] || !ended[used] {
		t.Errorf("sessions sent as ended with their agent = %v, want %s and %s", ended, idle, used)
	}
	for _, id := range []string{idle, used} {
		session := read("/v1/sessions/" + id)
		if _, has := session["sandbox"]; session["ended_at"] == nil || session["end_reason"] != "agent_deleted" || has {
			t.Errorf("session %s of the deleted agent = %v, want it ended without a sandbox", id, session)
		}
	}
	if sandboxes := aviary.service.Made(); len(sandboxes) != 0 {
		t.Errorf("sandboxes after the agent was deleted = %v, want none", sandboxes)
	}
	if session := read("/v1/sessions/" + other); session["ended_at"] != nil {
		t.Errorf("session of another agent = %v, want it not ended", session)
	}
	if turns, _ := read("/v1/sessions/" + used + "/turns")["turns"].([]any); len(turns) != 1 {
		t.Errorf("turns of a session of the deleted agent = %v, want its one turn", turns)
	}
	if sessions, _ := read(path + "/sessions")["sessions"].([]any); len(sessions) != 2 {
		t.Errorf("sessions listed for the deleted agent = %v, want its two", sessions)
	}

	model := fmt.Sprintf(`"model": {"provider": %q, "name": "a-model"}`, deleted["model"].(map[string]any)["provider"])
	for _, test := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"POST", path + "/sessions", ``, http.StatusConflict, "agent_deleted"},
		{"PUT", path, `{` + model + `}`, http.StatusConflict, "agent_deleted"},
		{"POST", "/v1/sessions/" + idle + "/messages", `{"text": "hello"}`, http.StatusConflict, "session_ended"},
		{"DELETE", "/v1/agents/" + uuid.NewString(), ``, http.StatusNotFound, "not_found"},
	} {
		var refusal struct{ Code, Message string }
		if status, _ := s.request(test.method, test.path, test.body, &refusal); status != test.status || refusal.Code != test.code || refusal.Message == "" {
			t.Errorf("%s %s %s = %d %s %q, want %d %s", test.method, test.path, test.body, status, refusal.Code, refusal.Message, test.status, test.code)
		}
	}
	// Deleting it again changes nothing.
	var again map[string]any
	if status, _ := s.request("DELETE", path, "", &again); status != http.StatusOK || !reflect.DeepEqual(again, deleted) {
		t.Errorf("second deletion = %d %v, want 200 with %v", status, again, deleted)
	}
	// Its slug is free again.
	if next := s.create(agents, `{"slug": "reviewer", "harness": "opencode", `+model+`}`); next == agent {
		t.Errorf("agent created with the slug of the deleted one has its ID %s", next)
	}
}
