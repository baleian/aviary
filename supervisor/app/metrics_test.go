package app

import (
	"net/http"
	"testing"
	"time"

	"aviary/supervisor/internal/driver"
	"aviary/supervisor/store"
)

// Each Supervisor counts only its own work, so that the sums over all
// Supervisors match what happened.
func TestMetrics(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) {
		config.MaxConcurrentTurns = 1
		config.MaxQueuedTurns = 1
		config.MaxQueueWait = time.Second
	})
	one, other := aviary.supervisor(), aviary.supervisor()
	total := func(series string) float64 { return one.metric(series) + other.metric(series) }
	ended := func(s *supervisor, turn string) string {
		t.Helper()
		events := s.follow(turn)
		return events[len(events)-1].Type
	}

	completed := one.send(one.session(), "one")
	runner := aviary.given(completed.ID)
	if running := total("aviary_turns_running"); running != 1 {
		t.Errorf("aviary_turns_running = %v, want 1", running)
	}
	thought := int64(3)
	runner.report(started("harness-session"))
	// A user answers one request; the other resolves with an outcome that
	// the Supervisor does not know.
	runner.report(asks("1", "rm -r build"))
	runner.report(asks("2", "git push"))
	if status, _ := one.approve(completed.ID, "1", `{"option_id": "never"}`); status != http.StatusAccepted {
		t.Fatalf("approve: status %d, want 202", status)
	}
	runner.answer()
	runner.report(answered("1", "selected", "never"))
	// A repeated resolution is counted once.
	runner.report(answered("1", "selected", "never"))
	runner.report(answered("2", "made up", ""))
	runner.report(finished("end_turn", &store.Usage{TotalTokens: 18, InputTokens: 11, OutputTokens: 7, ThoughtTokens: &thought}))
	if state := ended(one, completed.ID); state != "turn.completed" {
		t.Fatalf("first turn ended with %s, want turn.completed", state)
	}

	// A running turn, a queued turn that expires, and two refused messages.
	session := other.session()
	cancelled := other.send(session, "two")
	runner = aviary.given(cancelled.ID)
	runner.report(started("harness-session"))
	// A repeated start is counted once.
	runner.report(started("harness-session"))
	expired := one.send(one.session(), "three")
	if status, _ := one.request("POST", "/v1/sessions/"+session+"/messages", `{"text": "again"}`, nil); status != http.StatusConflict {
		t.Fatalf("send to a busy session: status %d, want 409", status)
	}
	if status, _ := other.request("POST", "/v1/sessions/"+other.session()+"/messages", `{"text": "four"}`, nil); status != http.StatusTooManyRequests {
		t.Fatalf("send to a full queue: status %d, want 429", status)
	}
	if state := ended(other, expired.ID); state != "turn.expired" {
		t.Fatalf("queued turn ended with %s, want turn.expired", state)
	}

	runner.report(asks("1", "rm -r build"))
	if status, _ := other.request("POST", "/v1/turns/"+cancelled.ID+"/cancel", "", nil); status != http.StatusAccepted {
		t.Fatalf("cancel: status %d, want 202", status)
	}
	runner.cancelled()
	runner.report(answered("1", "cancelled", ""))
	runner.report(finished("cancelled", nil))
	if state := ended(one, cancelled.ID); state != "turn.cancelled" {
		t.Fatalf("cancelled turn ended with %s, want turn.cancelled", state)
	}

	lost := other.send(other.session(), "five")
	runner = aviary.given(lost.ID)
	runner.report(started("harness-session"))
	runner.lose()
	if state := ended(other, lost.ID); state != "turn.failed" {
		t.Fatalf("turn without a sandbox ended with %s, want turn.failed", state)
	}

	expected := map[string]float64{
		// Three approval requests were resolved.
		`aviary_approval_wait_seconds_count{harness="opencode",outcome="reject_once"}`: 1,
		`aviary_approval_wait_seconds_count{harness="opencode",outcome="cancelled"}`:   1,
		`aviary_approval_wait_seconds_count{harness="opencode",outcome="other"}`:       1,
		"aviary_approval_wait_seconds_count":                                           3,
		// Four messages were accepted, one of which had to wait.
		"aviary_turn_queue_length_count":                       4,
		"aviary_turn_queue_length_sum":                         1,
		`aviary_messages_refused_total{reason="session_busy"}`: 1,
		`aviary_messages_refused_total{reason="queue_full"}`:   1,
		// Three turns were dispatched, started and ended.
		"aviary_turn_queue_duration_seconds_count":                                          3,
		"aviary_turn_startup_duration_seconds_count":                                        3,
		"aviary_turn_run_duration_seconds_count":                                            3,
		`aviary_turns_ended_total{harness="opencode",reason="",state="completed"}`:          1,
		`aviary_turns_ended_total{harness="opencode",reason="",state="cancelled"}`:          1,
		`aviary_turns_ended_total{harness="opencode",reason="",state="expired"}`:            1,
		`aviary_turns_ended_total{harness="opencode",reason="sandbox_lost",state="failed"}`: 1,
		"aviary_turns_ended_total":                                                          4,
		`aviary_turn_tokens_total{harness="opencode",kind="input"}`:                         11,
		`aviary_turn_tokens_total{harness="opencode",kind="output"}`:                        7,
		`aviary_turn_tokens_total{harness="opencode",kind="thought"}`:                       3,
		"aviary_turn_tokens_total":                                                          21,
		"aviary_turns_running":                                                              0,
	}
	// A turn is counted shortly after its last event.
	differing := func() map[string]float64 {
		differing := map[string]float64{}
		for series, value := range expected {
			if reported := total(series); reported != value {
				differing[series] = reported
			}
		}
		return differing
	}
	for deadline := time.Now().Add(soon); len(differing()) > 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	for series, reported := range differing() {
		t.Errorf("got %v for %s, want %v", reported, series, expected[series])
	}
}
