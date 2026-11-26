package app

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpEndpoint returns an MCP server that lists the tools, by Streamable HTTP
// or by SSE. A server with a token refuses a request without it with 401.
func mcpEndpoint(sse bool, token string, tools ...string) http.HandlerFunc {
	server := mcp.NewServer(&mcp.Implementation{Name: "a server for tests"}, nil)
	for _, name := range tools {
		server.AddTool(&mcp.Tool{Name: name, Description: "Does " + name + ".", InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{}, nil
			})
	}
	serving := func(*http.Request) *mcp.Server { return server }
	var handler http.Handler = mcp.NewStreamableHTTPHandler(serving, nil)
	if sse {
		handler = mcp.NewSSEHandler(serving, nil)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}
}

// toolsGot is what came of asking an MCP server for its tools. tools are the
// names of the tools that it listed, sorted and joined with commas.
type toolsGot struct {
	reached bool
	status  int
	reason  string
	tools   string
}

// toolsListing sends a request that lists the tools of an MCP server, and
// returns what came of it.
func (s *supervisor) toolsListing(method, path, body string) toolsGot {
	s.t.Helper()
	var got struct {
		Reached    bool
		Status     int
		Reason     string
		Tools      []struct{ Name, Description string }
		DurationMS *int64 `json:"duration_ms"`
	}
	if status, _ := s.request(method, path, body, &got); status != http.StatusOK {
		s.t.Fatalf("%s %s %s: status %d, want 200", method, path, body, status)
	}
	if got.DurationMS == nil || *got.DurationMS < 0 {
		s.t.Errorf("%s %s %s: duration_ms = %v", method, path, body, got.DurationMS)
	}
	var names []string
	for _, tool := range got.Tools {
		names = append(names, tool.Name)
		if tool.Description != "Does "+tool.Name+"." {
			s.t.Errorf("%s %s: tool %s has the description %q", method, path, tool.Name, tool.Description)
		}
	}
	slices.Sort(names)
	return toolsGot{got.Reached, got.Status, got.Reason, strings.Join(names, ",")}
}

// The tools of an MCP server are listed before it is registered: the
// Supervisor asks the server for them with the server's headers, as a harness
// reaches the server. The result is the tools, the status of the server's
// answer that was not a success, or why there was no answer.
func TestTheToolsOfAnMcpServerAreListed(t *testing.T) {
	t.Parallel()
	aviary := reaching(t, map[string]http.HandlerFunc{
		"tracker.example.com": mcpEndpoint(false, "t0ken", "search_issues", "get_issue"),
		"docs.example.com":    mcpEndpoint(true, "", "search_docs"),
		"open.example.com":    mcpEndpoint(false, "", "echo"),
		"page.example.com":    answers("<html>a page</html>"),
	})
	s := aviary.supervisor()
	project := s.project()
	tools := "/v1/projects/" + project + "/mcp-servers/tools"

	got := s.toolsListing("POST", tools, `{"url": "https://tracker.example.com/mcp", "transport": "http",
		"headers": [{"name": "authorization", "secret": {"value": "Bearer t0ken"}}, {"name": "x-team", "value": "a team"}]}`)
	if got != (toolsGot{reached: true, status: http.StatusOK, tools: "get_issue,search_issues"}) {
		t.Errorf("a server that takes a token: %+v", got)
	}
	if asked := aviary.endpoints.last(); asked.host != "tracker.example.com" || !asked.secure || asked.authorization != "Bearer t0ken" || !strings.HasPrefix(asked.path, "/mcp") {
		t.Errorf("the server got %+v", asked)
	}

	for name, test := range map[string]struct {
		body string
		want toolsGot
	}{
		"no token for a server that takes one": {`{"url": "https://tracker.example.com/mcp", "transport": "http"}`, toolsGot{reached: true, status: http.StatusUnauthorized}},
		"a token that the server refuses": {`{"url": "https://tracker.example.com/mcp", "transport": "http", "headers": [{"name": "authorization", "value": "Bearer other"}]}`,
			toolsGot{reached: true, status: http.StatusUnauthorized}},
		"a server of SSE":              {`{"url": "https://docs.example.com/sse", "transport": "sse"}`, toolsGot{reached: true, status: http.StatusOK, tools: "search_docs"}},
		"a server without a token":     {`{"url": "https://open.example.com/mcp", "transport": "http"}`, toolsGot{reached: true, status: http.StatusOK, tools: "echo"}},
		"a server that is not reached": {`{"url": "https://nowhere.example.com/mcp", "transport": "http"}`, toolsGot{reason: "the server cannot be reached"}},
	} {
		if got := s.toolsListing("POST", tools, test.body); got != test.want {
			t.Errorf("%s: %+v, want %+v", name, got, test.want)
		}
	}
	// A server that answers, but not as MCP does, is reached and lists no
	// tools.
	for name, body := range map[string]string{
		"a page":                  `{"url": "https://page.example.com/mcp", "transport": "http"}`,
		"SSE of a server of HTTP": `{"url": "https://open.example.com/mcp", "transport": "sse"}`,
		"HTTP of a server of SSE": `{"url": "https://docs.example.com/sse", "transport": "http"}`,
	} {
		if got := s.toolsListing("POST", tools, body); !got.reached || got.tools != "" || (got.status == http.StatusOK) == (got.reason == "") {
			t.Errorf("%s: %+v, want it reached, with a status that is not a success or a reason", name, got)
		}
	}

	// Nothing of it is stored.
	var registered struct {
		McpServers []struct{ ID string } `json:"mcp_servers"`
	}
	if status, _ := s.request("GET", "/v1/projects/"+project+"/mcp-servers", "", &registered); status != http.StatusOK || len(registered.McpServers) != 0 {
		t.Errorf("servers after the listings: status %d, %v", status, registered.McpServers)
	}
}

// An MCP server is registered with the tools that it lists then. It is tried
// before it is replaced with the secrets that it stores, and lists its tools
// again on request: if it does not list them, it keeps those that it listed.
func TestTheToolsOfAnMcpServerAreKept(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	serving := mcpEndpoint(false, "t0ken", "search_issues", "get_issue")
	aviary := reaching(t, map[string]http.HandlerFunc{
		"tracker.example.com": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			now := serving
			mu.Unlock()
			now(w, r)
		},
	})
	serve := func(handler http.HandlerFunc) {
		mu.Lock()
		defer mu.Unlock()
		serving = handler
	}
	s := aviary.supervisor()
	project := s.project()
	servers := "/v1/projects/" + project + "/mcp-servers"
	// read returns the names of the tools of the server, and when they were
	// listed.
	read := func(id string) (string, *time.Time, time.Time) {
		t.Helper()
		var server mcpServer
		if status, _ := s.request("GET", "/v1/mcp-servers/"+id, "", &server); status != http.StatusOK {
			t.Fatalf("GET server: status %d", status)
		}
		var listed []struct{ Name string }
		if server.Tools != nil {
			if err := json.Unmarshal(server.Tools, &listed); err != nil {
				t.Fatal(err)
			}
		}
		var names []string
		for _, tool := range listed {
			names = append(names, tool.Name)
		}
		slices.Sort(names)
		return strings.Join(names, ","), server.ToolsListedAt, server.UpdatedAt
	}

	const tracker = `"url": "https://tracker.example.com/mcp", "transport": "http"`
	created := s.create(servers, `{"slug": "tracker", `+tracker+`, "headers": [{"name": "authorization", "secret": {"value": "Bearer t0ken"}}]}`)
	names, listedAt, updatedAt := read(created)
	if names != "get_issue,search_issues" || listedAt == nil {
		t.Errorf("registered server: tools %q, listed at %v", names, listedAt)
	}

	// The server is tried with the secret that it stores, while its URL stays
	// the same.
	kept := `"headers": [{"name": "authorization", "secret": {"stored": true}}], "server": "` + created + `"`
	if got := s.toolsListing("POST", servers+"/tools", `{`+tracker+`, `+kept+`}`); got != (toolsGot{reached: true, status: http.StatusOK, tools: "get_issue,search_issues"}) {
		t.Errorf("a server tried with the secret that it stores: %+v", got)
	}
	for name, test := range map[string]struct{ body, why string }{
		"another URL":       {`{"url": "https://tracker.example.com/v2/mcp", "transport": "http", ` + kept + `}`, "headers[0].secret must be given again: the URL changed"},
		"an unknown server": {`{` + tracker + `, "server": "` + uuid.NewString() + `"}`, "server must be the ID of an MCP server of the project"},
		"a server of another project": {`{` + tracker + `, "server": "` + s.create("/v1/projects/"+s.project()+"/mcp-servers", `{"slug": "theirs", `+tracker+`}`) + `"}`,
			"server must be the ID of an MCP server of the project"},
	} {
		var refused struct{ Message string }
		if status, _ := s.request("POST", servers+"/tools", test.body, &refused); status != http.StatusBadRequest || refused.Message != test.why {
			t.Errorf("%s: %d %q, want 400 %q", name, status, refused.Message, test.why)
		}
	}

	// A server that lists other tools now has them once it is asked again.
	// Asking it changes nothing else of it.
	serve(mcpEndpoint(false, "t0ken", "search_issues", "get_issue", "create_issue"))
	if got := s.toolsListing("POST", "/v1/mcp-servers/"+created+"/tools", ""); got != (toolsGot{reached: true, status: http.StatusOK, tools: "create_issue,get_issue,search_issues"}) {
		t.Errorf("tools listed again: %+v", got)
	}
	names, relisted, updated := read(created)
	if names != "create_issue,get_issue,search_issues" || relisted == nil || !relisted.After(*listedAt) || !updated.Equal(updatedAt) {
		t.Errorf("server after its tools were listed again: tools %q, listed at %v, updated at %v", names, relisted, updated)
	}
	// A server that does not list them keeps those that it listed.
	serve(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	if got := s.toolsListing("POST", "/v1/mcp-servers/"+created+"/tools", ""); got != (toolsGot{reached: true, status: http.StatusServiceUnavailable}) {
		t.Errorf("tools listed again of a server that does not answer: %+v", got)
	}
	if names, _, _ := read(created); names != "create_issue,get_issue,search_issues" {
		t.Errorf("tools of a server that did not list them again: %q", names)
	}
	// A server is replaced with what it lists then, and registered without
	// tools if it lists none.
	serve(mcpEndpoint(false, "t0ken", "get_issue"))
	if status, _ := s.request("PUT", "/v1/mcp-servers/"+created, `{`+tracker+`, "headers": [{"name": "authorization", "secret": {"stored": true}}]}`, nil); status != http.StatusOK {
		t.Fatalf("PUT server: status %d", status)
	}
	if names, _, _ := read(created); names != "get_issue" {
		t.Errorf("tools of a replaced server: %q", names)
	}
	if names, listed, _ := read(s.create(servers, `{"slug": "gone", "url": "https://nowhere.example.com/mcp", "transport": "http"}`)); names != "" || listed != nil {
		t.Errorf("a server that is not reached is registered with tools %q, listed at %v", names, listed)
	}
}
