package app

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store"
)

// endpoint is how the Supervisor has the proxy route a model request: the
// headers that it sets, and whether it removes the Authorization header that
// carries the session token. headers are those of the provider, each as a
// line "name: value".
type endpoint struct {
	host, secure, authorization, headers string
	keyless                              bool
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
		name, value := header.GetHeader().GetKey(), header.GetHeader().GetValue()
		// The pod cannot set a header in place of the Supervisor.
		if header.GetAppendAction() != corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD {
			t.Errorf("header %s is set with %v, which keeps a header of the request", name, header.GetAppendAction())
		}
		switch name {
		case "x-aviary-upstream":
			found.host = value
		case "x-aviary-upstream-tls":
			found.secure = value
		case "authorization":
			found.authorization = value
		default:
			found.headers += name + ": " + value + "\n"
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
	project := s.project()
	provider := s.create("/v1/projects/"+project+"/providers",
		`{"name": "a provider", "url": "https://api.example.com/custom/v2/", "api_key": {"value": "sk-the-first-key"}}`)
	agent := s.agent(project, `{"harness": "opencode", "model": {"provider": "`+provider+`", "name": "a-model-of-the-provider"}}`)
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

// An endpoint may need headers besides its key, as a gateway does that
// routes by one. The Egress Proxy gets the headers of the turn's provider for
// model requests, and sets them in place of what the pod sends.
func TestModelRequestsCarryTheHeadersOfTheirProvider(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project := s.project()
	const gateway = `{"name": "a gateway", "url": "https://gateway.example.com/v1"`
	provider := s.create("/v1/projects/"+project+"/providers", gateway+`, "api_key": {"value": "sk-the-first-key"},
		"headers": [{"name": "X-Gateway-Route", "value": "team-a"}, {"name": "accept-language", "value": "en"}]}`)
	agent := s.agent(project, `{"harness": "opencode", "model": {"provider": "`+provider+`", "name": "a-model"}}`)
	runner := aviary.given(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID)
	token := aviary.token(runner.Sandbox)

	// Any Supervisor can answer the proxy.
	for _, supervisor := range []*supervisor{s, aviary.supervisor()} {
		got := named(t, supervisor.check(token, false, "/v1/chat/completions"))
		if got == nil || *got != (endpoint{
			host: "gateway.example.com", secure: "true", authorization: "Bearer sk-the-first-key",
			headers: "x-gateway-route: team-a\naccept-language: en\n",
		}) {
			t.Errorf("model request endpoint = %+v", got)
		}
	}
	// A forward proxy request goes where the pod sends it, without them.
	if got := named(t, s.check(token, true, "/v1/chat/completions")); got != nil {
		t.Errorf("forward proxy request endpoint = %+v, want none", got)
	}

	// Replaced headers apply from the next request of a running turn, and a
	// provider without a key has its headers all the same.
	if status, _ := s.request("PUT", "/v1/providers/"+provider, gateway+`, "headers": [{"name": "x-gateway-route", "value": "team-b"}]}`, nil); status != http.StatusOK {
		t.Fatalf("PUT provider: status %d, want 200", status)
	}
	got := named(t, s.check(token, false, "/v1/chat/completions"))
	if got == nil || *got != (endpoint{host: "gateway.example.com", secure: "true", keyless: true, headers: "x-gateway-route: team-b\n"}) {
		t.Errorf("endpoint after replacing the provider = %+v", got)
	}
}

// The secret of a header is read when a model request needs it, as a key is:
// a stored one is opened, and one in a secret store is read from the store.
// A secret that cannot be read refuses the request with the reason.
func TestModelRequestsCarryTheSecretsOfTheirHeaders(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project := s.project()
	const address = "https://" + storeHost
	kept := s.create("/v1/projects/"+project+"/secret-stores", scriptedStore("a store", address, "a-credential", "a-credential"))
	aviary.secrets.put(address, "team/gateway", "token", "t0ken\n")
	provider := s.create("/v1/projects/"+project+"/providers", `{"name": "a gateway", "url": "https://gateway.example.com/v1",
		"headers": [{"name": "x-token", "secret": {"value": "s3cret"}}, {"name": "x-team-token", "secret": `+in(kept, "team/gateway", "token")+`}]}`)
	agent := s.agent(project, `{"harness": "opencode", "model": {"provider": "`+provider+`", "name": "a-model"}}`)
	token := aviary.token(aviary.given(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID).Sandbox)

	// A secret that a store holds goes without the line break at its end.
	got := named(t, s.check(token, false, "/v1/chat/completions"))
	if got == nil || *got != (endpoint{host: "gateway.example.com", secure: "true", keyless: true, headers: "x-token: s3cret\nx-team-token: t0ken\n"}) {
		t.Errorf("model request endpoint = %+v", got)
	}
	if got := named(t, s.check(token, true, "/v1/chat/completions")); got != nil {
		t.Errorf("forward proxy request endpoint = %+v, want none", got)
	}

	// A Supervisor that has not read the secret reads it when it is needed.
	for value, why := range map[string]string{
		"t0ken\nx-other: a": "the secret of the model provider's header x-team-token in its secret store is not the value of an HTTP header\n",
		"":                  "cannot read the model provider's header x-team-token from its secret store: the store has no value at the path and under the key\n",
	} {
		aviary.secrets.put(address, "team/gateway", "token", value)
		if status, said := refusal(aviary.supervisor().check(token, false, "/v1/chat/completions")); status != http.StatusServiceUnavailable || said != why {
			t.Errorf("request with the secret %q in the store: denied with %d %q", value, status, said)
		}
	}

	// A secret sealed with the keys of another installation does not open.
	record, err := aviary.records.Provider(context.Background(), uuid.MustParse(provider))
	if err != nil {
		t.Fatal(err)
	}
	if record.Headers[0].Sealed, err = testenv.Keys(t).Seal("s3cret", store.ProviderHeaderPlace(record.ID, "x-token")); err != nil {
		t.Fatal(err)
	}
	if _, err := aviary.records.ReplaceProvider(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if status, why := refusal(s.check(token, false, "/v1/chat/completions")); status != http.StatusServiceUnavailable || why != "cannot read the model provider's header x-token\n" {
		t.Errorf("request with a secret that does not open: denied with %d %q", status, why)
	}
}

// To the proxy, the local host of a provider's URL is the proxy itself. An
// installation can name the host that stands in for it: the proxy then
// connects there, on the port of the URL.
func TestAProviderAtTheLocalHostIsReachedWhereTheInstallationSays(t *testing.T) {
	t.Parallel()
	for _, installed := range []struct {
		localhost string
		// upstreams are the hosts that the proxy connects to, by the URL of
		// the provider.
		upstreams map[string]string
	}{
		{"", map[string]string{
			"http://localhost:11434/v1": "localhost:11434",
			"http://127.0.0.1/v1":       "127.0.0.1",
			"http://[::1]:8000/v1":      "[::1]:8000",
		}},
		{"host.example.com", map[string]string{
			"http://localhost:11434/v1":        "host.example.com:11434",
			"http://LocalHost/v1":              "host.example.com",
			"http://127.0.0.1:8000/v1":         "host.example.com:8000",
			"http://127.1.2.3/v1":              "host.example.com",
			"https://[::1]:8443/v1":            "host.example.com:8443",
			"http://localhost.example.com/v1":  "localhost.example.com",
			"http://10.0.0.5:8000/v1":          "10.0.0.5:8000",
			"https://api.example.com/v1":       "api.example.com",
			"http://model.example.com:8000/v1": "model.example.com:8000",
		}},
		{"2001:db8::254", map[string]string{
			"http://localhost:11434/v1": "[2001:db8::254]:11434",
			"http://127.0.0.1/v1":       "[2001:db8::254]",
		}},
	} {
		aviary := install(t)
		aviary.localhost = installed.localhost
		s := aviary.supervisor()
		project := s.project()
		for address, upstream := range installed.upstreams {
			provider := s.create("/v1/projects/"+project+"/providers", `{"name": "`+unique("provider")+`", "url": "`+address+`"}`)
			agent := s.agent(project, `{"harness": "opencode", "model": {"provider": "`+provider+`", "name": "a-model"}}`)
			runner := aviary.given(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID)
			got := named(t, s.check(aviary.token(runner.Sandbox), false, "/v1/chat/completions"))
			secure := strconv.FormatBool(strings.HasPrefix(address, "https://"))
			if got == nil || *got != (endpoint{host: upstream, secure: secure, keyless: true}) {
				t.Errorf("with %q for the local host, the endpoint of a provider at %s = %+v, want the host %s", installed.localhost, address, got, upstream)
			}
		}
	}
}

// An agent's model must name a provider of the agent's own project and a
// model name.
func TestModelsThatAnAgentCannotName(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project, other := s.project(), s.project()
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
		status, _ := s.request("POST", "/v1/projects/"+project+"/agents", `{"slug": "an-agent", "harness": "opencode", "model": `+model+`}`, &refused)
		if status != http.StatusBadRequest || refused.Code != "invalid_request" || refused.Message == "" {
			t.Errorf("model %s: got %d %s %q, want 400 invalid_request", name, status, refused.Code, refused.Message)
		}
	}
}
