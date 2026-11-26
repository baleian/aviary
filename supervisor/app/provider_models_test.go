package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/internal/driver"
)

// reaching returns an installation whose Supervisors reach the endpoints in
// place of the hosts of their URLs, by host.
func reaching(t *testing.T, served map[string]http.HandlerFunc, changes ...func(*driver.Config)) *installation {
	t.Helper()
	aviary := install(t, changes...)
	for host, endpoint := range served {
		aviary.endpoints.serve(host, endpoint)
	}
	return aviary
}

// listed is what came of asking a provider for its models. models are the IDs
// that it listed, joined with commas.
type listed struct {
	reached bool
	status  int
	reason  string
	models  string
}

// listing sends a request that lists the models of a provider, and returns
// what came of it.
func (s *supervisor) listing(method, path, body string) listed {
	s.t.Helper()
	var got struct {
		Reached    bool
		Status     int
		Reason     string
		Models     []string
		DurationMS *int64 `json:"duration_ms"`
	}
	if status, _ := s.request(method, path, body, &got); status != http.StatusOK {
		s.t.Fatalf("%s %s %s: status %d, want 200", method, path, body, status)
	}
	if got.DurationMS == nil || *got.DurationMS < 0 {
		s.t.Errorf("%s %s %s: duration_ms = %v", method, path, body, got.DurationMS)
	}
	return listed{got.Reached, got.Status, got.Reason, strings.Join(got.Models, ",")}
}

// modelsAt lists the models of an endpoint that is not registered.
func (s *supervisor) modelsAt(project, body string) listed {
	s.t.Helper()
	return s.listing("POST", "/v1/projects/"+project+"/providers/models", body)
}

// keyedEndpoint lists two models to a request with the key, and answers 401
// to any other.
func keyedEndpoint(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"object": "list", "data": [{"id": "model-a", "object": "model"}, {"id": "model-b", "object": "model"}]}`)
	}
}

// routedEndpoint lists a model to a request with the header, and answers 400
// to any other, as a gateway does that routes its requests by a header.
func routedEndpoint(name, value string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(name) != value {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		io.WriteString(w, `{"object": "list", "data": [{"id": "routed-model", "object": "model"}]}`)
	}
}

// answers returns an endpoint that answers 200 with the body.
func answers(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }
}

// The models of an endpoint are listed before it is registered: the
// Supervisor asks the endpoint for them with the key and the headers, as a
// turn's model requests carry them. The result is the models and the status
// that the endpoint answered with, or why there was no answer.
func TestTheModelsOfAnEndpointAreListed(t *testing.T) {
	t.Parallel()
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the Supervisor followed a redirect of a model endpoint")
	}))
	t.Cleanup(elsewhere.Close)
	aviary := reaching(t, map[string]http.HandlerFunc{
		"model.example.com:8000": keyedEndpoint("sk-a-key"),
		"api.example.com":        keyedEndpoint("sk-in-the-store"),
		"gateway.example.com":    routedEndpoint("X-Gateway-Route", "team-a"),
		"open.example.com":       answers(""),
		"other.example.com":      answers(`{"models": [{"name": "model-a"}]}`),
		"none.example.com":       answers(`{"object": "list", "data": []}`),
		"some.example.com":       answers(`{"data": [{"id": ""}, {"id": "the-only-one"}, {"name": "unnamed"}]}`),
		"long.example.com":       answers(`{"data": [{"id": "model-a", "description": "` + strings.Repeat("long ", 1<<18) + `"}]}`),
		"moved.example.com": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL, http.StatusFound)
		},
	})
	s := aviary.supervisor()
	project := s.project()

	got := s.modelsAt(project, `{"url": "http://model.example.com:8000/v1/", "api_key": {"value": "sk-a-key"}}`)
	if got != (listed{reached: true, status: http.StatusOK, models: "model-a,model-b"}) {
		t.Errorf("an endpoint that lists its models: %+v", got)
	}
	if asked := aviary.endpoints.last(); asked != (passed{"GET", "model.example.com:8000", "/v1/models", "Bearer sk-a-key", false}) {
		t.Errorf("the endpoint got %+v", asked)
	}

	for name, test := range map[string]struct {
		body string
		want listed
	}{
		"a key that the endpoint refuses": {`{"url": "http://model.example.com:8000/v1", "api_key": {"value": "sk-another"}}`, listed{reached: true, status: http.StatusUnauthorized}},
		"no key for an endpoint with one": {`{"url": "http://model.example.com:8000/v1"}`, listed{reached: true, status: http.StatusUnauthorized}},
		"a URL with a longer path":        {`{"url": "http://model.example.com:8000/custom/v2", "api_key": {"value": "sk-a-key"}}`, listed{reached: true, status: http.StatusOK, models: "model-a,model-b"}},
		// The headers of the provider go with the request, as its key does.
		"a header that the endpoint needs": {`{"url": "https://gateway.example.com/v1", "headers": [{"name": "X-Gateway-Route", "value": "team-a"}]}`,
			listed{reached: true, status: http.StatusOK, models: "routed-model"}},
		"another value of the header":        {`{"url": "https://gateway.example.com/v1", "headers": [{"name": "x-gateway-route", "value": "team-b"}]}`, listed{reached: true, status: http.StatusBadRequest}},
		"no header for an endpoint with one": {`{"url": "https://gateway.example.com/v1"}`, listed{reached: true, status: http.StatusBadRequest}},
		"an endpoint that redirects":         {`{"url": "https://moved.example.com/v1"}`, listed{reached: true, status: http.StatusFound}},
		"an endpoint that is not reached":    {`{"url": "https://nowhere.example.com/v1"}`, listed{reason: "the endpoint cannot be reached"}},
		// An answer that is not a list of models as the OpenAI API gives it
		// has no models, and a model without an ID is left out.
		"an answer that is no list":   {`{"url": "https://other.example.com/v1"}`, listed{reached: true, status: http.StatusOK}},
		"a list without models":       {`{"url": "https://none.example.com/v1"}`, listed{reached: true, status: http.StatusOK}},
		"a list with a model to name": {`{"url": "https://some.example.com/v1"}`, listed{reached: true, status: http.StatusOK, models: "the-only-one"}},
		"a list that is too long":     {`{"url": "https://long.example.com/v1"}`, listed{reached: true, status: http.StatusOK}},
	} {
		if got := s.modelsAt(project, test.body); got != test.want {
			t.Errorf("%s: %+v, want %+v", name, got, test.want)
		}
	}
	// A provider without a key sends none, not even the token of the
	// listing.
	if got := s.modelsAt(project, `{"url": "https://open.example.com/custom/v2"}`); got != (listed{reached: true, status: http.StatusOK}) {
		t.Errorf("a provider without a key: %+v", got)
	}
	if asked := aviary.endpoints.last(); asked != (passed{"GET", "open.example.com", "/custom/v2/models", "", true}) {
		t.Errorf("the endpoint without a key got %+v", asked)
	}

	// A key in a secret store of the project is read as for a turn.
	const address = "https://" + storeHost
	kept := s.create("/v1/projects/"+project+"/secret-stores", scriptedStore("a store", address, "a-credential", "a-credential"))
	aviary.secrets.put(address, "llm/model", "api_key", "sk-in-the-store")
	if got := s.modelsAt(project, `{"url": "https://api.example.com/v1", "api_key": `+in(kept, "llm/model", "api_key")+`}`); got != (listed{reached: true, status: http.StatusOK, models: "model-a,model-b"}) {
		t.Errorf("an endpoint with its key in a secret store: %+v", got)
	}

	// Nothing of it is stored.
	var registered struct{ Providers []struct{ ID string } }
	if status, _ := s.request("GET", "/v1/projects/"+project+"/providers", "", &registered); status != http.StatusOK || len(registered.Providers) != 0 {
		t.Errorf("providers after the listings: status %d, %v", status, registered.Providers)
	}
}

// The models of a registered provider are listed with the URL and the key
// that it has then.
func TestTheModelsOfAProviderAreListed(t *testing.T) {
	t.Parallel()
	aviary := reaching(t, map[string]http.HandlerFunc{
		"model.example.com:8000": keyedEndpoint("sk-a-key"),
		"api.example.com":        keyedEndpoint("sk-in-the-store"),
		"gateway.example.com":    routedEndpoint("X-Gateway-Route", "team-a"),
		"open.example.com":       answers(""),
	})
	s := aviary.supervisor()
	project := s.project()
	const address = "https://" + storeHost
	kept := s.create("/v1/projects/"+project+"/secret-stores", scriptedStore("a store", address, "a-credential", "a-credential"))
	aviary.secrets.put(address, "llm/model", "api_key", "sk-in-the-store")
	providers := "/v1/projects/" + project + "/providers"
	stored := s.create(providers, `{"name": "stored", "url": "http://model.example.com:8000/v1", "api_key": {"value": "sk-a-key"}}`)
	elsewhere := s.create(providers, `{"name": "elsewhere", "url": "https://api.example.com/v1", "api_key": `+in(kept, "llm/model", "api_key")+`}`)
	keyless := s.create(providers, `{"name": "keyless", "url": "https://open.example.com/v1"}`)
	routed := s.create(providers, `{"name": "routed", "url": "https://gateway.example.com/v1", "headers": [{"name": "x-gateway-route", "value": "team-a"}]}`)
	gone := s.create(providers, `{"name": "gone", "url": "https://nowhere.example.com/v1"}`)

	for name, test := range map[string]struct {
		provider string
		want     listed
	}{
		"with a key that Aviary keeps":   {stored, listed{reached: true, status: http.StatusOK, models: "model-a,model-b"}},
		"with a key in a secret store":   {elsewhere, listed{reached: true, status: http.StatusOK, models: "model-a,model-b"}},
		"without a key and with no list": {keyless, listed{reached: true, status: http.StatusOK}},
		"with a header":                  {routed, listed{reached: true, status: http.StatusOK, models: "routed-model"}},
		"that is not reached":            {gone, listed{reason: "the endpoint cannot be reached"}},
	} {
		if got := s.listing("GET", "/v1/providers/"+test.provider+"/models", ""); got != test.want {
			t.Errorf("a provider %s: %+v, want %+v", name, got, test.want)
		}
	}
	// A provider that is replaced is asked where it then is.
	if status, _ := s.request("PUT", "/v1/providers/"+gone, `{"name": "back", "url": "http://model.example.com:8000/custom/v2", "api_key": {"value": "sk-a-key"}}`, nil); status != http.StatusOK {
		t.Fatalf("replace provider: status %d", status)
	}
	if got := s.listing("GET", "/v1/providers/"+gone+"/models", ""); got != (listed{reached: true, status: http.StatusOK, models: "model-a,model-b"}) {
		t.Errorf("a provider that was replaced: %+v", got)
	}
	if asked := aviary.endpoints.last(); asked != (passed{"GET", "model.example.com:8000", "/custom/v2/models", "Bearer sk-a-key", false}) {
		t.Errorf("the endpoint of the replaced provider got %+v", asked)
	}
	if status, _ := s.request("GET", "/v1/providers/"+uuid.NewString()+"/models", "", nil); status != http.StatusNotFound {
		t.Errorf("the models of an unknown provider: status %d, want 404", status)
	}
}

// A provider is tried before it is replaced with the secrets that it stores:
// the request names it, and they are lent as a replacement would keep them,
// only while the URL stays the same.
func TestAProviderIsTriedWithTheSecretsThatItStores(t *testing.T) {
	t.Parallel()
	aviary := reaching(t, map[string]http.HandlerFunc{
		"model.example.com:8000": keyedEndpoint("sk-a-key"),
		"gateway.example.com":    routedEndpoint("X-Gateway-Route", "team-a"),
	})
	s := aviary.supervisor()
	project := s.project()
	providers := "/v1/projects/" + project + "/providers"
	stored := s.create(providers, `{"name": "stored", "url": "http://model.example.com:8000/v1", "api_key": {"value": "sk-a-key"}}`)
	routed := s.create(providers, `{"name": "routed", "url": "https://gateway.example.com/v1", "headers": [{"name": "x-gateway-route", "secret": {"value": "team-a"}}]}`)
	theirs := s.create("/v1/projects/"+s.project()+"/providers", `{"name": "theirs", "url": "http://model.example.com:8000/v1", "api_key": {"value": "sk-a-key"}}`)

	for name, test := range map[string]struct {
		body string
		want listed
	}{
		"the stored key": {`{"url": "http://model.example.com:8000/v1", "api_key": {"stored": true}, "provider": "` + stored + `"}`,
			listed{reached: true, status: http.StatusOK, models: "model-a,model-b"}},
		"another key in its place": {`{"url": "http://model.example.com:8000/v1", "api_key": {"value": "sk-another"}, "provider": "` + stored + `"}`,
			listed{reached: true, status: http.StatusUnauthorized}},
		"the stored secret of a header": {`{"url": "https://gateway.example.com/v1", "headers": [{"name": "x-gateway-route", "secret": {"stored": true}}], "provider": "` + routed + `"}`,
			listed{reached: true, status: http.StatusOK, models: "routed-model"}},
	} {
		if got := s.modelsAt(project, test.body); got != test.want {
			t.Errorf("%s: %+v, want %+v", name, got, test.want)
		}
	}
	for name, test := range map[string]struct{ body, why string }{
		"another URL":                   {`{"url": "http://model.example.com:8000/v2", "api_key": {"stored": true}, "provider": "` + stored + `"}`, "api_key must be given again: the URL changed"},
		"no provider":                   {`{"url": "http://model.example.com:8000/v1", "api_key": {"stored": true}}`, "api_key has no stored secret to keep"},
		"a provider of another project": {`{"url": "http://model.example.com:8000/v1", "api_key": {"stored": true}, "provider": "` + theirs + `"}`, "provider must be the ID of a model provider of the project"},
		"an unknown provider":           {`{"url": "http://model.example.com:8000/v1", "provider": "` + uuid.NewString() + `"}`, "provider must be the ID of a model provider of the project"},
	} {
		var refused struct{ Message string }
		if status, _ := s.request("POST", "/v1/projects/"+project+"/providers/models", test.body, &refused); status != http.StatusBadRequest || refused.Message != test.why {
			t.Errorf("%s: %d %q, want 400 %q", name, status, refused.Message, test.why)
		}
	}
	// A try changes nothing of the provider.
	if got := s.listing("GET", "/v1/providers/"+stored+"/models", ""); got != (listed{reached: true, status: http.StatusOK, models: "model-a,model-b"}) {
		t.Errorf("the provider after the tries: %+v", got)
	}
}

// The models of an endpoint are not listed if its provider would not be
// registered.
func TestAnEndpointWhoseModelsAreNotListed(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project := s.project()
	for name, body := range map[string]string{
		"no URL":             `{}`,
		"a URL without host": `{"url": "https:///v1"}`,
		"a URL with a query": `{"url": "https://api.example.com/v1?a=b"}`,
		"a key of two kinds": `{"url": "https://api.example.com/v1", "api_key": {"value": "sk-a-key", "store": "` + uuid.NewString() + `"}}`,
		"an unknown store":   `{"url": "https://api.example.com/v1", "api_key": ` + in(uuid.NewString(), "llm/model", "api_key") + `}`,
		"a reserved header":  `{"url": "https://api.example.com/v1", "headers": [{"name": "host", "value": "other.example.com"}]}`,
		"a name":             `{"name": "a provider", "url": "https://api.example.com/v1"}`,
	} {
		var refused struct{ Code string }
		if status, _ := s.request("POST", "/v1/projects/"+project+"/providers/models", body, &refused); status != http.StatusBadRequest || refused.Code != "invalid_request" {
			t.Errorf("%s: got %d %s, want 400 invalid_request", name, status, refused.Code)
		}
	}
	if status, _ := s.request("POST", "/v1/projects/"+uuid.NewString()+"/providers/models", `{"url": "https://api.example.com/v1"}`, nil); status != http.StatusNotFound {
		t.Errorf("the models of an endpoint in an unknown project: status %d, want 404", status)
	}
	if asked := aviary.endpoints.asked(); asked != 0 {
		t.Errorf("the endpoints got %d requests of listings that were refused", asked)
	}
}

// A listing reaches the local host of the URL where the installation says.
func TestAListingReachesTheLocalHostWhereTheInstallationSays(t *testing.T) {
	t.Parallel()
	aviary := reaching(t, map[string]http.HandlerFunc{"host.example.com:11434": answers("")},
		func(config *driver.Config) { config.Localhost = "host.example.com" })
	s := aviary.supervisor()
	if got := s.modelsAt(s.project(), `{"url": "http://localhost:11434/v1"}`); got != (listed{reached: true, status: http.StatusOK}) {
		t.Errorf("an endpoint at the local host: %+v", got)
	}
	if asked := aviary.endpoints.last(); asked != (passed{"GET", "host.example.com:11434", "/v1/models", "", false}) {
		t.Errorf("the endpoint got %+v", asked)
	}
}
