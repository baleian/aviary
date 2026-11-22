package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"aviary/supervisor/identity"
	"aviary/supervisor/internal/api"
)

// tokens is an identity provider that maps tokens to callers.
type tokens map[string]identity.Caller

func (t tokens) Caller(_ context.Context, token string) (identity.Caller, error) {
	caller, known := t[token]
	if !known {
		return identity.Caller{}, errors.New("no such token")
	}
	return caller, nil
}

// me is the response of GET /v1/me.
type me struct {
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Groups      []string `json:"groups"`
	Admin       bool     `json:"admin"`
}

// With an identity provider, a request needs a bearer token of the provider,
// and /v1/me returns the caller that the token names.
func TestCallersAreAskedWhoTheyAre(t *testing.T) {
	installation := install(t)
	alice := identity.Caller{Subject: "subject-1", Username: "alice@example.com", DisplayName: "Alice Kim", Groups: []string{"team-a"}}
	installation.callers = api.Callers{Provider: tokens{"token-of-alice": alice}}
	s := installation.supervisor()

	send := func(method, path, authorization string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(method, s.api+path, strings.NewReader(`{"slug": "a-project"}`))
		if err != nil {
			t.Fatal(err)
		}
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response, err := httpClient.Do(request)
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
				t.Errorf("%s %s with Authorization %q = %d %q, WWW-Authenticate %q; want 401 unauthenticated, Bearer",
					request[0], request[1], authorization, response.StatusCode, answer.Code, response.Header.Get("WWW-Authenticate"))
			}
		}
	}
	// Health checks need no token.
	if response := send(http.MethodGet, "/healthz", ""); response.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz: status %d, want 200", response.StatusCode)
	}

	for _, authorization := range []string{"Bearer token-of-alice", "bearer token-of-alice"} {
		if response := send(http.MethodGet, "/v1/projects", authorization); response.StatusCode != http.StatusOK {
			t.Errorf("GET /v1/projects with Authorization %q: status %d, want 200", authorization, response.StatusCode)
		}
	}
	s.token = "token-of-alice"
	var told me
	if status, _ := s.request(http.MethodGet, "/v1/me", "", &told); status != http.StatusOK || uuid.Validate(told.ID) != nil ||
		!reflect.DeepEqual(told, me{ID: told.ID, Subject: "subject-1", Username: "alice@example.com", DisplayName: "Alice Kim", Groups: []string{"team-a"}}) {
		t.Errorf("GET /v1/me = %d %+v", status, told)
	}
}

// A caller becomes a user on the first request. The user ID is the same for
// every token of the subject and through every Supervisor, and differs from
// that of every other subject.
func TestCallersAreUsers(t *testing.T) {
	installation := install(t)
	installation.callers = api.Callers{Provider: tokens{
		"token-of-alice":   {Subject: "subject-1", Username: "alice@example.com", DisplayName: "Alice"},
		"another-of-alice": {Subject: "subject-1", Username: "alice@example.com", DisplayName: "Alice Kim"},
		"token-of-bob":     {Subject: "subject-2"},
	}}
	one, other := installation.supervisor(), installation.supervisor()
	ask := func(s *supervisor, token string) me {
		t.Helper()
		s.token = token
		var told me
		if status, _ := s.request(http.MethodGet, "/v1/me", "", &told); status != http.StatusOK || uuid.Validate(told.ID) != nil {
			t.Fatalf("GET /v1/me with %s = %d %+v", token, status, told)
		}
		return told
	}
	alice := ask(one, "token-of-alice")
	if again := ask(other, "token-of-alice"); again.ID != alice.ID {
		t.Errorf("user ID = %s on one Supervisor and %s on another", alice.ID, again.ID)
	}
	// Another token of the same subject updates the display name.
	if renamed := ask(one, "another-of-alice"); renamed.ID != alice.ID || renamed.DisplayName != "Alice Kim" {
		t.Errorf("GET /v1/me with another token = %+v, want user %s with the new display name", renamed, alice.ID)
	}
	if bob := ask(other, "token-of-bob"); bob.ID == alice.ID {
		t.Errorf("two subjects share user ID %s", bob.ID)
	}
	user, err := installation.records.UserOf(context.Background(), "subject-1", "", "Alice Kim")
	if err != nil || user.ID.String() != alice.ID || user.Username != "alice@example.com" {
		t.Errorf("UserOf = %+v, %v", user, err)
	}
}

// The admin groups make a caller an admin, and the user groups, if any,
// limit who may use the API.
func TestGroupsOfAdministratorsAndOfUsers(t *testing.T) {
	provider := tokens{
		"an-administrator": {Subject: "1", Groups: []string{"others", "platform"}},
		"a-user":           {Subject: "2", Groups: []string{"team-b"}},
		"a-stranger":       {Subject: "3", Groups: []string{"others"}},
		"without-groups":   {Subject: "4"},
	}
	for name, test := range map[string]struct {
		callers api.Callers
		// admins and refused are the tokens of the admins and of the callers
		// that may not use the API.
		admins, refused []string
	}{
		"admin and user groups": {
			callers: api.Callers{Provider: provider, Admins: []string{"platform", "owners"}, Users: []string{"team-a", "team-b"}},
			admins:  []string{"an-administrator"}, refused: []string{"a-stranger", "without-groups"},
		},
		"no user groups": {
			callers: api.Callers{Provider: provider, Admins: []string{"platform"}},
			admins:  []string{"an-administrator"},
		},
		"no admin groups": {
			callers: api.Callers{Provider: provider, Users: []string{"team-b"}},
			refused: []string{"an-administrator", "a-stranger", "without-groups"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			installation := install(t)
			installation.callers = test.callers
			s := installation.supervisor()
			for token, caller := range provider {
				s.token = token
				// Even a caller that may not use the API can read /v1/me.
				var told me
				if status, _ := s.request(http.MethodGet, "/v1/me", "", &told); status != http.StatusOK ||
					told.Subject != caller.Subject || told.Admin != contains(test.admins, token) {
					t.Errorf("%s: GET /v1/me = %d %+v", token, status, told)
				}
				status, expected := http.StatusOK, http.StatusOK
				var answer struct{ Code string }
				if contains(test.refused, token) {
					expected = http.StatusForbidden
				}
				if status, _ = s.request(http.MethodGet, "/v1/projects", "", &answer); status != expected ||
					(expected == http.StatusForbidden) != (answer.Code == "forbidden") {
					t.Errorf("%s: GET /v1/projects = %d %q, want %d", token, status, answer.Code, expected)
				}
			}
		})
	}
}

func contains(all []string, one string) bool {
	for _, each := range all {
		if each == one {
			return true
		}
	}
	return false
}

// Without an identity provider, tokens are ignored and every caller is an
// admin.
func TestNobodyIsAskedWithoutAnIdentityProvider(t *testing.T) {
	installation := install(t)
	// Groups are ignored without a provider.
	installation.callers = api.Callers{Admins: []string{"platform"}, Users: []string{"team-a"}}
	s := installation.supervisor()
	for _, token := range []string{"", "any-token"} {
		s.token = token
		var answer map[string]any
		if status, _ := s.request(http.MethodGet, "/v1/me", "", &answer); status != http.StatusOK || !reflect.DeepEqual(answer, map[string]any{"admin": true}) {
			t.Errorf("token %q: GET /v1/me = %d %v", token, status, answer)
		}
		if status, _ := s.request(http.MethodPost, "/v1/projects", `{"slug": "`+unique("project")+`"}`, nil); status != http.StatusCreated {
			t.Errorf("token %q: POST /v1/projects status %d, want 201", token, status)
		}
	}
}

// providers is an identity provider plugin of the kind "tokens", whose
// settings map tokens to callers.
type providers struct{}

func (providers) Kind() string { return "tokens" }

func (providers) Open(_ context.Context, settings json.RawMessage) (identity.Provider, error) {
	known := tokens{}
	if err := json.Unmarshal(settings, &known); err != nil {
		return nil, err
	}
	return known, nil
}

// callersOf opens the identity provider with the plugin for its kind, and
// reads the admin and user groups. Without a provider, it returns no groups
// either.
func TestCallerSettings(t *testing.T) {
	ctx := context.Background()
	plugins := []identity.Plugin{providers{}}
	t.Setenv(adminGroups, `["platform"]`)
	t.Setenv(userGroups, `["team-a", "team-b"]`)
	for _, none := range []string{`{}`, `{"kind": "", "settings": {}}`} {
		t.Setenv(identityProvider, none)
		if callers, err := callersOf(ctx, plugins); err != nil || !reflect.DeepEqual(callers, api.Callers{}) {
			t.Errorf("provider %s: callersOf() = %+v, %v", none, callers, err)
		}
	}
	t.Setenv(identityProvider, `{"kind": "tokens", "settings": {"a-token": {"Subject": "subject-1"}}}`)
	callers, err := callersOf(ctx, plugins)
	if err != nil {
		t.Fatal(err)
	}
	if caller, err := callers.Provider.Caller(ctx, "a-token"); err != nil || caller.Subject != "subject-1" {
		t.Errorf("Caller = %+v, %v", caller, err)
	}
	if !reflect.DeepEqual(callers.Admins, []string{"platform"}) || !reflect.DeepEqual(callers.Users, []string{"team-a", "team-b"}) {
		t.Errorf("Admins = %q, Users = %q", callers.Admins, callers.Users)
	}

	for _, wrong := range []struct{ name, value, says string }{
		{identityProvider, `{"kind": "another"}`, "tokens"},
		{identityProvider, `{"kind": "tokens", "settings": "none"}`, "cannot unmarshal"},
		{identityProvider, `not JSON`, identityProvider},
		{adminGroups, `platform`, adminGroups},
		{userGroups, `{"team-a": true}`, userGroups},
	} {
		t.Run(wrong.name+"="+wrong.value, func(t *testing.T) {
			t.Setenv(wrong.name, wrong.value)
			if _, err := callersOf(ctx, plugins); err == nil || !strings.Contains(err.Error(), wrong.says) {
				t.Errorf("callersOf() error = %v, want it to contain %q", err, wrong.says)
			}
		})
	}
}

// Sessions and turns record the user who created them, and no user without
// an identity provider.
func TestASessionIsOfItsCreator(t *testing.T) {
	installation := install(t)
	installation.callers = api.Callers{
		Provider: tokens{
			"an-administrator": {Subject: "subject-0", Groups: []string{"platform"}},
			"token-of-alice":   {Subject: "subject-1", Username: "alice@example.com"},
			"token-of-bob":     {Subject: "subject-2", Username: "bob@example.com"},
		},
		Admins: []string{"platform"},
	}
	s := installation.supervisor()
	// session creates a session of the agent as the caller with token, and
	// returns the caller, the created session and the session as read back.
	session := func(token, agent string) (caller me, created, read map[string]any) {
		t.Helper()
		s.token = token
		if status, _ := s.request(http.MethodGet, "/v1/me", "", &caller); status != http.StatusOK {
			t.Fatalf("GET /v1/me with %s: status %d", token, status)
		}
		if status, _ := s.request(http.MethodPost, "/v1/agents/"+agent+"/sessions", "", &created); status != http.StatusCreated {
			t.Fatalf("create session with %s: status %d", token, status)
		}
		if status, _ := s.request(http.MethodGet, "/v1/sessions/"+created["id"].(string), "", &read); status != http.StatusOK {
			t.Fatalf("GET session with %s: status %d", token, status)
		}
		return caller, created, read
	}
	s.token = "an-administrator"
	project := s.project()
	agent := s.agent(project, `{"harness": "opencode"}`)
	s.create("/v1/projects/"+project+"/members", `{"user": "alice@example.com", "role": "user"}`)
	s.create("/v1/projects/"+project+"/members", `{"user": "bob@example.com", "role": "user"}`)
	alice, created, read := session("token-of-alice", agent)
	if created["created_by"] != alice.ID || read["created_by"] != alice.ID {
		t.Errorf("session of user %s: created %v, read %v", alice.ID, created, read)
	}
	bob, created, read := session("token-of-bob", agent)
	if bob.ID == alice.ID || created["created_by"] != bob.ID || read["created_by"] != bob.ID {
		t.Errorf("session of user %s: created %v, read %v", bob.ID, created, read)
	}
	var sent, one map[string]any
	if status, _ := s.request(http.MethodPost, "/v1/sessions/"+created["id"].(string)+"/messages", `{"text": "hello"}`, &sent); status != http.StatusAccepted {
		t.Fatalf("send = %d %v, want 202", status, sent)
	}
	if status, _ := s.request(http.MethodGet, "/v1/turns/"+sent["id"].(string), "", &one); status != http.StatusOK ||
		sent["created_by"] != bob.ID || one["created_by"] != bob.ID {
		t.Errorf("turn of user %s: created %v, read %d %v", bob.ID, sent, status, one)
	}

	nobody := install(t).supervisor()
	var nobodys map[string]any
	if status, _ := nobody.request(http.MethodGet, "/v1/sessions/"+nobody.session(), "", &nobodys); status != http.StatusOK || nobodys["id"] == nil {
		t.Fatalf("GET session = %d %v", status, nobodys)
	}
	if by, told := nobodys["created_by"]; told {
		t.Errorf("session without a creator has created_by %v", by)
	}
	var unsigned map[string]any
	if status, _ := nobody.request(http.MethodPost, "/v1/sessions/"+nobodys["id"].(string)+"/messages", `{"text": "hello"}`, &unsigned); status != http.StatusAccepted || unsigned["id"] == nil {
		t.Fatalf("send = %d %v, want 202", status, unsigned)
	}
	if by, told := unsigned["created_by"]; told {
		t.Errorf("turn without a sender has created_by %v", by)
	}
}

// For its access log, the Egress Proxy gets the user ID and the subject of
// whoever sent the message of the turn that a request comes from.
func TestProxyIsToldWhoSentTheMessageOfATurn(t *testing.T) {
	aviary := install(t)
	aviary.callers = api.Callers{
		Provider: tokens{
			"an-administrator": {Subject: "1", Groups: []string{"platform"}},
			"token-of-alice":   {Subject: "2", Username: "alice@example.com"},
		},
		Admins: []string{"platform"},
	}
	s := aviary.supervisor()
	s.token = "an-administrator"
	project := s.project()
	agent := s.agent(project, `{"harness": "opencode"}`)
	s.create("/v1/projects/"+project+"/members", `{"user": "alice@example.com", "role": "user"}`)

	senders := map[string]bool{}
	for _, sender := range []string{"token-of-alice", "an-administrator"} {
		s.token = sender
		var who me
		s.request(http.MethodGet, "/v1/me", "", &who)
		runner := aviary.given(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID)
		for _, forward := range []bool{false, true} {
			if origin := s.origin(aviary.token(runner.Sandbox), forward); who.ID == "" || origin["user"] != who.ID || who.Subject == "" || origin["subject"] != who.Subject {
				t.Errorf("user %q, subject %q, forward %t: origin = %v",
					who.ID, who.Subject, forward, origin)
			}
		}
		senders[who.ID] = true
	}
	if len(senders) != 2 {
		t.Errorf("senders = %v, want two users", senders)
	}
}
