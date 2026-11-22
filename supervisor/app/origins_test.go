package app

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"aviary/supervisor/identity"
	"aviary/supervisor/internal/api"
)

// asked sends a request as the page of an origin does, with headers given
// as names and values in turn.
func (s *supervisor) asked(method, path, origin string, headers ...string) *http.Response {
	s.t.Helper()
	request, err := http.NewRequest(method, s.api+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := httpClient.Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { response.Body.Close() })
	return response
}

// names returns the names in a header that lists them, in lower case.
func names(header string) []string {
	var listed []string
	for _, name := range strings.Split(header, ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			listed = append(listed, name)
		}
	}
	return listed
}

const (
	page    = "https://aviary.example.com"
	another = "https://elsewhere.example.com"
)

// A browser asks before it sends what a page of another origin may not send
// by itself. It is told what the pages of an allowed origin may send, and
// it asks without a token.
func TestBrowsersAreToldWhatPagesMaySend(t *testing.T) {
	t.Parallel()
	installation := install(t)
	installation.origins = []string{page, "https://*.apps.example.com"}
	installation.callers = api.Callers{Provider: tokens{"a-token": identity.Caller{Subject: "subject-1"}}}
	s := installation.supervisor()

	for _, asking := range []struct{ origin, method, path, headers string }{
		{page, http.MethodPost, "/v1/projects", "authorization,content-type"},
		{page, http.MethodPut, "/v1/agents/a1", "authorization,content-type"},
		{page, http.MethodPatch, "/v1/sessions/s1", "authorization,content-type"},
		{page, http.MethodDelete, "/v1/sessions/s1", "authorization"},
		{page, http.MethodGet, "/v1/turns/t1/events", "authorization,last-event-id"},
		{"https://team.apps.example.com", http.MethodGet, "/v1/me", "authorization"},
	} {
		response := s.asked(http.MethodOptions, asking.path, asking.origin,
			"Access-Control-Request-Method", asking.method, "Access-Control-Request-Headers", asking.headers)
		allowed := names(response.Header.Get("Access-Control-Allow-Headers"))
		if response.StatusCode != http.StatusNoContent || response.Header.Get("Access-Control-Allow-Origin") != asking.origin ||
			!slices.Contains(names(response.Header.Get("Access-Control-Allow-Methods")), strings.ToLower(asking.method)) ||
			slices.ContainsFunc(names(asking.headers), func(name string) bool { return !slices.Contains(allowed, name) }) ||
			response.Header.Get("Access-Control-Max-Age") != "600" || !slices.Contains(names(strings.Join(response.Header.Values("Vary"), ",")), "origin") {
			t.Errorf("%s asks to send %s %s with %s: %d %v", asking.origin, asking.method, asking.path, asking.headers, response.StatusCode, response.Header)
		}
	}
	// What the pages of another origin ask is not allowed, nor a method or a
	// header that the API has no use for.
	for _, asking := range []struct{ origin, method, headers string }{
		{another, http.MethodGet, "authorization"},
		{"https://apps.example.com.elsewhere.example.com", http.MethodGet, "authorization"},
		{page, http.MethodTrace, "authorization"},
		{page, http.MethodGet, "authorization,x-another"},
	} {
		response := s.asked(http.MethodOptions, "/v1/me", asking.origin,
			"Access-Control-Request-Method", asking.method, "Access-Control-Request-Headers", asking.headers)
		if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s asks to send %s with %s: allowed for %q", asking.origin, asking.method, asking.headers, got)
		}
	}
}

// The answers to the pages of an allowed origin say so, whatever they are,
// so that the page may read them. The answers to others do not.
func TestPagesOfAllowedOriginsReadTheAnswers(t *testing.T) {
	t.Parallel()
	installation := install(t)
	installation.origins = []string{page}
	installation.callers = api.Callers{Provider: tokens{"a-token": identity.Caller{Subject: "subject-1"}}}
	s := installation.supervisor()

	for path, want := range map[string]int{
		"/v1/me":            http.StatusOK,
		"/v1/projects/none": http.StatusNotFound,
	} {
		response := s.asked(http.MethodGet, path, page, "Authorization", "Bearer a-token")
		if response.StatusCode != want || response.Header.Get("Access-Control-Allow-Origin") != page ||
			!slices.Contains(names(response.Header.Get("Access-Control-Expose-Headers")), "retry-after") ||
			!slices.Contains(names(strings.Join(response.Header.Values("Vary"), ",")), "origin") {
			t.Errorf("GET %s from the page = %d %v, want %d that the page may read", path, response.StatusCode, response.Header, want)
		}
	}
	// A refusal for want of a token can be read too: the page signs in then.
	if response := s.asked(http.MethodGet, "/v1/me", page); response.StatusCode != http.StatusUnauthorized || response.Header.Get("Access-Control-Allow-Origin") != page {
		t.Errorf("GET /v1/me without a token = %d, allowed for %q", response.StatusCode, response.Header.Get("Access-Control-Allow-Origin"))
	}
	// The API answers a page of another origin, and a client that is no
	// browser, as it answers anyone: the browser keeps the answer from the
	// page.
	for _, origin := range []string{another, ""} {
		response := s.asked(http.MethodGet, "/v1/me", origin, "Authorization", "Bearer a-token")
		if response.StatusCode != http.StatusOK || response.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("GET /v1/me from %q = %d, allowed for %q", origin, response.StatusCode, response.Header.Get("Access-Control-Allow-Origin"))
		}
	}
}

// A stream of events is open to the pages of an allowed origin like any
// other answer.
func TestPagesOfAllowedOriginsFollowStreams(t *testing.T) {
	t.Parallel()
	installation := install(t)
	installation.origins = []string{"*"}
	s := installation.supervisor()
	var project struct{ ID string }
	s.request(http.MethodPost, "/v1/projects", `{"slug": "a-project"}`, &project)
	response := s.asked(http.MethodGet, "/v1/projects/"+project.ID+"/events", page)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("stream = %d (%s), allowed for %q", response.StatusCode, response.Header.Get("Content-Type"), response.Header.Get("Access-Control-Allow-Origin"))
	}
}

// Without allowed origins the API says nothing to browsers about origins.
func TestNoOriginIsAllowedByDefault(t *testing.T) {
	t.Parallel()
	s := install(t).supervisor()
	if response := s.asked(http.MethodGet, "/v1/me", page); response.StatusCode != http.StatusOK || response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("GET /v1/me = %d, allowed for %q", response.StatusCode, response.Header.Get("Access-Control-Allow-Origin"))
	}
	response := s.asked(http.MethodOptions, "/v1/me", page, "Access-Control-Request-Method", http.MethodGet)
	if response.StatusCode == http.StatusNoContent || response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("OPTIONS /v1/me = %d, allowed for %q", response.StatusCode, response.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestBrowserOrigins(t *testing.T) {
	for encoded, want := range map[string][]string{
		`[]`:    {},
		`["*"]`: {"*"},
		`["https://aviary.example.com", "http://localhost:5173", "https://*.apps.example.com"]`: {"https://aviary.example.com", "http://localhost:5173", "https://*.apps.example.com"},
	} {
		t.Setenv(allowedOrigins, encoded)
		if got, err := browserOrigins(); err != nil || !slices.Equal(got, want) {
			t.Errorf("%s = %v, %v; want %v", encoded, got, err, want)
		}
	}
	// An origin is a scheme and a host, with a port if it is not the
	// default: an address with more is a mistake.
	for _, encoded := range []string{
		``,
		`aviary.example.com`,
		`["aviary.example.com"]`,
		`["https://aviary.example.com/"]`,
		`["https://aviary.example.com/web"]`,
		`["https://aviary.example.com?a=b"]`,
		`["https://aviary.example.com#top"]`,
		`["https://user@aviary.example.com"]`,
		`["ftp://aviary.example.com"]`,
		`["https://"]`,
		`["https://aviary.example.com", ""]`,
	} {
		t.Setenv(allowedOrigins, encoded)
		if got, err := browserOrigins(); err == nil {
			t.Errorf("%s = %v, want an error", encoded, got)
		}
	}
}
