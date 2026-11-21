package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Invalid requests and unknown resources are answered with a JSON error that
// has a code and a message.
func TestRefusals(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project := s.project()
	agents := "/v1/projects/" + project + "/agents"
	sessions := "/v1/agents/" + s.agent(project, `{"harness": "opencode"}`) + "/sessions"
	session := s.create(sessions, "")
	providers := "/v1/projects/" + project + "/providers"
	provided := s.create(providers, `{"name": "a provider", "url": "https://api.example.com/v1"}`)
	provider := "/v1/providers/" + provided
	// model is a valid model field of an agent.
	model := `"model": {"provider": "` + provided + `", "name": "a-model"}`
	none := uuid.NewString()
	long, err := json.Marshal(map[string]string{"text": strings.Repeat("a", 1<<20)})
	if err != nil {
		t.Fatal(err)
	}
	const invalid, notFound = "invalid_request", "not_found"
	for _, test := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"POST", "/v1/projects", ``, http.StatusBadRequest, invalid},
		{"POST", "/v1/projects", `{}`, http.StatusBadRequest, invalid},
		{"POST", "/v1/projects", `{"slug": "test", "owner": "someone"}`, http.StatusBadRequest, invalid},
		{"GET", "/v1/projects/" + none, ``, http.StatusNotFound, notFound},
		{"GET", "/v1/projects/not-an-id", ``, http.StatusNotFound, notFound},

		{"POST", providers, `{}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"url": "https://api.example.com/v1"}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider"}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider", "url": "api.example.com/v1"}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider", "url": "ftp://api.example.com/v1"}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider", "url": "https:///v1"}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider", "url": "https://user:password@api.example.com/v1"}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider", "url": "https://api.example.com/v1?key=value"}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider", "url": "https://api.example.com/v1?"}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider", "url": "https://api.example.com/v1#part"}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider", "url": "https://api.example.com/v1", "api_key": "sk-a-key"}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider", "url": "https://api.example.com/v1", "api_key": {}}`, http.StatusBadRequest, invalid},
		{"POST", providers, `{"name": "a provider", "url": "https://api.example.com/v1", "api_key": {"value": "sk-a-key", "stored": true}}`, http.StatusBadRequest, invalid},
		{"POST", "/v1/projects/" + none + "/providers", `{"name": "a provider", "url": "https://api.example.com/v1"}`, http.StatusNotFound, notFound},
		{"GET", "/v1/providers/" + none, ``, http.StatusNotFound, notFound},
		{"PUT", provider, `{"name": "a provider"}`, http.StatusBadRequest, invalid},
		{"PUT", "/v1/providers/" + none, `{"name": "a provider", "url": "https://api.example.com/v1"}`, http.StatusNotFound, notFound},

		{"POST", agents, `{"slug": "test", "harness": "another", ` + model + `}`, http.StatusBadRequest, invalid},
		{"POST", agents, `{"slug": "test", "harness": "opencode", "resources": {"limits": {"memory": "2Gi"}}, ` + model + `}`, http.StatusBadRequest, invalid},
		{"POST", agents, `{"slug": "test", "harness": "opencode", "approval": "always", ` + model + `}`, http.StatusBadRequest, invalid},
		{"POST", agents, `{"slug": "test", "harness": "opencode"}`, http.StatusBadRequest, invalid},
		{"POST", "/v1/projects/" + none + "/agents", `{"slug": "test", "harness": "opencode", ` + model + `}`, http.StatusNotFound, notFound},
		{"GET", "/v1/agents/" + none, ``, http.StatusNotFound, notFound},

		{"POST", sessions, `{"title": 7}`, http.StatusBadRequest, invalid},
		{"POST", sessions, `{"title": "a title", "agent_id": "` + none + `"}`, http.StatusBadRequest, invalid},
		{"POST", "/v1/agents/" + none + "/sessions", ``, http.StatusNotFound, notFound},
		{"GET", "/v1/sessions/" + none, ``, http.StatusNotFound, notFound},
		{"PATCH", "/v1/sessions/" + session, ``, http.StatusBadRequest, invalid},
		{"PATCH", "/v1/sessions/" + session, `{}`, http.StatusBadRequest, invalid},
		{"PATCH", "/v1/sessions/" + session, `{"title": null}`, http.StatusBadRequest, invalid},
		{"PATCH", "/v1/sessions/" + session, `{"title": 7}`, http.StatusBadRequest, invalid},
		{"PATCH", "/v1/sessions/" + session, `{"title": "a title", "agent_id": "` + none + `"}`, http.StatusBadRequest, invalid},
		{"PATCH", "/v1/sessions/" + none, `{"title": "a title"}`, http.StatusNotFound, notFound},

		{"POST", "/v1/sessions/" + session + "/messages", `{}`, http.StatusBadRequest, invalid},
		{"POST", "/v1/sessions/" + session + "/messages", string(long), http.StatusBadRequest, invalid},
		{"POST", "/v1/sessions/" + none + "/messages", `{"text": "hello"}`, http.StatusNotFound, notFound},

		{"GET", "/v1/sessions/" + none + "/turns", ``, http.StatusNotFound, notFound},
		{"GET", "/v1/sessions/" + session + "/turns?limit=0", ``, http.StatusBadRequest, invalid},
		{"GET", "/v1/sessions/" + session + "/turns?limit=101", ``, http.StatusBadRequest, invalid},
		{"GET", "/v1/sessions/" + session + "/turns?before=0", ``, http.StatusBadRequest, invalid},
		{"GET", "/v1/sessions/" + session + "/turns?before=first", ``, http.StatusBadRequest, invalid},
		{"GET", "/v1/turns/" + none, ``, http.StatusNotFound, notFound},
		{"POST", "/v1/turns/" + none + "/cancel", ``, http.StatusNotFound, notFound},
		{"GET", "/v1/turns/" + none + "/events", ``, http.StatusNotFound, notFound},
	} {
		var refusal struct{ Code, Message string }
		status, header := s.request(test.method, test.path, test.body, &refusal)
		if status != test.status || refusal.Code != test.code || refusal.Message == "" || header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s %.60s = %d %s %q, want %d %s",
				test.method, test.path, test.body, status, refusal.Code, refusal.Message, test.status, test.code)
		}
	}
	if status, _ := s.request("GET", "/healthz", "", nil); status != http.StatusOK {
		t.Errorf("GET /healthz: status %d, want 200", status)
	}
}

func TestResources(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	get := func(path string) map[string]any {
		t.Helper()
		var resource map[string]any
		if status, _ := s.request("GET", path, "", &resource); status != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, status)
		}
		return resource
	}
	// returned posts body to path and returns the new resource three ways:
	// from the response, from GET read+ID, and from the list at path under
	// the key list.
	returned := func(path, body, read, list string) map[string]map[string]any {
		t.Helper()
		var created map[string]any
		if status, _ := s.request("POST", path, body, &created); status != http.StatusCreated {
			t.Fatalf("POST %s %s: status %d, want 201", path, body, status)
		}
		id, _ := created["id"].(string)
		forms := map[string]map[string]any{"created": created, "read": get(read + id), "listed": nil}
		listed, _ := get(path)[list].([]any)
		for _, resource := range listed {
			if resource, _ := resource.(map[string]any); resource["id"] == id {
				forms["listed"] = resource
			}
		}
		return forms
	}
	// named reports whether the resource has the slug and the name, which is
	// returned even if it is empty.
	named := func(resource map[string]any, slug, name string) bool {
		given, present := resource["name"]
		return resource["slug"] == slug && present && given == name
	}

	projects := returned("/v1/projects", `{"slug": "a-project", "name": "A project"}`, "/v1/projects/", "projects")
	project, _ := projects["created"]["id"].(string)
	for how, resource := range projects {
		if resource["id"] != project || !named(resource, "a-project", "A project") || resource["created_at"] == nil {
			t.Errorf("%s project %v", how, resource)
		}
	}
	for how, resource := range returned("/v1/projects", `{"slug": "unnamed"}`, "/v1/projects/", "projects") {
		if !named(resource, "unnamed", "") {
			t.Errorf("%s project without a name %v", how, resource)
		}
	}

	provider := s.create("/v1/projects/"+project+"/providers", `{"name": "a provider", "url": "https://api.example.com/v1"}`)
	agents := returned("/v1/projects/"+project+"/agents", `{"slug": "reviewer", "name": "Code reviewer", "harness": "codex", `+
		`"instructions": "Be brief.", "approval": "ask", "model": {"provider": "`+provider+`", "name": "a-model"}}`, "/v1/agents/", "agents")
	agent, _ := agents["created"]["id"].(string)
	for how, full := range agents {
		if full["id"] != agent || full["project_id"] != project || !named(full, "reviewer", "Code reviewer") || full["harness"] != "codex" ||
			full["instructions"] != "Be brief." || full["approval"] != "ask" {
			t.Errorf("%s agent %v", how, full)
		}
	}
	// Optional fields that were not given are omitted, not defaulted, except
	// for the name, which is empty.
	for _, body := range []string{`{"harness": "opencode"}`, `{"harness": "opencode", "name": "", "instructions": "", "approval": ""}`} {
		plain := get("/v1/agents/" + s.agent(project, body))
		if name, given := plain["name"]; plain["instructions"] != nil || plain["approval"] != nil || plain["harness"] != "opencode" || !given || name != "" {
			t.Errorf("agent created with %s = %v", body, plain)
		}
	}

	sessions := returned("/v1/agents/"+agent+"/sessions", `{"title": "Fix the build"}`, "/v1/sessions/", "sessions")
	session, _ := sessions["created"]["id"].(string)
	for how, resource := range sessions {
		if resource["id"] != session || resource["agent_id"] != agent || resource["title"] != "Fix the build" || resource["created_at"] == nil {
			t.Errorf("%s session %v", how, resource)
		}
	}
	// A session created without a body has an empty title.
	untitled := get("/v1/sessions/" + s.create("/v1/agents/"+agent+"/sessions", ""))
	if title, given := untitled["title"]; untitled["agent_id"] != agent || !given || title != "" {
		t.Errorf("session created without a body %v", untitled)
	}
}

// A client can change the title of a session, and empty it.
func TestSessionTitles(t *testing.T) {
	t.Parallel()
	s := install(t).supervisor()
	agent := s.agent(s.project(), `{"harness": "opencode"}`)
	session := "/v1/sessions/" + s.create("/v1/agents/"+agent+"/sessions", `{"title": "Fix the build"}`)
	for _, title := range []string{"Fix the tests", ""} {
		body := fmt.Sprintf(`{"title": %q}`, title)
		var changed, read map[string]any
		if status, _ := s.request("PATCH", session, body, &changed); status != http.StatusOK || changed["title"] != title {
			t.Errorf("PATCH %s %s = %d, title %v, want %q", session, body, status, changed["title"], title)
		}
		if status, _ := s.request("GET", session, "", &read); status != http.StatusOK || read["title"] != title {
			t.Errorf("GET %s after PATCH %s = %d, title %v, want %q", session, body, status, read["title"], title)
		}
	}
}

// A slug is a lowercase DNS label. No two projects have the same slug, and no
// two agents of a project.
func TestSlugs(t *testing.T) {
	t.Parallel()
	s := install(t).supervisor()
	project, other := s.project(), s.project()
	providers := map[string]string{}
	for _, in := range []string{project, other} {
		providers[in] = s.create("/v1/projects/"+in+"/providers", `{"name": "a provider", "url": "https://api.example.com/v1"}`)
	}
	agents := func(project string) string { return "/v1/projects/" + project + "/agents" }
	// agent returns the body of an agent of the project with the slug.
	agent := func(project, slug string) string {
		return fmt.Sprintf(`{"slug": %q, "harness": "opencode", "model": {"provider": %q, "name": "a-model"}}`, slug, providers[project])
	}
	// post returns the status and the error code of the response.
	post := func(path, body string) (int, string) {
		t.Helper()
		var answer struct{ Code string }
		status, _ := s.request("POST", path, body, &answer)
		return status, answer.Code
	}

	const invalid = "invalid_request"
	for _, test := range []struct {
		slug   string
		status int
		code   string
	}{
		{"a", http.StatusCreated, ""},
		{"a-1", http.StatusCreated, ""},
		{"x--y9", http.StatusCreated, ""},
		{"a" + strings.Repeat("b", 62), http.StatusCreated, ""},
		{"", http.StatusBadRequest, invalid},
		{"Demo", http.StatusBadRequest, invalid},
		{"dEMO", http.StatusBadRequest, invalid},
		{"1demo", http.StatusBadRequest, invalid},
		{"-demo", http.StatusBadRequest, invalid},
		{"demo-", http.StatusBadRequest, invalid},
		{"de_mo", http.StatusBadRequest, invalid},
		{"de mo", http.StatusBadRequest, invalid},
		{"de.mo", http.StatusBadRequest, invalid},
		{"démo", http.StatusBadRequest, invalid},
		{"a" + strings.Repeat("b", 63), http.StatusBadRequest, invalid},
	} {
		for what, request := range map[string][2]string{
			"project": {"/v1/projects", fmt.Sprintf(`{"slug": %q}`, test.slug)},
			"agent":   {agents(project), agent(project, test.slug)},
		} {
			if status, code := post(request[0], request[1]); status != test.status || code != test.code {
				t.Errorf("%s with slug %q: got %d %q, want %d %q", what, test.slug, status, code, test.status, test.code)
			}
		}
	}

	for _, test := range []struct {
		what, path, body string
		status           int
		code             string
	}{
		{"project without a slug", "/v1/projects", `{"name": "Demo"}`, http.StatusBadRequest, invalid},
		{"agent without a slug", agents(project), `{"harness": "opencode", "model": {"provider": "` + providers[project] + `", "name": "a-model"}}`,
			http.StatusBadRequest, invalid},
		{"project with a taken slug", "/v1/projects", `{"slug": "a", "name": "Another"}`, http.StatusConflict, "slug_taken"},
		{"agent with a slug taken in its project", agents(project), agent(project, "a"), http.StatusConflict, "slug_taken"},
		{"agent with the slug of another project's agent", agents(other), agent(other, "a"), http.StatusCreated, ""},
	} {
		if status, code := post(test.path, test.body); status != test.status || code != test.code {
			t.Errorf("%s: got %d %q, want %d %q", test.what, status, code, test.status, test.code)
		}
	}
}

// Turns are listed newest first, with pagination.
func TestTurnsOfASession(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session, other := s.session(), s.session()
	for _, text := range []string{"one", "two", "three"} {
		accepted := s.send(session, text)
		s.run(accepted.ID, "answer to "+text)
		s.follow(accepted.ID)
	}
	s.send(other, "another")
	listed := func(session, query string) []turn {
		t.Helper()
		var list struct{ Turns []turn }
		if status, _ := s.request("GET", "/v1/sessions/"+session+"/turns"+query, "", &list); status != http.StatusOK {
			t.Fatalf("list turns: status %d, want 200", status)
		}
		if list.Turns == nil {
			t.Errorf("list turns %q: turns is null, want a list", query)
		}
		return list.Turns
	}

	for query, expected := range map[string][]string{
		"":                  {"three", "two", "one"},
		"?limit=2":          {"three", "two"},
		"?limit=2&before=3": {"two", "one"},
		"?limit=2&before=2": {"one"},
		"?before=1":         {},
	} {
		texts := []string{}
		for _, turn := range listed(session, query) {
			texts = append(texts, turn.Text)
			// Listed turns include their items.
			if said := items(t, turn); turn.State != "completed" || len(said) != 1 || said[0]["text"] != "answer to "+turn.Text {
				t.Errorf("turn %q listed as %s with items %v", turn.Text, turn.State, said)
			}
		}
		if !slices.Equal(texts, expected) {
			t.Errorf("list turns %q = %v, want %v", query, texts, expected)
		}
	}
	// A running turn has an empty list of items, not null.
	if running := listed(other, ""); len(running) != 1 || running[0].Items == nil || len(running[0].Items) != 0 {
		t.Errorf("turns of the other session = %+v, want one with empty items", running)
	}
}

// A client resumes the event stream with Last-Event-ID. Once the event log
// has expired the events, the stream still reports how the turn ended, and
// the turn keeps its items.
func TestEventsAreReadOn(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	accepted := s.send(s.session(), "hello")
	runner := aviary.given(accepted.ID)
	runner.report(started("harness-session"))
	runner.report(said("one "))
	runner.report(said("two "))

	client := s.read(accepted.ID, "")
	if first := client.next(); first.Type != "turn.started" || first.ID == "" {
		t.Errorf("first event = %v, want turn.started with an ID", first)
	}
	second := client.next()
	again := s.read(accepted.ID, second.ID)
	if next := again.next(); next.Type != "message.delta" || !strings.Contains(next.Data, "two ") {
		t.Errorf("event after %v = %v, want the second delta", second, next)
	}
	request, err := http.NewRequest("GET", s.api+"/v1/turns/"+accepted.ID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", "the last one")
	if response, err := http.DefaultClient.Do(request); err != nil || response.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid Last-Event-ID: response %v, %v; want 400", response, err)
	}

	runner.report(finished("end_turn", nil))
	if rest := client.rest(soon); !slices.Equal(types(rest), []string{"message.delta", "turn.completed"}) {
		t.Errorf("remaining events = %v", rest)
	}
	if rest := again.rest(soon); !slices.Equal(types(rest), []string{"turn.completed"}) {
		t.Errorf("remaining events of the resumed stream = %v", rest)
	}

	if err := aviary.log.Expire(context.Background(), uuid.MustParse(accepted.ID), 0); err != nil {
		t.Fatal(err)
	}
	late := s.follow(accepted.ID)
	if len(late) != 1 || late[0].Type != "turn.completed" || !strings.Contains(late[0].Data, "end_turn") {
		t.Errorf("events after expiry = %v, want only turn.completed", late)
	}
	if said := items(t, s.turn(accepted.ID)); len(said) != 1 || said[0]["text"] != "one two " {
		t.Errorf("items after expiry = %v", said)
	}
}
