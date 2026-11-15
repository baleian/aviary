package main

import (
	"net/http"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"aviary/supervisor/internal/driver"
	runnerv1 "aviary/supervisor/internal/gen/aviary/runner/v1"
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
	job := aviary.job(completed.ID)
	runner := other.take(job)
	if running := total("aviary_turns_running"); running != 1 {
		t.Errorf("with a runner connected, the Supervisors report %v running turns", running)
	}
	thought := int64(3)
	runner.report(started("harness-session"))
	runner.report(finished("end_turn", &runnerv1.Usage{TotalTokens: 18, InputTokens: 11, OutputTokens: 7, ThoughtTokens: &thought}))
	runner.leave()
	aviary.end(job, batchv1.JobComplete, nil)
	if state := ended(one, completed.ID); state != "turn.completed" {
		t.Fatalf("the first turn ended with %s", state)
	}

	// A turn that runs, one that waits behind it until it expires, and two
	// messages that are refused.
	session := other.session()
	cancelled := other.send(session, "two")
	job = aviary.job(cancelled.ID)
	runner = one.take(job)
	runner.report(started("harness-session"))
	// A start that the runner reports again is not another start.
	if _, err := runner.send(1, started("harness-session")); err != nil {
		t.Fatal(err)
	}
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

	// The turn that runs is cancelled.
	if status, _ := other.request("POST", "/v1/turns/"+cancelled.ID+"/cancel", "", nil); status != http.StatusAccepted {
		t.Fatalf("cancelling the running turn was answered with %d", status)
	}
	runner.cancelled()
	runner.report(finished("cancelled", nil))
	runner.leave()
	aviary.end(job, batchv1.JobComplete, nil)
	if state := ended(one, cancelled.ID); state != "turn.cancelled" {
		t.Fatalf("the cancelled turn ended with %s", state)
	}

	// A turn that fails.
	failed := other.send(other.session(), "five")
	job = aviary.job(failed.ID)
	runner = one.take(job)
	runner.report(started("harness-session"))
	runner.call.hang()
	aviary.end(job, batchv1.JobFailed, stopped(corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}))
	if state := ended(other, failed.ID); state != "turn.failed" {
		t.Fatalf("the turn that took too much memory ended with %s", state)
	}

	expected := map[string]float64{
		// Four messages were accepted, one of them when it had to wait.
		"aviary_turn_queue_length_count":                       4,
		"aviary_turn_queue_length_sum":                         1,
		`aviary_messages_refused_total{reason="session_busy"}`: 1,
		`aviary_messages_refused_total{reason="queue_full"}`:   1,
		// Three turns were dispatched and started, and ran to an end.
		"aviary_turn_queue_duration_seconds_count":                                           3,
		"aviary_turn_startup_duration_seconds_count":                                         3,
		"aviary_turn_run_duration_seconds_count":                                             3,
		`aviary_turns_ended_total{harness="opencode",reason="",state="completed"}`:           1,
		`aviary_turns_ended_total{harness="opencode",reason="",state="cancelled"}`:           1,
		`aviary_turns_ended_total{harness="opencode",reason="",state="expired"}`:             1,
		`aviary_turns_ended_total{harness="opencode",reason="out_of_memory",state="failed"}`: 1,
		"aviary_turns_ended_total":                                                           4,
		`aviary_turn_tokens_total{harness="opencode",kind="input"}`:                          11,
		`aviary_turn_tokens_total{harness="opencode",kind="output"}`:                         7,
		`aviary_turn_tokens_total{harness="opencode",kind="thought"}`:                        3,
		"aviary_turn_tokens_total":                                                           21,
		"aviary_turns_running":                                                               0,
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
