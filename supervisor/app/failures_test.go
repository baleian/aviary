package app

import (
	"encoding/json"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	runnerv1 "aviary/supervisor/internal/gen/aviary/runner/v1"
)

// A turn that fails says why: from what Kubernetes says of its Job and its
// pod, and from what its runner left with the pod.
func TestFailures(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	failed := batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}
	tool := &runnerv1.RunTurnRequest{Message: &runnerv1.RunTurnRequest_ToolCall{
		ToolCall: &runnerv1.ToolCall{Id: "t1", Title: "sleep 30", Status: "in_progress"},
	}}
	for _, test := range []struct {
		name string
		// reports are what the runner reports after the start of the turn.
		reports   []*runnerv1.RunTurnRequest
		condition batchv1.JobCondition
		// pod is the state of the pod, if the pod is still there.
		pod             *corev1.PodStatus
		reason, message string
		stopReason      string
		items           int
	}{
		{
			// A runner that is told to stop has the harness end the turn.
			name:      "the pod was deleted while a tool ran",
			reports:   []*runnerv1.RunTurnRequest{tool, finished("cancelled", nil)},
			condition: failed,
			reason:    "pod_lost", message: "the pod of the turn was deleted",
			stopReason: "cancelled", items: 1,
		},
		{
			name:      "the model endpoint refused the harness",
			condition: failed,
			pod: stopped(corev1.ContainerStateTerminated{
				ExitCode: 1, Message: `{"reason": "harness_error", "message": "the model is out of order"}`,
			}),
			reason: "harness_error", message: "the model is out of order",
		},
		{
			name:      "the pod took more memory than it may",
			reports:   []*runnerv1.RunTurnRequest{tool},
			condition: failed,
			pod:       stopped(corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}),
			reason:    "out_of_memory", message: "the pod used more memory than it may",
			items: 1,
		},
		{
			name:    "the turn ran for longer than a turn may",
			reports: []*runnerv1.RunTurnRequest{tool, finished("cancelled", nil)},
			condition: batchv1.JobCondition{
				Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: batchv1.JobReasonDeadlineExceeded,
			},
			pod:    stopped(corev1.ContainerStateTerminated{ExitCode: 143}),
			reason: "timed_out", message: "the turn ran for longer than 30m0s",
			stopReason: "cancelled", items: 1,
		},
		{
			name:      "the runner lost its Supervisor for too long",
			condition: failed,
			pod: stopped(corev1.ContainerStateTerminated{
				ExitCode: 1, Message: `{"reason": "supervisor_lost", "message": "no Supervisor for 60 seconds"}`,
			}),
			reason: "supervisor_lost", message: "no Supervisor for 60 seconds",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := s.session()
			accepted := s.send(session, "hello")
			watching := s.watch(accepted.ID)
			job := aviary.job(accepted.ID)
			runner := s.take(job)
			runner.report(started("harness-session"))
			for _, report := range test.reports {
				runner.report(report)
			}
			runner.call.hang()
			aviary.endAs(job, test.condition, test.pod)

			events := watching()
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
				record.StopReason != test.stopReason || len(record.Items) != test.items {
				t.Errorf("the record of the turn is %+v", record)
			}

			// The session goes on.
			next := s.send(session, "again")
			s.run(aviary.job(next.ID), "done")
			if events := s.follow(next.ID); events[len(events)-1].Type != "turn.completed" {
				t.Errorf("the next turn of the session ended with %v", events[len(events)-1])
			}
		})
	}
}
