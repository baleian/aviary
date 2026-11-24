package app

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

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

// A session says whether its sandbox is awake, asleep or lost, and nothing of
// a sandbox that it does not have.
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

	// The sandbox is deleted from outside, and the next turn gets a new one.
	runner.lose()
	eventually(t, patience, "the session does not say that its sandbox is lost", func() bool {
		return read()["sandbox"] == "lost"
	})
	third := s.send(session, "are you there")
	runner = aviary.given(third.ID)
	if state := read()["sandbox"]; state != "awake" {
		t.Errorf("sandbox of a session that got a new one = %v, want awake", state)
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

// A session whose sandbox has slept for long has its home archived in the
// depot, and says so: it has no sandbox then. Its next turn restores the home
// into a new sandbox, and the session says so meanwhile. The state metrics
// count both, and a client that watches the project is sent the session.
func TestSessionHomeIsArchivedAndRestored(t *testing.T) {
	t.Parallel()
	objects := runDepot(t, t.TempDir())
	aviary := install(t, func(config *driver.Config) {
		config.Depot = objects.Client
		config.Idle = 100 * time.Millisecond
		config.ArchiveAfter = 100 * time.Millisecond
		config.MaxRestore = time.Minute
		// Sandboxes are looked at only every few sweeps.
		config.Sweep = 20 * time.Millisecond
	})
	s := aviary.supervisor()
	s.hears()
	metrics := stateMetrics(t, aviary.records)
	counted := func(state string, sandboxes int) bool {
		t.Helper()
		status, lines := scrape(t, metrics)
		if status != http.StatusOK {
			t.Fatalf("scrape: status %d, want 200", status)
		}
		return slices.Contains(lines, fmt.Sprintf(`aviary_sandboxes{state=%q} %d`, state, sandboxes))
	}
	project := s.project()
	stream := s.changes(project)
	session := s.create("/v1/agents/"+s.agent(project, `{"harness": "opencode"}`)+"/sessions", "")
	path := "/v1/sessions/" + session
	state := func() any {
		t.Helper()
		var read map[string]any
		if status, _ := s.request("GET", path, "", &read); status != http.StatusOK {
			t.Fatalf("GET session: status %d, want 200", status)
		}
		return read["sandbox"]
	}
	if !counted("archived", 0) || !counted("restoring", 0) {
		t.Error("state metrics do not report 0 archived and restoring sandboxes from the start")
	}

	runner := aviary.given(s.send(session, "hello").ID)
	name := runner.Sandbox
	aviary.service.Home(name).Write("the files of the agent")
	slept := aviary.service.UID(name)
	runner.report(started("harness-session"))
	runner.report(finished("end_turn", nil))
	stream.sent("the session whose home is archived", func(sent map[string]any) bool {
		return sent["id"] == session && sent["sandbox"] == "archived"
	})
	if got := state(); got != "archived" {
		t.Errorf("sandbox of a session whose home is archived = %v, want archived", got)
	}
	eventually(t, soon, "the sandbox of the archived home still exists", func() bool {
		return aviary.service.UID(name) == ""
	})
	if !counted("archived", 1) || !counted("asleep", 0) {
		t.Error("state metrics do not count the archived home, or count its sandbox as asleep")
	}

	// The new sandbox does not run yet.
	aviary.service.Slow(true)
	next := s.send(session, "and then")
	eventually(t, soon, "the session does not say that its home is being restored", func() bool {
		return state() == "restoring"
	})
	if !counted("restoring", 1) || !counted("archived", 0) {
		t.Error("state metrics do not count the home that is being restored, or still count it as archived")
	}
	aviary.service.Slow(false)
	runner = aviary.given(next.ID)
	if content, unpacked := aviary.service.Home(name).Content(); content != "the files of the agent" || unpacked != 1 {
		t.Errorf("home of the new sandbox = %q, unpacked %d times; want the archived home, unpacked once", content, unpacked)
	}
	if restored := aviary.service.UID(name); restored == "" || restored == slept {
		t.Errorf("sandbox of the restored home = %q, want a new one in place of %s", restored, slept)
	}
	stream.sent("the session whose home is restored", func(sent map[string]any) bool {
		return sent["id"] == session && sent["sandbox"] == "awake"
	})
	if got := state(); got != "awake" {
		t.Errorf("sandbox of a session whose home is restored = %v, want awake", got)
	}
	if !counted("awake", 1) || !counted("restoring", 0) {
		t.Error("state metrics do not count the sandbox of the restored home as awake")
	}
	runner.report(started("harness-session"))
	runner.report(finished("end_turn", nil))
}

// A session that was not used for longer than its sandbox is kept ends when
// the sandbox is gone, and says why it ended.
func TestSessionExpires(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) {
		config.Retention = time.Hour
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
	first := s.send(session, "hello")
	runner := aviary.given(first.ID)
	runner.report(started("harness-session"))
	runner.report(finished("end_turn", nil))

	// Two hours later, the sandbox has expired.
	aviary.records.Age(2*time.Hour, uuid.MustParse(first.ID))
	runner.lose()
	eventually(t, patience, "the session whose sandbox expired has not ended", func() bool {
		ended := read()
		_, has := ended["sandbox"]
		return ended["ended_at"] != nil && !has
	})
	if reason := read()["end_reason"]; reason != "expired" {
		t.Errorf("end_reason of the expired session = %v, want expired", reason)
	}
	var refusal struct{ Code string }
	if status, _ := s.request("POST", path+"/messages", `{"text": "again"}`, &refusal); status != http.StatusConflict || refusal.Code != "session_ended" {
		t.Errorf("send to an expired session = %d %s, want 409 session_ended", status, refusal.Code)
	}

	// A session that a caller ends says nothing of why.
	var ended map[string]any
	if status, _ := s.request("DELETE", "/v1/sessions/"+s.session(), "", &ended); status != http.StatusOK || ended["ended_at"] == nil {
		t.Fatalf("end session = %d %v", status, ended)
	}
	if reason, has := ended["end_reason"]; has {
		t.Errorf("end_reason of a session that a caller ended = %v, want none", reason)
	}
}

// A session carries its newest turn, without the text and the items of the
// turn, wherever the API returns the session.
func TestSessionLastTurn(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	agent := s.agent(s.project(), `{"harness": "opencode", "approval": "ask"}`)
	session := s.create("/v1/agents/"+agent+"/sessions", "")
	path := "/v1/sessions/" + session
	// last returns the newest turn that a response of the API has for the
	// session.
	last := func(method, path, body string) map[string]any {
		t.Helper()
		var read map[string]any
		if status, _ := s.request(method, path, body, &read); status != http.StatusOK {
			t.Fatalf("%s %s: status %d, want 200", method, path, status)
		}
		if listed, list := read["sessions"].([]any); list {
			if len(listed) != 1 {
				t.Fatalf("%s %s lists %d sessions, want one", method, path, len(listed))
			}
			read = listed[0].(map[string]any)
		}
		turn, _ := read["last_turn"].(map[string]any)
		return turn
	}
	if turn := last("GET", path, ""); turn != nil {
		t.Errorf("last_turn of a session without turns = %v, want none", turn)
	}

	first := s.send(session, "hello")
	runner := aviary.given(first.ID)
	runner.report(started("harness-session"))
	runner.report(said("hi"))
	turn := last("GET", path, "")
	if turn["id"] != first.ID || turn["seq"] != float64(1) || turn["state"] != "running" || turn["started_at"] == nil {
		t.Errorf("last_turn of a session with a running turn = %v", turn)
	}
	for _, left := range []string{"text", "items", "approvals"} {
		if value, has := turn[left]; has {
			t.Errorf("last_turn has %s = %v, want it left out", left, value)
		}
	}

	// A request that waits for an answer is listed with the turn.
	runner.report(asks("1", "ls"))
	if waiting, _ := last("GET", path, "")["approvals"].([]any); len(waiting) != 1 || waiting[0].(map[string]any)["id"] != "1" {
		t.Errorf("approvals of the last turn = %v, want the request that waits", waiting)
	}
	if listed := last("GET", "/v1/agents/"+agent+"/sessions", ""); listed["id"] != first.ID || len(listed["approvals"].([]any)) != 1 {
		t.Errorf("last_turn in the list of sessions = %v", listed)
	}
	runner.report(answered("1", "cancelled", ""))
	runner.report(finished("end_turn", nil))

	second := s.send(session, "and then")
	runner = aviary.given(second.ID)
	runner.report(started("harness-session"))
	runner.report(failed("harness_error", "the model is out of order"))
	turn = last("PATCH", path, `{"title": "Renamed"}`)
	failure, _ := turn["failure"].(map[string]any)
	if turn["id"] != second.ID || turn["seq"] != float64(2) || turn["state"] != "failed" || failure["reason"] != "harness_error" || turn["approvals"] != nil {
		t.Errorf("last_turn of a session whose second turn failed = %v", turn)
	}
	if ended := last("DELETE", path, ""); ended["id"] != second.ID {
		t.Errorf("last_turn of the session as it ended = %v", ended)
	}
}
