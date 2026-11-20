package app

import (
	"net/http"
	"slices"
	"testing"

	"aviary/supervisor/identity"
	"aviary/supervisor/internal/api"
)

// Agents are listed to every member of the project, and sessions to those
// who may read them: their own, or everyone's.
func TestLists(t *testing.T) {
	installation := install(t)
	provider := tokens{"admin": {Subject: "admin", Groups: []string{"platform"}}}
	for _, caller := range []string{"ann", "ben", "viewer", "auditor", "stranger"} {
		provider[caller] = identity.Caller{Subject: caller, Username: caller + "@example.com"}
	}
	installation.callers = api.Callers{Provider: provider, Admins: []string{"platform"}}
	s := installation.supervisor()
	// as sets the caller of the following requests.
	as := func(caller string) *supervisor {
		s.token = caller
		return s
	}

	project, other := as("admin").create("/v1/projects", `{"name": "test"}`), s.create("/v1/projects", `{"name": "other"}`)
	members := map[string]string{}
	for caller, role := range map[string]string{"ann": "user", "ben": "user", "viewer": "viewer", "auditor": "auditor"} {
		members[caller] = s.create("/v1/projects/"+project+"/members", `{"user": "`+caller+`@example.com", "role": "`+role+`"}`)
	}
	first, second := s.agent(project, `{"harness": "opencode", "instructions": "Be brief."}`), s.agent(project, `{"harness": "codex"}`)
	elsewhere := s.agent(other, `{"harness": "opencode"}`)

	type agent struct {
		ID           string `json:"id"`
		ProjectID    string `json:"project_id"`
		Harness      string `json:"harness"`
		Instructions string `json:"instructions"`
	}
	for _, listed := range []struct {
		caller, project string
		agents          []agent
	}{
		{"viewer", project, []agent{{first, project, "opencode", "Be brief."}, {second, project, "codex", ""}}},
		{"admin", project, []agent{{first, project, "opencode", "Be brief."}, {second, project, "codex", ""}}},
		{"admin", other, []agent{{elsewhere, other, "opencode", ""}}},
	} {
		var answer struct{ Agents []agent }
		if status, _ := as(listed.caller).request("GET", "/v1/projects/"+listed.project+"/agents", "", &answer); status != http.StatusOK ||
			!slices.Equal(answer.Agents, listed.agents) {
			t.Errorf("%s: list agents = %d %+v, want %+v", listed.caller, status, answer.Agents, listed.agents)
		}
	}
	var empty struct{ Agents []agent }
	if status, _ := as("admin").request("GET", "/v1/projects/"+s.create("/v1/projects", `{"name": "new"}`)+"/agents", "", &empty); status != http.StatusOK || empty.Agents == nil {
		t.Errorf("list agents of an empty project = %d %+v, want an empty list", status, empty.Agents)
	}

	// Five sessions of the first agent, alternately created by ann and ben.
	var created []string
	for _, caller := range []string{"ann", "ben", "ann", "ben", "ann"} {
		created = append(created, as(caller).create("/v1/agents/"+first+"/sessions", ""))
	}
	apart := as("ann").create("/v1/agents/"+second+"/sessions", "")
	ann, ben := []string{created[0], created[2], created[4]}, []string{created[1], created[3]}
	type session struct {
		ID        string `json:"id"`
		AgentID   string `json:"agent_id"`
		CreatedBy string `json:"created_by"`
	}
	for _, listed := range []struct {
		caller, agent, query string
		sessions             []string
	}{
		{"ann", first, "", []string{ann[2], ann[1], ann[0]}},
		{"ann", first, "?limit=2", []string{ann[2], ann[1]}},
		{"ann", first, "?limit=2&before=" + ann[1], []string{ann[0]}},
		{"ann", first, "?before=" + ben[1], []string{ann[1], ann[0]}},
		{"ann", second, "", []string{apart}},
		{"ben", first, "", []string{ben[1], ben[0]}},
		{"ben", second, "", []string{}},
		// A caller who may read no session gets an empty list.
		{"viewer", first, "", []string{}},
		{"auditor", first, "", []string{created[4], created[3], created[2], created[1], created[0]}},
		{"auditor", first, "?limit=2", []string{created[4], created[3]}},
		{"auditor", first, "?limit=2&before=" + created[3], []string{created[2], created[1]}},
		{"auditor", first, "?before=" + created[0], []string{}},
		{"admin", first, "?limit=100", []string{created[4], created[3], created[2], created[1], created[0]}},
		{"admin", second, "", []string{apart}},
		{"admin", elsewhere, "", []string{}},
	} {
		var answer struct{ Sessions []session }
		status, _ := as(listed.caller).request("GET", "/v1/agents/"+listed.agent+"/sessions"+listed.query, "", &answer)
		ids := []string{}
		for _, session := range answer.Sessions {
			if session.AgentID != listed.agent || session.CreatedBy == "" {
				t.Errorf("session of agent %s listed as %+v", listed.agent, session)
			}
			ids = append(ids, session.ID)
		}
		if status != http.StatusOK || answer.Sessions == nil || !slices.Equal(ids, listed.sessions) {
			t.Errorf("list sessions%s as %s = %d %v, want %v", listed.query, listed.caller, status, ids, listed.sessions)
		}
	}

	// A member who may no longer run sessions does not see their own either.
	var demoted struct{ Sessions []session }
	if status, _ := as("admin").request("PUT", "/v1/members/"+members["ben"], `{"role": "viewer"}`, nil); status != http.StatusOK {
		t.Fatalf("PUT member: status %d, want 200", status)
	}
	if status, _ := as("ben").request("GET", "/v1/agents/"+first+"/sessions", "", &demoted); status != http.StatusOK || demoted.Sessions == nil || len(demoted.Sessions) != 0 {
		t.Errorf("list sessions after demotion = %d %+v, want an empty list", status, demoted.Sessions)
	}

	none := "00000000-0000-0000-0000-000000000000"
	for _, refused := range []struct {
		caller, path string
		status       int
	}{
		{"stranger", "/v1/projects/" + project + "/agents", http.StatusNotFound},
		{"ann", "/v1/projects/" + other + "/agents", http.StatusNotFound},
		{"admin", "/v1/projects/" + none + "/agents", http.StatusNotFound},
		{"stranger", "/v1/agents/" + first + "/sessions", http.StatusNotFound},
		{"ann", "/v1/agents/" + elsewhere + "/sessions", http.StatusNotFound},
		{"admin", "/v1/agents/" + none + "/sessions", http.StatusNotFound},
		{"ann", "/v1/agents/" + first + "/sessions?before=the-last", http.StatusBadRequest},
		{"ann", "/v1/agents/" + first + "/sessions?limit=0", http.StatusBadRequest},
		{"ann", "/v1/agents/" + first + "/sessions?limit=101", http.StatusBadRequest},
	} {
		if status, _ := as(refused.caller).request("GET", refused.path, "", nil); status != refused.status {
			t.Errorf("GET %s as %s: status %d, want %d", refused.path, refused.caller, status, refused.status)
		}
	}
}

// Without an identity provider, every session is listed to every caller.
func TestListsOfAnInstallationThatAsksNobody(t *testing.T) {
	s := install(t).supervisor()
	agent := s.agent(s.create("/v1/projects", `{"name": "test"}`), `{"harness": "opencode"}`)
	older, newer := s.create("/v1/agents/"+agent+"/sessions", ""), s.create("/v1/agents/"+agent+"/sessions", "")
	var answer struct{ Sessions []struct{ ID string } }
	if status, _ := s.request("GET", "/v1/agents/"+agent+"/sessions", "", &answer); status != http.StatusOK ||
		len(answer.Sessions) != 2 || answer.Sessions[0].ID != newer || answer.Sessions[1].ID != older {
		t.Errorf("list sessions %s and %s = %d %+v, want newest first", older, newer, status, answer.Sessions)
	}
}
