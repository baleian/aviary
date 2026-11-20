package app

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"testing"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store"
)

// endpoint is how the Supervisor has the proxy route a model request: the
// headers that it sets, and whether it removes the Authorization header that
// carries the session token.
type endpoint struct {
	host, secure, authorization string
	keyless                     bool
}

// named returns the endpoint of an allowed check, or nil if the check leaves
// the request unchanged.
func named(t *testing.T, answer *authv3.CheckResponse) *endpoint {
	t.Helper()
	if code := codes.Code(answer.GetStatus().GetCode()); code != codes.OK {
		t.Fatalf("check denied with %v %v", code, answer.GetDeniedResponse())
	}
	ok := answer.GetOkResponse()
	if ok == nil {
		return nil
	}
	found := &endpoint{keyless: slices.Contains(ok.GetHeadersToRemove(), "authorization")}
	for _, header := range ok.GetHeaders() {
		switch name, value := header.GetHeader().GetKey(), header.GetHeader().GetValue(); name {
		case "x-aviary-upstream":
			found.host = value
		case "x-aviary-upstream-tls":
			found.secure = value
		case "authorization":
			found.authorization = value
		default:
			t.Errorf("unexpected header %s: %s", name, value)
		}
	}
	return found
}

// refusal returns the status and the body of a denied check.
func refusal(answer *authv3.CheckResponse) (int, string) {
	denied := answer.GetDeniedResponse()
	return int(denied.GetStatus().GetCode()), denied.GetBody()
}

// An agent's model is a provider of its project and a model name. The
// harness reaches the model through the Egress Proxy, which gets the
// provider's endpoint and key for model requests only.
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
		t.Errorf("agent model = %+v", read.Model)
	}

	runner := aviary.given(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID)
	// The base URL is the proxy with the path of the provider's URL. The
	// harness gets no API key; its pod sends the session token instead.
	expected := map[string]string{
		"AVIARY_LLM_BASE_URL": "http://127.0.0.2:8080/custom/v2",
		"AVIARY_LLM_MODEL":    "a-model-of-the-provider",
	}
	if told := runner.Environment; !maps.Equal(told, expected) {
		t.Errorf("harness environment = %v, want %v", told, expected)
	}
	token := aviary.token(runner.Sandbox)

	// Any Supervisor can answer the proxy.
	for _, supervisor := range []*supervisor{s, aviary.supervisor()} {
		got := named(t, supervisor.check(token, false, "/custom/v2/chat/completions?stream=true"))
		if got == nil || *got != (endpoint{host: "api.example.com", secure: "true", authorization: "Bearer sk-the-first-key"}) {
			t.Errorf("model request endpoint = %+v", got)
		}
	}
	// A forward proxy request is not rerouted. The metadata names its turn
	// but no provider.
	forward := s.check(token, true, "/custom/v2/chat/completions")
	if got := named(t, forward); got != nil || forward.GetDynamicMetadata().GetFields()["turn"].GetStringValue() != runner.ID ||
		forward.GetDynamicMetadata().GetFields()["provider"] != nil {
		t.Errorf("forward proxy request: endpoint %+v, metadata %v", got, forward.GetDynamicMetadata())
	}
	// For the proxy's access log, a model request names its provider.
	model := s.check(token, false, "/custom/v2/chat/completions")
	if told := model.GetDynamicMetadata().GetFields()["provider"].GetStringValue(); told != provider {
		t.Errorf("provider metadata = %q, want %q", told, provider)
	}

	// Only the path of the provider's URL and paths below it are allowed.
	for path, status := range map[string]int{
		"/custom/v2":                http.StatusOK,
		"/custom/v2/":               http.StatusOK,
		"/custom/v2?query":          http.StatusOK,
		"/custom/v2/models/a/b":     http.StatusOK,
		"/custom/v20/chat":          http.StatusNotFound,
		"/custom":                   http.StatusNotFound,
		"/v1/chat/completions":      http.StatusNotFound,
		"/":                         http.StatusNotFound,
		"/other?/custom/v2/":        http.StatusNotFound,
		"/custom/v2/../../private":  http.StatusNotFound,
		"/custom/v2/%2e%2E/private": http.StatusNotFound,
		"/custom/v2/./chat":         http.StatusNotFound,
		"/custom/v2/%zz":            http.StatusNotFound,
		"/custom/v2/a..b/.c":        http.StatusOK,
	} {
		answer := s.check(token, false, path)
		if code := codes.Code(answer.GetStatus().GetCode()); (code == codes.OK) != (status == http.StatusOK) {
			t.Errorf("model request for %s: code %v", path, code)
			continue
		}
		if status == http.StatusOK {
			continue
		}
		if got, why := refusal(answer); got != status || why != "model requests must be under the path /custom/v2/\n" {
			t.Errorf("model request for %s denied with %d %q", path, got, why)
		}
	}

	// A replaced provider applies from the next request of a running turn.
	replace := func(body string) {
		t.Helper()
		if status, _ := s.request("PUT", "/v1/providers/"+provider, body, nil); status != http.StatusOK {
			t.Fatalf("PUT provider: status %d, want 200", status)
		}
	}
	replace(`{"name": "a provider", "url": "http://model.example.com:8000", "api_key": {"value": "sk-the-second-key"}}`)
	got := named(t, s.check(token, false, "/anything/at/all"))
	if got == nil || *got != (endpoint{host: "model.example.com:8000", secure: "false", authorization: "Bearer sk-the-second-key"}) {
		t.Errorf("endpoint after replacing the provider = %+v", got)
	}
	// A provider without a key gets no Authorization header, not even the
	// session token.
	replace(`{"name": "a provider", "url": "http://model.example.com:8000"}`)
	got = named(t, s.check(token, false, "/v1/chat/completions"))
	if got == nil || *got != (endpoint{host: "model.example.com:8000", secure: "false", keyless: true}) {
		t.Errorf("endpoint of a provider without a key = %+v", got)
	}

	// A key sealed with the keys of another installation does not open. The
	// request is denied, and the reason reveals nothing more.
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
	if status, why := refusal(s.check(token, false, "/v1/chat/completions")); status != http.StatusServiceUnavailable || why != "cannot read the model provider's API key\n" {
		t.Errorf("request with a key that does not open: denied with %d %q", status, why)
	}
	// A URL that cannot be parsed, which the API would refuse.
	kept.URL = "http://[::1"
	if _, err := aviary.records.ReplaceProvider(context.Background(), kept); err != nil {
		t.Fatal(err)
	}
	if status, why := refusal(s.check(token, false, "/v1/chat/completions")); status != http.StatusServiceUnavailable || why != "the model provider's URL is invalid\n" {
		t.Errorf("request with an invalid provider URL: denied with %d %q", status, why)
	}
}

// The sessions of an agent without a model take no messages, and a running
// turn of such an agent is denied model requests.
func TestAgentFromBeforeModels(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	// The API refuses such agents and their messages, so the test writes the
	// agent and its turn to the store.
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
	// Accepting a message makes the Supervisor dispatch the queued turns.
	s.send(s.session(), "hello")
	runner := aviary.given(queued.ID.String())
	if told := runner.Environment; len(told) != 0 {
		t.Errorf("harness environment without a model = %v, want none", told)
	}

	token := aviary.token(runner.Sandbox)
	if status, why := refusal(s.check(token, false, "/v1/chat/completions")); status != http.StatusNotFound || why != "the turn's agent has no model\n" {
		t.Errorf("model request without a model: denied with %d %q", status, why)
	}
	// Forward proxy requests are not affected.
	if got := named(t, s.check(token, true, "/")); got != nil {
		t.Errorf("forward proxy request: endpoint %+v, want nil", got)
	}
	var refused struct{ Code, Message string }
	status, _ := s.request("POST", "/v1/sessions/"+session+"/messages", `{"text": "hello again"}`, &refused)
	if status != http.StatusConflict || refused.Code != "agent_without_model" || refused.Message == "" {
		t.Errorf("send without a model = %d %s %q, want 409 agent_without_model", status, refused.Code, refused.Message)
	}
}

// An agent's model must name a provider of the agent's own project and a
// model name.
func TestModelsThatAnAgentCannotName(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project, other := s.create("/v1/projects", `{"name": "test"}`), s.create("/v1/projects", `{"name": "another"}`)
	provider := s.create("/v1/projects/"+project+"/providers", `{"name": "a provider", "url": "https://api.example.com/v1"}`)
	theirs := s.create("/v1/projects/"+other+"/providers", `{"name": "theirs", "url": "https://api.example.com/v1"}`)
	for name, model := range map[string]string{
		"provider of another project": `{"provider": "` + theirs + `", "name": "a-model"}`,
		"unknown provider":            `{"provider": "` + project + `", "name": "a-model"}`,
		"invalid provider ID":         `{"provider": "a provider", "name": "a-model"}`,
		"no provider":                 `{"name": "a-model"}`,
		"no name":                     `{"provider": "` + provider + `"}`,
		"empty object":                `{}`,
		"unknown field":               `{"provider": "` + provider + `", "name": "a-model", "url": "https://other.example.com"}`,
		"string":                      `"a-model"`,
		"null":                        `null`,
	} {
		var refused struct{ Code, Message string }
		status, _ := s.request("POST", "/v1/projects/"+project+"/agents", `{"harness": "opencode", "model": `+model+`}`, &refused)
		if status != http.StatusBadRequest || refused.Code != "invalid_request" || refused.Message == "" {
			t.Errorf("model %s: got %d %s %q, want 400 invalid_request", name, status, refused.Code, refused.Message)
		}
	}
}
