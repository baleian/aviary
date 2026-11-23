package app

import (
	"net/http"
	"slices"
	"testing"
)

// server registers an MCP server with the slug in the project, and returns
// its ID.
func (s *supervisor) server(project, slug string) string {
	s.t.Helper()
	return s.create("/v1/projects/"+project+"/mcp-servers", `{"slug": "`+slug+`", "url": "https://`+slug+`.example.com/mcp", "transport": "http"}`)
}

// An agent names the MCP servers of its project that it begins its sessions
// with. They are returned without repeats, in the order of the project's
// servers, and a server that is deleted leaves them.
func TestAgentsNameTheirMcpServers(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project, other := s.project(), s.project()
	first, second, theirs := s.server(project, "first"), s.server(project, "second"), s.server(other, "theirs")
	agent := s.agent(project, `{"harness": "opencode", "mcp_servers": ["`+second+`", "`+first+`", "`+second+`"]}`)
	// servers returns the servers of the agent.
	servers := func() []string {
		t.Helper()
		var read struct {
			McpServers []string `json:"mcp_servers"`
		}
		if status, _ := s.request("GET", "/v1/agents/"+agent, "", &read); status != http.StatusOK {
			t.Fatalf("GET agent: status %d", status)
		}
		return read.McpServers
	}
	if got := servers(); !slices.Equal(got, []string{first, second}) {
		t.Errorf("servers of the agent = %v, want %v", got, []string{first, second})
	}

	var refused struct{ Message string }
	for name, given := range map[string]string{
		"a server of another project": theirs,
		"no server":                   "00000000-0000-0000-0000-000000000000",
		"no ID":                       "first",
	} {
		body := `{"harness": "opencode", "slug": "refused", "model": {"provider": "` + s.agentProvider(agent) + `", "name": "a-model"}, "mcp_servers": ["` + first + `", "` + given + `"]}`
		if status, _ := s.request("POST", "/v1/projects/"+project+"/agents", body, &refused); status != http.StatusBadRequest ||
			refused.Message != "mcp_servers[1] must be the ID of an MCP server of the project" {
			t.Errorf("an agent with %s: %d %q", name, status, refused.Message)
		}
	}

	// PUT replaces them with the rest of the agent.
	replace := func(servers string) {
		t.Helper()
		body := `{"model": {"provider": "` + s.agentProvider(agent) + `", "name": "a-model"}` + servers + `}`
		if status, _ := s.request("PUT", "/v1/agents/"+agent, body, nil); status != http.StatusOK {
			t.Fatalf("PUT agent: status %d", status)
		}
	}
	replace(`, "mcp_servers": ["` + second + `"]`)
	if got := servers(); !slices.Equal(got, []string{second}) {
		t.Errorf("servers of the replaced agent = %v", got)
	}
	replace(``)
	if got := servers(); got != nil {
		t.Errorf("servers of an agent replaced without them = %v, want none", got)
	}
	replace(`, "mcp_servers": ["` + first + `", "` + second + `"]`)
	if status, _ := s.request("DELETE", "/v1/mcp-servers/"+second, "", nil); status != http.StatusNoContent {
		t.Fatalf("DELETE server: status %d", status)
	}
	if got := servers(); !slices.Equal(got, []string{first}) {
		t.Errorf("servers of the agent after one was deleted = %v", got)
	}
}

// agentProvider returns the provider of the agent's model.
func (s *supervisor) agentProvider(agent string) string {
	s.t.Helper()
	var read struct{ Model struct{ Provider string } }
	if status, _ := s.request("GET", "/v1/agents/"+agent, "", &read); status != http.StatusOK {
		s.t.Fatalf("GET agent: status %d", status)
	}
	return read.Model.Provider
}

// A session has the MCP servers of its agent but for those that it turns on
// or off, from when it begins until it ends, and follows its agent in the
// rest. Its record says which servers its next turns have.
func TestSessionsTurnMcpServersOnAndOff(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project, other := s.project(), s.project()
	a, b, c, theirs := s.server(project, "a"), s.server(project, "b"), s.server(project, "c"), s.server(other, "theirs")
	agent := s.agent(project, `{"harness": "opencode", "mcp_servers": ["`+a+`", "`+b+`"]}`)
	sessions := "/v1/agents/" + agent + "/sessions"
	// on returns the servers that a session record says it has.
	on := func(method, path, body string, status int) []string {
		t.Helper()
		var record struct {
			McpServers []string `json:"mcp_servers"`
			Message    string
		}
		if got, _ := s.request(method, path, body, &record); got != status {
			t.Fatalf("%s %s %s: status %d %q, want %d", method, path, body, got, record.Message, status)
		}
		return record.McpServers
	}
	var plain, chosen struct{ ID string }
	if status, _ := s.request("POST", sessions, ``, &plain); status != http.StatusCreated {
		t.Fatalf("create session: status %d", status)
	}
	if got := on("GET", "/v1/sessions/"+plain.ID, "", http.StatusOK); !slices.Equal(got, []string{a, b}) {
		t.Errorf("servers of a session = %v, want the agent's", got)
	}
	// A session can begin with others.
	if status, _ := s.request("POST", sessions, `{"mcp_servers": ["`+c+`", "`+a+`"]}`, &chosen); status != http.StatusCreated {
		t.Fatalf("create session with servers: status %d", status)
	}
	if got := on("GET", "/v1/sessions/"+chosen.ID, "", http.StatusOK); !slices.Equal(got, []string{a, c}) {
		t.Errorf("servers of a session that began with them = %v", got)
	}
	if got := on("POST", sessions, `{"mcp_servers": ["`+theirs+`"]}`, http.StatusBadRequest); got != nil {
		t.Errorf("a session with a server of another project: %v", got)
	}

	toggle := func(session, server, enabled string, status int) []string {
		t.Helper()
		return on("PUT", "/v1/sessions/"+session+"/mcp-servers/"+server, `{"enabled": `+enabled+`}`, status)
	}
	if got := toggle(plain.ID, c, "true", http.StatusOK); !slices.Equal(got, []string{a, b, c}) {
		t.Errorf("servers after turning one on = %v", got)
	}
	if got := toggle(plain.ID, a, "false", http.StatusOK); !slices.Equal(got, []string{b, c}) {
		t.Errorf("servers after turning one off = %v", got)
	}
	toggle(plain.ID, theirs, "true", http.StatusNotFound)
	toggle(plain.ID, "00000000-0000-0000-0000-000000000000", "true", http.StatusNotFound)
	on("PUT", "/v1/sessions/"+plain.ID+"/mcp-servers/"+a, `{}`, http.StatusBadRequest)

	// The sessions follow their agent but for what they turned on or off.
	body := `{"model": {"provider": "` + s.agentProvider(agent) + `", "name": "a-model"}, "mcp_servers": ["` + b + `"]}`
	if status, _ := s.request("PUT", "/v1/agents/"+agent, body, nil); status != http.StatusOK {
		t.Fatalf("PUT agent: status %d", status)
	}
	var list struct {
		Sessions []struct {
			ID         string
			McpServers []string `json:"mcp_servers"`
		}
	}
	if status, _ := s.request("GET", sessions, "", &list); status != http.StatusOK || len(list.Sessions) != 2 {
		t.Fatalf("list sessions: status %d, %+v", status, list.Sessions)
	}
	for _, session := range list.Sessions {
		want := map[string][]string{plain.ID: {b, c}, chosen.ID: {c}}[session.ID]
		if !slices.Equal(session.McpServers, want) {
			t.Errorf("listed session %s has %v, want %v", session.ID, session.McpServers, want)
		}
	}
	if got := on("DELETE", "/v1/sessions/"+plain.ID+"/mcp-servers", "", http.StatusOK); !slices.Equal(got, []string{b}) {
		t.Errorf("servers after going back to the agent's = %v", got)
	}

	// A session that has ended has none, and they stay.
	if got := on("DELETE", "/v1/sessions/"+plain.ID, "", http.StatusOK); got != nil {
		t.Errorf("servers of an ended session = %v, want none", got)
	}
	toggle(plain.ID, a, "true", http.StatusConflict)
	on("DELETE", "/v1/sessions/"+plain.ID+"/mcp-servers", "", http.StatusConflict)
}
