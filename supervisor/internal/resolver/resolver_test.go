package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

var ctx = context.Background()

// installation is a resolver, and a turn that runs in the sandbox sbx-1. Its
// agent's model endpoint is at https://models.example.com/v1, with the key
// sk-1.
type installation struct {
	t        *testing.T
	store    *memorystore.Store
	keys     *secret.Keys
	resolver *Resolver
	turn     store.Turn
	sender   store.User
	provider store.Provider
	mu       sync.Mutex
	records  bytes.Buffer
}

func install(t *testing.T) *installation {
	t.Helper()
	i := &installation{t: t, store: memorystore.New(), keys: testenv.Keys(t)}
	stores, err := secret.NewStores(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	i.resolver = New(i.store, i.keys, stores, slog.New(slog.NewJSONHandler(i, nil)))
	project, err := i.store.CreateProject(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	key, err := i.keys.Seal("sk-1", store.ProviderKeyPlace(id))
	if err != nil {
		t.Fatal(err)
	}
	if i.provider, err = i.store.CreateProvider(ctx, store.Provider{
		ID: id, ProjectID: project.ID, Name: "models", URL: "https://models.example.com/v1", APIKey: key,
	}); err != nil {
		t.Fatal(err)
	}
	agent, err := i.store.CreateAgent(ctx, project.ID, store.Agent{Harness: "opencode", Model: &store.Model{Provider: id, Name: "a-model"}})
	if err != nil {
		t.Fatal(err)
	}
	if i.sender, err = i.store.UserOf(ctx, "subject-of-ann", "ann", "Ann"); err != nil {
		t.Fatal(err)
	}
	session, err := i.store.CreateSession(ctx, agent.ID, i.sender.ID)
	if err != nil {
		t.Fatal(err)
	}
	if i.turn, _, err = i.store.CreateTurn(ctx, session.ID, i.sender.ID, "hello", 0, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := i.store.DispatchTurns(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := i.store.FollowTurn(ctx, i.turn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := i.store.SetSandbox(ctx, session.ID, "", "sbx-1"); err != nil {
		t.Fatal(err)
	}
	return i
}

// Write takes the records of the resolver.
func (i *installation) Write(written []byte) (int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.records.Write(written)
}

// last returns the last record that the resolver wrote.
func (i *installation) last() map[string]any {
	i.t.Helper()
	i.mu.Lock()
	defer i.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(i.records.String()), "\n")
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &record); err != nil {
		i.t.Fatalf("the resolver wrote %q", i.records.String())
	}
	return record
}

// ask asks the resolver as the egress of the sandbox does, about a request
// to the URL, by the path that the resolver gave for the sandbox. It
// returns the status and the headers of the answer.
func (i *installation) ask(sandbox, method, target string) (int, map[string]string) {
	i.t.Helper()
	endpoint, err := i.resolver.Endpoint("https://resolver.example.com:9443", sandbox)
	if err != nil {
		i.t.Fatal(err)
	}
	return i.askAt(endpoint, sandbox, method, target)
}

func (i *installation) askAt(endpoint, sandbox, method, target string) (int, map[string]string) {
	i.t.Helper()
	at, err := url.Parse(endpoint)
	if err != nil {
		i.t.Fatal(err)
	}
	request, err := url.Parse(target)
	if err != nil {
		i.t.Fatal(err)
	}
	port := request.Port()
	if port == "" {
		port = defaultPort(request.Scheme)
	}
	question, err := json.Marshal(map[string]any{
		"subject":     sandbox,
		"credential":  "aviary",
		"binding":     "aviary",
		"destination": map[string]string{"scheme": request.Scheme, "authority": request.Hostname() + ":" + port, "sni": request.Hostname()},
		"request":     map[string]any{"method": method, "host": request.Host, "path": request.RequestURI(), "headers": map[string]string{}},
	})
	if err != nil {
		i.t.Fatal(err)
	}
	answer := httptest.NewRecorder()
	i.resolver.Handler().ServeHTTP(answer, httptest.NewRequest(http.MethodPost, at.Path+"/resolve", bytes.NewReader(question)))
	var answered struct{ Headers map[string]string }
	if answer.Code == http.StatusOK {
		if err := json.Unmarshal(answer.Body.Bytes(), &answered); err != nil {
			i.t.Fatalf("the resolver answered %q", answer.Body.String())
		}
	}
	return answer.Code, answered.Headers
}

func TestARequestOfATurnIsRecordedAndHasTheKeyOfItsEndpoint(t *testing.T) {
	i := install(t)
	status, headers := i.ask("sbx-1", "POST", "https://models.example.com/v1/chat/completions?stream=true")
	if status != http.StatusOK || len(headers) != 1 || headers["authorization"] != "Bearer sk-1" {
		t.Fatalf("a model request was answered with %d and the headers %v", status, headers)
	}
	record := i.last()
	session, _ := i.store.Session(ctx, i.turn.SessionID)
	want := map[string]any{
		"msg": "request", "sandbox": "sbx-1", "turn": i.turn.ID.String(), "session": i.turn.SessionID.String(),
		"agent": session.AgentID.String(), "user": i.sender.ID.String(), "subject": "subject-of-ann",
		"provider": i.provider.ID.String(), "method": "POST", "scheme": "https", "host": "models.example.com", "port": "443",
		// The query is not kept.
		"path": "/v1/chat/completions",
	}
	for name, value := range want {
		if record[name] != value {
			t.Errorf("the record has %s=%v, not %v: %v", name, record[name], value, record)
		}
	}
	if record["project"] == nil {
		t.Errorf("the record names no project: %v", record)
	}
}

func TestTheKeyOfAnEndpointIsForTheEndpointAlone(t *testing.T) {
	i := install(t)
	for _, target := range []string{
		"https://pypi.org/simple/requests/",
		"https://models.example.com/v2/chat/completions",
		"https://models.example.com/v1/../v2/chat/completions",
		"https://models.example.com/v1/%2e%2e/v2",
		"http://models.example.com/v1/chat/completions",
		"https://models.example.com:8443/v1/chat/completions",
		"https://models.example.com.elsewhere.com/v1/chat/completions",
	} {
		status, headers := i.ask("sbx-1", "POST", target)
		if status != http.StatusOK || len(headers) != 0 {
			t.Errorf("a request to %s was answered with %d and the headers %v", target, status, headers)
		}
		// It is recorded all the same.
		if record := i.last(); record["msg"] != "request" || record["turn"] != i.turn.ID.String() || record["provider"] != nil {
			t.Errorf("a request to %s is recorded as %v", target, record)
		}
	}
	// The host is that of the endpoint whatever its case.
	if _, headers := i.ask("sbx-1", "GET", "https://Models.Example.com/v1/models"); headers["authorization"] != "Bearer sk-1" {
		t.Errorf("a request to the endpoint by its name in other letters has the headers %v", headers)
	}
}

func TestAnEndpointThatTakesNoKeyIsSentNone(t *testing.T) {
	i := install(t)
	if _, err := i.store.ReplaceProvider(ctx, store.Provider{ID: i.provider.ID, Name: "models", URL: "http://models.example.com:8000/v1"}); err != nil {
		t.Fatal(err)
	}
	status, headers := i.ask("sbx-1", "POST", "http://models.example.com:8000/v1/chat/completions")
	if status != http.StatusOK || len(headers) != 0 {
		t.Errorf("a request to an endpoint without a key was answered with %d and the headers %v", status, headers)
	}
	if record := i.last(); record["provider"] != i.provider.ID.String() || record["port"] != "8000" {
		t.Errorf("the request is recorded as %v", record)
	}
}

func TestAQuestionThatDoesNotComeByThePathOfItsSandboxIsNotAnswered(t *testing.T) {
	i := install(t)
	other, err := i.resolver.Endpoint("https://resolver.example.com:9443", "sbx-2")
	if err != nil {
		t.Fatal(err)
	}
	for what, endpoint := range map[string]string{
		"the path of another sandbox":     other,
		"a path that Aviary did not make": "https://resolver.example.com:9443/c2J4LTE",
		"no path":                         "https://resolver.example.com:9443",
	} {
		if status, headers := i.askAt(endpoint, "sbx-1", "POST", "https://models.example.com/v1/chat/completions"); status != http.StatusNotFound || headers != nil {
			t.Errorf("a question by %s was answered with %d and the headers %v", what, status, headers)
		}
	}
	if i.records.Len() != 0 {
		t.Errorf("the resolver recorded questions that were not of an egress: %s", i.records.String())
	}
}

func TestARequestOfASandboxInWhichNoTurnRunsIsRefused(t *testing.T) {
	i := install(t)
	if _, _, err := i.store.EndTurn(ctx, i.turn.ID, true, store.Failure{}, func(context.Context) ([]json.RawMessage, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	status, headers := i.ask("sbx-1", "POST", "https://models.example.com/v1/chat/completions")
	if status != http.StatusForbidden || headers != nil {
		t.Errorf("a request of a sandbox in which no turn runs was answered with %d and the headers %v", status, headers)
	}
	if record := i.last(); record["msg"] != "request refused" || record["sandbox"] != "sbx-1" || record["host"] != "models.example.com" {
		t.Errorf("the refused request is recorded as %v", record)
	}
}
