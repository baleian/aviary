package app

import (
	"context"
	"net/http"
	"slices"
	"testing"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"google.golang.org/grpc/codes"
)

// checkMcp asks the Supervisor about a request of the model listener with the
// token that names the MCP server, as a harness sends it to one.
func (s *supervisor) checkMcp(token, server, path string) *authv3.CheckResponse {
	s.t.Helper()
	answer, err := s.proxies.Check(context.Background(), &authv3.CheckRequest{Attributes: &authv3.AttributeContext{
		Request: &authv3.AttributeContext_Request{Http: &authv3.AttributeContext_HttpRequest{
			Headers: map[string]string{"authorization": "Bearer " + token, "x-aviary-mcp": server}, Path: path,
		}},
		ContextExtensions: map[string]string{"listener": "llm"},
	}})
	if err != nil {
		s.t.Fatal(err)
	}
	return answer
}

// A harness reaches the MCP servers of its session through the model listener
// of the Egress Proxy: a request names its server, and goes to the server with
// the server's headers, as long as the session has it on.
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
	token := aviary.token(runner.Sandbox)

	// The request goes to the server with its headers, the server's token in
	// place of the session's, and without the header that names the server.
	answer := s.checkMcp(token, tracker, "/mcp")
	if got := named(t, answer); got == nil || *got != (endpoint{host: "tracker.example.com", secure: "true", authorization: "Bearer t0ken", headers: "x-team: a team\n"}) {
		t.Errorf("request to the server of the agent: %+v", got)
	}
	if removed := answer.GetOkResponse().GetHeadersToRemove(); !slices.Equal(removed, []string{"x-aviary-mcp"}) {
		t.Errorf("headers removed from the request: %v", removed)
	}
	fields := answer.GetDynamicMetadata().GetFields()
	if fields["mcp_server"].GetStringValue() != tracker || fields["turn"].GetStringValue() != runner.ID || fields["provider"] != nil {
		t.Errorf("metadata of the request: %v", fields)
	}
	if status, why := refusal(s.checkMcp(token, tracker, "/other")); status != http.StatusNotFound || why != "requests to the MCP server must be under the path /mcp\n" {
		t.Errorf("request at another path: %d %q", status, why)
	}

	// A server that the session does not have is refused, and one that it
	// turns on is reached from then on, in the same turn. A server of SSE is
	// reached at any path of its host, where it says to send messages.
	const off = "the MCP server is not on in the session\n"
	for name, server := range map[string]string{"a server that is off": docs, "a server of another project": theirs, "no ID": "docs"} {
		if status, why := refusal(s.checkMcp(token, server, "/sse")); status != http.StatusForbidden || why != off {
			t.Errorf("%s: %d %q", name, status, why)
		}
	}
	if status, _ := s.request("PUT", "/v1/sessions/"+session+"/mcp-servers/"+docs, `{"enabled": true}`, nil); status != http.StatusOK {
		t.Fatalf("turn the server on: status %d", status)
	}
	answer = s.checkMcp(token, docs, "/messages?session_id=1")
	if got := named(t, answer); got == nil || *got != (endpoint{host: "docs.example.com:8080", secure: "false", keyless: true}) {
		t.Errorf("request to a server that was turned on: %+v", got)
	}
	if status, _ := s.request("PUT", "/v1/sessions/"+session+"/mcp-servers/"+tracker, `{"enabled": false}`, nil); status != http.StatusOK {
		t.Fatalf("turn the server off: status %d", status)
	}
	if status, why := refusal(s.checkMcp(token, tracker, "/mcp")); status != http.StatusForbidden || why != off {
		t.Errorf("request to a server that was turned off: %d %q", status, why)
	}

	// A request that names no server is a model request, and a forward proxy
	// request goes where the pod sends it.
	if got := named(t, s.check(token, false, "/v1/chat/completions")); got == nil || got.host != "api.example.com" {
		t.Errorf("model request: %+v", got)
	}
	// Once the turn has ended, the token reaches nothing.
	runner.report(started("harness-session"))
	runner.report(finished("end_turn", nil))
	eventually(t, patience, "the request of an ended turn is allowed", func() bool {
		return codes.Code(s.checkMcp(token, docs, "/sse").GetStatus().GetCode()) != codes.OK
	})
}
