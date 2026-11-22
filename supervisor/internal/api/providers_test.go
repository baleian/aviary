package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/events"
	memorylog "aviary/supervisor/events/memory"
	"aviary/supervisor/internal/testenv"
	memorystore "aviary/supervisor/store/memory"
)

// A trial does not wait longer than it may take for an endpoint that the
// proxy reaches and that does not answer.
func TestATrialOfAProviderThatDoesNotAnswerEnds(t *testing.T) {
	timeout := trialTimeout
	trialTimeout = 200 * time.Millisecond
	t.Cleanup(func() { trialTimeout = timeout })
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
	var got tried
	status := post("/v1/projects/"+project.ID+"/providers/test", `{"url": "https://api.example.com/v1"}`, &got)
	if took := time.Since(began); status != http.StatusOK || got.Reached || got.Reason != "the endpoint did not answer in 200ms" || took > 20*trialTimeout {
		t.Errorf("trial of an endpoint that does not answer: status %d, %+v after %s", status, got, took)
	}
}
