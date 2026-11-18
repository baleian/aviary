package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"aviary/supervisor/identity"
)

// tokens is an identity provider that knows the callers of some tokens.
type tokens map[string]identity.Caller

func (t tokens) Caller(_ context.Context, token string) (identity.Caller, error) {
	caller, known := t[token]
	if !known {
		return identity.Caller{}, errors.New("no such token")
	}
	return caller, nil
}

// me is what the API answers of the caller of a request.
type me struct {
	Subject     string   `json:"subject"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Groups      []string `json:"groups"`
}

// An installation with an identity provider takes a request only with a
// token of the provider, and tells the caller what it read from the token.
func TestCallersAreAskedWhoTheyAre(t *testing.T) {
	installation := install(t)
	alice := identity.Caller{Subject: "subject-1", Username: "alice@example.com", DisplayName: "Alice Kim", Groups: []string{"team-a"}}
	installation.callers = tokens{"token-of-alice": alice}
	s := installation.supervisor()

	// send sends a request with the header that tells who sends it.
	send := func(method, path, authorization string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(method, s.api+path, strings.NewReader(`{"name": "a project"}`))
		if err != nil {
			t.Fatal(err)
		}
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	for _, authorization := range []string{"", "Bearer", "Bearer another-token", "Basic token-of-alice", "token-of-alice"} {
		for _, request := range [][2]string{{http.MethodPost, "/v1/projects"}, {http.MethodGet, "/v1/me"}, {http.MethodGet, "/v1/nothing"}} {
			response := send(request[0], request[1], authorization)
			var answer struct{ Code string }
			_ = json.NewDecoder(response.Body).Decode(&answer)
			if response.StatusCode != http.StatusUnauthorized || answer.Code != "unauthenticated" || response.Header.Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("%s %s with the header %q is answered with %d %q and is asked for %q",
					request[0], request[1], authorization, response.StatusCode, answer.Code, response.Header.Get("WWW-Authenticate"))
			}
		}
	}
	// Kubernetes is not asked who it is.
	if response := send(http.MethodGet, "/healthz", ""); response.StatusCode != http.StatusOK {
		t.Errorf("a Supervisor is asked whether it is up, and answers %d", response.StatusCode)
	}

	for _, authorization := range []string{"Bearer token-of-alice", "bearer token-of-alice"} {
		if response := send(http.MethodPost, "/v1/projects", authorization); response.StatusCode != http.StatusCreated {
			t.Errorf("a project is created with the header %q, and that is answered with %d", authorization, response.StatusCode)
		}
	}
	s.token = "token-of-alice"
	var told me
	if status, _ := s.request(http.MethodGet, "/v1/me", "", &told); status != http.StatusOK ||
		!reflect.DeepEqual(told, me{Subject: "subject-1", Username: "alice@example.com", DisplayName: "Alice Kim", Groups: []string{"team-a"}}) {
		t.Errorf("the caller is told %d %+v", status, told)
	}
}

// An installation without an identity provider asks nobody: a request needs
// no token, and what it brings is not looked at.
func TestNobodyIsAskedWithoutAnIdentityProvider(t *testing.T) {
	s := install(t).supervisor()
	for _, token := range []string{"", "any-token"} {
		s.token = token
		var answer map[string]any
		if status, _ := s.request(http.MethodGet, "/v1/me", "", &answer); status != http.StatusOK || len(answer) != 0 {
			t.Errorf("a caller with the token %q is told %d %v", token, status, answer)
		}
		if status, _ := s.request(http.MethodPost, "/v1/projects", `{"name": "a project"}`, nil); status != http.StatusCreated {
			t.Errorf("a project is created with the token %q, and that is answered with %d", token, status)
		}
	}
}

// providers is a plugin for the identity providers of the kind "tokens",
// whose settings are the callers of the tokens.
type providers struct{}

func (providers) Kind() string { return "tokens" }

func (providers) Open(_ context.Context, settings json.RawMessage) (identity.Provider, error) {
	known := tokens{}
	if err := json.Unmarshal(settings, &known); err != nil {
		return nil, err
	}
	return known, nil
}

// The identity provider of an installation is the one that its settings
// name, opened by the plugin for its kind. An installation that names none
// has none.
func TestIdentityProviderSettings(t *testing.T) {
	ctx := context.Background()
	for _, none := range []string{`{}`, `{"kind": "", "settings": {}}`} {
		t.Setenv(identityProvider, none)
		if callers, err := identityOf(ctx, []identity.Plugin{providers{}}); err != nil || callers != nil {
			t.Errorf("with %s the installation has the identity provider %v, with the error %v", none, callers, err)
		}
	}
	t.Setenv(identityProvider, `{"kind": "tokens", "settings": {"a-token": {"Subject": "subject-1"}}}`)
	callers, err := identityOf(ctx, []identity.Plugin{providers{}})
	if err != nil {
		t.Fatal(err)
	}
	if caller, err := callers.Caller(ctx, "a-token"); err != nil || caller.Subject != "subject-1" {
		t.Errorf("the provider tells of the caller %+v, with the error %v", caller, err)
	}
	for wrong, says := range map[string]string{
		`{"kind": "another"}`:                    "tokens",
		`{"kind": "tokens", "settings": "none"}`: "cannot unmarshal",
		`not JSON`:                               identityProvider,
	} {
		t.Setenv(identityProvider, wrong)
		if _, err := identityOf(ctx, []identity.Plugin{providers{}}); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("with %s the provider is refused with %v, which does not say %q", wrong, err, says)
		}
	}
}
