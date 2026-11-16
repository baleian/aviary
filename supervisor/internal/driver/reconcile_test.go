package driver

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"aviary/supervisor/internal/events"
	"aviary/supervisor/internal/metrics"
	"aviary/supervisor/internal/store"
	"aviary/supervisor/internal/testenv"
)

// The tests of this file run a driver on a database of its own and a cluster
// in which nothing runs. They call what the driver does when it is woken and
// when its time to reconcile comes, and they make the time pass in the
// database.

var ctx = context.Background()

type installation struct {
	t        *testing.T
	database string
	store    *store.Store
	events   *events.Streams
	cluster  *fake.Clientset
	driver   *Driver
}

func install(t *testing.T, changes ...func(*Config)) *installation {
	t.Helper()
	config := config()
	// Addresses, which the driver does not look up.
	config.GatewayURL, config.LLMBaseURL, config.ProxyURL = "http://127.0.0.1:9090", "http://127.0.0.2:8080/v1", "http://127.0.0.2:3128"
	config.MaxConcurrentTurns = 50
	config.MaxStartup = 90 * time.Second
	for _, change := range changes {
		change(&config)
	}
	database := testenv.Database(t)
	i := &installation{
		t: t, database: database, store: testenv.Open(t, database), events: testenv.Events(t), cluster: fake.NewClientset(),
	}
	driver, err := New(config, i.cluster, i.store, i.events, metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	i.driver = driver
	return i
}

// queue accepts a message of a new session as a turn.
func (i *installation) queue() store.Turn {
	i.t.Helper()
	project, err := i.store.CreateProject(ctx, "test")
	if err != nil {
		i.t.Fatal(err)
	}
	agent, err := i.store.CreateAgent(ctx, project.ID, store.Agent{Harness: "opencode"})
	if err != nil {
		i.t.Fatal(err)
	}
	session, err := i.store.CreateSession(ctx, agent.ID)
	if err != nil {
		i.t.Fatal(err)
	}
	turn, err := i.driver.Accept(ctx, session.ID, "hello")
	if err != nil {
		i.t.Fatal(err)
	}
	return turn
}

// dispatched returns a turn that the driver has dispatched: it waits for
// its runner.
func (i *installation) dispatched() store.Turn {
	i.t.Helper()
	turn := i.queue()
	i.driver.dispatch(ctx)
	if i.job(turn.ID) == nil {
		i.t.Fatal("the driver launched no Job for the turn")
	}
	return turn
}

// running returns a turn that its runner has taken.
func (i *installation) running() store.Turn {
	i.t.Helper()
	turn := i.dispatched()
	var token string
	for _, variable := range i.job(turn.ID).Spec.Template.Spec.Containers[0].Env {
		if variable.Name == "AVIARY_TURN_TOKEN" {
			token = variable.Value
		}
	}
	runner := uuid.New()
	if _, err := i.store.TakeTurn(ctx, token, &runner); err != nil {
		i.t.Fatal(err)
	}
	return turn
}

// job returns the Job of the turn, or nothing if the cluster has none.
func (i *installation) job(turn uuid.UUID) *batchv1.Job {
	i.t.Helper()
	job, err := i.cluster.BatchV1().Jobs(namespace).Get(ctx, jobName(turn), metav1.GetOptions{})
	if err != nil {
		return nil
	}
	return job
}

// pod gives the turn a pod in the state.
func (i *installation) pod(turn uuid.UUID, status corev1.PodStatus) {
	i.t.Helper()
	_, err := i.cluster.CoreV1().Pods(namespace).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: jobName(turn), Namespace: namespace, Labels: map[string]string{turnLabel: turn.String()}},
		Status:     status,
	}, metav1.CreateOptions{})
	if err != nil {
		i.t.Fatal(err)
	}
}

// lose takes what the cluster holds of the turn out of it: the Job and the
// pod, as far as they are there.
func (i *installation) lose(turn uuid.UUID) {
	i.cluster.BatchV1().Jobs(namespace).Delete(ctx, jobName(turn), metav1.DeleteOptions{})
	i.cluster.CoreV1().Pods(namespace).Delete(ctx, jobName(turn), metav1.DeleteOptions{})
}

// ago moves a time of the turn so far into the past: when it was dispatched,
// or when it was asked to stop.
func (i *installation) ago(turn uuid.UUID, column string, ago time.Duration) {
	i.t.Helper()
	testenv.Exec(i.t, i.database, "update turns set "+column+" = now() - make_interval(secs => $2) where id = $1", turn, ago.Seconds())
}

func (i *installation) turn(id uuid.UUID) store.Turn {
	i.t.Helper()
	turn, err := i.store.Turn(ctx, id)
	if err != nil {
		i.t.Fatal(err)
	}
	return turn
}

// last returns the type of the last event of the turn, or nothing.
func (i *installation) last(turn uuid.UUID) string {
	i.t.Helper()
	var last string
	after := events.Start
	for {
		batch, err := i.events.Read(ctx, turn, after, time.Millisecond)
		if err != nil {
			i.t.Fatal(err)
		}
		if len(batch) == 0 {
			return last
		}
		last, after = batch[len(batch)-1].Type, batch[len(batch)-1].ID
	}
}

// A turn that has lost its Job would keep its session busy and count against
// the limit for good.
func TestReconcileEndsTurnsWithoutAJob(t *testing.T) {
	t.Parallel()
	i := install(t)
	lost, late, stopping, kept := i.dispatched(), i.dispatched(), i.running(), i.running()
	// The Job of the first was deleted two minutes ago. That of the second
	// has not been seen yet: the turn was dispatched just now. The third
	// still has its pod, which is on its way out.
	i.lose(lost.ID)
	i.lose(late.ID)
	i.lose(stopping.ID)
	i.pod(stopping.ID, corev1.PodStatus{Phase: corev1.PodRunning})
	for _, turn := range []store.Turn{lost, stopping, kept} {
		i.ago(turn.ID, "dispatched_at", 2*time.Minute)
	}

	i.driver.reconcile(ctx)
	ended := i.turn(lost.ID)
	if ended.State != store.TurnFailed || ended.Failure == nil || ended.Failure.Reason != store.FailurePodLost {
		t.Errorf("the turn whose Job is gone is %s, for %+v", ended.State, ended.Failure)
	}
	if last := i.last(lost.ID); last != events.TurnFailed {
		t.Errorf("the last event of the turn whose Job is gone is %q", last)
	}
	if _, _, err := i.store.CreateTurn(ctx, lost.SessionID, "again", 0, 50); err != nil {
		t.Errorf("the session of the turn whose Job is gone takes no message: %v", err)
	}
	for name, turn := range map[string]store.Turn{
		"was dispatched just now": late, "still has its pod": stopping, "has its Job": kept,
	} {
		if left := i.turn(turn.ID); left.EndedAt != nil {
			t.Errorf("the turn that %s ended as %s", name, left.State)
		}
	}
	if i.job(kept.ID) == nil {
		t.Error("the Job of a turn that runs was deleted")
	}
}

// A pod that cannot start, because no node has room for it or because an
// image cannot be pulled, would hold its turn until the turn's time is up.
func TestReconcileGivesUpTurnsWhosePodDoesNotStart(t *testing.T) {
	t.Parallel()
	i := install(t, func(config *Config) { config.MaxStartup = 10 * time.Second })
	stuck, fresh, taken := i.dispatched(), i.dispatched(), i.running()
	i.pod(stuck.ID, corev1.PodStatus{Conditions: []corev1.PodCondition{{
		Type: corev1.PodScheduled, Status: corev1.ConditionFalse,
		Reason: "Unschedulable", Message: "0/3 nodes are available: 3 Insufficient cpu.",
	}}})
	i.ago(stuck.ID, "dispatched_at", 20*time.Second)
	i.ago(taken.ID, "dispatched_at", 20*time.Second)

	i.driver.reconcile(ctx)
	// The turn is given up and its pod is stopped. It ends when the pod is
	// gone, and until then its runner is not given it.
	if i.job(stuck.ID) != nil {
		t.Error("the Job of the turn whose pod did not start is still there")
	}
	if given := i.turn(stuck.ID); given.EndedAt != nil || given.Failure == nil || given.Failure.Reason != store.FailureStartup {
		t.Errorf("the turn whose pod did not start is %+v", given)
	}
	if i.job(fresh.ID) == nil || i.job(taken.ID) == nil {
		t.Error("the Job of a turn whose pod has time to start, or has started, was deleted")
	}
	if left := i.turn(taken.ID); left.Failure != nil {
		t.Errorf("the turn whose runner has taken it was given up for %+v", left.Failure)
	}

	i.lose(stuck.ID)
	i.ago(stuck.ID, "dispatched_at", 2*time.Minute)
	i.driver.reconcile(ctx)
	ended := i.turn(stuck.ID)
	holdup := store.Failure{Reason: store.FailureStartup, Message: "Unschedulable: 0/3 nodes are available: 3 Insufficient cpu."}
	if ended.State != store.TurnFailed || ended.Failure == nil || *ended.Failure != holdup {
		t.Errorf("the turn whose pod did not start ended as %s, for %+v", ended.State, ended.Failure)
	}

	// Without a limit no turn is given up.
	unlimited := install(t, func(config *Config) { config.MaxStartup = 0 })
	waiting := unlimited.dispatched()
	unlimited.ago(waiting.ID, "dispatched_at", 50*time.Second)
	unlimited.driver.reconcile(ctx)
	if unlimited.job(waiting.ID) == nil || unlimited.turn(waiting.ID).Failure != nil {
		t.Error("a turn was given up although its pod may take any time to start")
	}
}

// A harness that does not stop when it is told to would run on after its
// turn was cancelled.
func TestReconcileStopsTurnsThatDoNotStopWhenCancelled(t *testing.T) {
	t.Parallel()
	i := install(t)
	stubborn, stopping := i.running(), i.running()
	for _, turn := range []store.Turn{stubborn, stopping} {
		if _, _, err := i.store.CancelTurn(ctx, turn.ID); err != nil {
			t.Fatal(err)
		}
	}
	i.ago(stubborn.ID, "cancel_requested_at", 40*time.Second)

	i.driver.reconcile(ctx)
	if i.job(stubborn.ID) != nil {
		t.Error("the Job of the turn that did not stop is still there")
	}
	if i.job(stopping.ID) == nil {
		t.Error("the Job of a turn that was cancelled just now was deleted")
	}
	if left := i.turn(stubborn.ID); left.EndedAt != nil {
		t.Errorf("the turn that did not stop ended as %s before its pod was gone", left.State)
	}

	i.ago(stubborn.ID, "dispatched_at", 2*time.Minute)
	i.driver.reconcile(ctx)
	if ended := i.turn(stubborn.ID); ended.State != store.TurnCancelled || ended.Failure != nil {
		t.Errorf("the turn that did not stop ended as %s, for %+v", ended.State, ended.Failure)
	}
}

func TestReconcileDeletesJobsWithoutATurnThatRuns(t *testing.T) {
	t.Parallel()
	i := install(t)
	running, ended := i.running(), i.running()
	// The turn ended, and its Job could not be deleted then.
	if !i.driver.end(ctx, ended.ID, false, store.Failure{Reason: store.FailurePodLost}) {
		t.Fatal("the turn was not ended")
	}
	orphan := uuid.New()
	for name, label := range map[string]string{jobName(orphan): orphan.String(), "turn-of-nothing": "nothing"} {
		_, err := i.cluster.BatchV1().Jobs(namespace).Create(ctx, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{turnLabel: label}},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
	}

	i.driver.reconcile(ctx)
	jobs, err := i.cluster.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 || jobs.Items[0].Name != jobName(running.ID) {
		t.Errorf("the cluster is left with %d Jobs, not only that of the turn that runs", len(jobs.Items))
	}
}

// A Supervisor that starts finds the turns that were queued and the Jobs
// that ended while none was running.
func TestRunCatchesUp(t *testing.T) {
	t.Parallel()
	i := install(t)
	ended := i.running()
	if err := i.store.SetFinished(ctx, ended.ID, "end_turn", nil); err != nil {
		t.Fatal(err)
	}
	job := i.job(ended.ID)
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	if _, err := i.cluster.BatchV1().Jobs(namespace).UpdateStatus(ctx, job, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	queued := i.queue()

	running, stop := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		i.driver.Run(running)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for i.turn(ended.ID).State != store.TurnCompleted || i.job(ended.ID) != nil || i.job(queued.ID) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the driver left the turn whose Job had ended as %s, with its Job %t, and the queued turn with a Job %t",
				i.turn(ended.ID).State, i.job(ended.ID) != nil, i.job(queued.ID) != nil)
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Error("the driver did not stop")
	}
}
