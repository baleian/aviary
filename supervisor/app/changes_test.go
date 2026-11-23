package app

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"aviary/supervisor/identity"
	"aviary/supervisor/internal/api"
)

// changes starts streaming the changes of the project's sessions, and
// returns when the stream is open.
func (s *supervisor) changes(project string) *reader {
	s.t.Helper()
	r := s.open("/v1/projects/"+project+"/events", "")
	select {
	case <-r.opened:
	case err := <-r.ended:
		s.t.Fatalf("event stream at %s: %v", r.path, err)
	case <-time.After(patience):
		s.t.Fatalf("timed out after %s: event stream at %s is not open", patience, r.path)
	}
	return r
}

// hears waits until the Supervisor hears the changes that are announced,
// which it misses until it has subscribed to the announcements.
func (s *supervisor) hears() {
	s.t.Helper()
	project := s.project()
	session := "/v1/sessions/" + s.create("/v1/agents/"+s.agent(project, `{"harness": "opencode"}`)+"/sessions", "")
	stream := s.changes(project)
	eventually(s.t, patience, "the Supervisor does not hear changes", func() bool {
		if status, _ := s.request("PATCH", session, `{"title": "heard"}`, nil); status != http.StatusOK {
			s.t.Fatalf("PATCH %s: status %d, want 200", session, status)
		}
		select {
		case <-stream.events:
			return true
		case <-time.After(200 * time.Millisecond):
			return false
		}
	})
}

// session returns the session that the event reports.
func (r *reader) session(sent event) map[string]any {
	r.t.Helper()
	if sent.Type != "session" || sent.ID != "" {
		r.t.Fatalf("event %+v at %s, want a session without an event ID", sent, r.path)
	}
	var session map[string]any
	if err := json.Unmarshal([]byte(sent.Data), &session); err != nil {
		r.t.Fatalf("session event %s: %v", sent.Data, err)
	}
	return session
}

// sent waits for the stream to send a session that holds accepts, and
// returns it. The sessions that it sends before are passed over.
func (r *reader) sent(what string, holds func(session map[string]any) bool) map[string]any {
	r.t.Helper()
	deadline := time.After(soon)
	for {
		select {
		case next := <-r.events:
			if session := r.session(next); holds(session) {
				return session
			}
		case <-deadline:
			r.t.Fatalf("timed out after %s: %s was not sent at %s", soon, what, r.path)
		}
	}
}

// A client that watches a project is sent each session of the project when
// the session or its newest turn changes, whichever Supervisor changes it.
func TestSessionChangesAreStreamed(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s, other := aviary.supervisor(), aviary.supervisor()
	other.hears()
	project := s.project()
	agent := s.agent(project, `{"harness": "opencode", "approval": "ask"}`)
	stream := other.changes(project)

	// The sessions of other projects are not sent.
	s.session()
	id := s.create("/v1/agents/"+agent+"/sessions", `{"title": "Fix the build"}`)
	path := "/v1/sessions/" + id
	// sent waits for the session in a state that holds accepts. last is its
	// newest turn.
	sent := func(what string, holds func(session, last map[string]any) bool) {
		t.Helper()
		stream.sent(what, func(session map[string]any) bool {
			if session["id"] != id {
				t.Fatalf("session %v was sent, want only the sessions of the project", session["id"])
			}
			last, _ := session["last_turn"].(map[string]any)
			for _, left := range []string{"text", "items"} {
				if value, has := last[left]; has {
					t.Fatalf("last_turn was sent with %s = %v, want it left out", left, value)
				}
			}
			return holds(session, last)
		})
	}
	sent("the new session", func(session, last map[string]any) bool {
		return session["title"] == "Fix the build" && session["agent_id"] == agent && last == nil
	})

	first := s.send(id, "hello")
	runner := aviary.given(first.ID)
	runner.report(started("harness-session"))
	sent("the session with its running turn", func(session, last map[string]any) bool {
		return last["id"] == first.ID && last["state"] == "running" && last["started_at"] != nil && session["sandbox"] == "awake"
	})

	runner.report(asks("1", "make test"))
	sent("the request that waits for an answer", func(_, last map[string]any) bool {
		waiting, _ := last["approvals"].([]any)
		return len(waiting) == 1 && waiting[0].(map[string]any)["id"] == "1"
	})
	if status, reply := s.approve(first.ID, "1", `{"option_id": "once"}`); status != http.StatusAccepted {
		t.Fatalf("approve = %d %v, want 202", status, reply)
	}
	sent("the turn without the answered request", func(_, last map[string]any) bool {
		return last["id"] == first.ID && last["state"] == "running" && last["approvals"] == nil
	})
	runner.answer()
	runner.report(answered("1", "selected", "once"))
	runner.report(finished("end_turn", nil))
	sent("the session with its completed turn", func(_, last map[string]any) bool {
		return last["id"] == first.ID && last["state"] == "completed" && last["ended_at"] != nil
	})

	second := s.send(id, "and then")
	runner = aviary.given(second.ID)
	sent("the session with its next turn", func(_, last map[string]any) bool {
		return last["id"] == second.ID && last["seq"] == float64(2) && last["ended_at"] == nil
	})
	runner.report(started("harness-session"))
	if status := s.answered("", "POST", "/v1/turns/"+second.ID+"/cancel", ""); status != http.StatusAccepted {
		t.Fatalf("cancel running turn: status %d, want 202", status)
	}
	sent("the turn that was asked to stop", func(_, last map[string]any) bool {
		return last["id"] == second.ID && last["state"] == "running" && last["cancel_requested_at"] != nil
	})
	runner.cancelled()
	runner.report(finished("cancelled", nil))
	sent("the session with its cancelled turn", func(_, last map[string]any) bool {
		return last["id"] == second.ID && last["state"] == "cancelled"
	})

	// So is which MCP servers its next turns have.
	server := s.server(project, "tools")
	if status, _ := s.request("PUT", path+"/mcp-servers/"+server, `{"enabled": true}`, nil); status != http.StatusOK {
		t.Fatalf("turn an MCP server on: status %d, want 200", status)
	}
	sent("the session with the MCP server on", func(session, _ map[string]any) bool {
		on, _ := session["mcp_servers"].([]any)
		return len(on) == 1 && on[0] == server
	})
	if status, _ := s.request("DELETE", path+"/mcp-servers", "", nil); status != http.StatusOK {
		t.Fatalf("go back to the MCP servers of the agent: status %d, want 200", status)
	}
	sent("the session with the MCP servers of its agent", func(session, _ map[string]any) bool { return session["mcp_servers"] == nil })

	if status, _ := s.request("PATCH", path, `{"title": "Fix the tests"}`, nil); status != http.StatusOK {
		t.Fatalf("PATCH %s: status %d, want 200", path, status)
	}
	sent("the new title", func(session, _ map[string]any) bool { return session["title"] == "Fix the tests" })

	// A session is sent as it is read.
	if status, _ := s.request("DELETE", path, "", nil); status != http.StatusOK {
		t.Fatalf("DELETE %s: status %d, want 200", path, status)
	}
	sent("the session that has ended, as it is read", func(session, _ map[string]any) bool {
		var read map[string]any
		if status, _ := other.request("GET", path, "", &read); status != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, status)
		}
		_, has := read["sandbox"]
		return read["ended_at"] != nil && !has && reflect.DeepEqual(session, read)
	})
}

// A stream of changes starts with the changes that follow its opening, and
// ends when its Supervisor stops.
func TestSessionChangesAreNotReplayed(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	s.hears()
	project := s.project()
	sessions := "/v1/agents/" + s.agent(project, `{"harness": "opencode"}`) + "/sessions"
	early := s.changes(project)
	before := s.create(sessions, "")
	if sent := early.session(early.next()); sent["id"] != before {
		t.Fatalf("session %v was sent, want %s", sent["id"], before)
	}

	late := s.changes(project)
	after := s.create(sessions, "")
	for _, stream := range []*reader{early, late} {
		if sent := stream.session(stream.next()); sent["id"] != after {
			t.Errorf("session %v was sent at %s, want %s", sent["id"], stream.path, after)
		}
	}

	s.stop()
	for _, stream := range []*reader{early, late} {
		if rest := stream.rest(soon); len(rest) != 0 {
			t.Errorf("events before the stream ended = %v, want none", rest)
		}
	}
}

// A member is sent the sessions that it may read: its own, or with
// sessions.view_all those of every member.
func TestSessionChangesAreSentToWhoMayReadThem(t *testing.T) {
	t.Parallel()
	installation := install(t)
	provider := tokens{"admin": {Subject: "admin", Groups: []string{"platform"}}}
	for _, caller := range []string{"user", "mate", "auditor", "viewer", "stranger"} {
		provider[caller] = identity.Caller{Subject: caller, Username: caller + "@example.com"}
	}
	installation.callers = api.Callers{Provider: provider, Admins: []string{"platform"}}
	s := installation.supervisor()
	s.token = "admin"
	s.hears()
	project := s.project()
	for caller, role := range map[string]string{"user": "user", "mate": "user", "auditor": "auditor", "viewer": "viewer"} {
		s.create("/v1/projects/"+project+"/members", `{"user": "`+caller+`@example.com", "role": "`+role+`"}`)
	}
	sessions := "/v1/agents/" + s.agent(project, `{"harness": "opencode"}`) + "/sessions"
	// as returns the Supervisor's client with the caller's token.
	as := func(caller string) *supervisor {
		with := *s
		with.token = caller
		return &with
	}
	streams := map[string]*reader{}
	for _, caller := range []string{"admin", "user", "mate", "auditor", "viewer"} {
		streams[caller] = as(caller).changes(project)
	}
	if status := s.answered("stranger", "GET", "/v1/projects/"+project+"/events", ""); status != http.StatusNotFound {
		t.Errorf("stream of changes for a caller who is no member: status %d, want 404", status)
	}

	// Each change is read by a caller who is sent every session before the
	// next is made, so the streams have the changes in this order.
	changed := map[string]string{}
	for _, by := range []string{"user", "mate", "user", "mate"} {
		session, found := changed[by]
		if !found {
			session = as(by).create(sessions, "")
			changed[by] = session
		} else if status := s.answered(by, "PATCH", "/v1/sessions/"+session, `{"title": "Renamed"}`); status != http.StatusOK {
			t.Fatalf("PATCH session as %s: status %d, want 200", by, status)
		}
		for _, caller := range []string{"admin", "auditor", by} {
			if sent := streams[caller].session(streams[caller].next()); sent["id"] != session {
				t.Errorf("session %v was sent to %s, want %s of %s", sent["id"], caller, session, by)
			}
		}
	}
	select {
	case sent := <-streams["viewer"].events:
		t.Errorf("event %+v was sent to a member who may read no session", sent)
	default:
	}
}
