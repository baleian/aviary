// Package web tests the server of the web UI's image: nginx with the
// configuration that the image starts it with, which `make test-web-server`
// starts once in front of an API and once alone. The tests play the browser
// and the API.
package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The servers: one passes the requests of the API on, and one has no API.
const (
	fronting = "http://fronting:8080"
	alone    = "http://alone:8080"
)

// patience is how long a test waits for a server.
const patience = 30 * time.Second

// page is the page that stands in for the web UI.
const page = "<!doctype html><title>the web UI</title>\n"

// behind plays the API behind the server.
var behind = &api{}

// api answers the requests that the server passes on with answer, and keeps
// them. It calls begun with a request before it reads the body.
type api struct {
	mu     sync.Mutex
	asked  []asked
	answer http.HandlerFunc
	begun  func(*http.Request)
}

// asked is a request that the API got.
type asked struct {
	method, path, authorization, body string
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	begun := a.begun
	a.mu.Unlock()
	if begun != nil {
		begun(r)
	}
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	a.asked = append(a.asked, asked{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), string(body)})
	answer := a.answer
	a.mu.Unlock()
	if answer == nil {
		http.NotFound(w, r)
		return
	}
	answer(w, r)
}

// answers has the API answer with answer from now on, and forget what it
// was asked.
func (a *api) answers(t *testing.T, answer http.HandlerFunc) {
	t.Helper()
	a.mu.Lock()
	a.asked, a.answer = nil, answer
	a.mu.Unlock()
	t.Cleanup(func() {
		a.mu.Lock()
		a.asked, a.answer, a.begun = nil, nil, nil
		a.mu.Unlock()
	})
}

func (a *api) got() []asked {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]asked(nil), a.asked...)
}

// browser sends requests as they are: it does not follow redirects, and it
// decompresses nothing by itself.
var browser = &http.Client{
	Timeout:       patience,
	Transport:     &http.Transport{DisableCompression: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func TestMain(m *testing.M) {
	if os.Getenv("AVIARY_TEST_WEB") == "" {
		fmt.Fprintln(os.Stderr, "AVIARY_TEST_WEB is not set: run the tests with `make test-web-server`")
		os.Exit(1)
	}
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	go http.Serve(listener, behind)
	// The server in front of the API starts once it can resolve the API.
	deadline := time.Now().Add(patience)
	for _, server := range []string{fronting, alone} {
		for {
			response, err := browser.Get(server + "/healthz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusNoContent {
					break
				}
			}
			if time.Now().After(deadline) {
				fmt.Fprintf(os.Stderr, "%s is not ready: %v\n", server, err)
				os.Exit(1)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	os.Exit(m.Run())
}

// get sends a GET request with headers, given as names and values in turn,
// and returns the response and its body.
func get(t *testing.T, url string, headers ...string) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := browser.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(body)
}

// The page is served with the headers that confine what it loads, and is
// asked for again every time.
func TestServesThePage(t *testing.T) {
	for _, server := range []string{fronting, alone} {
		response, body := get(t, server+"/")
		if response.StatusCode != http.StatusOK || body != page || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s/ = %d %q (%s), want the page", server, response.StatusCode, body, response.Header.Get("Content-Type"))
		}
		policy := response.Header.Get("Content-Security-Policy")
		for _, part := range []string{"default-src 'self'", "img-src 'self' data: blob:", "frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'"} {
			if !strings.Contains(policy, part) {
				t.Errorf("%s/: Content-Security-Policy %q lacks %q", server, policy, part)
			}
		}
		for name, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
			"Cache-Control":          "no-cache",
			// The server does not say its version.
			"Server": "nginx",
		} {
			if got := response.Header.Get(name); got != want {
				t.Errorf("%s/: %s = %q, want %q", server, name, got, want)
			}
		}
	}
}

// The page in which the web UI shows a web page that an agent wrote has no
// origin, whatever frames it, and only the web UI frames it.
func TestServesTheFrameOfAPageWithoutAnOrigin(t *testing.T) {
	for _, server := range []string{fronting, alone} {
		response, body := get(t, server+"/sandbox.html")
		if response.StatusCode != http.StatusOK || !strings.Contains(body, "a frame") {
			t.Fatalf("%s/sandbox.html = %d %q, want the frame", server, response.StatusCode, body)
		}
		// One policy, and not that of the web UI, which would keep the page
		// out of every frame and its scripts from running.
		if policies := response.Header.Values("Content-Security-Policy"); len(policies) != 1 || policies[0] != "sandbox allow-scripts; frame-ancestors 'self'" {
			t.Errorf("%s/sandbox.html: Content-Security-Policy %q", server, policies)
		}
		for name, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
			"Cache-Control":          "no-cache",
		} {
			if got := response.Header.Get(name); got != want {
				t.Errorf("%s/sandbox.html: %s = %q, want %q", server, name, got, want)
			}
		}
	}
}

// Every path that is not a file is a screen of the web UI: the page, which
// reads the path.
func TestServesThePageForItsScreens(t *testing.T) {
	for _, path := range []string{"/projects", "/p/storefront/agents/reviewer/s/0195", "/signin?code=a&state=b", "/v2/nothing"} {
		response, body := get(t, alone+path)
		if response.StatusCode != http.StatusOK || body != page || response.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s = %d %q, Cache-Control %q; want the page, not cached", path, response.StatusCode, body, response.Header.Get("Cache-Control"))
		}
	}
	// A file that is not there is not the page.
	for _, path := range []string{"/assets/none.js", "/fonts/none/font.woff2"} {
		if response, _ := get(t, alone+path); response.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, response.StatusCode)
		}
	}
}

// The files of a build have names that change with their contents, so they
// are kept, and they are compressed for a browser that asks.
func TestServesTheFilesOfABuild(t *testing.T) {
	response, body := get(t, alone+"/assets/app.js")
	if response.StatusCode != http.StatusOK || len(body) != 4096 {
		t.Fatalf("/assets/app.js = %d with %d bytes, want the file", response.StatusCode, len(body))
	}
	if got := response.Header.Get("Cache-Control"); got != "max-age=31536000" {
		t.Errorf("Cache-Control = %q, want a year", got)
	}
	if got := response.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q for a browser that did not ask", got)
	}
	response, body = get(t, alone+"/assets/app.js", "Accept-Encoding", "gzip")
	if response.Header.Get("Content-Encoding") != "gzip" || len(body) >= 4096 {
		t.Errorf("Content-Encoding = %q with %d bytes, want the file compressed", response.Header.Get("Content-Encoding"), len(body))
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
}

// The settings of the web UI are what the environment of the server says.
func TestTellsTheWebUIItsSettings(t *testing.T) {
	type settings struct {
		API  string `json:"api"`
		OIDC struct {
			Issuer   string `json:"issuer"`
			ClientID string `json:"clientId"`
			Scopes   string `json:"scopes"`
		} `json:"oidc"`
	}
	read := func(server string) settings {
		t.Helper()
		response, body := get(t, server+"/config.json")
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" || response.Header.Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s/config.json = %d (%s), Cache-Control %q", server, response.StatusCode, response.Header.Get("Content-Type"), response.Header.Get("Cache-Control"))
		}
		var told settings
		if err := json.Unmarshal([]byte(body), &told); err != nil {
			t.Fatalf("%s/config.json is not JSON: %v: %s", server, err, body)
		}
		return told
	}
	told := read(fronting)
	if told.API != "" || told.OIDC.Issuer != "https://login.example.com/realms/main" || told.OIDC.ClientID != "aviary" || told.OIDC.Scopes != "openid profile email groups" {
		t.Errorf("settings = %+v", told)
	}
	// The other server names an API for the browser to call, and no issuer.
	if told := read(alone); told.API != "https://api.example.com" || told.OIDC.Issuer != "" || told.OIDC.ClientID != "" || told.OIDC.Scopes != "" {
		t.Errorf("settings without an issuer = %+v, want only the API", told)
	}
}

// The server in front of the API passes the requests of /v1 on as they
// are, and the answers back.
func TestPassesTheAPIOn(t *testing.T) {
	behind.answers(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"code": "unauthenticated"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"code": "queue_full"}`)
	})
	response, body := get(t, fronting+"/v1/me")
	if response.StatusCode != http.StatusUnauthorized || body != `{"code": "unauthenticated"}` {
		t.Errorf("/v1/me without a token = %d %s", response.StatusCode, body)
	}
	request, _ := http.NewRequest(http.MethodPost, fronting+"/v1/sessions/s1/messages?wait=1", strings.NewReader(`{"text": "hello"}`))
	request.Header.Set("Authorization", "Bearer a-token")
	sent, err := browser.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := io.ReadAll(sent.Body)
	sent.Body.Close()
	if sent.StatusCode != http.StatusTooManyRequests || string(answer) != `{"code": "queue_full"}` || sent.Header.Get("Retry-After") != "7" || sent.Header.Get("Content-Type") != "application/json" {
		t.Errorf("message = %d %s, Retry-After %q", sent.StatusCode, answer, sent.Header.Get("Retry-After"))
	}
	want := []asked{
		{http.MethodGet, "/v1/me", "", ""},
		{http.MethodPost, "/v1/sessions/s1/messages?wait=1", "Bearer a-token", `{"text": "hello"}`},
	}
	if got := behind.got(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("the API was asked %+v, want %+v", got, want)
	}
}

// A file that a message carries is passed on whatever its size, and reaches
// the API while the browser still sends it: the server keeps none of it.
func TestPassesAnUploadOnAsItComes(t *testing.T) {
	begun := make(chan int64, 1)
	behind.answers(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"id": "a1"}`)
	})
	behind.mu.Lock()
	behind.begun = func(r *http.Request) { begun <- r.ContentLength }
	behind.mu.Unlock()

	// More megabytes than a server takes unless it is told to.
	half := strings.Repeat("a", 2<<20)
	file, sending := io.Pipe()
	request, _ := http.NewRequest(http.MethodPost, fronting+"/v1/sessions/s1/attachments?name=notes.txt", file)
	request.ContentLength = int64(2 * len(half))
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Authorization", "Bearer a-token")
	sent := make(chan error, 1)
	go func() {
		_, err := io.WriteString(sending, half)
		sent <- err
	}()
	answered := make(chan *http.Response, 1)
	go func() {
		response, err := browser.Do(request)
		if err != nil {
			t.Error(err)
		}
		answered <- response
	}()
	select {
	case size := <-begun:
		if size != request.ContentLength {
			t.Errorf("the API was told of %d bytes, want %d", size, request.ContentLength)
		}
	case <-time.After(5 * time.Second):
		t.Error("the API did not get the upload while the browser was sending it")
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	io.WriteString(sending, half)
	sending.Close()
	response := <-answered
	if response == nil {
		return
	}
	answer, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || string(answer) != `{"id": "a1"}` {
		t.Errorf("upload = %d %s, want 201 from the API", response.StatusCode, answer)
	}
	got := behind.got()
	if len(got) != 1 || got[0].method != http.MethodPost || got[0].path != "/v1/sessions/s1/attachments?name=notes.txt" || got[0].authorization != "Bearer a-token" || got[0].body != half+half {
		t.Errorf("the API got %d requests, want the upload with its %d bytes", len(got), 2*len(half))
	}
}

// Only /v1 is the API: the health of the server is its own, and the rest is
// the web UI.
func TestPassesOnlyTheAPIOn(t *testing.T) {
	behind.answers(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "from the API") })
	for path, want := range map[string]int{"/healthz": http.StatusNoContent, "/v10/me": http.StatusOK, "/metrics": http.StatusOK} {
		response, body := get(t, fronting+path)
		if response.StatusCode != want || body == "from the API" {
			t.Errorf("%s = %d %q, want %d from the server itself", path, response.StatusCode, body, want)
		}
	}
	if got := behind.got(); len(got) != 0 {
		t.Errorf("the API was asked %+v", got)
	}
}

// A server without an API has no /v1.
func TestHasNoAPIAlone(t *testing.T) {
	behind.answers(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "from the API") })
	if response, body := get(t, alone+"/v1/me"); response.StatusCode != http.StatusNotFound || body == page {
		t.Errorf("/v1/me = %d %q, want 404", response.StatusCode, body)
	}
	if got := behind.got(); len(got) != 0 {
		t.Errorf("the API was asked %+v", got)
	}
}

// An event of a stream reaches the browser when the API sends it, not when
// the stream ends or a buffer is full.
func TestPassesAnEventStreamOnAsItComes(t *testing.T) {
	more := make(chan struct{})
	after := make(chan string, 1)
	behind.answers(t, func(w http.ResponseWriter, r *http.Request) {
		after <- r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-more:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "event: turn.started\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-more:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "event: turn.completed\ndata: {}\n\n")
	})
	// within fails the test if what does not happen within a few seconds.
	within := func(what string, do func()) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			do()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not come while the stream was open", what)
		}
	}
	var stream *http.Response
	// The stream is open for the browser before its first event.
	within("the start of the stream", func() {
		request, _ := http.NewRequest(http.MethodGet, fronting+"/v1/turns/t1/events", nil)
		request.Header.Set("Last-Event-ID", "4")
		var err error
		if stream, err = (&http.Client{}).Do(request); err != nil {
			t.Error(err)
		}
	})
	if stream == nil {
		return
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK || stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream = %d (%s)", stream.StatusCode, stream.Header.Get("Content-Type"))
	}
	// The API is told where the stream goes on from.
	if got := <-after; got != "4" {
		t.Errorf("Last-Event-ID = %q at the API, want 4", got)
	}
	lines := bufio.NewReader(stream.Body)
	for _, event := range []string{"turn.started", "turn.completed"} {
		more <- struct{}{}
		within("the event "+event, func() {
			for {
				line, err := lines.ReadString('\n')
				if err != nil {
					t.Errorf("the stream ended before %s: %v", event, err)
					return
				}
				if line == "event: "+event+"\n" {
					return
				}
			}
		})
	}
}
