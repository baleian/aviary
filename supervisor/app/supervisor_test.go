package app

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"

	"aviary/supervisor/events"
	memorylog "aviary/supervisor/events/memory"
	"aviary/supervisor/internal/api"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/internal/sandbox/runnertest"
	"aviary/supervisor/internal/sandbox/sandboxtest"
	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

// The tests of this package run whole Supervisors through serveOn, with an
// in-memory store and event log and fake sandboxes in which nothing runs. A
// test plays the runner of each turn and the Egress Proxy.

// patience bounds waits for the Supervisor. soon bounds waits for what it
// must do immediately: it is shorter than the sweep interval, so that work
// left to the next sweep fails the test.
const (
	patience = 10 * time.Second
	soon     = 2 * time.Second
)

// httpClient sends the requests of the tests. It has a transport of its own:
// closing an httptest.Server, as the fake sandboxes do, closes the idle
// connections of the default transport, which can break a request on it
// whose response has no body.
var httpClient = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}

func TestMain(m *testing.M) {
	handover = 100 * time.Millisecond
	os.Exit(m.Run())
}

// installation is the state that the Supervisors of a test share.
type installation struct {
	t       *testing.T
	records *memorystore.Store
	// service fakes the sandboxes of the cluster.
	service *sandboxtest.Service
	config  driver.Config
	keys    *secret.Keys
	// storeHosts are the hosts at which secret stores are allowed, and
	// secrets holds the contents of those stores.
	storeHosts []string
	secrets    *shelves
	log        *memorylog.Log
	// callers has no identity provider unless a test sets one.
	callers api.Callers
}

// install returns an installation without Supervisors. changes modify the
// default driver config; a test that waits for a sweep should shorten the
// sweep interval.
func install(t *testing.T, changes ...func(*driver.Config)) *installation {
	t.Helper()
	config := driver.Config{
		Harnesses: map[string]sandbox.Harness{
			"opencode": {Image: "registry.example.com/harness-opencode:1.0"},
			"codex":    {Image: "registry.example.com/harness-codex:1.0"},
		},
		Models:             &url.URL{Scheme: "http", Host: "127.0.0.2:8080"},
		MaxStartup:         90 * time.Second,
		MaxTurn:            30 * time.Minute,
		MaxQueueWait:       2 * time.Minute,
		MaxQueuedTurns:     500,
		MaxConcurrentTurns: 50,
	}
	for _, change := range changes {
		change(&config)
	}
	return &installation{
		t: t, records: memorystore.New(), service: sandboxtest.New(t), config: config,
		keys: testenv.Keys(t), storeHosts: []string{storeHost}, secrets: &shelves{}, log: memorylog.New(),
	}
}

// token returns the session token that the sandbox's pod was created with.
func (i *installation) token(sandbox string) string {
	i.t.Helper()
	made, found := i.service.Made()[sandbox]
	if !found || made.Pod.Token == "" {
		i.t.Fatalf("sandbox %s has no token", sandbox)
	}
	return made.Pod.Token
}

// stores returns the secret store client of one Supervisor, with a cache of
// its own.
func (i *installation) stores() *secret.Stores {
	i.t.Helper()
	stores, err := secret.NewStores(i.storeHosts, time.Minute, scripted{i.secrets})
	if err != nil {
		i.t.Fatal(err)
	}
	i.t.Cleanup(stores.Close)
	return stores
}

// supervisor is a running Supervisor and its clients.
type supervisor struct {
	*installation
	api, metrics string
	// proxies calls the authz server as the Egress Proxy does.
	proxies authv3.AuthorizationClient
	// stop cancels the Supervisor's context, as SIGTERM does, and waits for
	// it to return.
	stop func()
	// token, if not empty, is sent as the bearer token of API requests.
	token string
}

// supervisor starts a Supervisor of the installation.
func (i *installation) supervisor() *supervisor {
	i.t.Helper()
	ev, stores := events.New(i.log), i.stores()
	listen := func() net.Listener {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			i.t.Fatal(err)
		}
		return listener
	}
	on := ports{api: listen(), authz: listen(), metrics: listen()}
	connection, err := grpc.NewClient(on.authz.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		i.t.Fatal(err)
	}
	i.t.Cleanup(func() { connection.Close() })
	s := &supervisor{
		installation: i,
		api:          "http://" + on.api.Addr().String(),
		metrics:      "http://" + on.metrics.Addr().String(),
		proxies:      authv3.NewAuthorizationClient(connection),
	}
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveOn(ctx, on, i.records, ev, i.service, i.config, i.keys, stores, i.callers)
	}()
	s.stop = sync.OnceFunc(func() {
		stop()
		select {
		case err := <-stopped:
			if err != nil {
				i.t.Errorf("serveOn: %v", err)
			}
		case <-time.After(patience):
			i.t.Error("Supervisor did not stop")
		}
	})
	i.t.Cleanup(s.stop)
	return s
}

// eventually polls holds until it returns true, and fails the test with what
// if that takes longer than within.
func eventually(t *testing.T, within time.Duration, what string, holds func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !holds() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s: %s", within, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// API client.

// turn is a turn as the API returns it.
type turn struct {
	ID                string            `json:"id"`
	SessionID         string            `json:"session_id"`
	Seq               int               `json:"seq"`
	State             string            `json:"state"`
	Text              string            `json:"text"`
	StartedAt         *time.Time        `json:"started_at"`
	StopReason        string            `json:"stop_reason"`
	EndedAt           *time.Time        `json:"ended_at"`
	CancelRequestedAt *time.Time        `json:"cancel_requested_at"`
	Usage             map[string]int64  `json:"usage"`
	Failure           map[string]string `json:"failure"`
	Items             []json.RawMessage `json:"items"`
	Approvals         []json.RawMessage `json:"approvals"`
}

// request sends an API request and returns the response status and header.
// If into is not nil, it decodes the response body into it.
func (s *supervisor) request(method, path, body string, into any) (int, http.Header) {
	s.t.Helper()
	request, err := http.NewRequest(method, s.api+path, strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	if into != nil {
		if err := json.Unmarshal(answer, into); err != nil {
			s.t.Fatalf("%s %s: decode response %d %s: %v", method, path, response.StatusCode, answer, err)
		}
	}
	return response.StatusCode, response.Header
}

// create creates a resource and returns its ID.
func (s *supervisor) create(path, body string) string {
	s.t.Helper()
	var created struct{ ID string }
	if status, _ := s.request("POST", path, body, &created); status != http.StatusCreated {
		s.t.Fatalf("POST %s %s: status %d, want 201", path, body, status)
	}
	return created.ID
}

var lastSlug atomic.Int64

// unique returns a slug that it has not returned before.
func unique(prefix string) string {
	return prefix + "-" + strconv.FormatInt(lastSlug.Add(1), 10)
}

// project creates a project with a slug of its own and returns its ID.
func (s *supervisor) project() string {
	s.t.Helper()
	return s.create("/v1/projects", `{"slug": "`+unique("project")+`"}`)
}

// session creates a session of a new agent in a new project. agent is the
// JSON body of the agent, which defaults to an OpenCode agent.
func (s *supervisor) session(agent ...string) string {
	s.t.Helper()
	body := `{"harness": "opencode"}`
	if len(agent) > 0 {
		body = agent[0]
	}
	return s.create("/v1/agents/"+s.agent(s.project(), body)+"/sessions", "")
}

// agent creates an agent from body and returns its ID. Unless body has them,
// the agent gets a slug of its own and a model of a new provider of the
// project.
func (s *supervisor) agent(project, body string) string {
	s.t.Helper()
	var settings map[string]any
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		s.t.Fatal(err)
	}
	if _, given := settings["slug"]; !given {
		settings["slug"] = unique("agent")
	}
	if _, given := settings["model"]; !given {
		provider := s.create("/v1/projects/"+project+"/providers",
			`{"name": "a provider", "url": "https://api.example.com/v1", "api_key": {"value": "sk-a-key"}}`)
		settings["model"] = map[string]string{"provider": provider, "name": "a-model"}
	}
	with, err := json.Marshal(settings)
	if err != nil {
		s.t.Fatal(err)
	}
	return s.create("/v1/projects/"+project+"/agents", string(with))
}

// send sends a message to the session and returns its turn.
func (s *supervisor) send(session, text string) turn {
	s.t.Helper()
	message, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		s.t.Fatal(err)
	}
	var accepted turn
	if status, _ := s.request("POST", "/v1/sessions/"+session+"/messages", string(message), &accepted); status != http.StatusAccepted {
		s.t.Fatalf("send %q: status %d, want 202", text, status)
	}
	return accepted
}

// turn returns a turn by ID.
func (s *supervisor) turn(id string) turn {
	s.t.Helper()
	var record turn
	if status, _ := s.request("GET", "/v1/turns/"+id, "", &record); status != http.StatusOK {
		s.t.Fatalf("GET turn %s: status %d, want 200", id, status)
	}
	return record
}

// event is a server-sent event.
type event struct {
	ID, Type, Data string
}

// reader streams the events that the API serves at a path.
type reader struct {
	t           *testing.T
	path, token string
	events      chan event
	// opened is closed when the stream is open.
	opened chan struct{}
	// ended receives nil when the Supervisor ends the stream, or the error
	// that broke it.
	ended chan error
}

// open starts streaming the events at path that follow the event ID after,
// or all of them if after is empty.
func (s *supervisor) open(path, after string) *reader {
	ctx, cancel := context.WithCancel(context.Background())
	s.t.Cleanup(cancel)
	r := &reader{
		t: s.t, path: path, token: s.token,
		events: make(chan event, 10000), opened: make(chan struct{}), ended: make(chan error, 1),
	}
	go func() { r.ended <- r.stream(ctx, s.api, after) }()
	return r
}

// read starts streaming the events of the turn that follow the event ID
// after, or all of them if after is empty.
func (s *supervisor) read(turn, after string) *reader {
	return s.open("/v1/turns/"+turn+"/events", after)
}

func (r *reader) stream(ctx context.Context, api, after string) error {
	request, err := http.NewRequestWithContext(ctx, "GET", api+r.path, nil)
	if err != nil {
		return err
	}
	if after != "" {
		request.Header.Set("Last-Event-ID", after)
	}
	if r.token != "" {
		request.Header.Set("Authorization", "Bearer "+r.token)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", r.path, response.StatusCode)
	}
	close(r.opened)
	var next event
	lines := bufio.NewScanner(response.Body)
	lines.Buffer(nil, 1<<20)
	for lines.Scan() {
		field, value, _ := strings.Cut(lines.Text(), ": ")
		switch field {
		case "id":
			next.ID = value
		case "event":
			next.Type = value
		case "data":
			next.Data = value
		case "":
			if next.Type != "" {
				r.events <- next
			}
			next = event{}
		}
	}
	return lines.Err()
}

// next waits for the next event, skipping turn.queued events.
func (r *reader) next() event {
	r.t.Helper()
	for {
		select {
		case event := <-r.events:
			if event.Type != "turn.queued" {
				return event
			}
		case <-time.After(soon):
			r.t.Fatalf("no event at %s", r.path)
		}
	}
}

// rest waits up to within for the Supervisor to end the stream, and returns
// the events that have not been taken yet.
func (r *reader) rest(within time.Duration) []event {
	r.t.Helper()
	select {
	case err := <-r.ended:
		if err != nil {
			r.t.Fatalf("event stream at %s: %v", r.path, err)
		}
	case <-time.After(within):
		r.t.Fatalf("timed out after %s: event stream at %s has not ended", within, r.path)
	}
	var rest []event
	for {
		select {
		case event := <-r.events:
			rest = append(rest, event)
		default:
			return rest
		}
	}
}

// follow returns all events of the turn once the Supervisor ends the stream
// at the end of the turn.
func (s *supervisor) follow(turn string) []event {
	s.t.Helper()
	return s.read(turn, "").rest(patience)
}

// watch starts streaming the events of the turn, as a client does while the
// turn runs. Call the returned function after the turn has ended; it expects
// the stream to end soon after.
func (s *supervisor) watch(turn string) func() []event {
	reader := s.read(turn, "")
	return func() []event {
		s.t.Helper()
		return reader.rest(soon)
	}
}

// types returns the event types without turn.queued, which a turn that does
// not have to wait may or may not get, depending on how fast it is dispatched.
func types(events []event) []string {
	types := []string{}
	for _, event := range events {
		if event.Type != "turn.queued" {
			types = append(types, event.Type)
		}
	}
	return types
}

// Fake runner.

// runner is the runner of a session and the turn that it was given.
type runner struct {
	*runnertest.Turn
	installation *installation
	// seq is the sequence number of the last reported event.
	seq uint64
}

// given waits for the turn to be dispatched to a runner, and returns the
// runner. A turn with a free slot is dispatched immediately.
func (i *installation) given(turn string) *runner {
	i.t.Helper()
	return &runner{Turn: i.service.TurnWith(i.t, turn), installation: i}
}

// message is an event that a runner reports.
type message struct {
	name string
	data any
}

// report sends the next event of the turn and waits until a Supervisor has
// recorded it. After a finished or failed event, it also waits for the turn
// to end.
func (r *runner) report(told message) {
	i := r.installation
	i.t.Helper()
	r.Tell(told.name, told.data)
	r.seq++
	id := uuid.MustParse(r.ID)
	eventually(i.t, soon, fmt.Sprintf("event %d of turn %s is not recorded", r.seq, r.ID), func() bool {
		recorded, err := events.New(i.log).Received(context.Background(), id)
		return err == nil && recorded >= r.seq
	})
	if told.name == "finished" || told.name == "failed" {
		eventually(i.t, soon, "turn "+r.ID+" has not ended", func() bool {
			turn, err := i.records.Turn(context.Background(), id)
			return err == nil && turn.EndedAt != nil
		})
	}
}

// lose deletes the runner's sandbox and its volume, as if it had expired.
func (r *runner) lose() {
	r.installation.service.Lose(r.Sandbox)
}

// Runner events.

func started(harnessSession string) message {
	return message{"started", map[string]string{"harness_session_id": harnessSession}}
}

func said(text string) message {
	return message{"message.delta", map[string]string{"text": text}}
}

// finished is the event that ends a turn, with usage if it is not nil.
func finished(stopReason string, usage *store.Usage) message {
	data := map[string]any{"stop_reason": stopReason}
	if usage != nil {
		data["usage"] = usage
	}
	return message{"finished", data}
}

// failed is the event of a turn that the runner could not finish.
func failed(reason, text string) message {
	return message{"failed", map[string]string{"reason": reason, "message": text}}
}

// run plays the runner through a turn that completes after the agent says
// text.
func (s *supervisor) run(turn, text string) {
	s.t.Helper()
	r := s.given(turn)
	r.report(started("harness-session"))
	r.report(said(text))
	r.report(finished("end_turn", nil))
}

// cancelled waits for the Supervisor to cancel the runner's turn.
func (r *runner) cancelled() {
	r.installation.t.Helper()
	r.Stopped(r.installation.t)
}

// asks is the event of an approval request to run command, with an option to
// allow and one to reject.
func asks(id, command string) message {
	return message{"approval.requested", map[string]any{
		"id":        id,
		"tool_call": map[string]string{"id": "call-" + id, "title": command},
		"options": []map[string]string{
			{"id": "once", "kind": "allow_once", "name": "Once"},
			{"id": "never", "kind": "reject_once", "name": "Never"},
		},
	}}
}

// answered is the event that resolves the approval request id.
func answered(id, outcome, option string) message {
	data := map[string]string{"id": id, "outcome": outcome}
	if option != "" {
		data["option_id"] = option
	}
	return message{"approval.resolved", data}
}

// answer waits for the Supervisor to deliver a user's answer to an approval
// request, and returns the request ID and the option.
func (r *runner) answer() (id, option string) {
	r.installation.t.Helper()
	return r.Answered(r.installation.t)
}

// Fake Egress Proxy.

// check sends an authz check as the Egress Proxy does: for a model request to
// path with token as the bearer token or, if forward, for a forward proxy
// request with token as the password.
func (s *supervisor) check(token string, forward bool, path string) *authv3.CheckResponse {
	s.t.Helper()
	headers, extensions := map[string]string{"authorization": "Bearer " + token}, map[string]string{"listener": "llm"}
	if forward {
		headers = map[string]string{"proxy-authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("session:"+token))}
		extensions["listener"] = "forward"
	}
	answer, err := s.proxies.Check(context.Background(), &authv3.CheckRequest{Attributes: &authv3.AttributeContext{
		Request:           &authv3.AttributeContext_Request{Http: &authv3.AttributeContext_HttpRequest{Headers: headers, Path: path}},
		ContextExtensions: extensions,
	}})
	if err != nil {
		s.t.Fatal(err)
	}
	return answer
}

// origin returns the metadata that the Supervisor attaches to a request with
// token, or nil if it denies the request.
func (s *supervisor) origin(token string, forward bool) map[string]string {
	s.t.Helper()
	answer := s.check(token, forward, "/v1/chat/completions")
	if codes.Code(answer.GetStatus().GetCode()) != codes.OK {
		return nil
	}
	origin := map[string]string{}
	for name, value := range answer.GetDynamicMetadata().GetFields() {
		origin[name] = value.GetStringValue()
	}
	return origin
}

// Metrics.

// metric returns the sum of the Supervisor's series whose name and labels
// start with series.
func (s *supervisor) metric(series string) float64 {
	s.t.Helper()
	response, err := httpClient.Get(s.metrics + "/metrics")
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	var sum float64
	lines := bufio.NewScanner(response.Body)
	for lines.Scan() {
		name, value, found := strings.Cut(lines.Text(), " ")
		if !found || !strings.HasPrefix(name, series) {
			continue
		}
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			s.t.Fatalf("invalid metrics line %q", lines.Text())
		}
		sum += number
	}
	if err := lines.Err(); err != nil {
		s.t.Fatal(err)
	}
	return sum
}

// exposes reports whether the Supervisor's metrics have the line.
func (s *supervisor) exposes(line string) bool {
	s.t.Helper()
	response, err := httpClient.Get(s.metrics + "/metrics")
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	return slices.Contains(strings.Split(string(body), "\n"), line)
}
