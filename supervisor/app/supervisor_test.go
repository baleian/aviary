package app

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"google.golang.org/grpc/metadata"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"aviary/supervisor/events"
	memorylog "aviary/supervisor/events/memory"
	"aviary/supervisor/identity"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/internal/gateway"
	runnerv1 "aviary/supervisor/internal/gen/aviary/runner/v1"
	"aviary/supervisor/internal/testenv"
	"aviary/supervisor/secret"
	memorystore "aviary/supervisor/store/memory"
)

// The tests of this package run whole Supervisors, as serveOn serves them,
// on records and an event log that are in memory. Their cluster is a fake
// in which no pod runs, so a test plays the part of the cluster for the
// turns that it sends: it is the runner that takes a turn, and it ends the
// turn's Job.

// patience is how long a test waits for what the Supervisor is to do, and
// soon how long for what it is to do at once: a Supervisor that does it only
// when it next looks, seconds later, is too late.
const (
	patience = 10 * time.Second
	soon     = 2 * time.Second
)

// namespace is where the Supervisor runs the turn pods.
const namespace = "aviary-runtime"

func TestMain(m *testing.M) {
	handover = 100 * time.Millisecond
	os.Exit(m.Run())
}

// installation is what the Supervisors of a test share: their records, an
// event log, a cluster and the keys that secrets are sealed with.
type installation struct {
	t *testing.T
	// records are the records of the installation, as its database holds
	// them.
	records *memorystore.Store
	cluster *fake.Clientset
	config  driver.Config
	keys    *secret.Keys
	// storeHosts are the hosts at which the installation allows the secret
	// stores of its projects, and secrets is what those stores hold. They
	// are of the kind that the tests have a plugin for.
	storeHosts []string
	secrets    *shelves
	// log is the event log of the installation, which its Supervisors share.
	log *memorylog.Log
	// sweep is how often its Supervisors look for what no Supervisor
	// announced. A test that waits for that sets it lower.
	sweep time.Duration
	// callers is the identity provider of the installation. Most
	// installations of the tests have none.
	callers identity.Provider
}

// install returns an installation without Supervisors. The functions change
// how its turns are run from what a chart with default values sets.
func install(t *testing.T, changes ...func(*driver.Config)) *installation {
	t.Helper()
	config := driver.Config{
		// Addresses, which the driver does not look up.
		GatewayURL:  "http://127.0.0.1:9090",
		RunnerImage: "runner",
		LLMBaseURL:  "http://127.0.0.2:8080",
		ProxyURL:    "http://127.0.0.2:3128",
		Harnesses: map[string]driver.Harness{
			"opencode": {Image: "opencode"},
			"codex":    {Image: "codex"},
		},
		MaxToolOutputBytes: 65536,
		MaxStartup:         90 * time.Second,
		MaxTurn:            30 * time.Minute,
		MaxReconnect:       time.Minute,
		MaxQueueWait:       2 * time.Minute,
		MaxQueuedTurns:     500,
		MaxConcurrentTurns: 50,
	}
	for _, change := range changes {
		change(&config)
	}
	return &installation{
		t: t, records: memorystore.New(), cluster: fake.NewClientset(), config: config,
		keys: testenv.Keys(t), storeHosts: []string{storeHost}, secrets: &shelves{}, log: memorylog.New(), sweep: gateway.Sweep,
	}
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
	runners      runnerv1.RunnerServiceClient
	proxies      authv3.AuthorizationClient
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
	on := ports{api: listen(), gateway: listen(), authz: listen(), metrics: listen()}
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
		runners:      runnerv1.NewRunnerServiceClient(connect(on.gateway)),
		proxies:      authv3.NewAuthorizationClient(connect(on.authz)),
	}
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveOn(ctx, on, i.records, ev, i.cluster, i.config, i.keys, stores, i.callers, i.sweep)
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
	t      *testing.T
	turn   string
	events chan event
	// ended holds how the stream ended, once it has: without an error if the
	// Supervisor ended it.
	ended chan error
}

// read begins to read the events of the turn after the one with the ID, or
// all of them without one.
func (s *supervisor) read(turn, after string) *reader {
	ctx, cancel := context.WithCancel(context.Background())
	s.t.Cleanup(cancel)
	r := &reader{t: s.t, turn: turn, events: make(chan event, 10000), ended: make(chan error, 1)}
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

// The cluster, which the test plays.

// job waits for the Job that runs the turn and returns it. A turn that has
// room to run is dispatched at once.
func (i *installation) job(turn string) *batchv1.Job {
	i.t.Helper()
	var job *batchv1.Job
	eventually(i.t, soon, "the turn "+turn+" has no Job", func() bool {
		found, err := i.cluster.BatchV1().Jobs(namespace).Get(context.Background(), "turn-"+turn, metav1.GetOptions{})
		job = found
		return err == nil
	})
	return job
}

// jobs returns the names of the Jobs that the cluster holds.
func (i *installation) jobs() []string {
	i.t.Helper()
	jobs, err := i.cluster.BatchV1().Jobs(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		i.t.Fatal(err)
	}
	names := []string{}
	for _, job := range jobs.Items {
		names = append(names, job.Name)
	}
	return names
}

// token returns the token that the pod of the Job holds for its turn.
func token(job *batchv1.Job) string {
	return told(job, "AVIARY_TURN_TOKEN")
}

// told returns what the agent container of the Job has in the variable of
// that name, which is nothing if it has no such variable.
func told(job *batchv1.Job, name string) string {
	for _, variable := range job.Spec.Template.Spec.Containers[0].Env {
		if variable.Name == name {
			return variable.Value
		}
	}
	return ""
}

// end ends the Job the way Kubernetes does when the pod of the Job has
// stopped: as complete, or as failed. The pod is left behind in the state
// that is given, if one is.
func (i *installation) end(job *batchv1.Job, outcome batchv1.JobConditionType, pod *corev1.PodStatus) {
	i.t.Helper()
	i.endAs(job, batchv1.JobCondition{Type: outcome, Status: corev1.ConditionTrue}, pod)
}

// endAs ends the Job with the condition that Kubernetes gives it.
func (i *installation) endAs(job *batchv1.Job, condition batchv1.JobCondition, pod *corev1.PodStatus) {
	i.t.Helper()
	ctx := context.Background()
	if pod != nil {
		_, err := i.cluster.CoreV1().Pods(namespace).Create(ctx, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: job.Name, Namespace: namespace, Labels: job.Labels},
			Status:     *pod,
		}, metav1.CreateOptions{})
		if err != nil {
			i.t.Fatal(err)
		}
	}
	ended := job.DeepCopy()
	ended.Status.Conditions = append(ended.Status.Conditions, condition)
	if _, err := i.cluster.BatchV1().Jobs(namespace).UpdateStatus(ctx, ended, metav1.UpdateOptions{}); err != nil {
		i.t.Fatal(err)
	}
}

// stopped is the state of a pod whose agent container has ended as given.
func stopped(agent corev1.ContainerStateTerminated) *corev1.PodStatus {
	return &corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
		Name: "agent", State: corev1.ContainerState{Terminated: &agent},
	}}}
}

// The runner of a turn, which the test plays.

// runner is the runner of a turn. It numbers its reports as a runner does.
type runner struct {
	t     *testing.T
	token string
	// id is the name that the runner made up for itself. A runner of an older
	// release has none.
	id   string
	seq  uint64
	call *call
}

// call is one call of a runner to a Supervisor.
type call struct {
	stream   grpc.BidiStreamingClient[runnerv1.RunTurnRequest, runnerv1.RunTurnResponse]
	start    *runnerv1.Start
	received chan uint64
	cancels  chan struct{}
	answers  chan *runnerv1.Approval
	// ended holds how the Supervisor ended the call, once it has.
	ended chan error
	hang  context.CancelFunc
}

// runner returns a runner for the turn that the token opens.
func (i *installation) runner(token string) *runner {
	return &runner{t: i.t, token: token, id: uuid.NewString()}
}

// attach calls the Supervisor, as a runner does at its start and when it has
// lost its Supervisor. It returns what the Supervisor starts the turn with,
// or the error that it ends the call with.
func (r *runner) attach(s *supervisor) (*runnerv1.Start, error) {
	r.t.Helper()
	pairs := []string{"authorization", "Bearer " + r.token}
	if r.id != "" {
		pairs = append(pairs, "aviary-runner", r.id)
	}
	ctx, hang := context.WithCancel(metadata.AppendToOutgoingContext(context.Background(), pairs...))
	r.t.Cleanup(hang)
	stream, err := s.runners.RunTurn(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	first, err := stream.Recv()
	if err != nil {
		hang()
		return nil, err
	}
	if first.GetStart() == nil {
		r.t.Fatalf("the Supervisor began the call with %v", first)
	}
	made := &call{
		stream: stream, start: first.GetStart(), hang: hang,
		received: make(chan uint64, 10000), cancels: make(chan struct{}, 100), ended: make(chan error, 1),
		answers: make(chan *runnerv1.Approval, 100),
	}
	go func() {
		for {
			response, err := stream.Recv()
			switch {
			case err != nil:
				made.ended <- err
				return
			case response.GetReceived() != nil:
				made.received <- response.GetReceived().GetSeq()
			case response.GetCancel() != nil:
				made.cancels <- struct{}{}
			case response.GetApproval() != nil:
				made.answers <- response.GetApproval()
			}
		}
	}()
	r.call = made
	return made.start, nil
}

// take has a new runner take the turn of the Job from the Supervisor.
func (s *supervisor) take(job *batchv1.Job) *runner {
	s.t.Helper()
	r := s.runner(token(job))
	if _, err := r.attach(s); err != nil {
		s.t.Fatalf("the runner was not given its turn: %v", err)
	}
	return r
}

// send sends a message with the number, and returns the number that the
// Supervisor answers with or the error that it ends the call with.
func (r *runner) send(seq uint64, message *runnerv1.RunTurnRequest) (uint64, error) {
	r.t.Helper()
	message.Seq = seq
	if err := r.call.stream.Send(message); err != nil && !errors.Is(err, io.EOF) {
		r.t.Fatal(err)
	}
	select {
	case received := <-r.call.received:
		return received, nil
	case err := <-r.call.ended:
		r.call.ended <- err
		return 0, err
	case <-time.After(patience):
		r.t.Fatalf("the Supervisor did not answer message %d", seq)
		return 0, nil
	}
}

// report sends the next message of the turn, which the Supervisor is to
// record.
func (r *runner) report(message *runnerv1.RunTurnRequest) {
	r.t.Helper()
	r.seq++
	if received, err := r.send(r.seq, message); err != nil || received != r.seq {
		r.t.Fatalf("message %d was answered with %d: %v", r.seq, received, err)
	}
}

// The messages of a runner.

func started(harnessSession string) *runnerv1.RunTurnRequest {
	return &runnerv1.RunTurnRequest{Message: &runnerv1.RunTurnRequest_Started{
		Started: &runnerv1.Started{HarnessSessionId: harnessSession},
	}}
}

func said(text string) *runnerv1.RunTurnRequest {
	return &runnerv1.RunTurnRequest{Message: &runnerv1.RunTurnRequest_MessageDelta{
		MessageDelta: &runnerv1.MessageDelta{Text: text},
	}}
}

func finished(stopReason string, usage *runnerv1.Usage) *runnerv1.RunTurnRequest {
	return &runnerv1.RunTurnRequest{Message: &runnerv1.RunTurnRequest_Finished{
		Finished: &runnerv1.Finished{StopReason: stopReason, Usage: usage},
	}}
}

// leave ends the runner's side of the call, as a runner does at the end of
// its turn, and waits for the Supervisor to end the call.
func (r *runner) leave() {
	r.t.Helper()
	if err := r.call.stream.CloseSend(); err != nil {
		r.t.Fatal(err)
	}
	select {
	case err := <-r.call.ended:
		if !errors.Is(err, io.EOF) {
			r.t.Fatalf("the Supervisor ended the call of a runner that had done with: %v", err)
		}
	case <-time.After(patience):
		r.t.Fatal("the Supervisor did not end the call of a runner that had done")
	}
}

// run has the runner go through a turn in which the agent says the text,
// and ends the turn's Job as complete.
func (s *supervisor) run(job *batchv1.Job, text string) {
	s.t.Helper()
	r := s.take(job)
	r.report(started("harness-session"))
	r.report(said(text))
	r.report(finished("end_turn", nil))
	r.leave()
	s.end(job, batchv1.JobComplete, nil)
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
	r.t.Helper()
	select {
	case <-r.call.cancels:
	case err := <-r.call.ended:
		r.t.Fatalf("the Supervisor ended the call of a runner whose turn was cancelled: %v", err)
	case <-time.After(soon):
		r.t.Fatal("the runner was not told to stop its turn")
	}
}

// asks is the report that the harness asks for approval before it runs the
// command, with the options that the fake harness of the runner's tests has.
func asks(id, command string) *runnerv1.RunTurnRequest {
	return &runnerv1.RunTurnRequest{Message: &runnerv1.RunTurnRequest_ApprovalRequested{
		ApprovalRequested: &runnerv1.ApprovalRequested{
			Id:       id,
			ToolCall: &runnerv1.ToolUpdate{Id: "call-" + id, Title: &command},
			Options: []*runnerv1.ApprovalOption{
				{Id: "once", Kind: "allow_once", Name: "Once"},
				{Id: "never", Kind: "reject_once", Name: "Never"},
			},
		},
	}}
}

// answered is the report that the harness has its answer to the request.
func answered(id, outcome, option string) *runnerv1.RunTurnRequest {
	resolved := &runnerv1.ApprovalResolved{Id: id, Outcome: outcome}
	if option != "" {
		resolved.OptionId = &option
	}
	return &runnerv1.RunTurnRequest{Message: &runnerv1.RunTurnRequest_ApprovalResolved{ApprovalResolved: resolved}}
}

// answer waits for the Supervisor to give the runner the answer of a person
// to a request for approval, and returns the request and the option.
func (r *runner) answer() (id, option string) {
	r.t.Helper()
	select {
	case answer := <-r.call.answers:
		return answer.GetId(), answer.GetOptionId()
	case err := <-r.call.ended:
		r.t.Fatalf("the Supervisor ended the call of a runner that waits for an answer: %v", err)
	case <-time.After(soon):
		r.t.Fatal("the runner was not given the answer to its request for approval")
	}
	return "", ""
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
