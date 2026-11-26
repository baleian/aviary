package app

import (
	"context"
	"maps"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"aviary/supervisor/internal/credentials"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store"
)

// next ends the runner's turn and sends the session another message, and
// returns the runner of its turn.
func (r *runner) next(s *supervisor, session string) *runner {
	r.installation.t.Helper()
	r.report(started("harness-session"))
	r.report(finished("end_turn", nil))
	return r.installation.given(s.send(session, "again").ID)
}

// An agent's model is a provider of its project and a model name. The
// harness reaches the provider at its URL, and the egress gateway puts the
// provider's key into the requests of the turn's sandbox to the provider.
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

	session := s.create("/v1/agents/"+agent+"/sessions", "")
	runner := aviary.given(s.send(session, "hello").ID)
	// The harness gets a placeholder in place of the key.
	expected := map[string]string{
		"AVIARY_LLM_BASE_URL": "https://api.example.com/custom/v2",
		"AVIARY_LLM_MODEL":    "a-model-of-the-provider",
		"AVIARY_LLM_API_KEY":  credentials.Placeholder,
		"AVIARY_LLM_HEADERS":  "{}",
	}
	if told := runner.Environment; !maps.Equal(told, expected) {
		t.Errorf("harness environment = %v, want %v", told, expected)
	}
	key := credentials.ProviderKey(uuid.MustParse(provider))
	keyed := func(host sandbox.Host) sandbox.Egress {
		return sandbox.Egress{Secrets: []sandbox.Secret{{Host: host, Header: "authorization", Prefix: "Bearer ", URI: key}}}
	}
	if egress := aviary.made(runner.Sandbox).Egress; !reflect.DeepEqual(egress, keyed(sandbox.Host{TLS: true, Name: "api.example.com", Port: 443})) {
		t.Errorf("egress = %+v, want the key for the provider's host", egress)
	}
	// Any Supervisor gives the gateway the key.
	for _, supervisor := range []*supervisor{s, aviary.supervisor()} {
		if got, code, why := supervisor.fetch(runner.Sandbox, key); code != codes.OK || got != "sk-the-first-key" {
			t.Errorf("the key of the provider = %q, refused with %v %q", got, code, why)
		}
	}

	// A replaced key goes into the next request, and a replaced URL is that
	// of the next turn.
	replace := func(body string) {
		t.Helper()
		if status, _ := s.request("PUT", "/v1/providers/"+provider, body, nil); status != http.StatusOK {
			t.Fatalf("PUT provider: status %d, want 200", status)
		}
	}
	replace(`{"name": "a provider", "url": "http://model.example.com:8000", "api_key": {"value": "sk-the-second-key"}}`)
	if got, code, why := s.fetch(runner.Sandbox, key); code != codes.OK || got != "sk-the-second-key" {
		t.Errorf("the key of the replaced provider = %q, refused with %v %q", got, code, why)
	}
	// A key sealed with the keys of another installation does not open. The
	// gateway is refused, and the reason reveals nothing more.
	kept, err := aviary.records.Provider(context.Background(), uuid.MustParse(provider))
	if err != nil {
		t.Fatal(err)
	}
	sealed := kept
	if sealed.APIKey, err = testenv.Keys(t).Seal("sk-the-third-key", store.ProviderKeyPlace(kept.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := aviary.records.ReplaceProvider(context.Background(), sealed); err != nil {
		t.Fatal(err)
	}
	if _, code, why := s.fetch(runner.Sandbox, key); code != codes.Unavailable || why != "the secret cannot be read: cannot decrypt the model provider's API key" {
		t.Errorf("a key that does not open: refused with %v %q", code, why)
	}
	if _, err := aviary.records.ReplaceProvider(context.Background(), kept); err != nil {
		t.Fatal(err)
	}

	runner = runner.next(s, session)
	model := sandbox.Host{Name: "model.example.com", Port: 8000}
	if address := runner.Environment["AVIARY_LLM_BASE_URL"]; address != "http://model.example.com:8000" {
		t.Errorf("AVIARY_LLM_BASE_URL of the next turn = %s", address)
	}
	if egress := aviary.made(runner.Sandbox).Egress; !reflect.DeepEqual(egress, keyed(model)) {
		t.Errorf("egress of the next turn = %+v, want the key for the provider's new host", egress)
	}
	// A provider without a key is reached as it is.
	replace(`{"name": "a provider", "url": "http://model.example.com:8000"}`)
	if _, code, why := s.fetch(runner.Sandbox, key); code != codes.NotFound || why != "the model provider has no API key" {
		t.Errorf("the key of a provider without one: refused with %v %q", code, why)
	}
	runner = runner.next(s, session)
	if egress := aviary.made(runner.Sandbox).Egress; !reflect.DeepEqual(egress, sandbox.Egress{Hosts: []sandbox.Host{model}}) {
		t.Errorf("egress for a provider without a key = %+v", egress)
	}
}

// An endpoint may need headers besides its key, as a gateway does that
// routes by one. The harness sends them with its model requests.
func TestModelRequestsCarryTheHeadersOfTheirProvider(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project := s.project()
	const gateway = `{"name": "a gateway", "url": "https://gateway.example.com/v1"`
	provider := s.create("/v1/projects/"+project+"/providers", gateway+`, "api_key": {"value": "sk-the-first-key"},
		"headers": [{"name": "X-Gateway-Route", "value": "team-a"}, {"name": "accept-language", "value": "en"}]}`)
	agent := s.agent(project, `{"harness": "opencode", "model": {"provider": "`+provider+`", "name": "a-model"}}`)
	session := s.create("/v1/agents/"+agent+"/sessions", "")
	runner := aviary.given(s.send(session, "hello").ID)
	if headers := runner.Environment["AVIARY_LLM_HEADERS"]; headers != `{"accept-language":"en","x-gateway-route":"team-a"}` {
		t.Errorf("AVIARY_LLM_HEADERS = %s", headers)
	}
	host := sandbox.Host{TLS: true, Name: "gateway.example.com", Port: 443}
	key := sandbox.Secret{Host: host, Header: "authorization", Prefix: "Bearer ", URI: credentials.ProviderKey(uuid.MustParse(provider))}
	if egress := aviary.made(runner.Sandbox).Egress; !reflect.DeepEqual(egress, sandbox.Egress{Secrets: []sandbox.Secret{key}}) {
		t.Errorf("egress = %+v, want only the key", egress)
	}

	// Replaced headers are those of the next turn, and a provider without a
	// key has its headers all the same.
	if status, _ := s.request("PUT", "/v1/providers/"+provider, gateway+`, "headers": [{"name": "x-gateway-route", "value": "team-b"}]}`, nil); status != http.StatusOK {
		t.Fatalf("PUT provider: status %d, want 200", status)
	}
	runner = runner.next(s, session)
	if headers := runner.Environment["AVIARY_LLM_HEADERS"]; headers != `{"x-gateway-route":"team-b"}` {
		t.Errorf("AVIARY_LLM_HEADERS after replacing the provider = %s", headers)
	}
	if egress := aviary.made(runner.Sandbox).Egress; !reflect.DeepEqual(egress, sandbox.Egress{Hosts: []sandbox.Host{host}}) {
		t.Errorf("egress after replacing the provider = %+v", egress)
	}
}

// The secret of a header is read when the gateway asks for it, as a key is:
// a stored one is opened, and one in a secret store is read from the store.
// The harness has a placeholder in its place. A secret that cannot be read
// is refused with the reason.
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
	runner := aviary.given(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID)
	if headers := runner.Environment["AVIARY_LLM_HEADERS"]; headers != `{"x-team-token":"aviary","x-token":"aviary"}` {
		t.Errorf("AVIARY_LLM_HEADERS = %s", headers)
	}
	id := uuid.MustParse(provider)
	host := sandbox.Host{TLS: true, Name: "gateway.example.com", Port: 443}
	token, team := credentials.ProviderHeader(id, "x-token"), credentials.ProviderHeader(id, "x-team-token")
	want := sandbox.Egress{Secrets: []sandbox.Secret{{Host: host, Header: "x-token", URI: token}, {Host: host, Header: "x-team-token", URI: team}}}
	if egress := aviary.made(runner.Sandbox).Egress; !reflect.DeepEqual(egress, want) {
		t.Errorf("egress = %+v, want %+v", egress, want)
	}

	// A secret that a store holds goes without the line break at its end.
	for uri, secret := range map[string]string{token: "s3cret", team: "t0ken"} {
		if got, code, why := s.fetch(runner.Sandbox, uri); code != codes.OK || got != secret {
			t.Errorf("secret %s = %q, refused with %v %q", uri, got, code, why)
		}
	}
	// A Supervisor that has not read the secret reads it when it is needed.
	for value, why := range map[string]string{
		"t0ken\nx-other: a": "the secret cannot be read: the secret of the model provider's header x-team-token in its secret store is not the value of an HTTP header",
		"":                  "the secret cannot be read: cannot read the model provider's header x-team-token from its secret store: the store has no value at the path and under the key",
	} {
		aviary.secrets.put(address, "team/gateway", "token", value)
		if _, code, said := aviary.supervisor().fetch(runner.Sandbox, team); code != codes.Unavailable || said != why {
			t.Errorf("the secret %q in the store: refused with %v %q", value, code, said)
		}
	}

	// A secret sealed with the keys of another installation does not open.
	record, err := aviary.records.Provider(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if record.Headers[0].Sealed, err = testenv.Keys(t).Seal("s3cret", store.ProviderHeaderPlace(record.ID, "x-token")); err != nil {
		t.Fatal(err)
	}
	if _, err := aviary.records.ReplaceProvider(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if _, code, why := s.fetch(runner.Sandbox, token); code != codes.Unavailable || why != "the secret cannot be read: cannot decrypt the model provider's header x-token" {
		t.Errorf("a secret that does not open: refused with %v %q", code, why)
	}
	// A header that the provider does not have has no secret.
	if _, code, why := s.fetch(runner.Sandbox, credentials.ProviderHeader(id, "x-other")); code != codes.NotFound || why != "the model provider has no such header" {
		t.Errorf("a header that the provider does not have: refused with %v %q", code, why)
	}
}

// A sandbox reaches the local host of a provider's URL at the host that the
// installation names in its place.
func TestAProviderAtTheLocalHostIsReachedWhereTheInstallationSays(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) { config.Localhost = "host.example.com" })
	s := aviary.supervisor()
	project := s.project()
	for address, reached := range map[string]struct {
		address string
		host    sandbox.Host
	}{
		"http://localhost:11434/v1":        {"http://host.example.com:11434/v1", sandbox.Host{Name: "host.example.com", Port: 11434}},
		"http://LocalHost/v1":              {"http://host.example.com/v1", sandbox.Host{Name: "host.example.com", Port: 80}},
		"http://127.0.0.1:8000/v1":         {"http://host.example.com:8000/v1", sandbox.Host{Name: "host.example.com", Port: 8000}},
		"http://127.1.2.3/v1":              {"http://host.example.com/v1", sandbox.Host{Name: "host.example.com", Port: 80}},
		"https://[::1]:8443/v1":            {"https://host.example.com:8443/v1", sandbox.Host{TLS: true, Name: "host.example.com", Port: 8443}},
		"http://localhost.example.com/v1":  {"http://localhost.example.com/v1", sandbox.Host{Name: "localhost.example.com", Port: 80}},
		"https://api.example.com/v1":       {"https://api.example.com/v1", sandbox.Host{TLS: true, Name: "api.example.com", Port: 443}},
		"http://model.example.com:8000/v1": {"http://model.example.com:8000/v1", sandbox.Host{Name: "model.example.com", Port: 8000}},
	} {
		provider := s.create("/v1/projects/"+project+"/providers", `{"name": "`+unique("provider")+`", "url": "`+address+`"}`)
		agent := s.agent(project, `{"harness": "opencode", "model": {"provider": "`+provider+`", "name": "a-model"}}`)
		runner := aviary.given(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID)
		if got := runner.Environment["AVIARY_LLM_BASE_URL"]; got != reached.address {
			t.Errorf("a provider at %s: AVIARY_LLM_BASE_URL = %s, want %s", address, got, reached.address)
		}
		if egress := aviary.made(runner.Sandbox).Egress; !reflect.DeepEqual(egress, sandbox.Egress{Hosts: []sandbox.Host{reached.host}}) {
			t.Errorf("a provider at %s: egress = %+v, want %+v", address, egress, reached.host)
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
