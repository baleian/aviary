package app

import (
	"encoding/json"
	"testing"
	"time"

	"aviary/supervisor/internal/driver"
)

// A failed turn reports a reason and a message, from its runner or from the
// Supervisor that follows it.
func TestFailures(t *testing.T) {
	t.Parallel()
	tool := message{"tool.call", map[string]string{"id": "t1", "title": "sleep 30", "status": "in_progress"}}
	for _, test := range []struct {
		name string
		// limits shortens a timeout for the cases that exceed it. It shortens
		// the sweep too, which paces how fast the next turn wakes the sandbox.
		limits  func(*driver.Config)
		reports []message
		// lost has the sandbox deleted after the reports.
		lost            bool
		reason, message string
		items           int
	}{
		{
			name:    "harness error",
			reports: []message{started("harness-session"), failed("harness_error", "the model is out of order")},
			reason:  "harness_error", message: "the model is out of order",
		},
		{
			name:    "resume failed",
			reports: []message{failed("resume_failed", "resume harness session: it has none")},
			reason:  "resume_failed", message: "resume harness session: it has none",
		},
		{
			name:    "runner error during a tool call",
			reports: []message{started("harness-session"), tool, failed("runner_error", "the harness is lost")},
			reason:  "runner_error", message: "the harness is lost",
			items: 1,
		},
		{
			// timed_out is reserved for the Supervisor.
			name:    "reserved reason from the runner",
			reports: []message{started("harness-session"), failed("timed_out", "the turn took too long")},
			reason:  "runner_error", message: "the turn took too long",
		},
		{
			name:    "sandbox lost during a tool call",
			reports: []message{started("harness-session"), tool},
			lost:    true,
			reason:  "sandbox_lost", message: "the session's sandbox is gone",
			items: 1,
		},
		{
			name:   "startup timeout",
			limits: func(config *driver.Config) { config.MaxStartup, config.Sweep = time.Second, 100*time.Millisecond },
			reason: "startup_timed_out", message: "the harness did not start the turn",
		},
		{
			name:    "turn timeout",
			limits:  func(config *driver.Config) { config.MaxTurn, config.Sweep = time.Second, 100*time.Millisecond },
			reports: []message{started("harness-session"), tool},
			reason:  "timed_out", message: "the turn ran for longer than 1s",
			items: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var limits []func(*driver.Config)
			if test.limits != nil {
				limits = append(limits, test.limits)
			}
			aviary := install(t, limits...)
			s := aviary.supervisor()
			session := s.session()
			accepted := s.send(session, "hello")
			runner := aviary.given(accepted.ID)
			for _, report := range test.reports {
				runner.report(report)
			}
			if test.lost {
				runner.lose()
			}

			events := s.follow(accepted.ID)
			last := events[len(events)-1]
			var failure map[string]string
			if err := json.Unmarshal([]byte(last.Data), &failure); err != nil {
				t.Fatal(err)
			}
			if last.Type != "turn.failed" || failure["reason"] != test.reason || failure["message"] != test.message {
				t.Errorf("last event = %v, want turn.failed with %s: %s", last, test.reason, test.message)
			}
			record := s.turn(accepted.ID)
			if record.State != "failed" || record.Failure["reason"] != test.reason || record.Failure["message"] != test.message ||
				record.StopReason != "" || len(record.Items) != test.items {
				t.Errorf("unexpected turn record %+v", record)
			}

			// The session still takes messages.
			next := s.send(session, "again")
			s.run(next.ID, "done")
			if events := s.follow(next.ID); events[len(events)-1].Type != "turn.completed" {
				t.Errorf("next turn ended with %v, want turn.completed", events[len(events)-1])
			}
		})
	}
}
