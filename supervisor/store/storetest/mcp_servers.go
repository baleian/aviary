package storetest

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// An MCP server is created with the ID that it is given, has a slug that is
// unique in its project, and is replaced whole but for its slug, or deleted.
func (suite suite) testMcpServers(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, other := st.project(), st.project()
	listed := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	given := store.McpServer{
		ID: uuid.New(), ProjectID: project.ID, Slug: "issue-tracker", Name: "Issue tracker", URL: "https://tracker.example.com/mcp",
		Transport:     store.TransportHTTP,
		Headers:       []store.Header{{Name: "authorization", Sealed: []byte("a sealed token")}, {Name: "x-team", Value: "a team"}},
		Tools:         []store.Tool{{Name: "search_issues", Description: "Search issues."}, {Name: "get_issue"}},
		ToolsListedAt: &listed,
	}
	created, err := st.CreateMcpServer(ctx, given)
	if err != nil {
		t.Fatal(err)
	}
	if !sameServer(created, given) || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreateMcpServer = %+v, want %+v", created, given)
	}
	if read, err := st.McpServer(ctx, given.ID); err != nil || !sameServer(read, created) || !read.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("McpServer = %+v, want %+v: %v", read, created, err)
	}

	// A slug is taken in its project only.
	taken := given
	taken.ID = uuid.New()
	if _, err := st.CreateMcpServer(ctx, taken); !errors.Is(err, store.ErrSlugTaken) {
		t.Errorf("CreateMcpServer with a slug that is taken: %v, want ErrSlugTaken", err)
	}
	elsewhere := taken
	elsewhere.ID, elsewhere.ProjectID = uuid.New(), other.ID
	if _, err := st.CreateMcpServer(ctx, elsewhere); err != nil {
		t.Errorf("CreateMcpServer with the slug of a server of another project: %v", err)
	}

	// A server that has not listed its tools has none, and a server without
	// headers has nil ones.
	quiet, err := st.CreateMcpServer(ctx, store.McpServer{
		ID: uuid.New(), ProjectID: project.ID, Slug: "docs", URL: "https://docs.example.com/sse", Transport: store.TransportSSE,
		Headers: []store.Header{}, Tools: []store.Tool{{Name: "unlisted"}},
	})
	if err != nil || quiet.Name != "" || quiet.Headers != nil || quiet.Tools != nil || quiet.ToolsListedAt != nil {
		t.Errorf("CreateMcpServer without headers or tools = %+v: %v", quiet, err)
	}
	servers, err := st.McpServers(ctx, project.ID)
	if err != nil || len(servers) != 2 || servers[0].ID != given.ID || servers[1].ID != quiet.ID {
		t.Errorf("McpServers = %+v: %v, want the two servers of the project in creation order", servers, err)
	}

	// The rest of a server is replaced together, and its slug stays. A server
	// that listed no tools has listed them all the same.
	relisted := listed.Add(time.Minute)
	replaced, err := st.ReplaceMcpServer(ctx, store.McpServer{
		ID: given.ID, Slug: "renamed", Name: "Tracker", URL: "https://tracker.example.com/v2/sse", Transport: store.TransportSSE,
		ToolsListedAt: &relisted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Slug != given.Slug || replaced.ProjectID != project.ID || replaced.Name != "Tracker" || replaced.URL != "https://tracker.example.com/v2/sse" ||
		replaced.Transport != store.TransportSSE || replaced.Headers != nil || len(replaced.Tools) != 0 || replaced.ToolsListedAt == nil ||
		!replaced.ToolsListedAt.Equal(relisted) || !replaced.CreatedAt.Equal(created.CreatedAt) || !replaced.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("ReplaceMcpServer = %+v, was %+v", replaced, created)
	}
	if read, err := st.McpServer(ctx, given.ID); err != nil || !sameServer(read, replaced) {
		t.Errorf("McpServer after ReplaceMcpServer = %+v, want %+v: %v", read, replaced, err)
	}

	// The tools of a server are set apart from the rest, which stays.
	now := listed.Add(2 * time.Minute)
	tools := []store.Tool{{Name: "list_issues"}}
	if err := st.SetMcpServerTools(ctx, given.ID, tools, now); err != nil {
		t.Fatal(err)
	}
	if read, err := st.McpServer(ctx, given.ID); err != nil || !reflect.DeepEqual(read.Tools, tools) || read.ToolsListedAt == nil ||
		!read.ToolsListedAt.Equal(now) || read.Name != "Tracker" || !read.UpdatedAt.Equal(replaced.UpdatedAt) {
		t.Errorf("McpServer after SetMcpServerTools = %+v: %v", read, err)
	}

	// A deleted server is gone, and its slug is free.
	if err := st.DeleteMcpServer(ctx, given.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.McpServer(ctx, given.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("McpServer after DeleteMcpServer: %v, want ErrNotFound", err)
	}
	if _, err := st.CreateMcpServer(ctx, taken); err != nil {
		t.Errorf("CreateMcpServer with the slug of a deleted server: %v", err)
	}

	none := uuid.New()
	for name, err := range map[string]error{
		"McpServer":        second(st.McpServer(ctx, none)),
		"ReplaceMcpServer": second(st.ReplaceMcpServer(ctx, store.McpServer{ID: none, URL: "https://a.example.com/mcp", Transport: store.TransportHTTP})),
		"CreateMcpServer": second(st.CreateMcpServer(ctx, store.McpServer{
			ID: uuid.New(), ProjectID: none, Slug: "a-server", URL: "https://a.example.com/mcp", Transport: store.TransportHTTP,
		})),
		"SetMcpServerTools": st.SetMcpServerTools(ctx, none, nil, listed),
		"DeleteMcpServer":   st.DeleteMcpServer(ctx, none),
	} {
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s = %v, want ErrNotFound", name, err)
		}
	}
}

// sameServer reports whether two MCP servers have equal stored fields, but for
// when they were created and replaced.
func sameServer(one, other store.McpServer) bool {
	listed := (one.ToolsListedAt == nil) == (other.ToolsListedAt == nil) &&
		(one.ToolsListedAt == nil || one.ToolsListedAt.Equal(*other.ToolsListedAt))
	return one.ID == other.ID && one.ProjectID == other.ProjectID && one.Slug == other.Slug && one.Name == other.Name &&
		one.URL == other.URL && one.Transport == other.Transport && reflect.DeepEqual(one.Headers, other.Headers) &&
		reflect.DeepEqual(one.Tools, other.Tools) && listed
}

// projectWithServers returns a project with a provider and MCP servers with
// the slugs, in the order of the slugs.
func (s supervisor) projectWithServers(slugs ...string) (store.Project, store.Provider, []uuid.UUID) {
	s.t.Helper()
	project := s.project()
	provider, err := s.CreateProvider(ctx, store.Provider{ID: uuid.New(), ProjectID: project.ID, Name: "a provider", URL: "https://api.example.com/v1"})
	if err != nil {
		s.t.Fatal(err)
	}
	servers := make([]uuid.UUID, len(slugs))
	for i, slug := range slugs {
		server, err := s.CreateMcpServer(ctx, store.McpServer{
			ID: uuid.New(), ProjectID: project.ID, Slug: slug, URL: "https://" + slug + ".example.com/mcp", Transport: store.TransportHTTP,
		})
		if err != nil {
			s.t.Fatal(err)
		}
		servers[i] = server.ID
	}
	return project, provider, servers
}

// An agent begins its sessions with MCP servers of its project, which it is
// given whole when it is created or replaced. They are in the order of the
// project's servers, and a server that is deleted leaves them.
func (suite suite) testAgentMcpServers(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, provider, servers := st.projectWithServers("first", "second", "third")
	first, second, third := servers[0], servers[1], servers[2]
	model := store.Model{Provider: provider.ID, Name: "a-model"}
	agent, err := st.CreateAgent(ctx, project.ID, store.Agent{Slug: unique("agent"), Harness: "a-harness", Model: model, McpServers: []uuid.UUID{third, first}})
	if err != nil || !slices.Equal(agent.McpServers, []uuid.UUID{first, third}) {
		t.Fatalf("CreateAgent = %v, %v, want its servers in the order of the project's", agent.McpServers, err)
	}
	// read returns the servers of the agent, as it is read alone and with the
	// others of the project.
	read := func() []uuid.UUID {
		t.Helper()
		alone, err := st.Agent(ctx, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		listed, err := st.Agents(ctx, project.ID)
		if err != nil || len(listed) != 1 || !slices.Equal(listed[0].McpServers, alone.McpServers) {
			t.Fatalf("Agents = %+v, %v, want the agent with servers %v", listed, err, alone.McpServers)
		}
		return alone.McpServers
	}
	if got := read(); !slices.Equal(got, []uuid.UUID{first, third}) {
		t.Errorf("servers of the agent = %v", got)
	}

	replaced, err := st.ReplaceAgent(ctx, store.Agent{ID: agent.ID, Model: model, McpServers: []uuid.UUID{second}})
	if err != nil || !slices.Equal(replaced.McpServers, []uuid.UUID{second}) || !slices.Equal(read(), []uuid.UUID{second}) {
		t.Errorf("ReplaceAgent = %v, %v, want the second server", replaced.McpServers, err)
	}
	if replaced, err := st.ReplaceAgent(ctx, store.Agent{ID: agent.ID, Model: model}); err != nil || replaced.McpServers != nil || read() != nil {
		t.Errorf("ReplaceAgent without servers = %v, %v, want none", replaced.McpServers, err)
	}
	if _, err := st.ReplaceAgent(ctx, store.Agent{ID: agent.ID, Model: model, McpServers: servers}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteMcpServer(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got := read(); !slices.Equal(got, []uuid.UUID{first, third}) {
		t.Errorf("servers of the agent after one was deleted = %v", got)
	}
	// A server that does not exist cannot be one of them.
	if _, err := st.CreateAgent(ctx, project.ID, store.Agent{Slug: unique("agent"), Harness: "a-harness", Model: model, McpServers: []uuid.UUID{uuid.New()}}); err == nil {
		t.Error("CreateAgent with a server that does not exist succeeded")
	}
	if got := read(); !slices.Equal(got, []uuid.UUID{first, third}) {
		t.Errorf("servers of the agent after a refused agent = %v", got)
	}
}

// A session has the MCP servers of its agent but for those that it turned on
// or off, until it ends. It follows its agent in the rest.
func (suite suite) testSessionMcpServers(t *testing.T) {
	t.Parallel()
	st := suite.open(t)
	project, provider, servers := st.projectWithServers("a", "b", "c")
	a, b, c := servers[0], servers[1], servers[2]
	model := store.Model{Provider: provider.ID, Name: "a-model"}
	agent, err := st.CreateAgent(ctx, project.ID, store.Agent{Slug: unique("agent"), Harness: "a-harness", Model: model, McpServers: []uuid.UUID{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := st.CreateSession(ctx, agent.ID, uuid.Nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	chosen, err := st.CreateSession(ctx, agent.ID, uuid.Nil, "", map[uuid.UUID]bool{b: false, c: true})
	if err != nil {
		t.Fatal(err)
	}
	// of returns the servers of the sessions.
	of := func(sessions ...uuid.UUID) map[uuid.UUID][]uuid.UUID {
		t.Helper()
		got, err := st.SessionMcpServers(ctx, sessions)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	want := map[uuid.UUID][]uuid.UUID{plain.ID: {a, b}, chosen.ID: {a, c}}
	if got := of(plain.ID, chosen.ID); !reflect.DeepEqual(got, want) {
		t.Errorf("SessionMcpServers = %v, want %v", got, want)
	}

	// A server turned on or off stays so, and one turned back to the agent's
	// follows the agent again.
	for _, set := range []struct {
		server  uuid.UUID
		enabled bool
		want    []uuid.UUID
	}{
		{c, true, []uuid.UUID{a, b, c}},
		{a, false, []uuid.UUID{b, c}},
		{a, true, []uuid.UUID{a, b, c}},
	} {
		session, err := st.SetSessionMcpServer(ctx, plain.ID, set.server, set.enabled)
		if err != nil || session.ID != plain.ID {
			t.Fatalf("SetSessionMcpServer = %+v, %v", session, err)
		}
		if got := of(plain.ID)[plain.ID]; !slices.Equal(got, set.want) {
			t.Errorf("servers after turning %v to %v = %v, want %v", set.server, set.enabled, got, set.want)
		}
	}
	if _, err := st.ReplaceAgent(ctx, store.Agent{ID: agent.ID, Model: model, McpServers: []uuid.UUID{b}}); err != nil {
		t.Fatal(err)
	}
	want = map[uuid.UUID][]uuid.UUID{plain.ID: {b, c}, chosen.ID: {c}}
	if got := of(plain.ID, chosen.ID); !reflect.DeepEqual(got, want) {
		t.Errorf("SessionMcpServers after the agent changed = %v, want %v", got, want)
	}
	if session, err := st.ResetSessionMcpServers(ctx, chosen.ID); err != nil || session.ID != chosen.ID {
		t.Fatalf("ResetSessionMcpServers = %+v, %v", session, err)
	}
	if got := of(chosen.ID)[chosen.ID]; !slices.Equal(got, []uuid.UUID{b}) {
		t.Errorf("servers after the session went back to its agent's = %v", got)
	}
	// A server that is deleted leaves the session, and a session without
	// servers is left out.
	if err := st.DeleteMcpServer(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetSessionMcpServer(ctx, chosen.ID, b, false); err != nil {
		t.Fatal(err)
	}
	want = map[uuid.UUID][]uuid.UUID{plain.ID: {b}}
	if got := of(plain.ID, chosen.ID); !reflect.DeepEqual(got, want) {
		t.Errorf("SessionMcpServers after a server was deleted = %v, want %v", got, want)
	}

	// A session that has ended has none, and keeps them as they were.
	if _, err := st.EndSession(ctx, plain.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := of(plain.ID); len(got) != 0 {
		t.Errorf("SessionMcpServers of an ended session = %v, want none", got)
	}
	none := uuid.New()
	for name, err := range map[string]struct{ got, want error }{
		"SetSessionMcpServer of an ended session":    {second(st.SetSessionMcpServer(ctx, plain.ID, b, false)), store.ErrSessionEnded},
		"ResetSessionMcpServers of an ended session": {second(st.ResetSessionMcpServers(ctx, plain.ID)), store.ErrSessionEnded},
		"SetSessionMcpServer":                        {second(st.SetSessionMcpServer(ctx, none, b, true)), store.ErrNotFound},
		"ResetSessionMcpServers":                     {second(st.ResetSessionMcpServers(ctx, none)), store.ErrNotFound},
	} {
		if !errors.Is(err.got, err.want) {
			t.Errorf("%s = %v, want %v", name, err.got, err.want)
		}
	}
}
