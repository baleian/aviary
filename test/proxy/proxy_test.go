// Package proxy tests the Egress Proxy: Envoy with the configuration that the
// chart renders, which `make test-proxy` starts. The tests play everything
// around it: the pods that send requests, the Supervisor that authorizes
// them, the model endpoint and the outside hosts.
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
	// token is the session token of a running turn, broken one that the
	// Supervisor fails to look up, and nameless one for which the Supervisor
	// names no model endpoint.
	token    = "the-session-token"
	broken   = "a-token-that-cannot-be-looked-up"
	nameless = "a-turn-without-an-endpoint"
	// key is the API key that the Supervisor returns for token.
	key = "the-real-key"
	// patience is how long a test waits for the proxy.
	patience = 30 * time.Second
)

// The proxies, named after their values files.
const (
	listed = "listed"
	open   = "open"
	closed = "closed"
)

// endpoint is a model endpoint that the Supervisor returns for a turn: its
// host, whether it uses TLS, and its key, if any.
type endpoint struct {
	host   string
	secure bool
	key    string
	// mistaken makes the Supervisor return the endpoint for forward proxy
	// requests too, as if by mistake.
	mistaken bool
}

// mistaken is the token of a turn for which the Supervisor wrongly returns a
// model endpoint to the forward proxy.
const mistaken = "a-turn-that-the-supervisor-errs-about"

// endpoints are the model endpoints that the Supervisor returns, by token.
// Every host resolves to the tests, except gone.invalid, which does not
// resolve. Nothing listens on port 9.
var endpoints = map[string]endpoint{
	token:                                {host: "model.test:8081", key: key},
	"a-turn-of-a-provider":               {host: "allowed.test", key: "the-key-of-a-provider"},
	"a-turn-of-a-provider-without-a-key": {host: "allowed.test"},
	"a-turn-of-a-provider-with-tls":      {host: "tls.test:8443", secure: true, key: "the-key-of-a-provider"},
	"a-turn-of-a-provider-of-another":    {host: "unnamed.test:8443", secure: true, key: "the-key-of-a-provider"},
	"a-turn-of-a-provider-not-on-a-list": {host: "other.test", key: "the-key-of-a-provider"},
	"a-turn-of-a-provider-on-a-port":     {host: "model.test:8081", key: "the-key-of-a-provider"},
	"a-turn-of-a-provider-that-is-down":  {host: "model.test:9", key: "the-key-of-a-provider"},
	"a-turn-of-a-provider-that-is-gone":  {host: "gone.invalid", key: "the-key-of-a-provider"},
	mistaken:                             {host: "other.test", key: "the-key-of-a-provider", mistaken: true},
}

// named returns a token for which the Supervisor returns an endpoint at host,
// with a key if key is true.
func named(host string, key bool) string {
	for turn, endpoint := range endpoints {
		if endpoint.host == host && (endpoint.key != "") == key && !endpoint.mistaken && turn != token {
			return turn
		}
	}
	panic("no test endpoint at " + host)
}

// supervisor plays the Supervisors' authorization service, and records the
// checks that it gets.
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
	origin, _ := structpb.NewStruct(map[string]any{"turn": "a-turn", "session": "a-session", "agent": "an-agent", "project": "a-project"})
	allowed := &authv3.CheckResponse{Status: &rpcstatus.Status{Code: int32(codes.OK)}, DynamicMetadata: origin}
	endpoint, provided := endpoints[sent]
	switch {
	case sent == nameless, provided && forward && !endpoint.mistaken:
		return allowed, nil
	case provided:
		// The endpoint of the turn's provider, with its key in place of the
		// token. A provider without a key gets no Authorization header.
		set := func(name, value string) *corev3.HeaderValueOption {
			return &corev3.HeaderValueOption{
				Header:       &corev3.HeaderValue{Key: name, Value: value},
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
			}
		}
		answer := &authv3.OkHttpResponse{Headers: []*corev3.HeaderValueOption{
			set("x-aviary-upstream", endpoint.host),
			set("x-aviary-upstream-tls", strconv.FormatBool(endpoint.secure)),
		}}
		if endpoint.key != "" {
			answer.Headers = append(answer.Headers, set("authorization", "Bearer "+endpoint.key))
		} else {
			answer.HeadersToRemove = []string{"authorization"}
		}
		allowed.HttpResponse = &authv3.CheckResponse_OkResponse{OkResponse: answer}
		return allowed, nil
	case sent == broken:
		return nil, status.Error(codes.Internal, "internal error")
	}
	denied := &authv3.DeniedHttpResponse{
		Status: &typev3.HttpStatus{Code: typev3.StatusCode_Unauthorized},
		Body:   "unknown token, or no turn is running in its session\n",
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

// questions returns the checks of the requests with the path.
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

// host is an upstream of the proxy: the model endpoint or an outside host. It
// echoes each request and records it.
type host struct {
	mu       sync.Mutex
	requests []*http.Request
}

func (h *host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests = append(h.requests, r)
	h.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	// ?status= makes the host refuse the request with that status.
	if status, err := strconv.Atoi(r.URL.Query().Get("status")); err == nil {
		http.Error(w, "refused by the host", status)
		return
	}
	// The response is streamed in two parts, ?pause= apart, as a model
	// streams its answer.
	fmt.Fprintf(w, "%s %s %s\n", r.Method, r.URL.Path, body)
	http.NewResponseController(w).Flush()
	if pause, err := time.ParseDuration(r.URL.Query().Get("pause")); err == nil {
		time.Sleep(pause)
	}
	fmt.Fprintln(w, "done")
}

// got returns the request with the path, or nil.
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

// The servers that the tests play: the Supervisor, the model endpoint, the
// outside hosts, and a host served over TLS.
var (
	asked   = &supervisor{}
	model   = &host{}
	outside = &host{}
	secured = &host{}
)

func TestMain(m *testing.M) {
	if os.Getenv("AVIARY_TEST_PROXY") == "" {
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
	// The proxies trust only this certificate, which `make test-proxy` made
	// for tls.test.
	go http.ServeTLS(listen(":8443"), secured, "../../.out/proxy/tls/cert.pem", "../../.out/proxy/tls/key.pem")
	// Echoes what a pod sends through a tunnel.
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
	// Envoy resolves the Supervisor's address periodically, so wait until
	// every proxy reaches it.
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
				fmt.Fprintf(os.Stderr, "proxy %s cannot reach the Supervisor: %v, %v\n", proxy, response, err)
				os.Exit(1)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	os.Exit(m.Run())
}

// answer is the response that a pod gets.
type answer struct {
	status int
	header http.Header
	body   string
}

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

// ask sends a model request as a harness does, to the proxy's model listener
// with credential as the Authorization header. headers are more header names
// and values, alternating.
func ask(t *testing.T, proxy, path, credential string, headers ...string) answer {
	t.Helper()
	request, err := http.NewRequest("POST", "http://"+proxy+":8080"+path, strings.NewReader("a question"))
	if err != nil {
		t.Fatal(err)
	}
	if credential != "" {
		request.Header.Set("Authorization", credential)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	return send(t, http.DefaultClient, request)
}

// through returns a client that uses the forward proxy with password, as the
// programs in a pod do.
func through(proxy, password string) *http.Client {
	address := &url.URL{Scheme: "http", Host: proxy + ":3128"}
	if password != "" {
		address.User = url.UserPassword("session", password)
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

// A model request carries the session token in place of the API key. The
// proxy replaces it with the key of the turn's model endpoint, and otherwise
// forwards the request as it is.
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
	// The proxy streams the response instead of buffering it.
	lines := bufio.NewReader(response.Body)
	first, err := lines.ReadString('\n')
	if err != nil || first != "POST /v1/chat/completions a question\n" || time.Since(began) > 900*time.Millisecond {
		t.Errorf("first line of the response = %q after %s: %v", first, time.Since(began), err)
	}
	if rest, err := io.ReadAll(lines); err != nil || string(rest) != "done\n" || response.StatusCode != http.StatusOK {
		t.Errorf("rest of the response = %q, status %d: %v", rest, response.StatusCode, err)
	}
	// The proxy adds no headers of its own to the response.
	for name := range response.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-envoy") {
			t.Errorf("response has header %s: %s", name, response.Header.Get(name))
		}
	}

	got := model.got("/v1/chat/completions")
	if got == nil {
		t.Fatal("model endpoint got no request")
	}
	if got.Header.Get("Authorization") != "Bearer "+key || got.Host != "model.test:8081" {
		t.Errorf("model endpoint got host %s, Authorization %q", got.Host, got.Header.Get("Authorization"))
	}
	// The endpoint learns nothing of the pod or the proxy.
	for name := range got.Header {
		if lower := strings.ToLower(name); strings.HasPrefix(lower, "x-forwarded") || strings.HasPrefix(lower, "x-envoy") || lower == "x-request-id" {
			t.Errorf("model endpoint got header %s: %s", name, got.Header.Get(name))
		}
	}
	// The Supervisor is asked with the token, as for a model request.
	questions := asked.questions("/v1/chat/completions?pause=1s")
	if len(questions) != 1 || questions[0].GetAttributes().GetRequest().GetHttp().GetHeaders()["authorization"] != "Bearer "+token ||
		questions[0].GetAttributes().GetContextExtensions()["listener"] == "forward" {
		t.Errorf("Supervisor checks = %v", questions)
	}
}

func TestModelRequestThatIsRefused(t *testing.T) {
	t.Parallel()
	for name, credential := range map[string]string{
		"without a token":       "",
		"with an unknown token": "Bearer another-token",
		"with the key itself":   "Bearer " + key,
		"with a proxy password": "Basic " + base64.StdEncoding.EncodeToString([]byte("session:"+token)),
	} {
		path := "/v1/refused/" + strings.ReplaceAll(name, " ", "-")
		refused := ask(t, listed, path, credential)
		if refused.status != http.StatusUnauthorized || refused.body != "unknown token, or no turn is running in its session\n" {
			t.Errorf("model request %s: got %d %q, want 401", name, refused.status, refused.body)
		}
		if model.got(path) != nil {
			t.Errorf("model request %s reached the model endpoint", name)
		}
	}

	// If no Supervisor can be asked, the answer is 503 and not 401, so that
	// the harness retries.
	if unknown := ask(t, listed, "/v1/unknown", "Bearer "+broken); unknown.status != http.StatusServiceUnavailable {
		t.Errorf("model request with a failed Supervisor check: got %d %q, want 503", unknown.status, unknown.body)
	}

	// A request for which the Supervisor names no endpoint is not forwarded,
	// whether hosts are listed or not, even to an upstream that the pod
	// names.
	for _, proxy := range []string{open, listed} {
		path := "/v1/nameless/" + proxy
		unnamed := ask(t, proxy, path, "Bearer "+nameless, "x-aviary-upstream", "allowed.test", "x-aviary-upstream-tls", "false")
		if unnamed.status != http.StatusServiceUnavailable || !strings.Contains(unnamed.body, "no model endpoint") {
			t.Errorf("%s: model request without an endpoint: got %d %q, want 503", proxy, unnamed.status, unnamed.body)
		}
		if model.got(path) != nil || outside.got(path) != nil {
			t.Errorf("%s: model request without an endpoint was forwarded", proxy)
		}
	}
	if health := ask(t, listed, "/healthz", ""); health.status != http.StatusOK {
		t.Errorf("health check: got status %d, want 200", health.status)
	}

	// A model endpoint that is not on the list is refused.
	notAllowed := ask(t, closed, "/v1/chat/completions", "Bearer "+token)
	if notAllowed.status != http.StatusForbidden || !strings.Contains(notAllowed.body, "egressProxy.allowedHosts") {
		t.Errorf("model request to a disallowed endpoint: got %d %q, want 403", notAllowed.status, notAllowed.body)
	}
}

// The replies that the proxy makes itself carry a header that the answers of
// a model endpoint do not, whatever their status: that tells whether a model
// request reached its endpoint.
func TestTheProxyMarksItsOwnReplies(t *testing.T) {
	t.Parallel()
	const mark = "X-Aviary-Proxy"
	provided := "Bearer " + named("allowed.test", true)
	for path, status := range map[string]int{"/v1/marked/answers": http.StatusOK, "/v1/marked/refuses?status=503": http.StatusServiceUnavailable} {
		if got := ask(t, open, path, provided); got.status != status || got.header.Get(mark) != "" {
			t.Errorf("an endpoint that answers %d: status %d with the header %q, want its answer without the header", status, got.status, got.header.Get(mark))
		}
	}
	for name, got := range map[string]answer{
		"an unknown token":                  ask(t, open, "/v1/marked/unknown", "Bearer another-token"),
		"a turn without an endpoint":        ask(t, open, "/v1/marked/nameless", "Bearer "+nameless),
		"an endpoint that is not allowed":   ask(t, closed, "/v1/marked/closed", "Bearer "+token),
		"an endpoint that is down":          ask(t, open, "/v1/marked/down", "Bearer "+named("model.test:9", true)),
		"an endpoint that does not resolve": ask(t, open, "/v1/marked/gone", "Bearer "+named("gone.invalid", true)),
	} {
		if got.header.Get(mark) != "true" || got.status < 400 {
			t.Errorf("%s: status %d %q with the header %q, want a reply of the proxy with it", name, got.status, got.body, got.header.Get(mark))
		}
	}
}

// A model request goes to the endpoint of the provider of the turn's agent,
// as the Supervisor names it, with the key that the Supervisor returns.
func TestModelRequestToANamedEndpoint(t *testing.T) {
	t.Parallel()
	const providerKey = "Bearer the-key-of-a-provider"
	// reached checks that at got the request for path, addressed to address
	// and with credential, and returns it.
	reached := func(what string, at *host, path, address, credential string) *http.Request {
		t.Helper()
		got := at.got(path)
		if got == nil {
			t.Errorf("%s did not reach its endpoint", what)
			return nil
		}
		if got.Host != address || got.Header.Get("Authorization") != credential {
			t.Errorf("%s: got host %s, Authorization %q; want %s, %q", what, got.Host, got.Header.Get("Authorization"), address, credential)
		}
		for name := range got.Header {
			if lower := strings.ToLower(name); strings.HasPrefix(lower, "x-aviary") || strings.HasPrefix(lower, "x-forwarded") || strings.HasPrefix(lower, "x-envoy") {
				t.Errorf("%s: endpoint got header %s: %s", what, name, got.Header.Get(name))
			}
		}
		return got
	}

	plain := ask(t, open, "/api/named/plain", "Bearer "+named("allowed.test", true))
	if plain.status != http.StatusOK || plain.body != "POST /api/named/plain a question\ndone\n" {
		t.Errorf("model request to a named endpoint: got %d %q", plain.status, plain.body)
	}
	reached("model request to a named endpoint", outside, "/api/named/plain", "allowed.test", providerKey)

	// An endpoint without a key gets none, and not the token either.
	ask(t, open, "/api/named/keyless", "Bearer "+named("allowed.test", false))
	reached("model request to an endpoint without a key", outside, "/api/named/keyless", "allowed.test", "")

	ask(t, open, "/v1/named/port", "Bearer "+named("model.test:8081", true))
	reached("model request to an endpoint with a port", model, "/v1/named/port", "model.test:8081", providerKey)

	// Over TLS, with HTTP/2 since the endpoint offers it.
	if encrypted := ask(t, open, "/api/named/tls", "Bearer "+named("tls.test:8443", true)); encrypted.status != http.StatusOK {
		t.Errorf("model request to a TLS endpoint: got %d %q, want 200", encrypted.status, encrypted.body)
	}
	if got := reached("model request to a TLS endpoint", secured, "/api/named/tls", "tls.test:8443", providerKey); got != nil && (got.TLS == nil || got.ProtoMajor != 2) {
		t.Errorf("TLS endpoint got the request over %s, TLS state %v; want HTTP/2 over TLS", got.Proto, got.TLS)
	}
	// The endpoint's certificate must match its host name.
	if another := ask(t, open, "/api/named/unnamed", "Bearer "+named("unnamed.test:8443", true)); another.status != http.StatusServiceUnavailable {
		t.Errorf("model request to an endpoint with another host's certificate: got %d %q, want 503", another.status, another.body)
	}
	if secured.got("/api/named/unnamed") != nil {
		t.Error("model request reached an endpoint with another host's certificate")
	}

	// The Supervisor chooses the upstream, not the pod.
	chosen := []string{"x-aviary-upstream", "other.test", "x-aviary-upstream-tls", "false"}
	ask(t, open, "/v1/named/chosen", "Bearer "+token, chosen...)
	reached("model request with an upstream set by the pod", model, "/v1/named/chosen", "model.test:8081", "Bearer "+key)
	ask(t, open, "/api/named/chosen", "Bearer "+named("allowed.test", true), chosen...)
	reached("model request with another upstream set by the pod", outside, "/api/named/chosen", "allowed.test", providerKey)
	if outside.got("/v1/named/chosen") != nil {
		t.Error("model request reached the upstream set by the pod")
	}

	// The list must allow a named endpoint like any other host.
	for _, proxy := range []string{listed, closed} {
		path := "/api/named/" + proxy
		if on := ask(t, proxy, path, "Bearer "+named("allowed.test", true)); on.status != http.StatusOK {
			t.Errorf("%s: model request to a listed endpoint: got %d %q, want 200", proxy, on.status, on.body)
		}
		reached(proxy+": model request to a listed endpoint", outside, path, "allowed.test", providerKey)
		off := ask(t, proxy, path+"/off", "Bearer "+named("other.test", true))
		if off.status != http.StatusForbidden || !strings.Contains(off.body, "egressProxy.allowedHosts") || outside.got(path+"/off") != nil {
			t.Errorf("%s: model request to an unlisted endpoint: got %d %q, want 403", proxy, off.status, off.body)
		}
	}
	// An endpoint's own refusal reaches the pod unchanged.
	own := ask(t, listed, "/api/named/refused?status=403", "Bearer "+named("allowed.test", true))
	if own.status != http.StatusForbidden || own.body != "refused by the host\n" {
		t.Errorf("endpoint's refusal passed on as %d %q, want it unchanged", own.status, own.body)
	}
	// A listed host with a port allows that port.
	ask(t, listed, "/v1/named/listed-port", "Bearer "+named("model.test:8081", true))
	reached("model request to a listed endpoint with a port", model, "/v1/named/listed-port", "model.test:8081", providerKey)
	if off := ask(t, closed, "/v1/named/closed-port", "Bearer "+named("model.test:8081", true)); off.status != http.StatusForbidden {
		t.Errorf("model request to an unlisted port: got %d %q, want 403", off.status, off.body)
	}
}

// The other requests of a pod go through the forward proxy, with the session
// token as the password, and reach only the allowed hosts.
func TestForward(t *testing.T) {
	t.Parallel()
	let := fetch(t, listed, token, "http://allowed.test/let/through")
	if let.status != http.StatusOK || let.body != "GET /let/through \ndone\n" {
		t.Errorf("request to an allowed host: got %d %q", let.status, let.body)
	}
	got := outside.got("/let/through")
	if got == nil {
		t.Fatal("allowed host got no request")
	}
	// The password is for the proxy, not for the host.
	if got.Host != "allowed.test" || got.Header.Get("Proxy-Authorization") != "" || got.Header.Get("X-Forwarded-For") != "" {
		t.Errorf("allowed host got host %s, headers %v", got.Host, got.Header)
	}
	questions := asked.questions("/let/through")
	if len(questions) != 1 || questions[0].GetAttributes().GetContextExtensions()["listener"] != "forward" {
		t.Errorf("Supervisor checks = %v", questions)
	}

	for address, status := range map[string]int{
		"http://ALLOWED.test/in/capitals":   http.StatusOK,
		"http://allowed.test:80/with/port":  http.StatusOK,
		"http://other.test/not/listed":      http.StatusForbidden,
		"http://sub.allowed.test/subdomain": http.StatusForbidden,
		"http://allowed.test:8081/port":     http.StatusForbidden,
	} {
		if answered := fetch(t, listed, token, address); answered.status != status {
			t.Errorf("request to %s: got status %d, want %d", address, answered.status, status)
		}
	}
	if outside.got("/not/listed") != nil {
		t.Error("request to a disallowed host reached the host")
	}

	// Some clients, such as git, send the password only when challenged.
	for name, password := range map[string]string{"without a password": "", "with an unknown password": "another-token"} {
		refused := fetch(t, listed, password, "http://allowed.test/refused")
		if refused.status != http.StatusProxyAuthRequired || refused.header.Get("Proxy-Authenticate") != `Basic realm="aviary"` {
			t.Errorf("request %s: got status %d, Proxy-Authenticate %q; want 407 with a Basic challenge", name, refused.status, refused.header.Get("Proxy-Authenticate"))
		}
	}
	if unknown := fetch(t, listed, broken, "http://allowed.test/unknown"); unknown.status != http.StatusServiceUnavailable {
		t.Errorf("request with a failed Supervisor check: got status %d, want 503", unknown.status)
	}

	// The default values allow every host.
	if anywhere := fetch(t, open, token, "http://other.test/anywhere"); anywhere.status != http.StatusOK {
		t.Errorf("request with every host allowed: got status %d, want 200", anywhere.status)
	}
	if refused := fetch(t, open, "", "http://other.test/anywhere"); refused.status != http.StatusProxyAuthRequired {
		t.Errorf("request without a password, with every host allowed: got status %d, want 407", refused.status)
	}
}

// For a forward proxy request, the Supervisor only identifies the turn. If it
// returned headers as for a model request, the model key would go to whatever
// host the pod asked for, so the proxy refuses the request instead.
func TestForwardTakesNoKey(t *testing.T) {
	t.Parallel()
	for _, proxy := range []string{open, listed} {
		path := "/mistaken/" + proxy
		refused := fetch(t, proxy, mistaken, "http://allowed.test"+path)
		if refused.status != http.StatusInternalServerError {
			t.Errorf("%s: request for which the Supervisor returned a key: got %d %q, want 500", proxy, refused.status, refused.body)
		}
		if got := outside.got(path); got != nil {
			t.Errorf("%s: request reached %s with Authorization %q", proxy, got.Host, got.Header.Get("Authorization"))
		}
		if status, _, _ := tunnel(t, proxy, mistaken, "allowed.test:443"); status != http.StatusInternalServerError {
			t.Errorf("%s: tunnel for which the Supervisor returned a key: got status %d, want 500", proxy, status)
		}
	}
}

// tunnel asks the forward proxy for a tunnel to address, as a client does for
// an HTTPS URL. It returns the response status and the connection, which is
// the tunnel if the status is 200.
func tunnel(t *testing.T, proxy, password, address string) (int, net.Conn, *bufio.Reader) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", proxy+":3128", patience)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	connection.SetDeadline(time.Now().Add(patience))
	credentials := base64.StdEncoding.EncodeToString([]byte("session:" + password))
	fmt.Fprintf(connection, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", address, address, credentials)
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil {
		t.Fatalf("CONNECT %s: %v", address, err)
	}
	return response.StatusCode, connection, reader
}

// Of a tunnel, the proxy sees only the host and port, and passes the rest
// through unread.
func TestTunnel(t *testing.T) {
	t.Parallel()
	status, connection, reader := tunnel(t, listed, token, "allowed.test:443")
	if status != http.StatusOK {
		t.Fatalf("tunnel to an allowed host: got status %d, want 200", status)
	}
	fmt.Fprint(connection, "what a pod sends\n")
	if back, err := reader.ReadString('\n'); err != nil || back != "what a pod sends\n" {
		t.Errorf("tunnel echoed %q: %v", back, err)
	}

	for name, refused := range map[string]struct {
		password, address string
		status            int
	}{
		"to a disallowed host":     {token, "other.test:443", http.StatusForbidden},
		"to another port":          {token, "allowed.test:8443", http.StatusForbidden},
		"with an unknown password": {"another-token", "allowed.test:443", http.StatusProxyAuthRequired},
	} {
		if status, _, _ := tunnel(t, listed, refused.password, refused.address); status != refused.status {
			t.Errorf("tunnel %s: got status %d, want %d", name, status, refused.status)
		}
	}
}

// The metrics port serves the request metrics and nothing else of Envoy's
// admin interface.
func TestMetrics(t *testing.T) {
	t.Parallel()
	ask(t, listed, "/v1/counted", "Bearer "+token)
	fetch(t, listed, token, "http://allowed.test/counted")
	fetch(t, listed, token, "http://other.test/counted")
	// series sums the samples whose name and labels start with prefix.
	series := func(prefix string) float64 {
		response, err := http.Get("http://" + listed + ":9092/metrics")
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("get metrics: %v: %v", response, err)
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
		`envoy_http_downstream_rq_time_count{envoy_http_conn_manager_prefix="llm"`,
	} {
		// Envoy flushes histograms periodically.
		deadline := time.Now().Add(patience)
		for series(counted) < 1 {
			if time.Now().After(deadline) {
				t.Errorf("metrics have no %s", counted)
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	for _, path := range []string{"/", "/stats", "/stats/prometheus", "/config_dump", "/quitquitquit", "/metrics/../config_dump", "/metricsx"} {
		response, err := http.Post("http://"+listed+":9092"+path, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s on the metrics port: got status %d, want 404", path, response.StatusCode)
		}
	}
}
