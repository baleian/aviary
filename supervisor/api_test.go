package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"

	"aviary/supervisor/internal/testenv"
)

// What the API cannot take, it answers with a status and a code that tell
// the client why.
func TestRefusals(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project := s.create("/v1/projects", `{"name": "test"}`)
	agents := "/v1/projects/" + project + "/agents"
	session := s.create("/v1/agents/"+s.create(agents, `{"harness": "opencode"}`)+"/sessions", "")
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

		{"POST", agents, `{"harness": "another"}`, http.StatusBadRequest, invalid},
		{"POST", agents, `{"harness": "opencode", "resources": {"limits": {"memory": "-1Gi"}}}`, http.StatusBadRequest, invalid},
		{"POST", agents, `{"harness": "opencode", "resources": {"limits": {"nvidia.com/gpu": "1"}}}`, http.StatusBadRequest, invalid},
		{"POST", agents, `{"harness": "opencode", "approval": "always"}`, http.StatusBadRequest, invalid},
		{"POST", "/v1/projects/" + none + "/agents", `{"harness": "opencode"}`, http.StatusNotFound, notFound},
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
	// get returns a resource as the API has it.
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

	// An agent has what it was created with. What was not given is not
	// filled in: an agent without resources follows those of its harness.
	agents := "/v1/projects/" + project + "/agents"
	full := get("/v1/agents/" + s.create(agents, `{"harness": "codex", "instructions": "Be brief.", "approval": "ask",
		"resources": {"requests": {"cpu": "500m"}, "limits": {"memory": "2Gi"}}}`))
	resources, err := json.Marshal(full["resources"])
	if err != nil {
		t.Fatal(err)
	}
	if full["project_id"] != project || full["harness"] != "codex" || full["instructions"] != "Be brief." ||
		full["approval"] != "ask" || string(resources) != `{"limits":{"memory":"2Gi"},"requests":{"cpu":"500m"}}` {
		t.Errorf("the agent is %v", full)
	}
	for _, body := range []string{`{"harness": "opencode"}`, `{"harness": "opencode", "instructions": "", "resources": {}, "approval": ""}`} {
		plain := get("/v1/agents/" + s.create(agents, body))
		if _, found := plain["resources"]; found || plain["instructions"] != nil || plain["approval"] != nil || plain["harness"] != "opencode" {
			t.Errorf("the agent that was created with %s is %v", body, plain)
		}
	}

	agent := full["id"].(string)
	if session := get("/v1/sessions/" + s.create("/v1/agents/"+agent+"/sessions", "")); session["agent_id"] != agent {
		t.Errorf("the session is %v", session)
	}
}

// The conversation of a session can be read back, newest turn first and a
// few turns at a time.
func TestTurnsOfASession(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session, other := s.session(), s.session()
	for _, text := range []string{"one", "two", "three"} {
		accepted := s.send(session, text)
		s.run(aviary.job(accepted.ID), "answer to "+text)
		s.follow(accepted.ID)
	}
	s.send(other, "another")
	// listed returns the turns that a request for the turns of the session
	// is answered with.
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
			// A turn is listed with what the agent said in it.
			if said := items(t, turn); turn.State != "completed" || len(said) != 1 || said[0]["text"] != "answer to "+turn.Text {
				t.Errorf("the turn %q is listed as %s with the items %v", turn.Text, turn.State, said)
			}
		}
		if !slices.Equal(texts, expected) {
			t.Errorf("the turns of the session, asked for with %q, are %v, not %v", query, texts, expected)
		}
	}
	// A turn that has not ended has no items yet, which is an empty list.
	if running := listed(other, ""); len(running) != 1 || running[0].Items == nil || len(running[0].Items) != 0 {
		t.Errorf("the turns of the other session are %+v", running)
	}
}

// A client reads the events of a turn as they come, and reads on after the
// last one that it has when its connection broke. Valkey keeps the events
// only for a while. The record of the turn does not need them.
func TestEventsAreReadOn(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	accepted := s.send(s.session(), "hello")
	job := aviary.job(accepted.ID)
	runner := s.take(job)
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
	runner.leave()
	aviary.end(job, batchv1.JobComplete, nil)
	if rest := client.rest(soon); !slices.Equal(types(rest), []string{"message.delta", "turn.completed"}) {
		t.Errorf("the client read the events %v to the end of the turn", rest)
	}
	if rest := again.rest(soon); !slices.Equal(types(rest), []string{"turn.completed"}) {
		t.Errorf("the client that read on read the events %v to the end of the turn", rest)
	}

	// The events are gone. A client still learns how the turn ended, and the
	// record has what the agent said.
	valkey := testenv.Valkey(t)
	if err := valkey.Do(context.Background(), valkey.B().Del().Key("turn:"+accepted.ID).Build()).Error(); err != nil {
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
