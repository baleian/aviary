package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// mcpServer is an MCP server as the API returns it.
type mcpServer struct {
	ID            string          `json:"id"`
	ProjectID     string          `json:"project_id"`
	Slug          string          `json:"slug"`
	Name          string          `json:"name"`
	URL           string          `json:"url"`
	Transport     string          `json:"transport"`
	Headers       []header        `json:"headers"`
	Tools         json.RawMessage `json:"tools"`
	ToolsListedAt *time.Time      `json:"tools_listed_at"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// A project registers MCP servers, each by a slug of its own. The headers of a
// server are those of a provider, and authorization is one of them: their
// secrets are stored or kept in a secret store, and never returned.
func TestMcpServers(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project := s.project()
	servers := "/v1/projects/" + project + "/mcp-servers"
	const token = "Bearer t0ken-of-the-server"
	// answer sends a request, and returns the status of the response and the
	// server or the message in it. It fails the test if the response reveals
	// the secret.
	answer := func(method, path, body string) (int, mcpServer, string) {
		t.Helper()
		var raw json.RawMessage
		status, _ := s.request(method, path, body, &raw)
		if strings.Contains(string(raw), "t0ken") || strings.Contains(string(raw), "sealed") {
			t.Errorf("%s %s response reveals the secret: %s", method, path, raw)
		}
		var got struct {
			mcpServer
			Message string
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
		}
		return status, got.mcpServer, got.Message
	}
	// sealed returns the secret of the server's header as it is stored.
	sealed := func(id, name string) []byte {
		t.Helper()
		record, err := aviary.records.McpServer(context.Background(), uuid.MustParse(id))
		if err != nil {
			t.Fatal(err)
		}
		for _, header := range record.Headers {
			if header.Name == name {
				return header.Sealed
			}
		}
		t.Fatalf("server %s has no header %s", id, name)
		return nil
	}

	status, created, why := answer("POST", servers, `{"slug": "issue-tracker", "name": "Issue tracker", "url": "https://tracker.example.com/mcp",
		"transport": "http", "headers": [{"name": "Authorization", "secret": {"value": "`+token+`"}}, {"name": "x-team", "value": "a team"}]}`)
	headers := `authorization: {"stored":true}` + "\nx-team: a team\n"
	if status != http.StatusCreated || created.ProjectID != project || created.Slug != "issue-tracker" || created.Name != "Issue tracker" ||
		created.URL != "https://tracker.example.com/mcp" || created.Transport != "http" || lines(created.Headers) != headers ||
		created.Tools != nil || created.ToolsListedAt != nil || created.CreatedAt.IsZero() || !created.UpdatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created server: %d %q, %+v", status, why, created)
	}
	path := "/v1/mcp-servers/" + created.ID
	// The secret is sealed for the server and the name of its header.
	if opened, err := aviary.keys.Open(sealed(created.ID, "authorization"), store.McpServerHeaderPlace(uuid.MustParse(created.ID), "authorization")); err != nil || opened != token {
		t.Errorf("stored secret opens to %q, %v", opened, err)
	}
	if status, read, _ := answer("GET", path, ""); status != http.StatusOK || read.ID != created.ID || lines(read.Headers) != headers {
		t.Errorf("GET server: %d %+v", status, read)
	}
	var list struct {
		McpServers []mcpServer `json:"mcp_servers"`
	}
	if status, _ := s.request("GET", servers, "", &list); status != http.StatusOK || len(list.McpServers) != 1 || list.McpServers[0].Slug != "issue-tracker" {
		t.Errorf("GET servers: %d %+v", status, list.McpServers)
	}

	// What is refused leaves the server unchanged.
	for name, refused := range map[string]struct {
		method, path, body string
		status             int
		why                string
	}{
		"a slug that is taken": {"POST", servers, `{"slug": "issue-tracker", "url": "https://other.example.com/mcp", "transport": "http"}`,
			http.StatusConflict, "the slug is already taken"},
		"no slug": {"POST", servers, `{"url": "https://other.example.com/mcp", "transport": "http"}`, http.StatusBadRequest, "slug is required"},
		"a slug that is not one": {"POST", servers, `{"slug": "issue_tracker", "url": "https://other.example.com/mcp", "transport": "http"}`,
			http.StatusBadRequest, "slug must be 1 to 63 lowercase letters, digits and hyphens, beginning with a letter and not ending with a hyphen"},
		"another slug": {"PUT", path, `{"slug": "tracker", "url": "https://tracker.example.com/mcp", "transport": "http"}`,
			http.StatusBadRequest, "slug cannot be changed"},
		"no transport": {"POST", servers, `{"slug": "other", "url": "https://other.example.com/mcp"}`,
			http.StatusBadRequest, `transport must be "http" for Streamable HTTP, or "sse"`},
		"stdio": {"POST", servers, `{"slug": "other", "url": "https://other.example.com/mcp", "transport": "stdio"}`,
			http.StatusBadRequest, `transport must be "http" for Streamable HTTP, or "sse"`},
		"a URL with a password": {"POST", servers, `{"slug": "other", "url": "https://user:password@other.example.com/mcp", "transport": "http"}`,
			http.StatusBadRequest, "url must not contain a user name or password; set the server's secrets in its headers"},
		"a header of MCP": {"POST", servers, `{"slug": "other", "url": "https://other.example.com/mcp", "transport": "http", "headers": [{"name": "Mcp-Session-Id", "value": "a"}]}`,
			http.StatusBadRequest, "headers[0].name must not be mcp-session-id, which is reserved"},
		"a stored secret for another URL": {"PUT", path, `{"url": "https://other.example.com/mcp", "transport": "http", "headers": [{"name": "authorization", "secret": {"stored": true}}]}`,
			http.StatusBadRequest, "headers[0].secret must be given again: the URL changed"},
	} {
		if status, _, why := answer(refused.method, refused.path, refused.body); status != refused.status || why != refused.why {
			t.Errorf("%s: %d %q, want %d %q", name, status, why, refused.status, refused.why)
		}
	}
	if status, read, _ := answer("GET", path, ""); status != http.StatusOK || read.Name != "Issue tracker" || !read.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("server after refused requests: %d %+v", status, read)
	}

	// PUT replaces all but the slug, and keeps the stored secret while the URL
	// stays the same.
	before := sealed(created.ID, "authorization")
	status, replaced, why := answer("PUT", path, `{"slug": "issue-tracker", "url": "https://tracker.example.com/mcp", "transport": "sse",
		"headers": [{"name": "authorization", "secret": {"stored": true}}]}`)
	if status != http.StatusOK || replaced.Slug != "issue-tracker" || replaced.Name != "" || replaced.Transport != "sse" ||
		lines(replaced.Headers) != `authorization: {"stored":true}`+"\n" || !replaced.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("replaced server: %d %q, %+v", status, why, replaced)
	}
	if !bytes.Equal(sealed(created.ID, "authorization"), before) {
		t.Error("the kept secret was sealed again")
	}

	// A deleted server is gone, and its slug is free.
	if status, _ := s.request("DELETE", path, "", nil); status != http.StatusNoContent {
		t.Errorf("DELETE server: status %d, want 204", status)
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if status, _, _ := answer(method, path, `{"url": "https://tracker.example.com/mcp", "transport": "http"}`); status != http.StatusNotFound {
			t.Errorf("%s of a deleted server: status %d, want 404", method, status)
		}
	}
	if status, _, why := answer("POST", servers, `{"slug": "issue-tracker", "url": "https://tracker.example.com/mcp", "transport": "http"}`); status != http.StatusCreated {
		t.Errorf("POST with the slug of a deleted server: %d %q", status, why)
	}
}
