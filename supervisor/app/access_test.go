package app

import (
	"context"
	"fmt"
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
	response, err := httpClient.Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

// outcome returns how a request was answered: p if it passed, with neither 403
// nor 404, f if it was forbidden, h if it was not found, and ! if it failed.
func outcome(status int) byte {
	switch {
	case status == http.StatusForbidden:
		return 'f'
	case status == http.StatusNotFound:
		return 'h'
	case status == http.StatusUnauthorized || status >= 500:
		return '!'
	}
	return 'p'
}

// The session and the turn belong to the member with the user role.
func TestWhoMayDoWhat(t *testing.T) {
	attachments, _ := attaching(t)
	installation := install(t, attachments)
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
	servers := "/v1/projects/" + project + "/mcp-servers"
	tools := s.create(servers, `{"slug": "a-server", "url": "https://mcp.example.com/mcp", "transport": "http"}`)
	admin := *s
	s.token = "user"
	session := s.create("/v1/agents/"+agent+"/sessions", "")
	turn := s.send(session, "hello").ID
	file := s.attach(session, "notes.txt", "to do").ID
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
		{"GET", "/v1/harnesses", ``, "pppppppppp"},
		{"GET", "/v1/limits", ``, "pppppppppp"},
		{"POST", "/v1/projects", `{}`, onlyAdmin},
		{"GET", "/v1/projects/" + project, ``, everyMember},
		{"GET", "/v1/projects/" + other, ``, "phhhhhhhhh"},
		// Every member gets the changes of the sessions that they may read.
		{"GET", "/v1/projects/" + project + "/events", ``, everyMember},
		{"GET", "/v1/projects/" + none + "/events", ``, "hhhhhhhhhh"},
		{"GET", members, ``, everyMember},
		{"POST", members, `{}`, onlyAdmin},
		{"PUT", "/v1/members/" + member, `{}`, onlyAdmin},
		// The member does not exist, so an admin gets 404.
		{"DELETE", "/v1/members/" + none, ``, "hfffffffff"},
		{"POST", "/v1/projects/" + project + "/agents", `{}`, "pppfffffph"},
		{"GET", "/v1/projects/" + project + "/agents", ``, everyMember},
		{"GET", "/v1/agents/" + agent, ``, everyMember},
		{"PUT", "/v1/agents/" + agent, `{}`, "pppfffffph"},
		// Deleting an agent ends the sessions of every member, so it also
		// needs sessions.control_all. The agent has a running turn, so those
		// who may delete it get 409.
		{"DELETE", "/v1/agents/" + agent, ``, "ppfffffffh"},
		{"POST", "/v1/projects/" + project + "/providers", `{}`, connections},
		{"POST", "/v1/projects/" + project + "/providers/models", `{}`, connections},
		{"GET", "/v1/projects/" + project + "/providers", ``, everyMember},
		{"GET", "/v1/providers/" + models, ``, everyMember},
		{"PUT", "/v1/providers/" + models, `{}`, connections},
		// Those who set the models of agents see what a provider offers.
		{"GET", "/v1/providers/" + models + "/models", ``, "pppfffffph"},
		{"POST", "/v1/projects/" + project + "/secret-stores", `{}`, connections},
		{"GET", "/v1/projects/" + project + "/secret-stores", ``, everyMember},
		{"GET", "/v1/secret-stores/" + secrets, ``, everyMember},
		{"PUT", "/v1/secret-stores/" + secrets, `{}`, connections},
		{"POST", servers, `{}`, connections},
		{"GET", servers, ``, everyMember},
		{"GET", "/v1/mcp-servers/" + tools, ``, everyMember},
		{"PUT", "/v1/mcp-servers/" + tools, `{}`, connections},
		{"POST", servers + "/tools", `{}`, connections},
		{"POST", "/v1/mcp-servers/" + tools + "/tools", ``, connections},
		{"POST", "/v1/agents/" + agent + "/sessions", ``, "ppppffpffh"},
		// Every member gets the sessions that they may read, possibly none.
		{"GET", "/v1/agents/" + agent + "/sessions", ``, everyMember},
		{"GET", "/v1/sessions/" + session, ``, reading},
		{"GET", "/v1/sessions/" + session + "/turns", ``, reading},
		{"GET", "/v1/turns/" + turn, ``, reading},
		{"GET", "/v1/turns/" + turn + "/events", ``, reading},
		// Besides admins, only the creator of the session may send messages,
		// and turn MCP servers on and off for them.
		{"POST", "/v1/sessions/" + session + "/messages", `{}`, "pffpfffffh"},
		{"PUT", "/v1/sessions/" + session + "/mcp-servers/" + tools, `{"enabled": true}`, "pffpfffffh"},
		// So for the files that the messages carry, which are read by those
		// who may read the session.
		{"POST", "/v1/sessions/" + session + "/attachments?name=a.txt", `a`, "pffpfffffh"},
		{"GET", "/v1/attachments/" + file, ``, reading},
		{"GET", "/v1/attachments/" + none, ``, "hhhhhhhhhh"},
		// The files of the session's workspace are read by those who may read
		// the session, and written by its creator.
		{"PUT", "/v1/sessions/" + session + "/files/content?path=a.txt", `a`, "pffpfffffh"},
		{"GET", "/v1/sessions/" + session + "/files", ``, reading},
		{"GET", "/v1/sessions/" + session + "/files/search?query=a", ``, reading},
		{"GET", "/v1/sessions/" + session + "/files/content?path=a.txt", ``, reading},
		{"GET", "/v1/sessions/" + session + "/files/archive", ``, reading},
		{"DELETE", "/v1/sessions/" + session + "/mcp-servers", ``, "pffpfffffh"},
		// The request does not exist, so those who may answer get 404.
		{"POST", "/v1/turns/" + turn + "/approvals/none", `{"option_id": "allow"}`, "hhfhfffhfh"},
		{"POST", "/v1/turns/" + turn + "/cancel", ``, "ppfpfffpfh"},
		{"PATCH", "/v1/sessions/" + session, `{"title": "a title"}`, "ppfpfffpfh"},
		// The turn is still running, so those who may end the session get 409.
		{"DELETE", "/v1/sessions/" + session, ``, "ppfpfffpfh"},
	} {
		for i, caller := range callers {
			status := s.answered(caller, row.method, row.path, row.body)
			if answered := outcome(status); answered != row.may[i] {
				t.Errorf("%s: %s %s = %d (%c), want %c", caller, row.method, row.path, status, answered, row.may[i])
			}
		}
	}
	// Each caller tries to delete an MCP server of its own.
	for i, caller := range callers {
		path := "/v1/mcp-servers/" + admin.create(servers, fmt.Sprintf(`{"slug": "server-%d", "url": "https://mcp.example.com/mcp", "transport": "http"}`, i))
		if status := s.answered(caller, "DELETE", path, ``); outcome(status) != connections[i] {
			t.Errorf("%s: DELETE %s = %d, want %c", caller, path, status, connections[i])
		}
	}

	// A caller who may not delete an agent is told which permission it lacks.
	for caller, lacked := range map[string]string{"editor": "sessions.control_all", "custom": "agents.manage", "user": "agents.manage"} {
		var refusal struct{ Code, Message string }
		as := *s
		as.token = caller
		if status, _ := as.request("DELETE", "/v1/agents/"+agent, "", &refusal); status != http.StatusForbidden ||
			refusal.Code != "forbidden" || !strings.Contains(refusal.Message, lacked) {
			t.Errorf("%s: DELETE agent = %d %s %q, want 403 for the lack of %s", caller, status, refusal.Code, refusal.Message, lacked)
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
