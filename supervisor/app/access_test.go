package app

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"aviary/supervisor/identity"
	"aviary/supervisor/internal/api"
)

// answered sends a request with token and returns the response status. It
// does not read the body, since an event stream does not end.
func (s *supervisor) answered(token, method, path, body string) int {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, s.api+path, strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

// The session and the turn belong to the member with the user role.
func TestWhoMayDoWhat(t *testing.T) {
	installation := install(t)
	// In the order of the columns of the table below.
	callers := []string{"admin", "owner", "editor", "user", "viewer", "auditor", "mate", "custom", "builder", "stranger"}
	provider := tokens{"admin": {Subject: "admin", Groups: []string{"platform"}}, "mate": {Subject: "mate", Groups: []string{"team-a", "others"}}}
	for _, caller := range callers[1:] {
		if caller != "mate" {
			provider[caller] = identity.Caller{Subject: caller, Username: caller + "@example.com"}
		}
	}
	installation.callers = api.Callers{Provider: provider, Admins: []string{"platform"}}
	s := installation.supervisor()

	s.token = "admin"
	project, other := s.project(), s.project()
	members := "/v1/projects/" + project + "/members"
	for _, role := range []string{"owner", "editor", "user", "viewer", "auditor"} {
		s.create(members, `{"user": "`+role+`@example.com", "role": "`+role+`"}`)
	}
	member := s.create(members, `{"group": "team-a", "role": "user"}`)
	s.create(members, `{"user": "custom@example.com", "permissions": ["sessions.control_all"]}`)
	s.create(members, `{"user": "builder@example.com", "permissions": ["agents.manage"]}`)
	agent := s.agent(project, `{"harness": "opencode"}`)
	models := s.create("/v1/projects/"+project+"/providers", `{"name": "another", "url": "https://api.example.com/v1", "api_key": {"value": "sk-a-key"}}`)
	secrets := s.create("/v1/projects/"+project+"/secret-stores", scriptedStore("a store", "https://"+storeHost, "a-credential", "a-credential"))
	s.token = "user"
	session := s.create("/v1/agents/"+agent+"/sessions", "")
	turn := s.send(session, "hello").ID
	none := "00000000-0000-0000-0000-000000000000"

	// p: passes, with neither 403 nor 404. f: 403 Forbidden. h: 404 Not Found.
	const (
		everyMember = "ppppppppph"
		onlyAdmin   = "pfffffffff"
		connections = "pppffffffh"
		reading     = "ppfpfpfpfh"
	)
	for _, row := range []struct {
		method, path, body string
		may                string
	}{
		{"GET", "/v1/roles", ``, "pppppppppp"},
		{"PUT", "/v1/roles/viewer", `{}`, onlyAdmin},
		{"POST", "/v1/projects", `{}`, onlyAdmin},
		{"GET", "/v1/projects/" + project, ``, everyMember},
		{"GET", "/v1/projects/" + other, ``, "phhhhhhhhh"},
		{"GET", members, ``, everyMember},
		{"POST", members, `{}`, onlyAdmin},
		{"PUT", "/v1/members/" + member, `{}`, onlyAdmin},
		// The member does not exist, so an admin gets 404.
		{"DELETE", "/v1/members/" + none, ``, "hfffffffff"},
		{"POST", "/v1/projects/" + project + "/agents", `{}`, "pppfffffph"},
		{"GET", "/v1/projects/" + project + "/agents", ``, everyMember},
		{"GET", "/v1/agents/" + agent, ``, everyMember},
		{"POST", "/v1/projects/" + project + "/providers", `{}`, connections},
		{"GET", "/v1/providers/" + models, ``, everyMember},
		{"PUT", "/v1/providers/" + models, `{}`, connections},
		{"POST", "/v1/projects/" + project + "/secret-stores", `{}`, connections},
		{"GET", "/v1/secret-stores/" + secrets, ``, everyMember},
		{"PUT", "/v1/secret-stores/" + secrets, `{}`, connections},
		{"POST", "/v1/agents/" + agent + "/sessions", ``, "ppppffpffh"},
		// Every member gets the sessions that they may read, possibly none.
		{"GET", "/v1/agents/" + agent + "/sessions", ``, everyMember},
		{"GET", "/v1/sessions/" + session, ``, reading},
		{"GET", "/v1/sessions/" + session + "/turns", ``, reading},
		{"GET", "/v1/turns/" + turn, ``, reading},
		{"GET", "/v1/turns/" + turn + "/events", ``, reading},
		// Besides admins, only the creator of the session may send messages.
		{"POST", "/v1/sessions/" + session + "/messages", `{}`, "pffpfffffh"},
		// The request does not exist, so those who may answer get 404.
		{"POST", "/v1/turns/" + turn + "/approvals/none", `{"option_id": "allow"}`, "hhfhfffhfh"},
		{"POST", "/v1/turns/" + turn + "/cancel", ``, "ppfpfffpfh"},
		// The turn is still running, so those who may end the session get 409.
		{"DELETE", "/v1/sessions/" + session, ``, "ppfpfffpfh"},
	} {
		for i, caller := range callers {
			status := s.answered(caller, row.method, row.path, row.body)
			answered := map[int]byte{http.StatusForbidden: 'f', http.StatusNotFound: 'h'}[status]
			if status == http.StatusUnauthorized || status >= 500 {
				answered = '!'
			} else if answered == 0 {
				answered = 'p'
			}
			if answered != row.may[i] {
				t.Errorf("%s: %s %s = %d (%c), want %c", caller, row.method, row.path, status, answered, row.may[i])
			}
		}
	}

	// A change to a role applies to its members at once.
	if status := s.answered("admin", "PUT", "/v1/roles/viewer", `{"permissions": ["sessions.view_all"]}`); status != http.StatusOK {
		t.Fatalf("PUT role: status %d, want 200", status)
	}
	if status := s.answered("viewer", "GET", "/v1/sessions/"+session, ``); status != http.StatusOK {
		t.Errorf("viewer with sessions.view_all: GET session status %d, want 200", status)
	}
	// Removing a group removes the membership of its members.
	if status := s.answered("admin", "DELETE", "/v1/members/"+member, ``); status != http.StatusNoContent {
		t.Fatalf("DELETE member: status %d, want 204", status)
	}
	if status := s.answered("mate", "GET", "/v1/projects/"+project, ``); status != http.StatusNotFound {
		t.Errorf("member of a removed group: GET project status %d, want 404", status)
	}
}
