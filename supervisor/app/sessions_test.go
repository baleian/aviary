package app

import (
	"net/http"
	"testing"
	"time"

	"aviary/supervisor/internal/driver"
)

// Ending a session deletes its sandbox and keeps its turns, and the session
// takes no more messages.
func TestSessionEnds(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session := s.session()
	path := "/v1/sessions/" + session
	type ended struct {
		ID      string     `json:"id"`
		EndedAt *time.Time `json:"ended_at"`
	}

	first := s.send(session, "hello")
	runner := aviary.given(first.ID)
	// A session with a running turn cannot be ended.
	var refusal struct{ Code string }
	if status, _ := s.request("DELETE", path, "", &refusal); status != http.StatusConflict || refusal.Code != "session_busy" {
		t.Errorf("end busy session = %d %s, want 409 session_busy", status, refusal.Code)
	}
	runner.report(started("harness-session"))
	runner.report(said("hi"))
	runner.report(finished("end_turn", nil))
	if sandboxes := aviary.service.Made(); len(sandboxes) != 1 {
		t.Fatalf("sandboxes before ending = %v, want one", sandboxes)
	}

	var over ended
	if status, _ := s.request("DELETE", path, "", &over); status != http.StatusOK || over.ID != session || over.EndedAt == nil {
		t.Fatalf("end session = %d %+v", status, over)
	}
	if sandboxes := aviary.service.Made(); len(sandboxes) != 0 {
		t.Errorf("sandboxes after ending = %v, want none", sandboxes)
	}
	// Ending is idempotent.
	var again ended
	if status, _ := s.request("DELETE", path, "", &again); status != http.StatusOK || again.EndedAt == nil || !again.EndedAt.Equal(*over.EndedAt) {
		t.Errorf("end session again = %d %+v, want 200 with the same ended_at", status, again)
	}

	if status, _ := s.request("POST", path+"/messages", `{"text": "again"}`, &refusal); status != http.StatusConflict || refusal.Code != "session_ended" {
		t.Errorf("send to an ended session = %d %s, want 409 session_ended", status, refusal.Code)
	}
	var read ended
	if status, _ := s.request("GET", path, "", &read); status != http.StatusOK || read.EndedAt == nil {
		t.Errorf("GET ended session = %d %+v", status, read)
	}
	var list struct{ Turns []turn }
	if status, _ := s.request("GET", path+"/turns", "", &list); status != http.StatusOK || len(list.Turns) != 1 || len(list.Turns[0].Items) != 1 {
		t.Errorf("turns of the ended session = %d %+v", status, list.Turns)
	}

	// A session without a sandbox can be ended too.
	fresh := s.session()
	if status, _ := s.request("DELETE", "/v1/sessions/"+fresh, "", &over); status != http.StatusOK || over.EndedAt == nil {
		t.Errorf("end session without turns = %d %+v", status, over)
	}
	if status, _ := s.request("DELETE", "/v1/sessions/00000000-0000-0000-0000-000000000000", "", nil); status != http.StatusNotFound {
		t.Errorf("end unknown session: status %d, want 404", status)
	}
}

// A session says whether its sandbox is awake or asleep, and nothing of a
// sandbox that it does not have.
func TestSessionSandbox(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) {
		config.Idle = 300 * time.Millisecond
		// Sandboxes are looked at only every few sweeps.
		config.Sweep = 20 * time.Millisecond
	})
	s := aviary.supervisor()
	session := s.session()
	path := "/v1/sessions/" + session
	read := func() map[string]any {
		t.Helper()
		var read map[string]any
		if status, _ := s.request("GET", path, "", &read); status != http.StatusOK {
			t.Fatalf("GET session: status %d, want 200", status)
		}
		return read
	}
	if state, has := read()["sandbox"]; has {
		t.Errorf("sandbox of a session without turns = %v, want none", state)
	}

	first := s.send(session, "hello")
	runner := aviary.given(first.ID)
	if state := read()["sandbox"]; state != "awake" {
		t.Errorf("sandbox of a session with a running turn = %v, want awake", state)
	}
	runner.report(started("harness-session"))
	runner.report(finished("end_turn", nil))
	eventually(t, patience, "the idle session does not say that it is asleep", func() bool {
		return read()["sandbox"] == "asleep"
	})
	// The list of sessions says the same.
	var list struct{ Sessions []map[string]any }
	if status, _ := s.request("GET", "/v1/agents/"+read()["agent_id"].(string)+"/sessions", "", &list); status != http.StatusOK ||
		len(list.Sessions) != 1 || list.Sessions[0]["sandbox"] != "asleep" {
		t.Errorf("list sessions = %d %v, want one that is asleep", status, list.Sessions)
	}

	second := s.send(session, "and then")
	runner = aviary.given(second.ID)
	if state := read()["sandbox"]; state != "awake" {
		t.Errorf("sandbox of a woken session = %v, want awake", state)
	}
	runner.report(started("harness-session"))
	runner.report(finished("end_turn", nil))

	var ended map[string]any
	if status, _ := s.request("DELETE", path, "", &ended); status != http.StatusOK || ended["ended_at"] == nil {
		t.Fatalf("end session = %d %v", status, ended)
	}
	if state, has := ended["sandbox"]; has {
		t.Errorf("sandbox of the session as it ended = %v, want none", state)
	}
	if state, has := read()["sandbox"]; has {
		t.Errorf("sandbox of an ended session = %v, want none", state)
	}
}
