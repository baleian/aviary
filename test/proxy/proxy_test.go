// Package proxy checks what the Egress Proxy does with the requests of turn
// pods. The proxy is Envoy with the configuration that the chart makes, and
// `make test-proxy` starts it. The tests are everything around it: the pod
// that sends a request, the Supervisor that the proxy asks whose request it
// is, the model endpoint and the hosts outside.
package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	// token is the token of a turn that runs, and broken one that the
	// Supervisor fails to look up.
	token  = "the-turn-token"
	broken = "a-token-that-cannot-be-looked-up"
	// key is the API key of the model endpoint, which the proxy holds.
	key = "the-real-key"
	// patience is how long a test waits for the proxy.
	patience = 30 * time.Second
)

// The proxies, by the files of values that they are set up with.
const (
	listed = "listed"
	open   = "open"
	closed = "closed"
)

// supervisor answers the proxy as a Supervisor does, and keeps what the
// proxy asked.
type supervisor struct {
	authv3.UnimplementedAuthorizationServer
	mu    sync.Mutex
	asked []*authv3.CheckRequest
}

func (s *supervisor) Check(_ context.Context, request *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	s.mu.Lock()
	s.asked = append(s.asked, request)
	s.mu.Unlock()
	headers := request.GetAttributes().GetRequest().GetHttp().GetHeaders()
	forward := request.GetAttributes().GetContextExtensions()["listener"] == "forward"
	sent, _ := strings.CutPrefix(headers["authorization"], "Bearer ")
	if forward {
		encoded, _ := strings.CutPrefix(headers["proxy-authorization"], "Basic ")
		credentials, _ := base64.StdEncoding.DecodeString(encoded)
		_, sent, _ = strings.Cut(string(credentials), ":")
	}
	switch sent {
	case token:
		origin, _ := structpb.NewStruct(map[string]any{"turn": "a-turn", "session": "a-session", "agent": "an-agent", "project": "a-project"})
		return &authv3.CheckResponse{Status: &rpcstatus.Status{Code: int32(codes.OK)}, DynamicMetadata: origin}, nil
	case broken:
		return nil, status.Error(codes.Internal, "internal error")
	}
	denied := &authv3.DeniedHttpResponse{
		Status: &typev3.HttpStatus{Code: typev3.StatusCode_Unauthorized},
		Body:   "the token belongs to no running turn\n",
	}
	if forward {
		denied.Status.Code = typev3.StatusCode_ProxyAuthenticationRequired
		denied.Headers = []*corev3.HeaderValueOption{{
			Header: &corev3.HeaderValue{Key: "Proxy-Authenticate", Value: `Basic realm="aviary"`},
		}}
	}
	return &authv3.CheckResponse{
		Status:       &rpcstatus.Status{Code: int32(codes.Unauthenticated)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{DeniedResponse: denied},
	}, nil
}

// questions returns what the proxy asked about the requests with the path.
func (s *supervisor) questions(path string) []*authv3.CheckRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var asked []*authv3.CheckRequest
	for _, request := range s.asked {
		if request.GetAttributes().GetRequest().GetHttp().GetPath() == path {
			asked = append(asked, request)
		}
	}
	return asked
}

// host is a server that the proxy passes requests on to: the model endpoint
// or a host outside. It answers with what it was asked, and keeps the
// requests.
type host struct {
	mu       sync.Mutex
	requests []*http.Request
}

func (h *host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests = append(h.requests, r)
	h.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	// The answer comes in two pieces with a pause between them, as the
	// answer of a model does.
	fmt.Fprintf(w, "%s %s %s\n", r.Method, r.URL.Path, body)
	http.NewResponseController(w).Flush()
	if pause, err := time.ParseDuration(r.URL.Query().Get("pause")); err == nil {
		time.Sleep(pause)
	}
	fmt.Fprintln(w, "done")
}

// got returns the request with the path that the server got, or nothing.
func (h *host) got(path string) *http.Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, request := range h.requests {
		if request.URL.Path == path {
			return request
		}
	}
	return nil
}

var (
	asked   = &supervisor{}
	model   = &host{}
	outside = &host{}
)

func TestMain(m *testing.M) {
	if os.Getenv("AVIARY_TEST_PROXY") == "" {
		// Tests that were skipped here would look like tests that passed.
		fmt.Fprintln(os.Stderr, "AVIARY_TEST_PROXY is not set: run the tests with `make test-proxy`")
		os.Exit(1)
	}
	listen := func(address string) net.Listener {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return listener
	}
	checks := grpc.NewServer()
	authv3.RegisterAuthorizationServer(checks, asked)
	go checks.Serve(listen(":9091"))
	go http.Serve(listen(":8081"), model)
	go http.Serve(listen(":80"), outside)
	// What a pod sends through a tunnel comes back as it is.
	tunnels := listen(":443")
	go func() {
		for {
			tunnel, err := tunnels.Accept()
			if err != nil {
				return
			}
			go func() {
				defer tunnel.Close()
				io.Copy(tunnel, tunnel)
			}()
		}
	}()
	// A proxy looks the Supervisor up every few seconds until it finds it.
	deadline := time.Now().Add(patience)
	for _, proxy := range []string{listed, open, closed} {
		for {
			response, err := http.Get("http://" + proxy + ":3128/ready")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusProxyAuthRequired {
					break
				}
			}
			if time.Now().After(deadline) {
				fmt.Fprintf(os.Stderr, "the proxy %s does not reach the Supervisor: %v, %v\n", proxy, response, err)
				os.Exit(1)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	os.Exit(m.Run())
}

// answer is what a pod gets back for a request.
type answer struct {
	status int
	header http.Header
	body   string
}

// send sends the request of a pod and returns the answer.
func send(t *testing.T, client *http.Client, request *http.Request) answer {
	t.Helper()
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", request.Method, request.URL, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return answer{response.StatusCode, response.Header, string(body)}
}

// ask sends a request to the model endpoint as a harness does: to the proxy,
// with what the pod holds in place of the API key.
func ask(t *testing.T, proxy, path, credential string) answer {
	t.Helper()
	request, err := http.NewRequest("POST", "http://"+proxy+":8080"+path, strings.NewReader("a question"))
	if err != nil {
		t.Fatal(err)
	}
	if credential != "" {
		request.Header.Set("Authorization", credential)
	}
	return send(t, http.DefaultClient, request)
}

// through returns a client that sends its requests through the forward
// proxy, as the programs in a pod do, with the password.
func through(proxy, password string) *http.Client {
	address := &url.URL{Scheme: "http", Host: proxy + ":3128"}
	if password != "" {
		address.User = url.UserPassword("turn", password)
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(address)}}
}

// fetch gets a URL through the forward proxy.
func fetch(t *testing.T, proxy, password, address string) answer {
	t.Helper()
	request, err := http.NewRequest("GET", address, nil)
	if err != nil {
		t.Fatal(err)
	}
	return send(t, through(proxy, password), request)
}

// A model request carries the token of its turn where the API key belongs.
// The proxy puts the key there and passes the request on as it is.
func TestModelRequest(t *testing.T) {
	t.Parallel()
	began := time.Now()
	request, err := http.NewRequest("POST", "http://"+listed+":8080/v1/chat/completions?pause=1s", strings.NewReader("a question"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	// The answer of a model comes piece by piece, and the proxy does not
	// hold a piece back until the answer is whole.
	lines := bufio.NewReader(response.Body)
	first, err := lines.ReadString('\n')
	if err != nil || first != "POST /v1/chat/completions a question\n" || time.Since(began) > 900*time.Millisecond {
		t.Errorf("the first piece of the answer is %q after %s: %v", first, time.Since(began), err)
	}
	if rest, err := io.ReadAll(lines); err != nil || string(rest) != "done\n" || response.StatusCode != http.StatusOK {
		t.Errorf("the answer ended with %q and the status %d: %v", rest, response.StatusCode, err)
	}

	got := model.got("/v1/chat/completions")
	if got == nil {
		t.Fatal("the model endpoint got no request")
	}
	if got.Header.Get("Authorization") != "Bearer "+key || got.Host != "model.test:8081" {
		t.Errorf("the model endpoint got the request for %s with the authorization %q", got.Host, got.Header.Get("Authorization"))
	}
	// The endpoint learns nothing of the pod or of the proxy.
	for name := range got.Header {
		if lower := strings.ToLower(name); strings.HasPrefix(lower, "x-forwarded") || strings.HasPrefix(lower, "x-envoy") || lower == "x-request-id" {
			t.Errorf("the model endpoint got the header %s: %s", name, got.Header.Get(name))
		}
	}
	// The Supervisor was asked with the token, and not as for the forward
	// proxy.
	questions := asked.questions("/v1/chat/completions?pause=1s")
	if len(questions) != 1 || questions[0].GetAttributes().GetRequest().GetHttp().GetHeaders()["authorization"] != "Bearer "+token ||
		questions[0].GetAttributes().GetContextExtensions()["listener"] == "forward" {
		t.Errorf("the Supervisor was asked %v", questions)
	}
}

func TestModelRequestThatIsRefused(t *testing.T) {
	t.Parallel()
	for name, credential := range map[string]string{
		"without a token":       "",
		"with a token of none":  "Bearer another-token",
		"with the key itself":   "Bearer " + key,
		"with a proxy password": "Basic " + base64.StdEncoding.EncodeToString([]byte("turn:"+token)),
	} {
		path := "/v1/refused/" + strings.ReplaceAll(name, " ", "-")
		refused := ask(t, listed, path, credential)
		if refused.status != http.StatusUnauthorized || refused.body != "the token belongs to no running turn\n" {
			t.Errorf("a model request %s was answered with %d %q", name, refused.status, refused.body)
		}
		if model.got(path) != nil {
			t.Errorf("a model request %s reached the model endpoint", name)
		}
	}

	// A request that the proxy cannot ask a Supervisor about is not one
	// with a wrong token: the harness is to send it again.
	if unknown := ask(t, listed, "/v1/unknown", "Bearer "+broken); unknown.status != http.StatusServiceUnavailable {
		t.Errorf("a model request that no Supervisor could be asked about was answered with %d %q", unknown.status, unknown.body)
	}

	// The proxy serves the model endpoint under the path of its URL, and
	// asks no Supervisor about what is not there.
	if elsewhere := ask(t, listed, "/v2/chat/completions", "Bearer "+token); elsewhere.status != http.StatusNotFound {
		t.Errorf("a request beside the path of the model endpoint was answered with %d", elsewhere.status)
	}
	if questions := asked.questions("/v2/chat/completions"); len(questions) != 0 {
		t.Errorf("the Supervisor was asked about a request beside the path of the model endpoint: %v", questions)
	}
	if health := ask(t, listed, "/healthz", ""); health.status != http.StatusOK {
		t.Errorf("the proxy answered whether it is up with %d", health.status)
	}

	// A model endpoint that the list does not allow is not reached.
	notAllowed := ask(t, closed, "/v1/chat/completions", "Bearer "+token)
	if notAllowed.status != http.StatusForbidden || !strings.Contains(notAllowed.body, "egressProxy.allowedHosts") {
		t.Errorf("a model request to an endpoint that is not allowed was answered with %d %q", notAllowed.status, notAllowed.body)
	}
}

// What else a pod sends out goes through the forward proxy, with the token
// of the turn as the password. The proxy lets it through to the hosts that
// the installation allows.
func TestForward(t *testing.T) {
	t.Parallel()
	let := fetch(t, listed, token, "http://allowed.test/let/through")
	if let.status != http.StatusOK || let.body != "GET /let/through \ndone\n" {
		t.Errorf("a request to an allowed host was answered with %d %q", let.status, let.body)
	}
	got := outside.got("/let/through")
	if got == nil {
		t.Fatal("the allowed host got no request")
	}
	// The password is for the proxy, not for the host.
	if got.Host != "allowed.test" || got.Header.Get("Proxy-Authorization") != "" || got.Header.Get("X-Forwarded-For") != "" {
		t.Errorf("the allowed host got the request for %s with the headers %v", got.Host, got.Header)
	}
	questions := asked.questions("/let/through")
	if len(questions) != 1 || questions[0].GetAttributes().GetContextExtensions()["listener"] != "forward" {
		t.Errorf("the Supervisor was asked %v", questions)
	}

	for address, status := range map[string]int{
		"http://ALLOWED.test/in/capitals":   http.StatusOK,
		"http://allowed.test:80/with/port":  http.StatusOK,
		"http://other.test/not/listed":      http.StatusForbidden,
		"http://sub.allowed.test/subdomain": http.StatusForbidden,
		"http://allowed.test:8081/port":     http.StatusForbidden,
	} {
		if answered := fetch(t, listed, token, address); answered.status != status {
			t.Errorf("a request to %s was answered with %d, not %d", address, answered.status, status)
		}
	}
	if outside.got("/not/listed") != nil {
		t.Error("a request to a host that is not allowed reached the host")
	}

	// A client that sends its password only when it is asked for it has to
	// be asked. git is one.
	for name, password := range map[string]string{"without a password": "", "with a password of no turn": "another-token"} {
		refused := fetch(t, listed, password, "http://allowed.test/refused")
		if refused.status != http.StatusProxyAuthRequired || refused.header.Get("Proxy-Authenticate") != `Basic realm="aviary"` {
			t.Errorf("a request %s was answered with %d, asking for %q", name, refused.status, refused.header.Get("Proxy-Authenticate"))
		}
	}
	if unknown := fetch(t, listed, broken, "http://allowed.test/unknown"); unknown.status != http.StatusServiceUnavailable {
		t.Errorf("a request that no Supervisor could be asked about was answered with %d", unknown.status)
	}

	// The chart's own values allow every host.
	if anywhere := fetch(t, open, token, "http://other.test/anywhere"); anywhere.status != http.StatusOK {
		t.Errorf("with every host allowed, a request was answered with %d", anywhere.status)
	}
	if refused := fetch(t, open, "", "http://other.test/anywhere"); refused.status != http.StatusProxyAuthRequired {
		t.Errorf("with every host allowed, a request without a password was answered with %d", refused.status)
	}
}

// tunnel asks the forward proxy for a tunnel to the address, as a client
// does for an HTTPS URL. It returns the status of the answer and the
// connection, which is the tunnel if the status says so.
func tunnel(t *testing.T, proxy, password, address string) (int, net.Conn, *bufio.Reader) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", proxy+":3128", patience)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	connection.SetDeadline(time.Now().Add(patience))
	credentials := base64.StdEncoding.EncodeToString([]byte("turn:" + password))
	fmt.Fprintf(connection, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", address, address, credentials)
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil {
		t.Fatalf("a tunnel to %s: %v", address, err)
	}
	return response.StatusCode, connection, reader
}

// A request for an HTTPS URL is a tunnel through the proxy. The proxy sees
// the host and the port, and passes on the rest unread.
func TestTunnel(t *testing.T) {
	t.Parallel()
	status, connection, reader := tunnel(t, listed, token, "allowed.test:443")
	if status != http.StatusOK {
		t.Fatalf("a tunnel to an allowed host was answered with %d", status)
	}
	fmt.Fprint(connection, "what a pod sends\n")
	if back, err := reader.ReadString('\n'); err != nil || back != "what a pod sends\n" {
		t.Errorf("through the tunnel came back %q: %v", back, err)
	}

	for name, refused := range map[string]struct {
		password, address string
		status            int
	}{
		"to a host that is not allowed": {token, "other.test:443", http.StatusForbidden},
		"to another port":               {token, "allowed.test:8443", http.StatusForbidden},
		"with a password of no turn":    {"another-token", "allowed.test:443", http.StatusProxyAuthRequired},
	} {
		if status, _, _ := tunnel(t, listed, refused.password, refused.address); status != refused.status {
			t.Errorf("a tunnel %s was answered with %d, not %d", name, status, refused.status)
		}
	}
}

// The proxy tells Prometheus of the requests, and nothing else of itself.
func TestMetrics(t *testing.T) {
	t.Parallel()
	ask(t, listed, "/v1/counted", "Bearer "+token)
	fetch(t, listed, token, "http://allowed.test/counted")
	fetch(t, listed, token, "http://other.test/counted")
	// series adds up the series whose names and labels begin as given.
	series := func(prefix string) float64 {
		response, err := http.Get("http://" + listed + ":9092/metrics")
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("the metrics were answered with %v: %v", response, err)
		}
		defer response.Body.Close()
		var sum float64
		lines := bufio.NewScanner(response.Body)
		lines.Buffer(nil, 1<<20)
		for lines.Scan() {
			name, value, _ := strings.Cut(lines.Text(), " ")
			if number, err := strconv.ParseFloat(value, 64); err == nil && strings.HasPrefix(name, prefix) {
				sum += number
			}
		}
		return sum
	}
	for _, counted := range []string{
		`envoy_http_ext_authz_ok{envoy_http_conn_manager_prefix="llm"`,
		`envoy_http_ext_authz_ok{envoy_http_conn_manager_prefix="forward"`,
		`envoy_http_rbac_denied`,
		// How long the requests to the model took.
		`envoy_http_downstream_rq_time_count{envoy_http_conn_manager_prefix="llm"`,
	} {
		// Envoy counts the time of a request a few seconds after it.
		deadline := time.Now().Add(patience)
		for series(counted) < 1 {
			if time.Now().After(deadline) {
				t.Errorf("the metrics have nothing for %s", counted)
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	// Envoy's own interface can stop Envoy and show its configuration.
	for _, path := range []string{"/", "/stats", "/stats/prometheus", "/config_dump", "/quitquitquit", "/metrics/../config_dump", "/metricsx"} {
		response, err := http.Post("http://"+listed+":9092"+path, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s on the port of the metrics was answered with %d", path, response.StatusCode)
		}
	}
}
