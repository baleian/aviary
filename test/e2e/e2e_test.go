// Package e2e tests that the parts of Aviary work together as the chart
// installs them in a cluster. `make e2e` creates a kind cluster, installs
// Aviary with the values in this directory, runs the tests, and deletes the
// cluster if they pass.
//
// The model endpoint is mock-llm, which follows a script: each message to a
// session is an instruction. See test/mock-llm/main.go.
package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// The namespaces of Aviary, of the sandboxes, and of the identity provider
	// and the secret store.
	system  = "aviary-system"
	runtime = "aviary-runtime"
	data    = "aviary-data"
	// patience is how long a test waits for a turn to end, which includes
	// starting the sandbox for the first turn of a session, and the slowest
	// drawl of the tests.
	patience = 3 * time.Minute
	// soon is how long a test waits for something that should follow at once.
	soon = 30 * time.Second
	// idle is sandboxes.idleSeconds in values.yaml, and look how often the
	// Supervisors look at the sandboxes.
	idle = 30 * time.Second
	look = time.Minute
)

var (
	ctx     = context.Background()
	cluster *kubernetes.Clientset
	// apiURL is the Aviary API at a node port of the cluster.
	apiURL = os.Getenv("AVIARY_E2E_API")
	// identity reports whether the installation authenticates requests with
	// the cluster's identity provider, as in a cluster with Keycloak, which
	// `make keycloak` installs.
	identity = os.Getenv("AVIARY_E2E_IDENTITY") != ""
	// harnesses are the names of the installed harnesses. Tests that do not
	// depend on the harness use the first.
	harnesses = strings.Fields(os.Getenv("AVIARY_E2E_HARNESSES"))
)

func TestMain(m *testing.M) {
	kubeconfig := os.Getenv("AVIARY_E2E_KUBECONFIG")
	if kubeconfig == "" || apiURL == "" || len(harnesses) == 0 {
		fmt.Fprintln(os.Stderr, "AVIARY_E2E_KUBECONFIG, AVIARY_E2E_API and AVIARY_E2E_HARNESSES must be set: run the tests with `make e2e`")
		os.Exit(1)
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err == nil {
		// The parallel tests query the API server often.
		config.QPS, config.Burst = 200, 400
		cluster, err = kubernetes.NewForConfig(config)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// eventually waits for holds to report true, and fails the test with what if
// it does not in time.
func eventually(t *testing.T, within time.Duration, what string, holds func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !holds() {
		if time.Now().After(deadline) {
			t.Fatalf("after %s, %s", within, what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// client sends every request on a new connection, so that none goes to a
// Supervisor that has stopped since the previous request. It gives up on a
// connection quickly, since the node port may have routed it to a Supervisor
// that has just gone.
var client = &http.Client{Transport: &http.Transport{
	DisableKeepAlives: true,
	DialContext:       (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
}}

// The users of the cluster's identity provider, from hack/kind/keycloak.yaml.
// The administrator is in the admin group, the member and the teammate are in
// the user group, the teammate is also in team, and the stranger is in no
// group.
const (
	administrator = "alice"
	member        = "bob"
	teammate      = "carol"
	stranger      = "dave"
	team          = "team-a"
)

// tokens caches a token for each user. A token outlasts a run of the tests.
var tokens = struct {
	sync.Mutex
	of map[string]string
}{of: map[string]string{}}

// token returns an access token of the user from the identity provider,
// through the API server proxy. Each user's password is its name.
func token(user string) (string, error) {
	tokens.Lock()
	defer tokens.Unlock()
	if given, ok := tokens.of[user]; ok {
		return given, nil
	}
	form := url.Values{"grant_type": {"password"}, "client_id": {"aviary"}, "username": {user}, "password": {user}}
	raw, err := cluster.CoreV1().RESTClient().Post().Namespace(data).
		Resource("services").Name("keycloak:http").SubResource("proxy").Suffix("realms/aviary/protocol/openid-connect/token").
		SetHeader("Content-Type", "application/x-www-form-urlencoded").Body([]byte(form.Encode())).Do(ctx).Raw()
	var answer struct {
		AccessToken string `json:"access_token"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &answer)
	}
	if err != nil || answer.AccessToken == "" {
		return "", fmt.Errorf("get token for %s from the identity provider: %v: %s (`make keycloak` installs the provider)", user, err, raw)
	}
	tokens.of[user] = answer.AccessToken
	return answer.AccessToken, nil
}

// call sends a request to the API with headers as alternating names and
// values. If the installation authenticates requests and user is not empty,
// the request carries the user's token. A failed dial is retried, since
// nothing was sent: while the Supervisors are replaced, a node may refuse
// connections for a moment.
func call(ctx context.Context, user, method, path, body string, headers ...string) (*http.Response, error) {
	if identity && user != "" {
		given, err := token(user)
		if err != nil {
			return nil, err
		}
		headers = slices.Concat(headers, []string{"Authorization", "Bearer " + given})
	}
	for refused := time.Now().Add(soon); ; time.Sleep(200 * time.Millisecond) {
		request, err := http.NewRequestWithContext(ctx, method, apiURL+path, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		for i := 0; i+1 < len(headers); i += 2 {
			request.Header.Set(headers[i], headers[i+1])
		}
		response, err := client.Do(request)
		var failed *net.OpError
		if errors.As(err, &failed) && failed.Op == "dial" && time.Now().Before(refused) {
			continue
		}
		return response, err
	}
}

// request sends a request to the API as the administrator and returns the
// status code. If into is not nil, the response body is decoded into it.
func request(t *testing.T, method, path, body string, into any) int {
	t.Helper()
	return requestAs(t, administrator, method, path, body, into)
}

// requestAs is request as the user.
func requestAs(t *testing.T, user, method, path, body string, into any) int {
	t.Helper()
	response, err := call(ctx, user, method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if into != nil {
		if err := json.Unmarshal(answer, into); err != nil {
			t.Fatalf("%s %s: decode response %d %s: %v", method, path, response.StatusCode, answer, err)
		}
	}
	return response.StatusCode
}

// create creates a resource and returns its ID.
func create(t *testing.T, path, body string) string {
	t.Helper()
	var created struct{ ID string }
	if status := request(t, "POST", path, body, &created); status != http.StatusCreated {
		t.Fatalf("POST %s %s: got status %d, want 201", path, body, status)
	}
	return created.ID
}

// The mock model server. It accepts only requests with mockKey, which the
// sandboxes do not hold: the Egress Proxy adds it to model requests. It also
// serves a Python package at probeURL.
const (
	mockHost = "mock-llm.aviary-e2e"
	mockURL  = "http://" + mockHost + "/v1"
	mockKey  = "mock-llm-key"
	probeURL = "http://" + mockHost + "/packages/aviary_probe-1.0-py3-none-any.whl"
)

// provider creates a provider of the mock model server in the project, with
// key as its API key, and returns its ID.
func provider(t *testing.T, project, key string) string {
	t.Helper()
	return create(t, "/v1/projects/"+project+"/providers", fmt.Sprintf(`{"name": "mock", "url": %q, "api_key": {"value": %q}}`, mockURL, key))
}

// The cluster's secret store, and supervisor.secretStores.cacheSeconds in
// values.yaml.
const (
	storeAddress = "http://openbao.aviary-data:8200"
	remembered   = 5 * time.Second
)

// rootToken is the root token of the secret store, which `make openbao`
// keeps in a Secret next to it.
var rootToken = sync.OnceValues(func() (string, error) {
	root, err := cluster.CoreV1().Secrets(data).Get(ctx, "openbao-root", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return string(root.Data["token"]), nil
})

// owner sends a request to the secret store with the root token, through the
// API server proxy, and returns the data of the response. statuses are
// accepted besides 200 and 204.
func owner(t *testing.T, method, path string, body any, statuses ...int) map[string]any {
	t.Helper()
	token, err := rootToken()
	if err != nil {
		t.Fatalf("get secret store root token: %v (`make e2e` installs the store)", err)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	answered := cluster.CoreV1().RESTClient().Verb(method).Namespace(data).
		Resource("services").Name("openbao:http").SubResource("proxy").Suffix(path).
		SetHeader("X-Vault-Token", token).Body(encoded).Do(ctx)
	var status int
	answered.StatusCode(&status)
	raw, err := answered.Raw()
	if status == 0 {
		t.Fatalf("secret store %s %s: %v", method, path, err)
	}
	if !slices.Contains(append(statuses, http.StatusOK, http.StatusNoContent), status) {
		t.Fatalf("secret store %s %s: got %d %s", method, path, status, raw)
	}
	var answer struct{ Data map[string]any }
	json.Unmarshal(raw, &answer)
	return answer.Data
}

type agent struct{ id string }

// newProject creates a project and returns its ID. The slug is random, since
// the cluster may have the projects of earlier runs.
func newProject(t *testing.T) string {
	t.Helper()
	return create(t, "/v1/projects", fmt.Sprintf(`{"slug": %q}`, unique("e2e")))
}

// agentOf creates an agent in the project from body, with a slug of its own
// and a model of the provider.
func agentOf(t *testing.T, project, provider, body string) agent {
	t.Helper()
	var settings map[string]any
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		t.Fatal(err)
	}
	settings["slug"] = unique("agent")
	settings["model"] = map[string]string{"provider": provider, "name": "mock"}
	with, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	return agent{create(t, "/v1/projects/"+project+"/agents", string(with))}
}

// newAgent creates an agent from body in a new project, with a model of the
// mock model server.
func newAgent(t *testing.T, body string) agent {
	t.Helper()
	project := newProject(t)
	return agentOf(t, project, provider(t, project, mockKey), body)
}

// session creates a session of the agent and ends it when the test is done.
func (a agent) session(t *testing.T) string {
	t.Helper()
	id := create(t, "/v1/agents/"+a.id+"/sessions", "")
	t.Cleanup(func() { end(t, id) })
	return id
}

// end ends the session, which deletes its sandbox. Sandboxes left behind would
// fill the cluster.
func end(t *testing.T, session string) {
	t.Helper()
	path := "/v1/sessions/" + session
	if request(t, "DELETE", path, "", nil) == http.StatusOK {
		return
	}
	// A failed test may have left a turn running, and a session with a
	// running turn cannot be ended.
	var list struct{ Turns []turn }
	request(t, "GET", path+"/turns?limit=1", "", &list)
	for _, left := range list.Turns {
		request(t, "POST", "/v1/turns/"+left.ID+"/cancel", "", nil)
	}
	eventually(t, patience, "session "+session+" could not be ended and its sandbox is left behind", func() bool {
		return request(t, "DELETE", path, "", nil) == http.StatusOK
	})
}

// turn is a turn as returned by the API.
type turn struct {
	ID         string            `json:"id"`
	Seq        int               `json:"seq"`
	State      string            `json:"state"`
	StartedAt  *time.Time        `json:"started_at"`
	StopReason string            `json:"stop_reason"`
	Usage      map[string]int64  `json:"usage"`
	Failure    map[string]string `json:"failure"`
	Items      []json.RawMessage `json:"items"`
	// Approvals are the pending approval requests.
	Approvals []approvalRequest `json:"approvals"`
}

// approvalRequest is a harness's request for approval of a tool call.
type approvalRequest struct {
	ID       string `json:"id"`
	ToolCall struct {
		Input json.RawMessage `json:"input"`
	} `json:"tool_call"`
	Options []struct{ ID, Kind string } `json:"options"`
}

// said returns the text of the turn's messages.
func (r turn) said() string {
	var said strings.Builder
	for _, encoded := range r.Items {
		var item struct{ Type, Text string }
		if json.Unmarshal(encoded, &item) == nil && item.Type == "message" {
			said.WriteString(item.Text)
		}
	}
	return said.String()
}

// tools returns the turn's tool call items as JSON.
func (r turn) tools() string {
	var tools strings.Builder
	for _, encoded := range r.Items {
		var item struct{ Type string }
		if json.Unmarshal(encoded, &item) == nil && item.Type == "tool_call" {
			tools.Write(encoded)
		}
	}
	return tools.String()
}

// send sends a message to the session and returns its turn.
func send(t *testing.T, session, text string) turn {
	t.Helper()
	message, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		t.Fatal(err)
	}
	var accepted turn
	if status := request(t, "POST", "/v1/sessions/"+session+"/messages", string(message), &accepted); status != http.StatusAccepted {
		t.Fatalf("send %q: got status %d, want 202", text, status)
	}
	return accepted
}

func record(t *testing.T, id string) turn {
	t.Helper()
	var record turn
	if status := request(t, "GET", "/v1/turns/"+id, "", &record); status != http.StatusOK {
		t.Fatalf("get turn %s: got status %d, want 200", id, status)
	}
	return record
}

// event is a server-sent event of a turn.
type event struct {
	ID, Type, Data string
}

// ends reports whether an event of the type ends its turn.
func ends(eventType string) bool {
	switch eventType {
	case "turn.completed", "turn.failed", "turn.cancelled", "turn.expired":
		return true
	}
	return false
}

// reader follows the events of a turn. If the stream breaks before the turn
// ends, as it does when its Supervisor is replaced, reader resumes after the
// last event that came.
type reader struct {
	turn   string
	events chan event
}

func read(t *testing.T, id string) *reader {
	t.Helper()
	following, stop := context.WithTimeout(ctx, patience)
	t.Cleanup(stop)
	r := &reader{turn: id, events: make(chan event, 100000)}
	go func() {
		defer close(r.events)
		var last string
		for following.Err() == nil {
			response, err := call(following, administrator, "GET", "/v1/turns/"+id+"/events", "", "Last-Event-ID", last)
			if err == nil && response.StatusCode != http.StatusOK {
				response.Body.Close()
				err = fmt.Errorf("get events: status %d", response.StatusCode)
			}
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			stream := response.Body
			var next event
			lines := bufio.NewScanner(stream)
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
					if next.Type == "" {
						continue
					}
					if next.ID != "" {
						last = next.ID
					}
					r.events <- next
					if ends(next.Type) {
						stream.Close()
						return
					}
					next = event{}
				}
			}
			stream.Close()
		}
	}()
	return r
}

// until reads events up to the first one for which holds reports true, and
// returns the events read.
func (r *reader) until(t *testing.T, what string, holds func(event) bool) []event {
	t.Helper()
	var events []event
	deadline := time.After(patience)
	for {
		select {
		case next, open := <-r.events:
			if !open {
				t.Fatalf("event stream of turn %s closed before the turn %s; events: %v", r.turn, what, events)
			}
			events = append(events, next)
			if holds(next) {
				return events
			}
		case <-deadline:
			t.Fatalf("after %s, turn %s has not %s; events: %v", patience, r.turn, what, events)
		}
	}
}

// end reads up to the end of the turn and returns its last event.
func (r *reader) end(t *testing.T) event {
	t.Helper()
	events := r.until(t, "ended", func(next event) bool { return ends(next.Type) })
	return events[len(events)-1]
}

// change is a session as the stream of its project's changes sends it.
type change struct {
	ID       string `json:"id"`
	Sandbox  string `json:"sandbox"`
	LastTurn struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"last_turn"`
}

// watch follows the changes of the project's sessions from now on. The
// channel is closed when the stream ends.
func watch(t *testing.T, project string) <-chan change {
	t.Helper()
	following, stop := context.WithTimeout(ctx, patience)
	t.Cleanup(stop)
	response, err := call(following, administrator, "GET", "/v1/projects/"+project+"/events", "")
	if err != nil {
		t.Fatalf("watch project %s: %v", project, err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("watch project %s: got status %d, want 200", project, response.StatusCode)
	}
	changes := make(chan change, 1000)
	go func() {
		defer close(changes)
		defer response.Body.Close()
		lines := bufio.NewScanner(response.Body)
		lines.Buffer(nil, 1<<20)
		for lines.Scan() {
			data, found := strings.CutPrefix(lines.Text(), "data: ")
			var sent change
			if found && json.Unmarshal([]byte(data), &sent) == nil {
				changes <- sent
			}
		}
	}()
	return changes
}

// run sends a message to the session, waits for its turn to complete, and
// returns the turn.
func run(t *testing.T, session, text string) turn {
	t.Helper()
	accepted := send(t, session, text)
	if last := read(t, accepted.ID).end(t); last.Type != "turn.completed" {
		t.Fatalf("turn %s for %q ended with %s %s, want turn.completed", accepted.ID, text, last.Type, last.Data)
	}
	return record(t, accepted.ID)
}

// The cluster.

func pods(t *testing.T, namespace, selector string) []corev1.Pod {
	t.Helper()
	pods, err := cluster.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		t.Fatal(err)
	}
	return pods.Items
}

// running returns the pods of the component that are not terminating.
func running(t *testing.T, component string) []corev1.Pod {
	t.Helper()
	var running []corev1.Pod
	for _, pod := range pods(t, system, "app.kubernetes.io/component="+component) {
		if pod.DeletionTimestamp == nil {
			running = append(running, pod)
		}
	}
	return running
}

// metrics returns the metrics of all running pods of the component together.
func metrics(t *testing.T, component string) string {
	t.Helper()
	var metrics strings.Builder
	for _, pod := range running(t, component) {
		read, err := cluster.CoreV1().RESTClient().Get().Namespace(system).Resource("pods").
			Name(pod.Name + ":9092").SubResource("proxy").Suffix("metrics").Do(ctx).Raw()
		if err != nil {
			t.Fatalf("get metrics of %s: %v", pod.Name, err)
		}
		metrics.Write(read)
	}
	return metrics.String()
}

// recorded returns the access log lines of the Egress Proxies for the turn.
func recorded(t *testing.T, id string) []map[string]any {
	t.Helper()
	var requests []map[string]any
	for _, pod := range running(t, "egress-proxy") {
		log, err := cluster.CoreV1().Pods(system).GetLogs(pod.Name, &corev1.PodLogOptions{}).Stream(ctx)
		if err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(log)
		lines.Buffer(nil, 1<<20)
		for lines.Scan() {
			var line map[string]any
			if json.Unmarshal(lines.Bytes(), &line) == nil && line["turn"] == id {
				requests = append(requests, line)
			}
		}
		log.Close()
	}
	return requests
}

// logged waits for an access log line of the turn with the route, host and
// status, and returns it.
func logged(t *testing.T, id, route, host string, status float64) map[string]any {
	t.Helper()
	var found map[string]any
	eventually(t, soon, fmt.Sprintf("no Egress Proxy access log line with route %s, turn %s, host %s and status %v", route, id, host, status), func() bool {
		for _, line := range recorded(t, id) {
			if line["route"] == route && line["host"] == host && line["status"] == status {
				found = line
			}
		}
		return found != nil
	})
	return found
}

// sandbox is the part of an agent-sandbox Sandbox that the tests read.
type sandbox struct {
	Metadata struct{ Name, UID string }
	Spec     struct{ OperatingMode string }
	Status   struct {
		Conditions []struct{ Type, Status string }
	}
}

const sandboxAPI = "/apis/agents.x-k8s.io/v1beta1/namespaces/" + runtime + "/sandboxes"

// sandboxesOf returns the Sandboxes of the session, or of all sessions if
// session is empty.
func sandboxesOf(t *testing.T, session string) []sandbox {
	t.Helper()
	selector := "aviary.io/session"
	if session != "" {
		selector += "=" + session
	}
	raw, err := cluster.CoreV1().RESTClient().Get().AbsPath(sandboxAPI).Param("labelSelector", selector).DoRaw(ctx)
	if err != nil {
		t.Fatalf("list sandboxes: %v: %s", err, raw)
	}
	var listed struct{ Items []sandbox }
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	return listed.Items
}

// sandboxes returns the names of the Sandboxes that sandboxesOf returns.
func sandboxes(t *testing.T, session string) []string {
	t.Helper()
	var names []string
	for _, found := range sandboxesOf(t, session) {
		names = append(names, found.Metadata.Name)
	}
	return names
}

// asleep waits until the Supervisors have put the session's sandbox to sleep
// and its pod has terminated.
func asleep(t *testing.T, session string) {
	t.Helper()
	eventually(t, patience, "session "+session+" is not asleep", func() bool {
		found := sandboxesOf(t, session)
		if len(found) != 1 || found[0].Spec.OperatingMode != "Suspended" {
			return false
		}
		for _, condition := range found[0].Status.Conditions {
			if condition.Type == "Suspended" {
				return condition.Status == "True"
			}
		}
		return false
	})
}

// sandboxState returns what the session says of its sandbox: awake, asleep or
// lost, or nothing if it has none.
func sandboxState(t *testing.T, session string) string {
	t.Helper()
	var read struct{ Sandbox string }
	if status := request(t, "GET", "/v1/sessions/"+session, "", &read); status != http.StatusOK {
		t.Fatalf("GET session %s: status %d, want 200", session, status)
	}
	return read.Sandbox
}

// sessionPods returns the pods of the session's sandbox, one while it runs.
func sessionPods(t *testing.T, session string) []corev1.Pod {
	t.Helper()
	return pods(t, runtime, "aviary.io/session="+session)
}

// total sums the samples in metrics whose name and labels start with series.
func total(metrics, series string) float64 {
	var sum float64
	for _, line := range strings.Split(metrics, "\n") {
		name, value, _ := strings.Cut(line, " ")
		if strings.HasPrefix(name, series) {
			var number float64
			fmt.Sscan(value, &number)
			sum += number
		}
	}
	return sum
}

// words returns count words, w1 to wN, and the drawl message that has the
// model say them.
func words(count int) (said, message string) {
	var all []string
	for i := 1; i <= count; i++ {
		all = append(all, fmt.Sprintf("w%d", i))
	}
	said = strings.Join(all, " ")
	return said, "drawl " + said
}
