package app

import (
	"encoding/json"
	"testing"
	"time"

	"aviary/supervisor/internal/driver"
)

// A turn that fails says why: with what its runner tells of the failure, or
// with what the Supervisor that follows it finds.
func TestFailures(t *testing.T) {
	t.Parallel()
	tool := message{"tool.call", map[string]string{"id": "t1", "title": "sleep 30", "status": "in_progress"}}
	for _, test := range []struct {
		name string
		// limits sets how long a turn may take lower, for a turn that is to
		// take too long.
		limits func(*driver.Config)
		// reports are what the runner tells of the turn.
		reports []message
		// lost is whether the turn then loses its sandbox.
		lost            bool
		reason, message string
		items           int
	}{
		{
			name:    "the model endpoint refused the harness",
			reports: []message{started("harness-session"), failed("harness_error", "the model is out of order")},
			reason:  "harness_error", message: "the model is out of order",
		},
		{
			name:    "the harness could not continue its session",
			reports: []message{failed("resume_failed", "the harness could not continue its session: it has none")},
			reason:  "resume_failed", message: "the harness could not continue its session: it has none",
		},
		{
			name:    "the runner lost its harness while a tool ran",
			reports: []message{started("harness-session"), tool, failed("runner_error", "the harness is lost")},
			reason:  "runner_error", message: "the harness is lost",
			items: 1,
		},
		{
			// Only a Supervisor tells that a turn took too long.
			name:    "the runner told a reason that is not a runner's to tell",
			reports: []message{started("harness-session"), failed("timed_out", "the turn took too long")},
			reason:  "runner_error", message: "the turn took too long",
		},
		{
			name:    "the sandbox was lost while a tool ran",
			reports: []message{started("harness-session"), tool},
			lost:    true,
			reason:  "sandbox_lost", message: "the sandbox of the session is gone",
			items: 1,
		},
		{
			name:   "the harness did not take the message in time",
			limits: func(config *driver.Config) { config.MaxStartup = time.Second },
			reason: "startup_timed_out", message: "the harness did not start the turn",
		},
		{
			name:    "the turn ran for longer than a turn may",
			limits:  func(config *driver.Config) { config.MaxTurn = time.Second },
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
				t.Errorf("the turn ended with %v, not as failed for %s: %s", last, test.reason, test.message)
			}
			record := s.turn(accepted.ID)
			if record.State != "failed" || record.Failure["reason"] != test.reason || record.Failure["message"] != test.message ||
				record.StopReason != "" || len(record.Items) != test.items {
				t.Errorf("the record of the turn is %+v", record)
			}

			// The session goes on.
			next := s.send(session, "again")
			s.run(next.ID, "done")
			if events := s.follow(next.ID); events[len(events)-1].Type != "turn.completed" {
				t.Errorf("the next turn of the session ended with %v", events[len(events)-1])
			}
		})
	}
}
