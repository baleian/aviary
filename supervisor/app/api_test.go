package app

import (
	"context"
	"encoding/json"
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
	project := s.create("/v1/projects", `{"name": "test"}`)
	agents := "/v1/projects/" + project + "/agents"
	session := s.create("/v1/agents/"+s.agent(project, `{"harness": "opencode"}`)+"/sessions", "")
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
		{"POST", "/v1/projects", `{"name": "test", "owner": "someone"}`, http.StatusBadRequest, invalid},
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

		{"POST", agents, `{"harness": "another", ` + model + `}`, http.StatusBadRequest, invalid},
		{"POST", agents, `{"harness": "opencode", "resources": {"limits": {"memory": "2Gi"}}, ` + model + `}`, http.StatusBadRequest, invalid},
		{"POST", agents, `{"harness": "opencode", "approval": "always", ` + model + `}`, http.StatusBadRequest, invalid},
		{"POST", agents, `{"harness": "opencode"}`, http.StatusBadRequest, invalid},
		{"POST", "/v1/projects/" + none + "/agents", `{"harness": "opencode", ` + model + `}`, http.StatusNotFound, notFound},
		{"GET", "/v1/agents/" + none, ``, http.StatusNotFound, notFound},

		{"POST", "/v1/agents/" + none + "/sessions", ``, http.StatusNotFound, notFound},
		{"GET", "/v1/sessions/" + none, ``, http.StatusNotFound, notFound},

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
			t.Errorf("%s %s %.60s was answered with %d %s %q, not with %d %s",
				test.method, test.path, test.body, status, refusal.Code, refusal.Message, test.status, test.code)
		}
	}
	if status, _ := s.request("GET", "/healthz", "", nil); status != http.StatusOK {
		t.Errorf("the Supervisor answered whether it is up with %d", status)
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
			t.Fatalf("GET %s was answered with %d", path, status)
		}
		return resource
	}

	project := s.create("/v1/projects", `{"name": "a project"}`)
	if created := get("/v1/projects/" + project); created["id"] != project || created["name"] != "a project" || created["created_at"] == nil {
		t.Errorf("the project is %v", created)
	}

	// Optional fields that were not given are omitted, not defaulted.
	full := get("/v1/agents/" + s.agent(project, `{"harness": "codex", "instructions": "Be brief.", "approval": "ask"}`))
	if full["project_id"] != project || full["harness"] != "codex" || full["instructions"] != "Be brief." || full["approval"] != "ask" {
		t.Errorf("the agent is %v", full)
	}
	for _, body := range []string{`{"harness": "opencode"}`, `{"harness": "opencode", "instructions": "", "approval": ""}`} {
		plain := get("/v1/agents/" + s.agent(project, body))
		if plain["instructions"] != nil || plain["approval"] != nil || plain["harness"] != "opencode" {
			t.Errorf("the agent that was created with %s is %v", body, plain)
		}
	}

	agent := full["id"].(string)
	if session := get("/v1/sessions/" + s.create("/v1/agents/"+agent+"/sessions", "")); session["agent_id"] != agent {
		t.Errorf("the session is %v", session)
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
			t.Fatalf("the turns of the session were answered with %d", status)
		}
		if list.Turns == nil {
			t.Errorf("the turns of the session, asked for with %q, are no list", query)
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
				t.Errorf("the turn %q is listed as %s with the items %v", turn.Text, turn.State, said)
			}
		}
		if !slices.Equal(texts, expected) {
			t.Errorf("the turns of the session, asked for with %q, are %v, not %v", query, texts, expected)
		}
	}
	// A running turn has an empty list of items, not null.
	if running := listed(other, ""); len(running) != 1 || running[0].Items == nil || len(running[0].Items) != 0 {
		t.Errorf("the turns of the other session are %+v", running)
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
		t.Errorf("the first event of the turn is %v", first)
	}
	second := client.next()
	again := s.read(accepted.ID, second.ID)
	if next := again.next(); next.Type != "message.delta" || !strings.Contains(next.Data, "two ") {
		t.Errorf("a client that read on after %v got %v", second, next)
	}
	request, err := http.NewRequest("GET", s.api+"/v1/turns/"+accepted.ID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", "the last one")
	if response, err := http.DefaultClient.Do(request); err != nil || response.StatusCode != http.StatusBadRequest {
		t.Errorf("a client that read on after something that is no event was answered with %v: %v", response, err)
	}

	runner.report(finished("end_turn", nil))
	if rest := client.rest(soon); !slices.Equal(types(rest), []string{"message.delta", "turn.completed"}) {
		t.Errorf("the client read the events %v to the end of the turn", rest)
	}
	if rest := again.rest(soon); !slices.Equal(types(rest), []string{"turn.completed"}) {
		t.Errorf("the client that read on read the events %v to the end of the turn", rest)
	}

	if err := aviary.log.Expire(context.Background(), uuid.MustParse(accepted.ID), 0); err != nil {
		t.Fatal(err)
	}
	late := s.follow(accepted.ID)
	if len(late) != 1 || late[0].Type != "turn.completed" || !strings.Contains(late[0].Data, "end_turn") {
		t.Errorf("a client that came after the events were gone read %v", late)
	}
	if said := items(t, s.turn(accepted.ID)); len(said) != 1 || said[0]["text"] != "one two " {
		t.Errorf("the record of the turn whose events are gone has the items %v", said)
	}
}
