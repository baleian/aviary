package app

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"aviary/supervisor/internal/credentials"
	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/internal/sandbox/runnertest"
)

// A harness reaches the MCP servers that its session has on at their URLs,
// with their headers. It has a placeholder in place of each secret, which
// the egress gateway replaces as long as the session has the server on.
func TestRequestsToTheMcpServersOfASession(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	project, other := s.project(), s.project()
	servers := "/v1/projects/" + project + "/mcp-servers"
	tracker := s.create(servers, `{"slug": "tracker", "url": "https://tracker.example.com/mcp", "transport": "http",
		"headers": [{"name": "authorization", "secret": {"value": "Bearer t0ken"}}, {"name": "x-team", "value": "a team"}]}`)
	docs := s.create(servers, `{"slug": "docs", "url": "http://docs.example.com:8080/sse", "transport": "sse"}`)
	theirs := s.server(other, "theirs")
	agent := s.agent(project, `{"harness": "opencode", "mcp_servers": ["`+tracker+`"]}`)
	session := s.create("/v1/agents/"+agent+"/sessions", "")
	runner := aviary.given(s.send(session, "hello").ID)
	want := []runnertest.McpServer{{ID: tracker, Name: "tracker", Transport: "http", URL: "https://tracker.example.com/mcp",
		Headers: []runnertest.McpHeader{{Name: "authorization", Value: credentials.Placeholder}, {Name: "x-team", Value: "a team"}}}}
	if !reflect.DeepEqual(runner.McpServers, want) {
		t.Errorf("MCP servers of the turn = %+v, want %+v", runner.McpServers, want)
	}
	secret := credentials.McpServerHeader(uuid.MustParse(tracker), "authorization")
	egress := aviary.made(runner.Sandbox).Egress
	if !slices.Contains(egress.Secrets, sandbox.Secret{Host: sandbox.Host{TLS: true, Name: "tracker.example.com", Port: 443}, Header: "authorization", URI: secret}) ||
		len(egress.Secrets) != 2 || len(egress.Hosts) != 0 {
		t.Errorf("egress = %+v, want the key of the provider and the secret of the server", egress)
	}
	if got, code, why := s.fetch(runner.Sandbox, secret); code != codes.OK || got != "Bearer t0ken" {
		t.Errorf("the secret of the server = %q, refused with %v %q", got, code, why)
	}

	// The secrets of a server that the session does not have on are refused.
	const off = "the MCP server is not on in the session"
	for name, server := range map[string]string{"a server that is off": docs, "a server of another project": theirs} {
		if _, code, why := s.fetch(runner.Sandbox, credentials.McpServerHeader(uuid.MustParse(server), "authorization")); code != codes.PermissionDenied || why != off {
			t.Errorf("%s: refused with %v %q", name, code, why)
		}
	}
	if _, code, _ := s.fetch(runner.Sandbox, "ate-secret://"+credentials.Class+"/default/mcp-servers/docs/headers/authorization"); code != codes.NotFound {
		t.Errorf("a URI without the ID of a server: refused with %v, want NotFound", code)
	}
	if status, _ := s.request("PUT", "/v1/sessions/"+session+"/mcp-servers/"+docs, `{"enabled": true}`, nil); status != http.StatusOK {
		t.Fatalf("turn the server on: status %d", status)
	}
	if _, code, why := s.fetch(runner.Sandbox, credentials.McpServerHeader(uuid.MustParse(docs), "authorization")); code != codes.NotFound || why != "the MCP server has no such header" {
		t.Errorf("a header that the server does not have: refused with %v %q", code, why)
	}
	// A server that the session turns off gets no secrets from then on, in
	// the same turn.
	if status, _ := s.request("PUT", "/v1/sessions/"+session+"/mcp-servers/"+tracker, `{"enabled": false}`, nil); status != http.StatusOK {
		t.Fatalf("turn the server off: status %d", status)
	}
	if _, code, why := s.fetch(runner.Sandbox, secret); code != codes.PermissionDenied || why != off {
		t.Errorf("the secret of a server that was turned off: refused with %v %q", code, why)
	}

	// The next turn has the servers that the session has on by then. The
	// runner names those that its harness does not support, and the turn
	// keeps them.
	runner.report(started("harness-session"))
	runner.report(finished("end_turn", nil))
	again := s.send(session, "again")
	next := aviary.given(again.ID)
	if want := []runnertest.McpServer{{ID: docs, Name: "docs", Transport: "sse", URL: "http://docs.example.com:8080/sse"}}; !reflect.DeepEqual(next.McpServers, want) {
		t.Errorf("MCP servers of the next turn = %+v, want %+v", next.McpServers, want)
	}
	if hosts := aviary.made(next.Sandbox).Egress.Hosts; !slices.Equal(hosts, []sandbox.Host{{Name: "docs.example.com", Port: 8080}}) {
		t.Errorf("hosts of the next turn = %+v, want the server's", hosts)
	}
	unsupported := map[string]any{"servers": []map[string]string{{"id": docs, "name": "docs", "transport": "sse"}}}
	next.report(started("harness-session"))
	next.report(message{"mcp.unsupported", unsupported})
	next.report(finished("end_turn", nil))
	said, err := json.Marshal(items(t, s.turn(again.ID)))
	if want := `[{"servers":[{"id":"` + docs + `","name":"docs","transport":"sse"}],"type":"mcp_unsupported"}]`; err != nil || string(said) != want {
		t.Errorf("items of the next turn = %s, want %s", said, want)
	}
}
