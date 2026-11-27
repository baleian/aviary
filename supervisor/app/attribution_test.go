package app

import (
	"maps"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"

	"aviary/supervisor/internal/api"
)

// The egress gateway asks whom each connection of a sandbox is for, and any
// Supervisor answers with the turn that runs in the sandbox's session or,
// between turns, the one that ran last, with the user who sent its message.
func TestConnectionsOfASandboxAreAttributed(t *testing.T) {
	t.Parallel()
	installation := install(t)
	installation.callers = api.Callers{
		Provider: tokens{"admin": {Subject: "admin", Username: "admin@example.com", Groups: []string{"platform"}}},
		Admins:   []string{"platform"},
	}
	s := installation.supervisor()
	s.token = "admin"
	var me struct{ ID string }
	if status, _ := s.request("GET", "/v1/me", "", &me); status != http.StatusOK || me.ID == "" {
		t.Fatalf("GET /v1/me: status %d, %+v", status, me)
	}
	project := s.project()
	agent := s.agent(project, `{"harness": "opencode"}`)
	session := s.create("/v1/agents/"+agent+"/sessions", "")
	first := s.send(session, "hello")
	runner := installation.given(first.ID)
	want := map[string]string{"turn": first.ID, "session": session, "agent": agent, "project": project, "user": me.ID, "subject": "admin"}
	attributed := func(what string, want map[string]string) {
		t.Helper()
		for _, supervisor := range []*supervisor{s, installation.supervisor()} {
			if got, code := supervisor.attribute(runner.Sandbox); code != codes.OK || !maps.Equal(got, want) {
				t.Errorf("%s: connection attributed to %v (%v), want %v", what, got, code, want)
			}
		}
	}
	attributed("running turn", want)

	runner.report(started("harness-session"))
	runner.report(finished("end_turn", nil))
	attributed("between turns", want)
	second := s.send(session, "and then")
	installation.given(second.ID)
	want["turn"] = second.ID
	attributed("next turn", want)

	// A sandbox that is not that of a session is refused.
	if got, code := s.attribute("not-a-session"); code != codes.PermissionDenied {
		t.Errorf("connection of another sandbox attributed to %v (%v), want PermissionDenied", got, code)
	}
}
