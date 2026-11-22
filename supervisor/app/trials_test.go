package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"aviary/supervisor/internal/api"
	"aviary/supervisor/internal/authz"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/store"
)

// proxy stands in for the model listener of the Egress Proxy. It asks a
// Supervisor about each request, as the proxy does, and answers for the
// endpoint that the Supervisor names.
type proxy struct {
	t *testing.T
	// asks is the Supervisor that it asks.
	asks *supervisor
	// endpoints are the model endpoints that it reaches, by host.
	endpoints map[string]http.HandlerFunc

	mu sync.Mutex
	// tokens are the tokens of the requests that it got, and got are the
	// requests that it passed on to an endpoint.
	tokens []string
	got    []passed
}

// passed is a request as an endpoint got it.
type passed struct {
	method, host, path, authorization, secure string
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	p.mu.Lock()
	p.tokens = append(p.tokens, token)
	p.mu.Unlock()
	// replies answers as the proxy does when it makes the reply itself.
	replies := func(status int, body string) {
		w.Header().Set("x-aviary-proxy", "true")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
	answer := p.asks.check(token, false, r.URL.RequestURI())
	if codes.Code(answer.GetStatus().GetCode()) != codes.OK {
		status, why := refusal(answer)
		replies(status, why)
		return
	}
	to := named(p.t, answer)
	endpoint, reached := p.endpoints[to.host]
	if !reached {
		replies(http.StatusServiceUnavailable, "upstream connect error or disconnect/reset before headers. reset reason: connection timeout")
		return
	}
	p.mu.Lock()
	p.got = append(p.got, passed{r.Method, to.host, r.URL.RequestURI(), to.authorization, to.secure})
	p.mu.Unlock()
	onward := r.Clone(r.Context())
	onward.Header.Del("Authorization")
	if !to.keyless {
		onward.Header.Set("Authorization", to.authorization)
	}
	endpoint(w, onward)
}

// last returns the last request that an endpoint got, and the token that it
// came to the proxy with.
func (p *proxy) last() (passed, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.got) == 0 {
		p.t.Fatal("no endpoint got a request")
	}
	return p.got[len(p.got)-1], p.tokens[len(p.tokens)-1]
}

// proxied returns an installation whose model requests go to a stand-in for
// the Egress Proxy, which reaches the endpoints.
func proxied(t *testing.T, endpoints map[string]http.HandlerFunc) (*installation, *proxy) {
	t.Helper()
	standIn := &proxy{t: t, endpoints: endpoints}
	server := httptest.NewServer(standIn)
	t.Cleanup(server.Close)
	at, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return install(t, func(config *driver.Config) { config.Models = at }), standIn
}

// tried is the result of trying a model provider.
type tried struct {
	Reached    bool
	Status     int
	Reason     string
	DurationMS *int64 `json:"duration_ms"`
}

// try tries a provider of the project and returns the result.
func (s *supervisor) try(project, body string) tried {
	s.t.Helper()
	var result tried
	if status, _ := s.request("POST", "/v1/projects/"+project+"/providers/test", body, &result); status != http.StatusOK {
		s.t.Fatalf("try %s: status %d, want 200", body, status)
	}
	if result.DurationMS == nil || *result.DurationMS < 0 {
		s.t.Errorf("try %s: duration_ms = %v", body, result.DurationMS)
	}
	result.DurationMS = nil
	return result
}

// keyedEndpoint answers 200 to a request with the key, and 401 to any other.
func keyedEndpoint(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}
}

// A provider is tried before it is registered: the Supervisor asks its
// endpoint for the models, through the Egress Proxy and with the provider's
// key, as a turn's model requests go. The result is the status that the
// endpoint answered with, or why the proxy got no answer.
func TestAProviderIsTriedThroughTheProxy(t *testing.T) {
	t.Parallel()
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the Supervisor followed a redirect of a model endpoint")
	}))
	t.Cleanup(elsewhere.Close)
	aviary, standIn := proxied(t, map[string]http.HandlerFunc{
		"model.example.com:8000": keyedEndpoint("sk-a-key"),
		"api.example.com":        keyedEndpoint("sk-in-the-store"),
		"open.example.com":       func(http.ResponseWriter, *http.Request) {},
		"moved.example.com": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL, http.StatusFound)
		},
	})
	s := aviary.supervisor()
	standIn.asks = s
	project := s.project()

	got := s.try(project, `{"url": "http://model.example.com:8000/v1/", "api_key": {"value": "sk-a-key"}}`)
	if got != (tried{Reached: true, Status: http.StatusOK}) {
		t.Errorf("a provider that answers: %+v", got)
	}
	asked, token := standIn.last()
	if asked != (passed{"GET", "model.example.com:8000", "/v1/models", "Bearer sk-a-key", "false"}) {
		t.Errorf("the endpoint got %+v", asked)
	}
	// The request names its project for the proxy's access log, and no
	// turn. Its token is of no use to a sandbox as a proxy password.
	if from := s.origin(token, false); len(from) != 1 || from["project"] != project {
		t.Errorf("origin of the model request of a trial = %v", from)
	}
	if from := s.origin(token, true); from != nil {
		t.Errorf("a trial's token as a proxy password: origin %v, want it denied", from)
	}
	// Only the path of the provider's URL and paths below it are allowed.
	if status, _ := refusal(s.check(token, false, "/v2/models")); status != http.StatusNotFound {
		t.Errorf("the token of a trial on another path: status %d, want 404", status)
	}

	for name, test := range map[string]struct {
		body string
		want tried
	}{
		"a key that the endpoint refuses": {`{"url": "http://model.example.com:8000/v1", "api_key": {"value": "sk-another"}}`, tried{Reached: true, Status: http.StatusUnauthorized}},
		"no key for an endpoint with one": {`{"url": "http://model.example.com:8000/v1"}`, tried{Reached: true, Status: http.StatusUnauthorized}},
		"a URL with a longer path":        {`{"url": "http://model.example.com:8000/custom/v2", "api_key": {"value": "sk-a-key"}}`, tried{Reached: true, Status: http.StatusOK}},
		"an endpoint that redirects":      {`{"url": "https://moved.example.com/v1"}`, tried{Reached: true, Status: http.StatusFound}},
		"an endpoint that is not reached": {`{"url": "https://nowhere.example.com/v1"}`,
			tried{Reason: "upstream connect error or disconnect/reset before headers. reset reason: connection timeout"}},
	} {
		if got := s.try(project, test.body); got != test.want {
			t.Errorf("%s: %+v, want %+v", name, got, test.want)
		}
	}
	// A provider without a key sends none, not even the token of the trial.
	if got := s.try(project, `{"url": "https://open.example.com/custom/v2"}`); got != (tried{Reached: true, Status: http.StatusOK}) {
		t.Errorf("a provider without a key: %+v", got)
	}
	if asked, _ := standIn.last(); asked != (passed{"GET", "open.example.com", "/custom/v2/models", "", "true"}) {
		t.Errorf("the endpoint without a key got %+v", asked)
	}

	// A key in a secret store of the project is read as for a turn.
	const address = "https://" + storeHost
	kept := s.create("/v1/projects/"+project+"/secret-stores", scriptedStore("a store", address, "a-credential", "a-credential"))
	aviary.secrets.put(address, "llm/model", "api_key", "sk-in-the-store")
	if got := s.try(project, `{"url": "https://api.example.com/v1", "api_key": `+in(kept, "llm/model", "api_key")+`}`); got != (tried{Reached: true, Status: http.StatusOK}) {
		t.Errorf("a provider with its key in a secret store: %+v", got)
	}

	// Nothing of a trial is stored.
	var listed struct{ Providers []struct{ ID string } }
	if status, _ := s.request("GET", "/v1/projects/"+project+"/providers", "", &listed); status != http.StatusOK || len(listed.Providers) != 0 {
		t.Errorf("providers after the trials: status %d, %v", status, listed.Providers)
	}
}

// A trial is refused like the registration of its provider would be.
func TestAProviderThatCannotBeTried(t *testing.T) {
	t.Parallel()
	aviary, standIn := proxied(t, nil)
	s := aviary.supervisor()
	standIn.asks = s
	project := s.project()
	for name, body := range map[string]string{
		"no URL":             `{}`,
		"a URL without host": `{"url": "https:///v1"}`,
		"a URL with a query": `{"url": "https://api.example.com/v1?a=b"}`,
		"a key of two kinds": `{"url": "https://api.example.com/v1", "api_key": {"value": "sk-a-key", "store": "` + uuid.NewString() + `"}}`,
		"an unknown store":   `{"url": "https://api.example.com/v1", "api_key": ` + in(uuid.NewString(), "llm/model", "api_key") + `}`,
		"a name":             `{"name": "a provider", "url": "https://api.example.com/v1"}`,
	} {
		var refused struct{ Code string }
		if status, _ := s.request("POST", "/v1/projects/"+project+"/providers/test", body, &refused); status != http.StatusBadRequest || refused.Code != "invalid_request" {
			t.Errorf("%s: got %d %s, want 400 invalid_request", name, status, refused.Code)
		}
	}
	if status, _ := s.request("POST", "/v1/projects/"+uuid.NewString()+"/providers/test", `{"url": "https://api.example.com/v1"}`, nil); status != http.StatusNotFound {
		t.Errorf("a trial in an unknown project: status %d, want 404", status)
	}
	if len(standIn.tokens) != 0 {
		t.Errorf("the proxy got %d requests of trials that were refused", len(standIn.tokens))
	}
}

// The model request of a trial is routed like that of a turn, and is logged
// with the caller who tried the provider.
func TestTheModelRequestOfATrial(t *testing.T) {
	t.Parallel()
	aviary, standIn := proxied(t, map[string]http.HandlerFunc{"host.example.com:11434": func(http.ResponseWriter, *http.Request) {}})
	aviary.localhost = "host.example.com"
	aviary.callers = api.Callers{Provider: tokens{"a-token": {Subject: "subject-1", Username: "alice@example.com", Groups: []string{"platform"}}}, Admins: []string{"platform"}}
	s := aviary.supervisor()
	standIn.asks = s
	s.token = "a-token"
	project := s.project()
	var me struct{ ID string }
	s.request("GET", "/v1/me", "", &me)

	// The local host of the URL is where the installation says.
	if got := s.try(project, `{"url": "http://localhost:11434/v1"}`); got != (tried{Reached: true, Status: http.StatusOK}) {
		t.Errorf("a provider at the local host: %+v", got)
	}
	_, token := standIn.last()
	if from := s.origin(token, false); len(from) != 3 || from["project"] != project || from["user"] != me.ID || from["subject"] != "subject-1" {
		t.Errorf("origin of the model request of a trial = %v, want the project and the caller %s", from, me.ID)
	}

	// A token is accepted for as long as its trial may take, and only with
	// the keys of the installation. A key in a secret store of another
	// project is not read.
	theirs := s.create("/v1/projects/"+s.project()+"/secret-stores", scriptedStore("theirs", "https://"+storeHost, "a-credential", "a-credential"))
	aviary.secrets.put("https://"+storeHost, "llm/model", "api_key", "sk-theirs")
	provider := store.Provider{ID: uuid.New(), ProjectID: uuid.MustParse(project), URL: "http://localhost:11434/v1"}
	elsewhere := provider
	elsewhere.APIKeyAt = &store.SecretReference{Store: uuid.MustParse(theirs), Path: "llm/model", Key: "api_key"}
	for name, trial := range map[string]authz.Trial{
		"a trial that is over":                  {Provider: provider, Until: time.Now().Add(-time.Second)},
		"a trial with the key of another store": {Provider: elsewhere, Until: time.Now().Add(time.Minute)},
	} {
		token, err := authz.TrialToken(aviary.keys, trial)
		if err != nil {
			t.Fatal(err)
		}
		if from := s.origin(token, false); from != nil {
			t.Errorf("%s: origin %v, want it denied", name, from)
		}
	}
	valid := authz.Trial{Provider: provider, Until: time.Now().Add(time.Minute)}
	if token, err := authz.TrialToken(aviary.keys, valid); err != nil || s.origin(token, false) == nil {
		t.Errorf("a trial that goes on is denied: %v", err)
	}
	if token, err := authz.TrialToken(testenv.Keys(t), valid); err != nil || s.origin(token, false) != nil {
		t.Errorf("a trial sealed with other keys is allowed: %v", err)
	}
}
