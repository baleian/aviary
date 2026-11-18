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
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Groups      []string `json:"groups"`
	Admin       bool     `json:"admin"`
}

// An installation with an identity provider takes a request only with a
// token of the provider, and tells the caller what it read from the token.
func TestCallersAreAskedWhoTheyAre(t *testing.T) {
	installation := install(t)
	alice := identity.Caller{Subject: "subject-1", Username: "alice@example.com", DisplayName: "Alice Kim", Groups: []string{"team-a"}}
	installation.callers = api.Callers{Provider: tokens{"token-of-alice": alice}}
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
		if response := send(http.MethodGet, "/v1/projects", authorization); response.StatusCode != http.StatusOK {
			t.Errorf("the projects are asked for with the header %q, and that is answered with %d", authorization, response.StatusCode)
		}
	}
	s.token = "token-of-alice"
	var told me
	if status, _ := s.request(http.MethodGet, "/v1/me", "", &told); status != http.StatusOK || uuid.Validate(told.ID) != nil ||
		!reflect.DeepEqual(told, me{ID: told.ID, Subject: "subject-1", Username: "alice@example.com", DisplayName: "Alice Kim", Groups: []string{"team-a"}}) {
		t.Errorf("the caller is told %d %+v", status, told)
	}
}

// A caller is a user of the installation from the first request on: the
// same one with every request and through every Supervisor, and another
// than every other caller.
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
			t.Fatalf("the caller with %s is told %d %+v", token, status, told)
		}
		return told
	}
	alice := ask(one, "token-of-alice")
	if again := ask(other, "token-of-alice"); again.ID != alice.ID {
		t.Errorf("a caller is the user %s to one Supervisor and %s to another", alice.ID, again.ID)
	}
	// A token that tells something else of the same caller.
	if renamed := ask(one, "another-of-alice"); renamed.ID != alice.ID || renamed.DisplayName != "Alice Kim" {
		t.Errorf("the caller is told %+v with another of its tokens, and was the user %s", renamed, alice.ID)
	}
	if bob := ask(other, "token-of-bob"); bob.ID == alice.ID {
		t.Errorf("two callers are the same user, %s", bob.ID)
	}
	user, err := installation.records.UserOf(context.Background(), "subject-1", "", "Alice Kim")
	if err != nil || user.ID.String() != alice.ID || user.Username != "alice@example.com" {
		t.Errorf("the records have the user %+v, with the error %v", user, err)
	}
}

// The groups that the identity provider gives a caller say whether the
// caller is an administrator of the installation, and whether the caller
// may use the API at all.
func TestGroupsOfAdministratorsAndOfUsers(t *testing.T) {
	provider := tokens{
		"an-administrator": {Subject: "1", Groups: []string{"others", "platform"}},
		"a-user":           {Subject: "2", Groups: []string{"team-b"}},
		"a-stranger":       {Subject: "3", Groups: []string{"others"}},
		"without-groups":   {Subject: "4"},
	}
	for name, test := range map[string]struct {
		callers api.Callers
		// admins and refused are the tokens of the callers that are
		// administrators and of those that may not use the API.
		admins, refused []string
	}{
		"groups of administrators and of users": {
			callers: api.Callers{Provider: provider, Admins: []string{"platform", "owners"}, Users: []string{"team-a", "team-b"}},
			admins:  []string{"an-administrator"}, refused: []string{"a-stranger", "without-groups"},
		},
		"no groups of users: everyone whom the provider knows may use the API": {
			callers: api.Callers{Provider: provider, Admins: []string{"platform"}},
			admins:  []string{"an-administrator"},
		},
		"no groups of administrators: nobody is one": {
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
				// Every caller is told who the installation takes them for,
				// also one that may do nothing else.
				var told me
				if status, _ := s.request(http.MethodGet, "/v1/me", "", &told); status != http.StatusOK ||
					told.Subject != caller.Subject || told.Admin != contains(test.admins, token) {
					t.Errorf("%s is told %d %+v", token, status, told)
				}
				status, expected := http.StatusOK, http.StatusOK
				var answer struct{ Code string }
				if contains(test.refused, token) {
					expected = http.StatusForbidden
				}
				if status, _ = s.request(http.MethodGet, "/v1/projects", "", &answer); status != expected ||
					(expected == http.StatusForbidden) != (answer.Code == "forbidden") {
					t.Errorf("%s asks for its projects, and that is answered with %d %q, not %d", token, status, answer.Code, expected)
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

// An installation without an identity provider asks nobody: a request needs
// no token, what it brings is not looked at, and its caller is an
// administrator.
func TestNobodyIsAskedWithoutAnIdentityProvider(t *testing.T) {
	installation := install(t)
	// Groups mean nothing without a provider that gives them.
	installation.callers = api.Callers{Admins: []string{"platform"}, Users: []string{"team-a"}}
	s := installation.supervisor()
	for _, token := range []string{"", "any-token"} {
		s.token = token
		var answer map[string]any
		if status, _ := s.request(http.MethodGet, "/v1/me", "", &answer); status != http.StatusOK || !reflect.DeepEqual(answer, map[string]any{"admin": true}) {
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

// Who may use the API is what the settings of the installation say: the
// identity provider that they name, opened by the plugin for its kind, and
// the groups of administrators and of users. An installation that names no
// provider has none, and no groups.
func TestCallerSettings(t *testing.T) {
	ctx := context.Background()
	plugins := []identity.Plugin{providers{}}
	t.Setenv(adminGroups, `["platform"]`)
	t.Setenv(userGroups, `["team-a", "team-b"]`)
	for _, none := range []string{`{}`, `{"kind": "", "settings": {}}`} {
		t.Setenv(identityProvider, none)
		if callers, err := callersOf(ctx, plugins); err != nil || !reflect.DeepEqual(callers, api.Callers{}) {
			t.Errorf("with the provider %s the callers are %+v, with the error %v", none, callers, err)
		}
	}
	t.Setenv(identityProvider, `{"kind": "tokens", "settings": {"a-token": {"Subject": "subject-1"}}}`)
	callers, err := callersOf(ctx, plugins)
	if err != nil {
		t.Fatal(err)
	}
	if caller, err := callers.Provider.Caller(ctx, "a-token"); err != nil || caller.Subject != "subject-1" {
		t.Errorf("the provider tells of the caller %+v, with the error %v", caller, err)
	}
	if !reflect.DeepEqual(callers.Admins, []string{"platform"}) || !reflect.DeepEqual(callers.Users, []string{"team-a", "team-b"}) {
		t.Errorf("the administrators are %q and the users %q", callers.Admins, callers.Users)
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
				t.Errorf("the callers are refused with %v, which does not say %q", err, wrong.says)
			}
		})
	}
}

// A session is that of the caller who created it, and a turn that of the
// caller who sent its message. In an installation that asks nobody who they
// are, they are nobody's.
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
	// session creates a session of the agent with the token, and returns
	// who the caller is and whose the session is: as it was created and as
	// it is read.
	session := func(token, agent string) (caller me, created, read map[string]any) {
		t.Helper()
		s.token = token
		if status, _ := s.request(http.MethodGet, "/v1/me", "", &caller); status != http.StatusOK {
			t.Fatalf("the caller with %s is told %d", token, status)
		}
		if status, _ := s.request(http.MethodPost, "/v1/agents/"+agent+"/sessions", "", &created); status != http.StatusCreated {
			t.Fatalf("the session of the caller with %s is answered with %d", token, status)
		}
		if status, _ := s.request(http.MethodGet, "/v1/sessions/"+created["id"].(string), "", &read); status != http.StatusOK {
			t.Fatalf("the session of the caller with %s is read with %d", token, status)
		}
		return caller, created, read
	}
	s.token = "an-administrator"
	project := s.create("/v1/projects", `{"name": "test"}`)
	agent := s.agent(project, `{"harness": "opencode"}`)
	s.create("/v1/projects/"+project+"/members", `{"user": "alice@example.com", "role": "user"}`)
	s.create("/v1/projects/"+project+"/members", `{"user": "bob@example.com", "role": "user"}`)
	alice, created, read := session("token-of-alice", agent)
	if created["created_by"] != alice.ID || read["created_by"] != alice.ID {
		t.Errorf("the session of the user %s was created as %v and is read as %v", alice.ID, created, read)
	}
	bob, created, read := session("token-of-bob", agent)
	if bob.ID == alice.ID || created["created_by"] != bob.ID || read["created_by"] != bob.ID {
		t.Errorf("the session of the user %s was created as %v and is read as %v", bob.ID, created, read)
	}
	var sent, one map[string]any
	if status, _ := s.request(http.MethodPost, "/v1/sessions/"+created["id"].(string)+"/messages", `{"text": "hello"}`, &sent); status != http.StatusAccepted {
		t.Fatalf("a message is answered with %d %v", status, sent)
	}
	if status, _ := s.request(http.MethodGet, "/v1/turns/"+sent["id"].(string), "", &one); status != http.StatusOK ||
		sent["created_by"] != bob.ID || one["created_by"] != bob.ID {
		t.Errorf("the turn of the user %s was created as %v and is read with %d as %v", bob.ID, sent, status, one)
	}

	nobody := install(t).supervisor()
	var nobodys map[string]any
	if status, _ := nobody.request(http.MethodGet, "/v1/sessions/"+nobody.session(), "", &nobodys); status != http.StatusOK || nobodys["id"] == nil {
		t.Fatalf("a session is read with %d %v", status, nobodys)
	}
	if by, told := nobodys["created_by"]; told {
		t.Errorf("a session that nobody created is said to be that of %v", by)
	}
	var unsigned map[string]any
	if status, _ := nobody.request(http.MethodPost, "/v1/sessions/"+nobodys["id"].(string)+"/messages", `{"text": "hello"}`, &unsigned); status != http.StatusAccepted || unsigned["id"] == nil {
		t.Fatalf("a message is answered with %d %v", status, unsigned)
	}
	if by, told := unsigned["created_by"]; told {
		t.Errorf("a turn that nobody sent is said to be that of %v", by)
	}
}

// The Egress Proxy is told who sent the message of the turn that a request
// comes from, for its record: the user, and the subject that the identity
// provider knows the user by.
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
	project := s.create("/v1/projects", `{"name": "test"}`)
	agent := s.agent(project, `{"harness": "opencode"}`)
	s.create("/v1/projects/"+project+"/members", `{"user": "alice@example.com", "role": "user"}`)

	senders := map[string]bool{}
	for _, sender := range []string{"token-of-alice", "an-administrator"} {
		s.token = sender
		var who me
		s.request(http.MethodGet, "/v1/me", "", &who)
		job := aviary.job(s.send(s.create("/v1/agents/"+agent+"/sessions", ""), "hello").ID)
		s.take(job)
		for _, forward := range []bool{false, true} {
			if origin := s.origin(token(job), forward); who.ID == "" || origin["user"] != who.ID || who.Subject == "" || origin["subject"] != who.Subject {
				t.Errorf("of a request of the turn that the user %q, the subject %q, sent, through the forward proxy %t, the proxy is told %v",
					who.ID, who.Subject, forward, origin)
			}
		}
		senders[who.ID] = true
	}
	if len(senders) != 2 {
		t.Errorf("the two who sent messages are %v", senders)
	}
}
