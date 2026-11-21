package app

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The name, description, model, instructions and approval of an agent are
// replaced together, and the next turn of its sessions runs with them. Its
// slug and its harness stay.
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
		return []string{runner.Instructions, runner.Approval, runner.Environment["AVIARY_APPROVAL"], runner.Environment["AVIARY_LLM_MODEL"]}
	}
	if settings := given(); !reflect.DeepEqual(settings, []string{"Be brief.", "", "", "a-model"}) {
		t.Errorf("runner of the agent's turn was given %q", settings)
	}

	// What the body leaves out is empty afterwards.
	var replaced map[string]any
	body := `{"name": "Reviewer", "instructions": "Be thorough.", "approval": "ask", ` + model(second, "another-model") + `}`
	if status, _ := s.request("PUT", path, body, &replaced); status != http.StatusOK {
		t.Fatalf("PUT %s %s: status %d, want 200", path, body, status)
	}
	described, present := replaced["description"]
	if replaced["id"] != created["id"] || replaced["project_id"] != project || replaced["slug"] != "reviewer" || replaced["harness"] != "codex" ||
		replaced["name"] != "Reviewer" || !present || described != "" || replaced["instructions"] != "Be thorough." || replaced["approval"] != "ask" ||
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
	if settings := given(); !reflect.DeepEqual(settings, []string{"Be thorough.", "ask", "ask", "another-model"}) {
		t.Errorf("runner of the replaced agent's turn was given %q", settings)
	}

	// The slug and the harness may be given, as they are.
	var plain map[string]any
	body = `{"slug": "reviewer", "harness": "codex", ` + model(first, "a-model") + `}`
	if status, _ := s.request("PUT", path, body, &plain); status != http.StatusOK || plain["slug"] != "reviewer" || plain["harness"] != "codex" ||
		plain["name"] != "" || plain["instructions"] != nil || plain["approval"] != nil {
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
		{path, `{"approval": "always", ` + model(first, "a-model") + `}`, http.StatusBadRequest, "invalid_request"},
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
