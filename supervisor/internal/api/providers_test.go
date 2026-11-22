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
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

// The Egress Proxy sets the headers of a provider on every model request as
// they are kept, so they are checked when they are given: a header that would
// send a request elsewhere, or change how it is carried, is refused.
func TestTheHeadersOfAProviderAreChecked(t *testing.T) {
	kept, err := headersOf([]store.Header{{Name: "X-Team", Value: " a team\t"}, {Name: "accept-language", Value: `en;q=0.8, "*"`}})
	if want := []store.Header{{Name: "x-team", Value: "a team"}, {Name: "accept-language", Value: `en;q=0.8, "*"`}}; err != nil || !slices.Equal(kept, want) {
		t.Errorf("headersOf = %v, %v, want %v", kept, err, want)
	}
	if kept, err := headersOf(nil); err != nil || kept != nil {
		t.Errorf("headersOf of no headers = %v, %v", kept, err)
	}

	// many returns that many headers with different names, each with the
	// value.
	many := func(count int, value string) []store.Header {
		headers := make([]store.Header, count)
		for i := range headers {
			headers[i] = store.Header{Name: fmt.Sprintf("x-%02d", i), Value: value}
		}
		return headers
	}
	if kept, err := headersOf(many(20, strings.Repeat("v", 8192/20-4))); err != nil || len(kept) != 20 {
		t.Errorf("headersOf of as many and as large headers as allowed = %d headers, %v", len(kept), err)
	}

	const name, reserved = "headers[0].name must be the name of an HTTP header", ", which is reserved"
	const value = "headers[0].value must be the value of an HTTP header, and not empty"
	for _, test := range []struct {
		given []store.Header
		why   string
	}{
		{[]store.Header{{Value: "a"}}, name},
		{[]store.Header{{Name: "x team", Value: "a"}}, name},
		{[]store.Header{{Name: ":path", Value: "/"}}, name},
		{[]store.Header{{Name: "x-team:", Value: "a"}}, name},
		{[]store.Header{{Name: "x-tëam", Value: "a"}}, name},
		{[]store.Header{{Name: "Authorization", Value: "Bearer a"}}, "headers[0].name must not be authorization; set the endpoint's key in api_key"},
		{[]store.Header{{Name: "Host", Value: "other.example.com"}}, "headers[0].name must not be host" + reserved},
		{[]store.Header{{Name: "content-length", Value: "0"}}, "headers[0].name must not be content-length" + reserved},
		{[]store.Header{{Name: "Transfer-Encoding", Value: "chunked"}}, "headers[0].name must not be transfer-encoding" + reserved},
		{[]store.Header{{Name: "X-Aviary-Upstream", Value: "other.example.com"}}, "headers[0].name must not be x-aviary-upstream" + reserved},
		{[]store.Header{{Name: "x-envoy-upstream-rq-timeout-ms", Value: "1"}}, "headers[0].name must not be x-envoy-upstream-rq-timeout-ms" + reserved},
		{[]store.Header{{Name: "x-team", Value: "a"}, {Name: "X-Team", Value: "b"}}, "headers[1].name must not be x-team, which another header has"},
		{[]store.Header{{Name: "x-team"}}, value},
		{[]store.Header{{Name: "x-team", Value: " \t"}}, value},
		{[]store.Header{{Name: "x-team", Value: "a\r\nx-other: b"}}, value},
		{[]store.Header{{Name: "x-team", Value: "a\x00"}}, value},
		{many(21, "a"), "headers must be at most 20"},
		{many(2, strings.Repeat("v", 4096)), "the names and values of headers must be at most 8192 bytes together"},
	} {
		var refused *apiError
		if kept, err := headersOf(test.given); !errors.As(err, &refused) || refused.Message != test.why || refused.status != http.StatusBadRequest {
			t.Errorf("headersOf(%.60q) = %v, %v, want it refused with %q", test.given, kept, err, test.why)
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
