package storetest

import (
	"errors"
	"reflect"
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
