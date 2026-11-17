package main

import (
	"context"
	"net/http"
	"slices"
	"testing"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store"
)

// endpoint is what the Supervisor tells the proxy to make of a model
// request: the headers that it sets, and whether it takes the one with the
// turn token away.
type endpoint struct {
	host, secure, authorization string
	keyless                     bool
}

// named returns the endpoint that the answer names, or nothing if it names
// none: the proxy then passes the request on as it always did.
func named(t *testing.T, answer *authv3.CheckResponse) *endpoint {
	t.Helper()
	if code := codes.Code(answer.GetStatus().GetCode()); code != codes.OK {
		t.Fatalf("the request was refused with %v %v", code, answer.GetDeniedResponse())
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
			t.Errorf("the answer sets the header %s: %s", name, value)
		}
	}
	return found
}

// refusal returns the status and the reason with which the Supervisor has
// the proxy refuse a request.
func refusal(answer *authv3.CheckResponse) (int, string) {
	denied := answer.GetDeniedResponse()
	return int(denied.GetStatus().GetCode()), denied.GetBody()
}

// An agent names the model that its turns use: a provider of its project and
// a model of that provider. The pod of a turn is told the model and reaches
// it at the Egress Proxy. The proxy is told the endpoint of the provider and
// its key, and only for a model request.
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

	job := aviary.job(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID)
	// The harness reaches the proxy, under the path of the provider's URL.
	if base, model := told(job, "AVIARY_LLM_BASE_URL"), told(job, "AVIARY_LLM_MODEL"); base != "http://127.0.0.2:8080/custom/v2" || model != "a-model-of-the-provider" {
		t.Errorf("the pod of the turn is told the model %q at %q", model, base)
	}
	if told(job, "AVIARY_LLM_API_KEY") != token(job) {
		t.Error("the pod of the turn holds something else than its token in place of the API key")
	}
	s.take(job)

	// Any Supervisor answers the proxy.
	for _, supervisor := range []*supervisor{s, aviary.supervisor()} {
		got := named(t, supervisor.check(token(job), false, "/custom/v2/chat/completions?stream=true"))
		if got == nil || *got != (endpoint{host: "api.example.com", secure: "true", authorization: "Bearer sk-the-first-key"}) {
			t.Errorf("the proxy is told to pass a model request on to %+v", got)
		}
	}
	// What a pod sends through the forward proxy goes where the pod says: the
	// proxy is told whose request it is, and nothing of the provider.
	forward := s.check(token(job), true, "/custom/v2/chat/completions")
	if got := named(t, forward); got != nil || forward.GetDynamicMetadata().GetFields()["turn"].GetStringValue() == "" {
		t.Errorf("about a request through the forward proxy, the proxy is told %+v and %v", got, forward.GetDynamicMetadata())
	}

	// The endpoint is the URL of the provider and what is below it.
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
		answer := s.check(token(job), false, path)
		if code := codes.Code(answer.GetStatus().GetCode()); (code == codes.OK) != (status == http.StatusOK) {
			t.Errorf("a model request for %s was answered with %v", path, code)
			continue
		}
		if status == http.StatusOK {
			continue
		}
		if got, why := refusal(answer); got != status || why != "the model endpoint of the turn is at the path /custom/v2/\n" {
			t.Errorf("a model request for %s is refused with %d %q", path, got, why)
		}
	}

	// A provider is replaced while turns use it: the next request of a turn
	// goes to the endpoint as it is then, with the key as it is then.
	replace := func(body string) {
		t.Helper()
		if status, _ := s.request("PUT", "/v1/providers/"+provider, body, nil); status != http.StatusOK {
			t.Fatalf("the provider was replaced with %d", status)
		}
	}
	replace(`{"name": "a provider", "url": "http://model.example.com:8000", "api_key": {"value": "sk-the-second-key"}}`)
	got := named(t, s.check(token(job), false, "/anything/at/all"))
	if got == nil || *got != (endpoint{host: "model.example.com:8000", secure: "false", authorization: "Bearer sk-the-second-key"}) {
		t.Errorf("after the provider was replaced, the proxy is told to pass a model request on to %+v", got)
	}
	// An endpoint that takes no key gets none, and not the token of the turn.
	replace(`{"name": "a provider", "url": "http://model.example.com:8000"}`)
	got = named(t, s.check(token(job), false, "/v1/chat/completions"))
	if got == nil || *got != (endpoint{host: "model.example.com:8000", secure: "false", keyless: true}) {
		t.Errorf("for a provider without a key, the proxy is told to pass a model request on to %+v", got)
	}

	// A key that the Supervisor cannot open: the request is refused, and the
	// reason does not say more than that. A Supervisor of an installation
	// with other keys cannot.
	replace(`{"name": "a provider", "url": "http://model.example.com:8000", "api_key": {"value": "sk-the-third-key"}}`)
	stranger := *aviary
	stranger.keys = testenv.Keys(t)
	if status, why := refusal(stranger.supervisor().check(token(job), false, "/v1/chat/completions")); status != http.StatusServiceUnavailable || why != "the API key of the model provider cannot be read\n" {
		t.Errorf("a model request whose key cannot be opened is refused with %d %q", status, why)
	}
	// A URL that cannot be read, which the API does not take.
	kept, err := aviary.records.Provider(context.Background(), uuid.MustParse(provider))
	if err != nil {
		t.Fatal(err)
	}
	kept.URL = "http://[::1"
	if _, err := aviary.records.ReplaceProvider(context.Background(), kept); err != nil {
		t.Fatal(err)
	}
	if status, why := refusal(s.check(token(job), false, "/v1/chat/completions")); status != http.StatusServiceUnavailable || why != "the URL of the model provider cannot be read\n" {
		t.Errorf("a model request to a provider whose URL cannot be read is refused with %d %q", status, why)
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
	queued, _, err := aviary.records.CreateTurn(ctx, uuid.MustParse(session), "hello", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	// A Supervisor dispatches the turns that wait when it takes a message.
	s.send(s.session(), "hello")
	job := aviary.job(queued.ID.String())
	s.take(job)

	if status, why := refusal(s.check(token(job), false, "/v1/chat/completions")); status != http.StatusNotFound || why != "the agent of the turn names no model\n" {
		t.Errorf("a model request of a turn whose agent names no model is refused with %d %q", status, why)
	}
	// What else the turn sends out goes on as before.
	if got := named(t, s.check(token(job), true, "/")); got != nil {
		t.Errorf("about a request through the forward proxy, the proxy is told %+v", got)
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
