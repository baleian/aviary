package app

import (
	"context"
	"maps"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store"
)

// An agent names the model that its turns use: a provider of its project and
// a model of that provider. The harness of a turn is told the model and the
// URL of the provider, and holds no key. The egress of its sandbox is given
// the key of the provider, and only for a request to the endpoint.
func TestAgentNamesItsModel(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project := s.create("/v1/projects", `{"name": "test"}`)
	providers, agents := "/v1/projects/"+project+"/providers", "/v1/projects/"+project+"/agents"
	provider := s.create(providers, `{"name": "a provider", "url": "https://api.example.com/custom/v2/", "api_key": {"value": "sk-the-first-key"}}`)
	agent := s.create(agents, `{"harness": "opencode", "model": {"provider": "`+provider+`", "name": "a-model-of-the-provider"}}`)
	var read struct {
		Model struct{ Provider, Name string }
	}
	if status, _ := s.request("GET", "/v1/agents/"+agent, "", &read); status != http.StatusOK || read.Model.Provider != provider || read.Model.Name != "a-model-of-the-provider" {
		t.Errorf("the agent is read with the model %+v", read.Model)
	}

	turn := s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID
	// The harness is started with the model and the URL of the provider, and
	// with a word in place of the API key: the key is not in the sandbox.
	expected := map[string]string{
		"AVIARY_LLM_BASE_URL": "https://api.example.com/custom/v2",
		"AVIARY_LLM_MODEL":    "a-model-of-the-provider",
		"AVIARY_LLM_API_KEY":  "none",
	}
	runner := aviary.given(turn)
	if told := runner.Environment; !maps.Equal(told, expected) {
		t.Errorf("the harness of the turn is started with %v, not %v", told, expected)
	}
	// key returns the key that the Supervisor has the egress put into a
	// request of the turn to the URL, if any.
	key := func(s *supervisor, target string) string {
		t.Helper()
		status, headers := s.resolve(runner.Sandbox, "POST", target)
		if status != http.StatusOK || len(headers) > 1 {
			t.Fatalf("a request to %s was answered with %d and the headers %v", target, status, headers)
		}
		return headers["authorization"]
	}

	// Any Supervisor answers the egress.
	for _, supervisor := range []*supervisor{s, aviary.supervisor()} {
		if got := key(supervisor, "https://api.example.com/custom/v2/chat/completions?stream=true"); got != "Bearer sk-the-first-key" {
			t.Errorf("a model request is sent with %q", got)
		}
	}
	// The record tells which provider a model request went to.
	if record := aviary.record(turn); record["provider"] != provider {
		t.Errorf("a model request is recorded as %v", record)
	}
	// What a sandbox sends elsewhere is recorded with its turn, and with
	// nothing of the provider.
	if got := key(s, "https://elsewhere.example.com/custom/v2/chat/completions"); got != "" {
		t.Errorf("a request to another host is sent with %q", got)
	}
	if record := aviary.record(turn); record["host"] != "elsewhere.example.com" || record["provider"] != nil {
		t.Errorf("a request to another host is recorded as %v", record)
	}

	// The endpoint is the URL of the provider and what is below it.
	for path, endpoint := range map[string]bool{
		"/custom/v2":                true,
		"/custom/v2/":               true,
		"/custom/v2?query":          true,
		"/custom/v2/models/a/b":     true,
		"/custom/v20/chat":          false,
		"/custom":                   false,
		"/v1/chat/completions":      false,
		"/":                         false,
		"/other?/custom/v2/":        false,
		"/custom/v2/../../private":  false,
		"/custom/v2/%2e%2E/private": false,
		"/custom/v2/./chat":         false,
		"/custom/v2/a..b/.c":        true,
	} {
		if got := key(s, "https://api.example.com"+path); (got != "") != endpoint {
			t.Errorf("a request for %s is sent with %q", path, got)
		}
	}

	// A provider is replaced while turns use it: the next request of a turn
	// to the endpoint as it is then gets the key as it is then.
	replace := func(body string) {
		t.Helper()
		if status, _ := s.request("PUT", "/v1/providers/"+provider, body, nil); status != http.StatusOK {
			t.Fatalf("the provider was replaced with %d", status)
		}
	}
	replace(`{"name": "a provider", "url": "https://api.example.com/other/v3", "api_key": {"value": "sk-the-second-key"}}`)
	if got := key(s, "https://api.example.com/other/v3/chat/completions"); got != "Bearer sk-the-second-key" {
		t.Errorf("after the provider was replaced, a model request is sent with %q", got)
	}
	if got := key(s, "https://api.example.com/custom/v2/chat/completions"); got != "" {
		t.Errorf("after the provider was replaced, a request to where its endpoint was is sent with %q", got)
	}
	// An endpoint that takes no key gets none.
	replace(`{"name": "a provider", "url": "https://api.example.com/other/v3"}`)
	if got := key(s, "https://api.example.com/other/v3/chat/completions"); got != "" {
		t.Errorf("for a provider without a key, a model request is sent with %q", got)
	}

	// A key that the Supervisor cannot open, as one that is sealed with keys
	// of another installation: the request is refused, and the reason does
	// not say more than that.
	kept, err := aviary.records.Provider(context.Background(), uuid.MustParse(provider))
	if err != nil {
		t.Fatal(err)
	}
	if kept.APIKey, err = testenv.Keys(t).Seal("sk-the-third-key", store.ProviderKeyPlace(kept.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := aviary.records.ReplaceProvider(context.Background(), kept); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.resolve(runner.Sandbox, "POST", "https://api.example.com/other/v3/chat/completions"); status != http.StatusServiceUnavailable {
		t.Errorf("a model request whose key cannot be opened was answered with %d", status)
	}
	if record := aviary.record(turn); record["msg"] != "request refused" || record["why"] != "the API key of the model provider cannot be read" {
		t.Errorf("a model request whose key cannot be opened is recorded as %v", record)
	}
	// A URL that cannot be read, which the API does not take: no request
	// gets the key.
	kept.URL = "http://[::1"
	if _, err := aviary.records.ReplaceProvider(context.Background(), kept); err != nil {
		t.Fatal(err)
	}
	if got := key(s, "https://api.example.com/other/v3/chat/completions"); got != "" {
		t.Errorf("a request of a turn whose provider has a URL that cannot be read is sent with %q", got)
	}
}

// An agent from before agents named their models has none. Its sessions
// take no more messages, and a turn of it that still runs reaches no model.
func TestAgentFromBeforeModels(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	// The API creates no such agent and takes no message for one, so the
	// agent and its turn are recorded as an older release left them.
	ctx := context.Background()
	agent, err := aviary.records.CreateAgent(ctx, uuid.MustParse(s.create("/v1/projects", `{"name": "test"}`)), store.Agent{Harness: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	session := s.create("/v1/agents/"+agent.ID.String()+"/sessions", "")
	queued, _, err := aviary.records.CreateTurn(ctx, uuid.MustParse(session), uuid.Nil, "hello", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	// A Supervisor dispatches the turns that wait when it takes a message.
	s.send(s.session(), "hello")
	turn := queued.ID.String()
	runner := aviary.given(turn)
	if told := runner.Environment; len(told) != 0 {
		t.Errorf("the harness of a turn whose agent names no model is started with %v", told)
	}

	// What the turn sends out goes on as before, and is recorded, but no
	// request of it gets a key.
	if status, headers := s.resolve(runner.Sandbox, "POST", "https://api.example.com/v1/chat/completions"); status != http.StatusOK || len(headers) != 0 {
		t.Errorf("a model request of a turn whose agent names no model was answered with %d and the headers %v", status, headers)
	}
	if record := aviary.record(turn); record["msg"] != "request" || record["provider"] != nil {
		t.Errorf("a request of a turn whose agent names no model is recorded as %v", record)
	}
	var refused struct{ Code, Message string }
	status, _ := s.request("POST", "/v1/sessions/"+session+"/messages", `{"text": "hello again"}`, &refused)
	if status != http.StatusConflict || refused.Code != "agent_without_model" || refused.Message == "" {
		t.Errorf("a message to a session of an agent that names no model was answered with %d %s %q", status, refused.Code, refused.Message)
	}
}

// The model of an agent is a provider of the agent's own project and a name.
func TestModelsThatAnAgentCannotName(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project, other := s.create("/v1/projects", `{"name": "test"}`), s.create("/v1/projects", `{"name": "another"}`)
	provider := s.create("/v1/projects/"+project+"/providers", `{"name": "a provider", "url": "https://api.example.com/v1"}`)
	theirs := s.create("/v1/projects/"+other+"/providers", `{"name": "theirs", "url": "https://api.example.com/v1"}`)
	for name, model := range map[string]string{
		"the provider of another project": `{"provider": "` + theirs + `", "name": "a-model"}`,
		"a provider that does not exist":  `{"provider": "` + project + `", "name": "a-model"}`,
		"a provider that is no ID":        `{"provider": "a provider", "name": "a-model"}`,
		"no provider":                     `{"name": "a-model"}`,
		"no name":                         `{"provider": "` + provider + `"}`,
		"nothing":                         `{}`,
		"more than a model":               `{"provider": "` + provider + `", "name": "a-model", "url": "https://other.example.com"}`,
		"only a name":                     `"a-model"`,
		"a model that is none":            `null`,
	} {
		var refused struct{ Code, Message string }
		status, _ := s.request("POST", "/v1/projects/"+project+"/agents", `{"harness": "opencode", "model": `+model+`}`, &refused)
		if status != http.StatusBadRequest || refused.Code != "invalid_request" || refused.Message == "" {
			t.Errorf("an agent that names %s was answered with %d %s %q", name, status, refused.Code, refused.Message)
		}
	}
}
