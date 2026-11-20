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
	"os"
	"strconv"
	"strings"
	"sync"
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
	"aviary/supervisor/internal/sandbox/sandboxtest"
	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/secret"
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

// The tests of this package run whole Supervisors, as serveOn serves them,
// on records and an event log that are in memory. Their sandbox service is
// one in which nothing runs, so a test plays the part of the runner for the
// turns that it sends: it is given the turn, and tells what comes of it.

// patience is how long a test waits for what the Supervisor is to do, and
// soon how long for what it is to do at once: a Supervisor that does it only
// when it next looks, seconds later, is too late.
const (
	patience = 10 * time.Second
	soon     = 2 * time.Second
)

func TestMain(m *testing.M) {
	handover = 100 * time.Millisecond
	os.Exit(m.Run())
}

// installation is what the Supervisors of a test share: their records, an
// event log, a sandbox service and the keys that secrets are sealed with.
type installation struct {
	t *testing.T
	// records are the records of the installation, as its database holds
	// them.
	records *memorystore.Store
	// service is the sandbox service of the installation, and sandboxes how
	// its Supervisors ask it.
	service   *sandboxtest.Service
	sandboxes *sandbox.Client
	config    driver.Config
	keys      *secret.Keys
	// tokens are the tokens of the turns that were dispatched, by the IDs
	// of the turns.
	tokens *sync.Map
	// storeHosts are the hosts at which the installation allows the secret
	// stores of its projects, and secrets is what those stores hold. They
	// are of the kind that the tests have a plugin for.
	storeHosts []string
	secrets    *shelves
	// log is the event log of the installation, which its Supervisors share.
	log *memorylog.Log
	// callers are who may use the API of the installation. Most
	// installations of the tests have no identity provider.
	callers api.Callers
}

// install returns an installation without Supervisors, whose release has
// had the templates of its harnesses built. The functions change how its
// turns are run from what a chart with default values sets. A test that
// waits for what the Supervisors do only when they sweep sets the sweep
// lower.
func install(t *testing.T, changes ...func(*driver.Config)) *installation {
	t.Helper()
	service := sandboxtest.New(t)
	sandboxes, err := sandbox.New(service.URL, sandboxtest.Key)
	if err != nil {
		t.Fatal(err)
	}
	size := sandbox.Size{CPU: "1", Memory: "2Gi", Disk: "4Gi"}
	config := driver.Config{
		Templates: sandbox.Templates{
			Harnesses: map[string]sandbox.Harness{
				"opencode": {Image: "opencode", Sandbox: size},
				"codex":    {Image: "codex", Sandbox: size},
			},
			Store:              "s3://sandboxes/templates",
			MaxToolOutputBytes: 65536,
			Poll:               5 * time.Millisecond,
		},
		Hosts:              []string{"*"},
		MaxStartup:         90 * time.Second,
		MaxTurn:            30 * time.Minute,
		MaxQueueWait:       2 * time.Minute,
		MaxQueuedTurns:     500,
		MaxConcurrentTurns: 50,
	}
	if err := sandboxes.Bake(context.Background(), config.Templates); err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		change(&config)
	}
	return &installation{
		t: t, records: memorystore.New(), service: service, sandboxes: sandboxes, config: config, tokens: &sync.Map{},
		keys: testenv.Keys(t), storeHosts: []string{storeHost}, secrets: &shelves{}, log: memorylog.New(),
	}
}

// resolvedAt is where the Supervisors of the tests say that the egress of
// a sandbox asks about its requests.
const resolvedAt = "https://resolver.example.com:9443"

// dispatching are the records as a Supervisor of the tests has them. They
// note the token of every turn that is dispatched, which only the one who
// dispatches a turn is told.
type dispatching struct {
	store.Store
	tokens *sync.Map
}

func (d dispatching) DispatchTurns(ctx context.Context, limit int) ([]store.Dispatch, error) {
	dispatches, err := d.Store.DispatchTurns(ctx, limit)
	for _, dispatch := range dispatches {
		d.tokens.Store(dispatch.TurnID.String(), dispatch.Token)
	}
	return dispatches, err
}

// stores returns the secret stores of the installation as one of its
// Supervisors has them: it asks them with the plugin of the tests, and
// remembers by itself what it read.
func (i *installation) stores() *secret.Stores {
	i.t.Helper()
	stores, err := secret.NewStores(i.storeHosts, time.Minute, scripted{i.secrets})
	if err != nil {
		i.t.Fatal(err)
	}
	i.t.Cleanup(stores.Close)
	return stores
}

// supervisor is a Supervisor that serves.
type supervisor struct {
	*installation
	api, metrics string
	// resolver is where the Supervisor answers the egress of the sandbox
	// service, without TLS here.
	resolver string
	proxies  authv3.AuthorizationClient
	// stop tells the Supervisor to stop, as Kubernetes does, and returns
	// when it has.
	stop func()
	// token is what the requests of the test tell who they are with, if
	// they tell it.
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
	on := ports{api: listen(), authz: listen(), metrics: listen(), resolver: listen()}
	connect := func(listener net.Listener) *grpc.ClientConn {
		connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			i.t.Fatal(err)
		}
		i.t.Cleanup(func() { connection.Close() })
		return connection
	}
	s := &supervisor{
		installation: i,
		api:          "http://" + on.api.Addr().String(),
		metrics:      "http://" + on.metrics.Addr().String(),
		resolver:     "http://" + on.resolver.Addr().String(),
		proxies:      authv3.NewAuthorizationClient(connect(on.authz)),
	}
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveOn(ctx, on, dispatching{i.records, i.tokens}, ev, i.sandboxes, i.config, i.keys, stores, i.callers, resolvedAt)
	}()
	s.stop = sync.OnceFunc(func() {
		stop()
		select {
		case err := <-stopped:
			if err != nil {
				i.t.Errorf("the Supervisor stopped with: %v", err)
			}
		case <-time.After(patience):
			i.t.Error("the Supervisor did not stop")
		}
	})
	i.t.Cleanup(s.stop)
	return s
}

// eventually waits for the condition to hold, for as long as is given.
func eventually(t *testing.T, within time.Duration, what string, holds func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !holds() {
		if time.Now().After(deadline) {
			t.Fatalf("after %s, %s", within, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The API, as its clients see it.

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

// request sends a request to the API. It returns the status of the answer
// and decodes the body of the answer into the last argument, if there is one.
func (s *supervisor) request(method, path, body string, into any) (int, http.Header) {
	s.t.Helper()
	request, err := http.NewRequest(method, s.api+path, strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	response, err := http.DefaultClient.Do(request)
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
			s.t.Fatalf("%s %s was answered with %d %s: %v", method, path, response.StatusCode, answer, err)
		}
	}
	return response.StatusCode, response.Header
}

// create creates a resource and returns its ID.
func (s *supervisor) create(path, body string) string {
	s.t.Helper()
	var created struct{ ID string }
	if status, _ := s.request("POST", path, body, &created); status != http.StatusCreated {
		s.t.Fatalf("POST %s %s was answered with %d", path, body, status)
	}
	return created.ID
}

// session creates a session of a new agent of a new project. The agent is
// as the body says, or one of OpenCode without one.
func (s *supervisor) session(agent ...string) string {
	s.t.Helper()
	body := `{"harness": "opencode"}`
	if len(agent) > 0 {
		body = agent[0]
	}
	return s.create("/v1/agents/"+s.agent(s.create("/v1/projects", `{"name": "test"}`), body)+"/sessions", "")
}

// agent creates an agent of the project as the body says. Its model is one
// of a new provider of the project.
func (s *supervisor) agent(project, body string) string {
	s.t.Helper()
	var settings map[string]any
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		s.t.Fatal(err)
	}
	provider := s.create("/v1/projects/"+project+"/providers",
		`{"name": "a provider", "url": "https://api.example.com/v1", "api_key": {"value": "sk-a-key"}}`)
	settings["model"] = map[string]string{"provider": provider, "name": "a-model"}
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
		s.t.Fatalf("the message %q was answered with %d", text, status)
	}
	return accepted
}

// turn returns the record of a turn.
func (s *supervisor) turn(id string) turn {
	s.t.Helper()
	var record turn
	if status, _ := s.request("GET", "/v1/turns/"+id, "", &record); status != http.StatusOK {
		s.t.Fatalf("the turn %s was answered with %d", id, status)
	}
	return record
}

// event is an event of a turn as a client of the API reads it.
type event struct {
	ID, Type, Data string
}

// reader is a client that reads the events of a turn as they come.
type reader struct {
	t *testing.T
	// token is what the reader tells who it is with, if it tells it.
	turn, token string
	events      chan event
	// ended holds how the stream ended, once it has: without an error if the
	// Supervisor ended it.
	ended chan error
}

// read begins to read the events of the turn after the one with the ID, or
// all of them without one.
func (s *supervisor) read(turn, after string) *reader {
	ctx, cancel := context.WithCancel(context.Background())
	s.t.Cleanup(cancel)
	r := &reader{t: s.t, turn: turn, token: s.token, events: make(chan event, 10000), ended: make(chan error, 1)}
	go func() { r.ended <- r.stream(ctx, s.api, after) }()
	return r
}

func (r *reader) stream(ctx context.Context, api, after string) error {
	request, err := http.NewRequestWithContext(ctx, "GET", api+"/v1/turns/"+r.turn+"/events", nil)
	if err != nil {
		return err
	}
	if after != "" {
		request.Header.Set("Last-Event-ID", after)
	}
	if r.token != "" {
		request.Header.Set("Authorization", "Bearer "+r.token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the events of turn %s were answered with %d", r.turn, response.StatusCode)
	}
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

// next waits for the next event that tells what the turn does.
func (r *reader) next() event {
	r.t.Helper()
	for {
		select {
		case event := <-r.events:
			if event.Type != "turn.queued" {
				return event
			}
		case <-time.After(soon):
			r.t.Fatalf("the reader of turn %s got no event", r.turn)
		}
	}
}

// rest waits for the Supervisor to end the stream, for as long as is given,
// and returns the events that came since the reader last took one.
func (r *reader) rest(within time.Duration) []event {
	r.t.Helper()
	select {
	case err := <-r.ended:
		if err != nil {
			r.t.Fatalf("the events of turn %s broke off: %v", r.turn, err)
		}
	case <-time.After(within):
		r.t.Fatalf("after %s, the events of turn %s have not ended", within, r.turn)
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

// follow reads the events of a turn to its end, at which the Supervisor
// ends the stream.
func (s *supervisor) follow(turn string) []event {
	s.t.Helper()
	return s.read(turn, "").rest(patience)
}

// watch reads along with the events of the turn from now on, as a client
// does while a turn runs. The function that it returns is called when the
// turn has ended: the client is to have the last event by then.
func (s *supervisor) watch(turn string) func() []event {
	reader := s.read(turn, "")
	return func() []event {
		s.t.Helper()
		return reader.rest(soon)
	}
}

// types returns the types of the events, without the ones that tell a turn
// its place in the queue: whether a turn that need not wait is told one
// depends on how fast it is dispatched.
func types(events []event) []string {
	types := []string{}
	for _, event := range events {
		if event.Type != "turn.queued" {
			types = append(types, event.Type)
		}
	}
	return types
}

// The runners in the sandboxes, which the test plays.

// runner is the runner of a session, with a turn that it was given.
type runner struct {
	*sandboxtest.Turn
	installation *installation
	// seq is the number of the last event that it told of the turn.
	seq uint64
}

// given waits for a runner to be given the turn, and returns it. A turn
// that has room to run is dispatched at once.
func (i *installation) given(turn string) *runner {
	i.t.Helper()
	return &runner{Turn: i.service.TurnWith(i.t, turn), installation: i}
}

// token waits for the turn to be dispatched, and returns its token.
func (i *installation) token(turn string) string {
	i.t.Helper()
	var token string
	eventually(i.t, soon, "the turn "+turn+" is not dispatched", func() bool {
		noted, found := i.tokens.Load(turn)
		if found {
			token = noted.(string)
		}
		return found
	})
	return token
}

// message is what a runner tells of its turn: an event.
type message struct {
	name string
	data any
}

// report tells the next event of the turn, and waits for a Supervisor to
// record it. After the last event of a turn, it waits for the turn to end.
func (r *runner) report(told message) {
	i := r.installation
	i.t.Helper()
	r.Tell(told.name, told.data)
	r.seq++
	id := uuid.MustParse(r.ID)
	eventually(i.t, soon, fmt.Sprintf("the event %d of the turn %s is not recorded", r.seq, r.ID), func() bool {
		recorded, err := events.New(i.log).Received(context.Background(), id)
		return err == nil && recorded >= r.seq
	})
	if told.name == "finished" || told.name == "failed" {
		eventually(i.t, soon, "the turn "+r.ID+" has not ended", func() bool {
			turn, err := i.records.Turn(context.Background(), id)
			return err == nil && turn.EndedAt != nil
		})
	}
}

// lose takes the sandbox of the runner away, as a node that is lost does.
func (r *runner) lose() {
	i := r.installation
	i.t.Helper()
	if err := i.sandboxes.DeleteSandbox(context.Background(), r.Sandbox); err != nil {
		i.t.Fatal(err)
	}
	r.Cut()
}

// The events of a runner.

func started(harnessSession string) message {
	return message{"started", map[string]string{"harness_session_id": harnessSession}}
}

func said(text string) message {
	return message{"message.delta", map[string]string{"text": text}}
}

// finished is the event that the harness ended the turn, with the token
// usage that it reported, if it reported any.
func finished(stopReason string, usage *store.Usage) message {
	data := map[string]any{"stop_reason": stopReason}
	if usage != nil {
		data["usage"] = usage
	}
	return message{"finished", data}
}

// failed is the event that the runner could not finish the turn.
func failed(reason, text string) message {
	return message{"failed", map[string]string{"reason": reason, "message": text}}
}

// run has the runner go through a turn in which the agent says the text.
func (s *supervisor) run(turn, text string) {
	s.t.Helper()
	r := s.given(turn)
	r.report(started("harness-session"))
	r.report(said(text))
	r.report(finished("end_turn", nil))
}

// The Egress Proxy, which asks whose request a token belongs to.

// check asks the Supervisor about a request as the proxy does: a model
// request for the path, which carries the token as its bearer token, or one
// through the forward proxy, which carries it as the password.
func (s *supervisor) check(token string, forward bool, path string) *authv3.CheckResponse {
	s.t.Helper()
	headers, extensions := map[string]string{"authorization": "Bearer " + token}, map[string]string{"listener": "llm"}
	if forward {
		headers = map[string]string{"proxy-authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("turn:"+token))}
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

// origin returns where the Supervisor says a request with the token comes
// from, or nothing if it refuses the request.
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

// cancelled waits for the Supervisor to tell the runner to stop its turn.
func (r *runner) cancelled() {
	r.installation.t.Helper()
	r.Stopped(r.installation.t)
}

// asks is the event that the harness asks for approval before it runs the
// command, with the options that the fake harness of the runner's tests has.
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

// answered is the event that the harness has its answer to the request.
func answered(id, outcome, option string) message {
	data := map[string]string{"id": id, "outcome": outcome}
	if option != "" {
		data["option_id"] = option
	}
	return message{"approval.resolved", data}
}

// answer waits for the Supervisor to give the runner the answer of a person
// to a request for approval, and returns the request and the option.
func (r *runner) answer() (id, option string) {
	r.installation.t.Helper()
	return r.Answered(r.installation.t)
}

// The metrics, as Prometheus reads them.

// metric adds up the series of the Supervisor whose names and labels begin
// as given.
func (s *supervisor) metric(series string) float64 {
	s.t.Helper()
	response, err := http.Get(s.metrics + "/metrics")
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
			s.t.Fatalf("the metrics have the line %q", lines.Text())
		}
		sum += number
	}
	if err := lines.Err(); err != nil {
		s.t.Fatal(err)
	}
	return sum
}
