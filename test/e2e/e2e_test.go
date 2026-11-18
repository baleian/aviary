// Package e2e checks what only a cluster shows: that the parts of Aviary
// work together as the chart installs them. What a part does by itself is
// checked where the part is. `make e2e` makes a cluster for the tests,
// installs Aviary in it with the values in this directory, and removes the
// cluster when the tests have passed.
//
// The model endpoint is mock-llm, which answers by a script: a message to a
// session is an instruction to it. See test/mock-llm/main.go.
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
	// system is the namespace of Aviary, and runtime that of the turn pods.
	system  = "aviary-system"
	runtime = "aviary-runtime"
	// patience is how long a test waits for a turn to end.
	patience = 2 * time.Minute
	// soon is how long it waits for what follows on something at once.
	soon = 30 * time.Second
)

var (
	ctx     = context.Background()
	cluster *kubernetes.Clientset
	// apiURL is where the API of Aviary is: a port of a node of the cluster.
	apiURL = os.Getenv("AVIARY_E2E_API")
	// harnesses are the harnesses that the installation has, by the names
	// that agents are created with. The first runs what does not depend on
	// the harness.
	harnesses = strings.Fields(os.Getenv("AVIARY_E2E_HARNESSES"))
)

func TestMain(m *testing.M) {
	kubeconfig := os.Getenv("AVIARY_E2E_KUBECONFIG")
	if kubeconfig == "" || apiURL == "" || len(harnesses) == 0 {
		// Tests that were skipped here would look like tests that passed.
		fmt.Fprintln(os.Stderr, "AVIARY_E2E_KUBECONFIG, AVIARY_E2E_API and AVIARY_E2E_HARNESSES are not set: run the tests with `make e2e`")
		os.Exit(1)
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err == nil {
		// The tests send their requests to Aviary through the API server.
		config.QPS, config.Burst = 200, 400
		cluster, err = kubernetes.NewForConfig(config)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// eventually waits for the condition to hold.
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

// The API of Aviary, as its clients see it. A request goes to a port of a
// node, from where the cluster passes it on to one of the Supervisors. Every
// request has a connection of its own, so that none is sent to a Supervisor
// that has stopped since the request before it. A connection that is not
// there after a moment is given up: the node may have passed it on to a
// Supervisor that has just gone.
var client = &http.Client{Transport: &http.Transport{
	DisableKeepAlives: true,
	DialContext:       (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
}}

// call sends a request to the API, with the headers that are given as names
// and values. While the Supervisors are replaced, a node may take no
// connection for a moment: the request, of which nothing was sent, is then
// sent again.
func call(ctx context.Context, method, path, body string, headers ...string) (*http.Response, error) {
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

// request sends a request to the API. It returns the status of the answer
// and decodes the body of the answer into the last argument, if there is one.
func request(t *testing.T, method, path, body string, into any) int {
	t.Helper()
	response, err := call(ctx, method, path, body)
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
			t.Fatalf("%s %s was answered with %d %s: %v", method, path, response.StatusCode, answer, err)
		}
	}
	return response.StatusCode
}

// create creates a resource and returns its ID.
func create(t *testing.T, path, body string) string {
	t.Helper()
	var created struct{ ID string }
	if status := request(t, "POST", path, body, &created); status != http.StatusCreated {
		t.Fatalf("POST %s %s was answered with %d", path, body, status)
	}
	return created.ID
}

// The model server of the tests, which answers only the requests that carry
// its key.
const (
	mockURL = "http://mock-llm.aviary-e2e/v1"
	mockKey = "mock-llm-key"
)

// provider creates a provider of the project: the model server of the
// tests, with the key.
func provider(t *testing.T, project, key string) string {
	t.Helper()
	return create(t, "/v1/projects/"+project+"/providers", fmt.Sprintf(`{"name": "mock", "url": %q, "api_key": {"value": %q}}`, mockURL, key))
}

// The other model server of the tests, which has a key of its own, and the
// secret store of the cluster, where the tests keep that key.
const (
	otherURL     = "http://mock-llm-other.aviary-e2e/v1"
	otherHost    = "mock-llm-other.aviary-e2e"
	otherKey     = "other-mock-llm-key"
	storeAddress = "http://openbao.aviary-data:8200"
	data         = "aviary-data"
	// remembered is for how long a Supervisor of the installation uses what
	// it read from a secret store, as the values of the e2e set it.
	remembered = 5 * time.Second
)

// rootToken is the token of the secret store's owner, which `make
// secret-store` keeps in a secret beside the store.
var rootToken = sync.OnceValues(func() (string, error) {
	root, err := cluster.CoreV1().Secrets(data).Get(ctx, "openbao-root", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return string(root.Data["token"]), nil
})

// owner sends the secret store of the cluster a request as its owner does,
// through the API server, and returns the data of the answer. The store may
// answer with one of the statuses beside those of success.
func owner(t *testing.T, method, path string, body any, statuses ...int) map[string]any {
	t.Helper()
	token, err := rootToken()
	if err != nil {
		t.Fatalf("the root token of the secret store: %v: `make e2e` installs the store", err)
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
		t.Fatalf("%s %s of the secret store: %v", method, path, err)
	}
	if !slices.Contains(append(statuses, http.StatusOK, http.StatusNoContent), status) {
		t.Fatalf("the secret store answered %s %s with %d %s", method, path, status, raw)
	}
	var answer struct{ Data map[string]any }
	json.Unmarshal(raw, &answer)
	return answer.Data
}

// agent is an agent of a project.
type agent struct{ id, project string }

// agentOf creates an agent of the project as the body says, whose model is
// one of the provider.
func agentOf(t *testing.T, project, provider, body string) agent {
	t.Helper()
	var settings map[string]any
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		t.Fatal(err)
	}
	settings["model"] = map[string]string{"provider": provider, "name": "mock"}
	with, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	return agent{create(t, "/v1/projects/"+project+"/agents", string(with)), project}
}

// newAgent creates an agent as the body says, in a project of its own. Its
// model is the model server of the tests.
func newAgent(t *testing.T, body string) agent {
	t.Helper()
	project := create(t, "/v1/projects", `{"name": "e2e"}`)
	return agentOf(t, project, provider(t, project, mockKey), body)
}

// session creates a session of the agent.
func (a agent) session(t *testing.T) string {
	t.Helper()
	return create(t, "/v1/agents/"+a.id+"/sessions", "")
}

// turn is a turn as the API returns it.
type turn struct {
	ID         string            `json:"id"`
	Seq        int               `json:"seq"`
	State      string            `json:"state"`
	StartedAt  *time.Time        `json:"started_at"`
	StopReason string            `json:"stop_reason"`
	Usage      map[string]int64  `json:"usage"`
	Failure    map[string]string `json:"failure"`
	Items      []json.RawMessage `json:"items"`
	// Approvals are the requests for approval that wait for a person.
	Approvals []approvalRequest `json:"approvals"`
}

// approvalRequest is what a harness asks a person before it acts.
type approvalRequest struct {
	ID       string `json:"id"`
	ToolCall struct {
		Input json.RawMessage `json:"input"`
	} `json:"tool_call"`
	Options []struct{ ID, Kind string } `json:"options"`
}

// said returns what the record of a turn has the agent say.
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

// tools returns what the record of a turn tells of the tools that the agent
// called.
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
		t.Fatalf("the message %q was answered with %d", text, status)
	}
	return accepted
}

// record returns the record of a turn.
func record(t *testing.T, id string) turn {
	t.Helper()
	var record turn
	if status := request(t, "GET", "/v1/turns/"+id, "", &record); status != http.StatusOK {
		t.Fatalf("the turn %s was answered with %d", id, status)
	}
	return record
}

// event is an event of a turn as a client of the API reads it.
type event struct {
	ID, Type, Data string
}

// ends tells whether an event of the type is the last of its turn.
func ends(eventType string) bool {
	switch eventType {
	case "turn.completed", "turn.failed", "turn.cancelled", "turn.expired":
		return true
	}
	return false
}

// reader reads the events of a turn as they come. A stream that breaks off
// before the turn has ended, as it does when its Supervisor is replaced, is
// read on from the last event that came.
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
			response, err := call(following, "GET", "/v1/turns/"+id+"/events", "", "Last-Event-ID", last)
			if err == nil && response.StatusCode != http.StatusOK {
				response.Body.Close()
				err = fmt.Errorf("the events were answered with %d", response.StatusCode)
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

// until reads on to an event that the condition holds for, and returns the
// events that it read.
func (r *reader) until(t *testing.T, what string, holds func(event) bool) []event {
	t.Helper()
	var events []event
	deadline := time.After(patience)
	for {
		select {
		case next, open := <-r.events:
			if !open {
				t.Fatalf("the turn %s ended before %s; its events were %v", r.turn, what, events)
			}
			events = append(events, next)
			if holds(next) {
				return events
			}
		case <-deadline:
			t.Fatalf("after %s, the turn %s has not %s; its events were %v", patience, r.turn, what, events)
		}
	}
}

// end reads on to the end of the turn and returns its last event.
func (r *reader) end(t *testing.T) event {
	t.Helper()
	events := r.until(t, "ended", func(next event) bool { return ends(next.Type) })
	return events[len(events)-1]
}

// run sends a message to the session, waits for its turn to complete and
// returns the record of the turn.
func run(t *testing.T, session, text string) turn {
	t.Helper()
	accepted := send(t, session, text)
	if last := read(t, accepted.ID).end(t); last.Type != "turn.completed" {
		t.Fatalf("the turn %s of the message %q ended with %s %s", accepted.ID, text, last.Type, last.Data)
	}
	return record(t, accepted.ID)
}

// The cluster.

// pods returns the pods of the namespace that the selector picks.
func pods(t *testing.T, namespace, selector string) []corev1.Pod {
	t.Helper()
	pods, err := cluster.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		t.Fatal(err)
	}
	return pods.Items
}

// left tells what the cluster still holds of a turn: its Job and its pod.
func left(t *testing.T, id string) []string {
	t.Helper()
	selector := metav1.ListOptions{LabelSelector: "aviary.io/turn=" + id}
	jobs, err := cluster.BatchV1().Jobs(runtime).List(ctx, selector)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, job := range jobs.Items {
		left = append(left, "job/"+job.Name)
	}
	for _, pod := range pods(t, runtime, selector.LabelSelector) {
		left = append(left, "pod/"+pod.Name)
	}
	return left
}

// running returns the pods of a part of Aviary that are not on their way
// out.
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

// metrics returns the metrics of all the pods of a part of Aviary, which
// Prometheus would read from each of them.
func metrics(t *testing.T, component string) string {
	t.Helper()
	var metrics strings.Builder
	for _, pod := range running(t, component) {
		read, err := cluster.CoreV1().RESTClient().Get().Namespace(system).Resource("pods").
			Name(pod.Name + ":9092").SubResource("proxy").Suffix("metrics").Do(ctx).Raw()
		if err != nil {
			t.Fatalf("the metrics of %s: %v", pod.Name, err)
		}
		metrics.Write(read)
	}
	return metrics.String()
}

// total adds up the series whose names and labels begin as given, over the
// pods whose metrics these are.
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

// recorded returns the requests of a turn that the Egress Proxies have
// recorded, each as the fields of its line.
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

// logged waits for the proxies to have recorded a request of the turn to the
// host that ended with the status, and returns the record.
func logged(t *testing.T, id, route, host string, status float64) map[string]any {
	t.Helper()
	var found map[string]any
	eventually(t, soon, fmt.Sprintf("no Egress Proxy has recorded a %s request of the turn to %s that ended with %v", route, host, status), func() bool {
		for _, line := range recorded(t, id) {
			if line["route"] == route && line["host"] == host && line["status"] == status {
				found = line
			}
		}
		return found != nil
	})
	return found
}

// words returns so many words, w1 and on, and the message that has the
// model say them slowly.
func words(count int) (said, message string) {
	var all []string
	for i := 1; i <= count; i++ {
		all = append(all, fmt.Sprintf("w%d", i))
	}
	said = strings.Join(all, " ")
	return said, "drawl " + said
}
