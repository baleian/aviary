package driver

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"aviary/supervisor/internal/store"
)

func TestAgentResources(t *testing.T) {
	for _, none := range []string{``, `null`, `{}`, `{"requests": {}}`, `{"requests": {}, "limits": {}}`} {
		resources, err := AgentResources([]byte(none))
		if err != nil || resources != nil {
			t.Errorf("AgentResources(%s) is %s, %v, not nothing", none, resources, err)
		}
	}

	asked := `{"requests": {"cpu": "500m", "memory": "1Gi"}, "limits": {"memory": "2Gi"}}`
	kept := `{"limits":{"memory":"2Gi"},"requests":{"cpu":"500m","memory":"1Gi"}}`
	if resources, err := AgentResources([]byte(asked)); err != nil || string(resources) != kept {
		t.Errorf("AgentResources(%s) is %s, %v, not %s", asked, resources, err, kept)
	}

	for name, refused := range map[string]string{
		"not an object":             `"2Gi"`,
		"an unknown field":          `{"request": {"cpu": "1"}}`,
		"not a quantity":            `{"requests": {"cpu": "plenty"}}`,
		"another resource":          `{"limits": {"nvidia.com/gpu": "1"}}`,
		"a claim":                   `{"claims": [{"name": "gpu"}]}`,
		"below zero":                `{"limits": {"memory": "-1Gi"}}`,
		"a request above its limit": `{"requests": {"memory": "2Gi"}, "limits": {"memory": "1Gi"}}`,
	} {
		if resources, err := AgentResources([]byte(refused)); err == nil {
			t.Errorf("%s: AgentResources(%s) is %s, not an error", name, refused, resources)
		}
	}
}

func TestOutcome(t *testing.T) {
	condition := func(kind batchv1.JobConditionType, status corev1.ConditionStatus) batchv1.JobCondition {
		return batchv1.JobCondition{Type: kind, Status: status}
	}
	tests := []struct {
		name             string
		conditions       []batchv1.JobCondition
		succeeded, ended bool
	}{
		{name: "a job that runs"},
		{
			name:       "a job whose pod succeeded",
			conditions: []batchv1.JobCondition{condition(batchv1.JobComplete, corev1.ConditionTrue)},
			succeeded:  true, ended: true,
		},
		{
			name:       "a job whose pod failed",
			conditions: []batchv1.JobCondition{condition(batchv1.JobFailed, corev1.ConditionTrue)},
			ended:      true,
		},
		{
			// Kubernetes sets these as soon as it knows how the job will end,
			// while the pod may still be stopping.
			name: "a job whose pod has not stopped",
			conditions: []batchv1.JobCondition{
				condition(batchv1.JobFailureTarget, corev1.ConditionTrue),
				condition(batchv1.JobSuccessCriteriaMet, corev1.ConditionTrue),
				condition(batchv1.JobFailed, corev1.ConditionFalse),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := &batchv1.Job{Status: batchv1.JobStatus{Conditions: test.conditions}}
			if succeeded, ended := outcome(job); succeeded != test.succeeded || ended != test.ended {
				t.Errorf("succeeded is %t and ended is %t", succeeded, ended)
			}
		})
	}
}

func TestFailure(t *testing.T) {
	failed := batchv1.JobCondition{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}
	agent := func(terminated corev1.ContainerStateTerminated) *corev1.PodStatus {
		return &corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: agentContainer, State: corev1.ContainerState{Terminated: &terminated},
		}}}
	}
	tests := []struct {
		name      string
		condition batchv1.JobCondition
		// pod is the status of the turn's pod, if the pod is still there.
		pod     *corev1.PodStatus
		failure store.Failure
	}{
		{
			name: "the turn ran for too long",
			condition: batchv1.JobCondition{
				Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: batchv1.JobReasonDeadlineExceeded,
			},
			pod:     agent(corev1.ContainerStateTerminated{ExitCode: 143}),
			failure: store.Failure{Reason: store.FailureTimedOut, Message: "the turn ran for longer than 30m0s"},
		},
		{
			name:      "the pod was deleted",
			condition: failed,
			failure:   store.Failure{Reason: store.FailurePodLost, Message: "the pod of the turn was deleted"},
		},
		{
			name:      "the pod was evicted",
			condition: failed,
			pod:       &corev1.PodStatus{Reason: "Evicted", Message: "The node was low on resource: memory."},
			failure:   store.Failure{Reason: store.FailurePodLost, Message: "The node was low on resource: memory."},
		},
		{
			name:      "the runner left the reason",
			condition: failed,
			pod: agent(corev1.ContainerStateTerminated{
				ExitCode: 1, Message: `{"reason": "harness_error", "message": "the model is out of order"}`,
			}),
			failure: store.Failure{Reason: store.FailureHarness, Message: "the model is out of order"},
		},
		{
			name:      "the runner left a reason that is not its to give",
			condition: failed,
			pod:       agent(corev1.ContainerStateTerminated{ExitCode: 1, Message: `{"reason": "timed_out"}`}),
			failure:   store.Failure{Reason: store.FailureRunner, Message: "the runner ended with exit code 1"},
		},
		{
			name:      "the pod took more memory than it may",
			condition: failed,
			pod:       agent(corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}),
			failure:   store.Failure{Reason: store.FailureOutOfMemory, Message: "the pod used more memory than it may"},
		},
		{
			name:      "the runner ended when it was told to stop",
			condition: failed,
			pod:       agent(corev1.ContainerStateTerminated{ExitCode: 143}),
			failure:   store.Failure{Reason: store.FailurePodLost, Message: "the pod was stopped"},
		},
		{
			name:      "the runner was killed",
			condition: failed,
			pod:       agent(corev1.ContainerStateTerminated{Signal: 9}),
			failure:   store.Failure{Reason: store.FailurePodLost, Message: "the pod was stopped"},
		},
		{
			name:      "the runner failed and left no reason",
			condition: failed,
			pod:       agent(corev1.ContainerStateTerminated{ExitCode: 101}),
			failure:   store.Failure{Reason: store.FailureRunner, Message: "the runner ended with exit code 101"},
		},
		{
			name:      "the agent never started",
			condition: failed,
			pod: &corev1.PodStatus{InitContainerStatuses: []corev1.ContainerStatus{{
				Name: "home", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
			}}},
			failure: store.Failure{Reason: store.FailureRunner, Message: "the pod failed before the agent started"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			labels := map[string]string{turnLabel: uuid.NewString()}
			// The pod of another turn, which must not be taken for this one.
			objects := []runtime.Object{&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "another", Namespace: namespace, Labels: map[string]string{turnLabel: uuid.NewString()},
				},
				Status: *agent(corev1.ContainerStateTerminated{ExitCode: 1, Message: `{"reason": "resume_failed"}`}),
			}}
			if test.pod != nil {
				objects = append(objects, &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: namespace, Labels: labels},
					Status:     *test.pod,
				})
			}
			driver := &Driver{config: Config{MaxTurn: 30 * time.Minute}, client: fake.NewClientset(objects...)}
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "job", Namespace: namespace, Labels: labels},
				Status:     batchv1.JobStatus{Conditions: []batchv1.JobCondition{test.condition}},
			}
			if failure := driver.failure(context.Background(), job); failure != test.failure {
				t.Errorf("the failure is %+v, not %+v", failure, test.failure)
			}
		})
	}
}

func TestHoldup(t *testing.T) {
	waiting := func(reason, message string) []corev1.ContainerStatus {
		return []corev1.ContainerStatus{{State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: message},
		}}}
	}
	tests := []struct {
		name   string
		pod    *corev1.Pod
		holdup string
	}{
		{name: "no pod", holdup: "the pod was not created"},
		{
			name: "a pod that fits on no node",
			pod: &corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
				Type: corev1.PodScheduled, Status: corev1.ConditionFalse,
				Reason: "Unschedulable", Message: "0/3 nodes are available: 3 Insufficient cpu.",
			}}}},
			holdup: "Unschedulable: 0/3 nodes are available: 3 Insufficient cpu.",
		},
		{
			name: "an image of the harness that cannot be pulled",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: waiting("ErrImagePull", "registry.invalid/harness: not found"),
			}},
			holdup: "ErrImagePull: registry.invalid/harness: not found",
		},
		{
			name: "an image of the runner that cannot be pulled",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				InitContainerStatuses: waiting("ImagePullBackOff", "back-off pulling the image"),
				ContainerStatuses:     waiting("PodInitializing", ""),
			}},
			holdup: "ImagePullBackOff: back-off pulling the image",
		},
		{
			name: "a pod that Kubernetes has nothing to say about",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Conditions:        []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionTrue}},
				ContainerStatuses: waiting("ContainerCreating", ""),
			}},
			holdup: "the runner did not connect",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if holdup := holdup(test.pod); holdup != test.holdup {
				t.Errorf("the holdup is %q, not %q", holdup, test.holdup)
			}
		})
	}
}
