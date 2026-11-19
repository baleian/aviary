// Package sandboxtest is a sandbox service for tests: its lifecycle API, its
// gateway, and behind the gateway the runners of its sandboxes, which the
// test plays.
package sandboxtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Key is the API key of the service.
const Key = "the-key-of-the-service"

// routeHeader is the header in which the gateway is told where a request
// goes.
const routeHeader = "OpenSandbox-Ingress-To"

// patience is how long a test waits for what the service is to be asked.
const patience = 5 * time.Second

// Service is the sandbox service.
type Service struct {
	// URL is the address of the lifecycle API.
	URL string

	gateway string
	// secret is what the routes that the service gives end with.
	secret string
	closed chan struct{}
	turns  chan *Turn

	mu        sync.Mutex
	templates []*template
	sandboxes map[string]*sandbox
	made      int
	// holds is whether a template stays unbuilt until Ends tells how its
	// building ended.
	holds bool
	// refuses is what is answered to one who asks for a template, if it is
	// set.
	refuses int
}

type template struct {
	ID       string            `json:"templateId"`
	Metadata map[string]string `json:"metadata"`
	Status   struct {
		Phase   string `json:"phase"`
		Message string `json:"message"`
	} `json:"status"`
	spec json.RawMessage
}

type sandbox struct {
	state    string
	metadata map[string]string
	runner   *runner
}

// New starts a sandbox service, which is stopped when the test ends.
func New(t testing.TB) *Service {
	t.Helper()
	s := &Service{
		secret:    "signed-by-the-service",
		closed:    make(chan struct{}),
		turns:     make(chan *Turn, 64),
		sandboxes: map[string]*sandbox{},
	}
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/templates", s.createTemplate)
	api.HandleFunc("GET /v1/templates", s.listTemplates)
	api.HandleFunc("POST /v1/sandboxes", s.createSandbox)
	api.HandleFunc("GET /v1/sandboxes/{id}", s.getSandbox)
	api.HandleFunc("DELETE /v1/sandboxes/{id}", s.deleteSandbox)
	api.HandleFunc("GET /v1/sandboxes/{id}/endpoints/{port}", s.endpoint)
	lifecycle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OPEN-SANDBOX-API-KEY") != Key {
			refuse(w, http.StatusUnauthorized, "UNAUTHORIZED", "who are you")
			return
		}
		api.ServeHTTP(w, r)
	}))
	gateway := httptest.NewServer(http.HandlerFunc(s.gate))
	s.URL = lifecycle.URL
	s.gateway = strings.TrimPrefix(gateway.URL, "http://")
	t.Cleanup(func() {
		// Those who read the events of a turn are let go first.
		close(s.closed)
		gateway.Close()
		lifecycle.Close()
	})
	return s
}

func refuse(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}

// HoldTemplates has the templates that the service is asked for from here
// on stay unbuilt, until Ends tells how the building of each ended.
func (s *Service) HoldTemplates() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holds = true
}

// RefuseTemplates has the service answer the status to those who ask it for
// a template: zero is to build them again.
func (s *Service) RefuseTemplates(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuses = status
}

// Ends tells how the building of the template ended: in which phase, and
// with what message.
func (s *Service) Ends(id, phase, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, template := range s.templates {
		if template.ID == id {
			template.Status.Phase, template.Status.Message = phase, message
		}
	}
}

// Asked waits until the service was asked for so many templates, and
// returns what each was to be built from.
func (s *Service) Asked(t testing.TB, count int) []json.RawMessage {
	t.Helper()
	for end := time.Now().Add(patience); ; time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		var specs []json.RawMessage
		for _, template := range s.templates {
			specs = append(specs, template.spec)
		}
		s.mu.Unlock()
		if len(specs) >= count {
			return specs
		}
		if time.Now().After(end) {
			t.Fatalf("the service was asked for %d templates, and not for %d", len(specs), count)
		}
	}
}

func (s *Service) createTemplate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var spec json.RawMessage
	var asked struct {
		Metadata map[string]string `json:"metadata"`
	}
	if json.NewDecoder(r.Body).Decode(&spec) != nil || json.Unmarshal(spec, &asked) != nil {
		refuse(w, http.StatusBadRequest, "INVALID", "that is no template")
		return
	}
	if s.refuses != 0 {
		refuse(w, s.refuses, "REFUSED", "the store is not there")
		return
	}
	built := &template{ID: fmt.Sprintf("tpl-%d", len(s.templates)+1), Metadata: asked.Metadata, spec: spec}
	built.Status.Phase = "Succeeded"
	if s.holds {
		built.Status.Phase = "Pending"
	}
	s.templates = append(s.templates, built)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(built)
}

func (s *Service) listTemplates(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, value, _ := strings.Cut(r.URL.Query().Get("metadata"), "=")
	found := []*template{}
	for _, template := range s.templates {
		if template.Metadata[name] == value {
			found = append(found, template)
		}
	}
	// Two to a page, to have more than one.
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	from := min(max(page-1, 0)*2, len(found))
	to := min(from+2, len(found))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":      found[from:to],
		"pagination": map[string]any{"hasNextPage": to < len(found)},
	})
}

// Sandboxes returns the sandboxes that are there: the metadata of each, by
// its ID.
func (s *Service) Sandboxes() map[string]map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sandboxes := map[string]map[string]string{}
	for id, sandbox := range s.sandboxes {
		sandboxes[id] = sandbox.metadata
	}
	return sandboxes
}

func (s *Service) createSandbox(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var asked struct {
		TemplateID string            `json:"templateId"`
		Timeout    *int64            `json:"timeout"`
		Metadata   map[string]string `json:"metadata"`
	}
	if json.NewDecoder(r.Body).Decode(&asked) != nil || asked.Timeout == nil {
		refuse(w, http.StatusBadRequest, "INVALID", "a sandbox of a template is made with a timeout")
		return
	}
	built := slices.ContainsFunc(s.templates, func(template *template) bool {
		return template.ID == asked.TemplateID && template.Status.Phase == "Succeeded"
	})
	if !built {
		refuse(w, http.StatusNotFound, "TEMPLATE_NOT_FOUND", "there is no such template")
		return
	}
	s.made++
	id := fmt.Sprintf("sbx-%d", s.made)
	s.sandboxes[id] = &sandbox{state: "Pending", metadata: asked.Metadata, runner: &runner{service: s, sandbox: id}}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "status": map[string]string{"state": "Pending"}})
}

func (s *Service) getSandbox(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sandbox := s.sandboxes[r.PathValue("id")]
	if sandbox == nil {
		refuse(w, http.StatusNotFound, "FSB::SANDBOX_NOT_FOUND", "there is no such sandbox")
		return
	}
	// A sandbox takes a moment to run: until it is looked at.
	sandbox.state = "Running"
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": r.PathValue("id"), "status": map[string]string{"state": sandbox.state},
	})
}

func (s *Service) deleteSandbox(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sandboxes[r.PathValue("id")] == nil {
		refuse(w, http.StatusNotFound, "FSB::SANDBOX_NOT_FOUND", "there is no such sandbox")
		return
	}
	delete(s.sandboxes, r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

// endpoint gives the way to a port of a sandbox, also of one that is not
// there, as the service does.
func (s *Service) endpoint(w http.ResponseWriter, r *http.Request) {
	route := strings.Join([]string{"f1", r.PathValue("id"), r.PathValue("port"), s.secret}, ".")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"endpoint": s.gateway,
		"headers":  map[string]string{routeHeader: route},
	})
}

// gate is the gateway: it takes a request to the runner of the sandbox that
// the route names.
func (s *Service) gate(w http.ResponseWriter, r *http.Request) {
	route := r.Header.Get(routeHeader)
	if route == "" {
		http.Error(w, "the request names no sandbox", http.StatusBadRequest)
		return
	}
	parts := strings.Split(route, ".")
	if len(parts) != 4 || parts[0] != "f1" || parts[3] != s.secret {
		http.Error(w, "the route is not one of the service", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	sandbox := s.sandboxes[parts[1]]
	running := sandbox != nil && sandbox.state == "Running"
	s.mu.Unlock()
	if !running {
		http.Error(w, "the sandbox does not run", http.StatusServiceUnavailable)
		return
	}
	sandbox.runner.ServeHTTP(w, r)
}

// Turn waits for the next turn that a runner of the service is given.
func (s *Service) Turn(t testing.TB) *Turn {
	t.Helper()
	select {
	case turn := <-s.turns:
		return turn
	case <-time.After(patience):
		t.Fatal("no runner was given a turn")
		return nil
	}
}

// runner is the runner of a sandbox.
type runner struct {
	service *Service
	sandbox string
	mu      sync.Mutex
	// turn is the turn that the runner has: the last that it was given.
	turn *Turn
}

// Turn is a turn that a runner was given, which the test runs.
type Turn struct {
	// Sandbox is the sandbox of the runner.
	Sandbox string `json:"-"`
	ID      string `json:"-"`
	// What the turn was started with.
	Text         string            `json:"text"`
	Instructions string            `json:"instructions"`
	Approval     string            `json:"approval"`
	Environment  map[string]string `json:"environment"`

	closed  chan struct{}
	stops   chan struct{}
	answers chan [2]string

	mu     sync.Mutex
	events []event
	ended  bool
	// cuts counts how often those who read the events were cut off.
	cuts int
	// changed is closed when there is more to read.
	changed chan struct{}
}

type event struct {
	name string
	data []byte
}

func (rn *runner) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /turns/{turn}", rn.start)
	mux.HandleFunc("GET /turns/{turn}/events", rn.events)
	mux.HandleFunc("POST /turns/{turn}/cancel", func(w http.ResponseWriter, r *http.Request) {
		rn.tell(w, r, func(turn *Turn) { turn.stops <- struct{}{} })
	})
	mux.HandleFunc("PUT /turns/{turn}/approvals/{request}", func(w http.ResponseWriter, r *http.Request) {
		var answer struct {
			Option string `json:"option_id"`
		}
		if json.NewDecoder(r.Body).Decode(&answer) != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		rn.tell(w, r, func(turn *Turn) { turn.answers <- [2]string{r.PathValue("request"), answer.Option} })
	})
	mux.ServeHTTP(w, r)
}

// has returns the turn that the request is about, if the runner has it.
func (rn *runner) has(r *http.Request) *Turn {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if rn.turn == nil || rn.turn.ID != r.PathValue("turn") {
		return nil
	}
	return rn.turn
}

func (rn *runner) start(w http.ResponseWriter, r *http.Request) {
	turn := &Turn{
		Sandbox: rn.sandbox,
		ID:      r.PathValue("turn"),
		closed:  rn.service.closed,
		stops:   make(chan struct{}, 16),
		answers: make(chan [2]string, 16),
		changed: make(chan struct{}),
	}
	if json.NewDecoder(r.Body).Decode(turn) != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	rn.mu.Lock()
	defer rn.mu.Unlock()
	switch {
	case rn.turn != nil && rn.turn.ID == turn.ID:
	case rn.turn != nil && !rn.turn.over():
		w.WriteHeader(http.StatusConflict)
		return
	default:
		rn.turn = turn
		rn.service.turns <- turn
	}
	w.WriteHeader(http.StatusNoContent)
}

func (rn *runner) tell(w http.ResponseWriter, r *http.Request, tell func(*Turn)) {
	turn := rn.has(r)
	if turn == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	tell(turn)
	w.WriteHeader(http.StatusNoContent)
}

// events sends the events of the turn as server-sent events, from the one
// after Last-Event-ID, until the turn ends.
func (rn *runner) events(w http.ResponseWriter, r *http.Request) {
	turn := rn.has(r)
	if turn == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	after, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	turn.mu.Lock()
	cuts := turn.cuts
	turn.mu.Unlock()
	for {
		turn.mu.Lock()
		events := turn.events[min(after, len(turn.events)):]
		ended, changed, cut := turn.ended, turn.changed, turn.cuts != cuts
		turn.mu.Unlock()
		if cut {
			// The connection is dropped, and the answer has no end.
			panic(http.ErrAbortHandler)
		}
		for _, event := range events {
			after++
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", after, event.name, event.data)
		}
		w.(http.Flusher).Flush()
		if ended {
			return
		}
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		case <-turn.closed:
			return
		}
	}
}

func (t *Turn) over() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ended
}

// change lets those who read the events of the turn look again.
func (t *Turn) change() {
	close(t.changed)
	t.changed = make(chan struct{})
}

// Tell adds an event to the turn. The events finished and failed end it:
// what is told after them is not heard.
func (t *Turn) Tell(name string, data any) {
	encoded, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended {
		return
	}
	t.events = append(t.events, event{name, encoded})
	t.ended = name == "finished" || name == "failed"
	t.change()
}

// Cut cuts off those who read the events of the turn.
func (t *Turn) Cut() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cuts++
	t.change()
}

// Stopped waits until the turn was told to stop.
func (t *Turn) Stopped(tb testing.TB) {
	tb.Helper()
	select {
	case <-t.stops:
	case <-time.After(patience):
		tb.Fatalf("the turn %s was not told to stop", t.ID)
	}
}

// Answered waits until a request of the turn was answered, and returns the
// request and the option that was chosen.
func (t *Turn) Answered(tb testing.TB) (request, option string) {
	tb.Helper()
	select {
	case answer := <-t.answers:
		return answer[0], answer[1]
	case <-time.After(patience):
		tb.Fatalf("no request of the turn %s was answered", t.ID)
		return "", ""
	}
}
