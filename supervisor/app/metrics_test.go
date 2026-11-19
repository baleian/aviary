package app

import (
	"net/http"
	"testing"
	"time"

	"aviary/supervisor/internal/driver"
	"aviary/supervisor/store"
)

// Each Supervisor reports what it did itself, so the numbers of all of them
// add up to what happened.
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

	// A turn that completes, and reports its usage.
	completed := one.send(one.session(), "one")
	runner := aviary.given(completed.ID)
	if running := total("aviary_turns_running"); running != 1 {
		t.Errorf("with a turn that one of them follows, the Supervisors report %v running turns", running)
	}
	thought := int64(3)
	runner.report(started("harness-session"))
	// Its harness asks twice. One request is answered by a person, and the
	// runner makes up an outcome for the other.
	runner.report(asks("1", "rm -r build"))
	runner.report(asks("2", "git push"))
	if status, _ := one.approve(completed.ID, "1", `{"option_id": "never"}`); status != http.StatusAccepted {
		t.Fatalf("the answer to a request was answered with %d", status)
	}
	runner.answer()
	runner.report(answered("1", "selected", "never"))
	// An answer that the runner tells again is not another answer.
	runner.report(answered("1", "selected", "never"))
	runner.report(answered("2", "made up", ""))
	runner.report(finished("end_turn", &store.Usage{TotalTokens: 18, InputTokens: 11, OutputTokens: 7, ThoughtTokens: &thought}))
	if state := ended(one, completed.ID); state != "turn.completed" {
		t.Fatalf("the first turn ended with %s", state)
	}

	// A turn that runs, one that waits behind it until it expires, and two
	// messages that are refused.
	session := other.session()
	cancelled := other.send(session, "two")
	runner = aviary.given(cancelled.ID)
	runner.report(started("harness-session"))
	// A start that the runner tells again is not another start.
	runner.report(started("harness-session"))
	expired := one.send(one.session(), "three")
	if status, _ := one.request("POST", "/v1/sessions/"+session+"/messages", `{"text": "again"}`, nil); status != http.StatusConflict {
		t.Fatalf("a message to a session with a running turn was answered with %d", status)
	}
	if status, _ := other.request("POST", "/v1/sessions/"+other.session()+"/messages", `{"text": "four"}`, nil); status != http.StatusTooManyRequests {
		t.Fatalf("a message beyond the most that may wait was answered with %d", status)
	}
	if state := ended(other, expired.ID); state != "turn.expired" {
		t.Fatalf("the turn that waited ended with %s", state)
	}

	// The turn that runs asks, and is cancelled.
	runner.report(asks("1", "rm -r build"))
	if status, _ := other.request("POST", "/v1/turns/"+cancelled.ID+"/cancel", "", nil); status != http.StatusAccepted {
		t.Fatalf("cancelling the running turn was answered with %d", status)
	}
	runner.cancelled()
	runner.report(answered("1", "cancelled", ""))
	runner.report(finished("cancelled", nil))
	if state := ended(one, cancelled.ID); state != "turn.cancelled" {
		t.Fatalf("the cancelled turn ended with %s", state)
	}

	// A turn that fails.
	lost := other.send(other.session(), "five")
	runner = aviary.given(lost.ID)
	runner.report(started("harness-session"))
	runner.lose()
	if state := ended(other, lost.ID); state != "turn.failed" {
		t.Fatalf("the turn that lost its sandbox ended with %s", state)
	}

	expected := map[string]float64{
		// Three requests for approval have their answers.
		`aviary_approval_wait_seconds_count{harness="opencode",outcome="reject_once"}`: 1,
		`aviary_approval_wait_seconds_count{harness="opencode",outcome="cancelled"}`:   1,
		`aviary_approval_wait_seconds_count{harness="opencode",outcome="other"}`:       1,
		"aviary_approval_wait_seconds_count":                                           3,
		// Four messages were accepted, one of them when it had to wait.
		"aviary_turn_queue_length_count":                       4,
		"aviary_turn_queue_length_sum":                         1,
		`aviary_messages_refused_total{reason="session_busy"}`: 1,
		`aviary_messages_refused_total{reason="queue_full"}`:   1,
		// Three turns were dispatched and started, and ran to an end.
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
	// A Supervisor counts a turn a moment after the turn's last event.
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
		t.Errorf("the Supervisors report %v for %s, not %v", reported, series, expected[series])
	}
}
