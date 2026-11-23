package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/events"
	memorylog "aviary/supervisor/events/memory"
	"aviary/supervisor/internal/testenv"
	memorystore "aviary/supervisor/store/memory"
)

// The Egress Proxy sets the headers of an endpoint on every request as they
// are kept, so they are checked when they are given: a header that would send
// a request elsewhere, or change how it is carried, is refused.
func TestTheHeadersOfAnEndpointAreChecked(t *testing.T) {
	plain := func(name, value string) headerField { return headerField{Name: name, Value: value} }
	kept, err := headersOf([]headerField{plain("X-Team", " a team\t"), plain("accept-language", `en;q=0.8, "*"`)}, false)
	if want := []headerField{plain("x-team", "a team"), plain("accept-language", `en;q=0.8, "*"`)}; err != nil || !slices.Equal(kept, want) {
		t.Errorf("headersOf = %v, %v, want %v", kept, err, want)
	}
	if kept, err := headersOf(nil, false); err != nil || kept != nil {
		t.Errorf("headersOf of no headers = %v, %v", kept, err)
	}
	// A header has a secret in place of its value.
	kept, err = headersOf([]headerField{{Name: "X-Token", Secret: &secretField{Value: " s3cret "}}}, false)
	if err != nil || len(kept) != 1 || kept[0].Name != "x-token" || kept[0].Value != "" || *kept[0].Secret != (secretField{Value: "s3cret"}) {
		t.Errorf("headersOf of a secret = %+v, %v", kept, err)
	}
	// An endpoint that takes no key of its own can take it in authorization.
	if kept, err := headersOf([]headerField{{Name: "Authorization", Secret: &secretField{Stored: true}}}, true); err != nil || kept[0].Name != "authorization" {
		t.Errorf("headersOf of authorization = %+v, %v", kept, err)
	}

	// many returns that many headers with different names, each with the
	// value.
	many := func(count int, value string) []headerField {
		headers := make([]headerField, count)
		for i := range headers {
			headers[i] = plain(fmt.Sprintf("x-%02d", i), value)
		}
		return headers
	}
	if kept, err := headersOf(many(20, strings.Repeat("v", 8192/20-4)), false); err != nil || len(kept) != 20 {
		t.Errorf("headersOf of as many and as large headers as allowed = %d headers, %v", len(kept), err)
	}

	const name, reserved = "headers[0].name must be the name of an HTTP header", ", which is reserved"
	const value = "headers[0].value must be the value of an HTTP header, and not empty"
	const size = "the names and values of headers must be at most 8192 bytes together"
	for _, test := range []struct {
		given []headerField
		why   string
	}{
		{[]headerField{plain("", "a")}, name},
		{[]headerField{plain("x team", "a")}, name},
		{[]headerField{plain(":path", "/")}, name},
		{[]headerField{plain("x-team:", "a")}, name},
		{[]headerField{plain("x-tëam", "a")}, name},
		{[]headerField{plain("Authorization", "Bearer a")}, "headers[0].name must not be authorization; set the endpoint's key in api_key"},
		{[]headerField{plain("Host", "other.example.com")}, "headers[0].name must not be host" + reserved},
		{[]headerField{plain("content-length", "0")}, "headers[0].name must not be content-length" + reserved},
		{[]headerField{plain("Transfer-Encoding", "chunked")}, "headers[0].name must not be transfer-encoding" + reserved},
		{[]headerField{plain("X-Aviary-Upstream", "other.example.com")}, "headers[0].name must not be x-aviary-upstream" + reserved},
		{[]headerField{plain("x-envoy-upstream-rq-timeout-ms", "1")}, "headers[0].name must not be x-envoy-upstream-rq-timeout-ms" + reserved},
		{[]headerField{plain("Mcp-Session-Id", "a")}, "headers[0].name must not be mcp-session-id" + reserved},
		{[]headerField{plain("x-team", "a"), plain("X-Team", "b")}, "headers[1].name must not be x-team, which another header has"},
		{[]headerField{plain("x-team", "")}, value},
		{[]headerField{plain("x-team", " \t")}, value},
		{[]headerField{plain("x-team", "a\r\nx-other: b")}, value},
		{[]headerField{plain("x-team", "a\x00")}, value},
		{[]headerField{{Name: "x-team", Value: "a", Secret: &secretField{Value: "b"}}}, "headers[0] must have a value or a secret"},
		{[]headerField{{Name: "x-team", Secret: &secretField{Value: "a\r\nx-other: b"}}}, "headers[0].secret.value must be the value of an HTTP header"},
		{many(21, "a"), "headers must be at most 20"},
		{many(2, strings.Repeat("v", 4096)), size},
		// A secret whose length is not known counts all the same.
		{append(many(19, strings.Repeat("v", 8192/19-4)), headerField{Name: "x-last", Secret: &secretField{Stored: true}}), size},
	} {
		var refused *apiError
		if kept, err := headersOf(test.given, false); !errors.As(err, &refused) || refused.Message != test.why || refused.status != http.StatusBadRequest {
			t.Errorf("headersOf(%.60v) = %v, %v, want it refused with %q", test.given, kept, err, test.why)
		}
	}
}

// The models of an endpoint that the proxy reaches and that does not answer
// are not waited for longer than a listing may take.
func TestTheListingOfAnEndpointThatDoesNotAnswerEnds(t *testing.T) {
	timeout := listTimeout
	listTimeout = 200 * time.Millisecond
	t.Cleanup(func() { listTimeout = timeout })
	// The proxy holds the request for as long as its client waits.
	proxy := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(proxy.Close)
	models, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	server := httptest.NewServer(New(ctx, memorystore.New(), events.New(memorylog.New()), nil, testenv.Keys(t), nil, Callers{}, models))
	t.Cleanup(server.Close)
	post := func(path, body string, into any) int {
		t.Helper()
		response, err := http.Post(server.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if err := json.NewDecoder(response.Body).Decode(into); err != nil {
			t.Fatal(err)
		}
		return response.StatusCode
	}
	var project struct{ ID string }
	if status := post("/v1/projects", `{"slug": "a-project"}`, &project); status != http.StatusCreated {
		t.Fatalf("create project: status %d", status)
	}

	began := time.Now()
	var got listed
	status := post("/v1/projects/"+project.ID+"/providers/models", `{"url": "https://api.example.com/v1"}`, &got)
	if took := time.Since(began); status != http.StatusOK || got.Reached || got.Reason != "the endpoint did not answer in 200ms" || took > 20*listTimeout {
		t.Errorf("the models of an endpoint that does not answer: status %d, %+v after %s", status, got, took)
	}

	// Nor is an endpoint reached if the proxy is not.
	proxy.Close()
	got = listed{}
	if status := post("/v1/projects/"+project.ID+"/providers/models", `{"url": "https://api.example.com/v1"}`, &got); status != http.StatusOK || got.Reached || got.Reason != "the Egress Proxy did not answer" {
		t.Errorf("the models of an endpoint without the proxy: status %d, %+v", status, got)
	}
}
