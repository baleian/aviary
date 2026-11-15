package e2e

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The tests run in this order. Those in the middle run at once, each with
// sessions of its own. The last two take the Supervisors away, which breaks
// what the others would have open.
func TestAviary(t *testing.T) {
	if !t.Run("the installation runs turns", installation) {
		t.Fatal("the installation runs no turns, so nothing else was tried")
	}
	together := t.Run("turns", func(t *testing.T) {
		for _, harness := range harnesses {
			t.Run("a session of "+harness, func(t *testing.T) {
				t.Parallel()
				session(t, harness)
			})
		}
		for name, test := range map[string]func(*testing.T){
			"a turn pod reaches the outside only through the proxy": egress,
			"a turn that fails says why":                            failures,
			"a turn is cancelled":                                   cancel,
			"the Supervisors report the turns":                      supervisorMetrics,
			"homes are made on two nodes at once":                   homes,
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				test(t)
			})
		}
	})
	if !together {
		t.Fatal("the tests that follow replace the Supervisors, whose logs tell of the turns that failed, so they were not tried")
	}
	t.Run("a turn outlasts its Supervisors", outlasts)
	t.Run("a turn waits for a Supervisor", waits)
}

// unique returns a word that nothing else in the cluster has.
func unique(prefix string) string {
	return prefix + "-" + strings.ToLower(rand.Text())[:8]
}

func harness(name string) string {
	return fmt.Sprintf(`{"harness": %q}`, name)
}

// ready tells whether so many pods of a part of Aviary run and are ready.
func ready(t *testing.T, component string, count int) bool {
	t.Helper()
	pods := running(t, component)
	for _, pod := range pods {
		var ready bool
		for _, condition := range pod.Status.Conditions {
			ready = ready || condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue
		}
		if !ready {
			return false
		}
	}
	return len(pods) == count
}

// The cluster is new. Its volume takes a while before a pod can open files
// on it, and until then the pod of a turn does not start in time. The first
// turns are tried until one of each harness completes.
func installation(t *testing.T) {
	for _, component := range []string{"supervisor", "egress-proxy"} {
		if !ready(t, component, 2) {
			t.Fatalf("the installation does not run two of %s: run `make e2e`, which installs it", component)
		}
	}
	// The pods of the last turns of an earlier run may still be on their way
	// out.
	eventually(t, soon, "the cluster holds turn pods that are not of these tests: run `make e2e`, which makes a new cluster", func() bool {
		return len(pods(t, runtime, "")) == 0
	})
	for _, name := range harnesses {
		session := newAgent(t, harness(name)).session(t)
		deadline := time.Now().Add(5 * time.Minute)
		for {
			accepted := send(t, session, "say ready")
			last := read(t, accepted.ID).end(t)
			if last.Type == "turn.completed" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no turn of %s completes; the last ended with %s %s", name, last.Type, last.Data)
			}
			t.Logf("a first turn of %s ended with %s %s; trying another", name, last.Type, last.Data)
		}
	}
}

// A session keeps its conversation and its files from one turn to the next,
// each in a pod of its own, and no other session sees them.
func session(t *testing.T, name string) {
	word, content, file := unique("plum"), unique("content"), unique("note")+".txt"
	agent := newAgent(t, fmt.Sprintf(`{"harness": %q, "instructions": "The word of the day is %s."}`, name, word))
	session := agent.session(t)
	write := fmt.Sprintf("run printf %s > %s", content, file)
	first := run(t, session, write)
	// The harness reports the usage of one answer of the model or of all of
	// them, and every answer of mock-llm takes 11 tokens in and 7 out.
	answers := first.Usage["total_tokens"] / 18
	if answers < 1 || first.Usage["input_tokens"] != 11*answers || first.Usage["output_tokens"] != 7*answers {
		t.Errorf("the first turn records the usage %v", first.Usage)
	}
	eventually(t, soon, "the Job or the pod of the first turn is still there", func() bool { return len(left(t, first.ID)) == 0 })

	t.Run("goes on", func(t *testing.T) {
		t.Parallel()
		if second := run(t, session, "recall"); !strings.Contains(second.said(), write) {
			t.Errorf("the second turn did not know what was said in the first: %q", second.said())
		}
		third := run(t, session, "run cat "+file)
		if !strings.Contains(third.said(), content) || !strings.Contains(third.tools(), content) {
			t.Errorf("the third turn did not read the file of the first: it said %q and its tools %s", third.said(), third.tools())
		}
		// The agent is told its instructions in every turn.
		if fourth := run(t, session, "find "+word); !strings.Contains(fourth.said(), "present") {
			t.Errorf("the model was not sent the instructions of the agent: %q", fourth.said())
		}
		var list struct{ Turns []turn }
		if status := request(t, "GET", "/v1/sessions/"+session+"/turns?limit=2", "", &list); status != http.StatusOK ||
			len(list.Turns) != 2 || list.Turns[0].Seq != 4 || list.Turns[1].Seq != 3 || list.Turns[1].said() != third.said() {
			t.Errorf("the last two turns of the session are listed as %+v", list.Turns)
		}
	})
	t.Run("is apart from another", func(t *testing.T) {
		t.Parallel()
		other := agent.session(t)
		if recalled := run(t, other, "recall"); !strings.Contains(recalled.said(), "nothing") {
			t.Errorf("another session recalled %q", recalled.said())
		}
		listed := run(t, other, "run ls -a")
		if listed.tools() == "" || strings.Contains(listed.said()+listed.tools(), file) {
			t.Errorf("another session saw the files %q with the tools %s", listed.said(), listed.tools())
		}
	})
}

// Commands for the agent to run in its pod. fetch gets a URL the way the
// pod is set up to, which is through the Egress Proxy, and prints the status
// of the answer. connect opens a connection to port 80 of an address,
// without the proxy, and prints whether the address answered.
const (
	fetch   = `run node -e "fetch(process.argv[1]).then(r => console.log('status', r.status), () => console.log('failed'))" `
	connect = `run node -e "const s = require('net').connect({host: process.argv[1], port: 80, timeout: 3000}); ` +
		`s.on('connect', () => { console.log('connected'); s.destroy() }); ` +
		`s.on('timeout', () => { console.log('no answer'); s.destroy() }); s.on('error', () => console.log('no answer'))" `
)

// The pod of a turn holds no key of the model endpoint and reaches nothing
// by itself. What it sends out goes through the Egress Proxy, which lets a
// request through to an allowed host and refuses any other, and records
// both with the turn.
func egress(t *testing.T) {
	agent := newAgent(t, harness(harnesses[0]))
	// logged waits for the proxies to have recorded a request of the turn
	// to the host that ended with the status.
	logged := func(t *testing.T, id, route, host string, status float64) map[string]any {
		t.Helper()
		var found map[string]any
		eventually(t, soon, fmt.Sprintf("no Egress Proxy has recorded a %s request of the turn to %s that ended with %v", route, host, status), func() bool {
			for _, line := range recorded(t, id) {
				if line["route"] == route && line["host"] == host && line["status"] == status {
					found = line
				}
			}
			return found != nil
		})
		return found
	}

	t.Run("requests", func(t *testing.T) {
		t.Run("to the model", func(t *testing.T) {
			t.Parallel()
			secret, err := cluster.CoreV1().Secrets(system).Get(ctx, "aviary-llm-mock", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			session := agent.session(t)
			environment := run(t, session, `run tr '\0' '\n' < /proc/1/environ`)
			seen := environment.said() + environment.tools()
			if !strings.Contains(seen, "AVIARY_LLM_BASE_URL=http://aviary-egress-proxy") {
				t.Errorf("the pod does not have the Egress Proxy as its model endpoint: %s", seen)
			}
			if strings.Contains(seen, string(secret.Data["api-key"])) {
				t.Error("the pod holds the key of the model endpoint")
			}
			// mock-llm answers only requests with its key, so the proxy
			// put it there. Its record says whose request it was.
			line := logged(t, environment.ID, "llm", "mock-llm.aviary-e2e", 200)
			if line["session"] != session || line["agent"] != agent.id || line["project"] != agent.project {
				t.Errorf("the model request of the turn is recorded as %v", line)
			}
		})
		t.Run("to an allowed host", func(t *testing.T) {
			t.Parallel()
			allowed := run(t, agent.session(t), fetch+"http://mock-llm.aviary-e2e/healthz")
			if !strings.Contains(allowed.said(), "status 200") {
				t.Errorf("the agent did not reach an allowed host through the proxy: %q", allowed.said())
			}
			logged(t, allowed.ID, "forward", "mock-llm.aviary-e2e", 200)
		})
		t.Run("to another host", func(t *testing.T) {
			t.Parallel()
			refused := run(t, agent.session(t), fetch+"http://example.com/")
			if !strings.Contains(refused.said(), "status 403") {
				t.Errorf("the proxy did not refuse a host that is not allowed: %q", refused.said())
			}
			logged(t, refused.ID, "forward", "example.com", 403)
		})
		t.Run("without the proxy", func(t *testing.T) {
			t.Parallel()
			// Not even the address of the host that the proxy lets it reach.
			model, err := cluster.CoreV1().Services("aviary-e2e").Get(ctx, "mock-llm", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			direct := run(t, agent.session(t), connect+model.Spec.ClusterIP)
			if !strings.Contains(direct.said(), "no answer") {
				t.Errorf("the pod connected to an address without the proxy: %q", direct.said())
			}
		})
	})

	// The metrics of the proxies tell of these requests: those that were
	// let through on both ways, the one that was refused, and how long the
	// requests to the model took.
	for _, series := range []string{
		`envoy_http_ext_authz_ok{envoy_http_conn_manager_prefix="llm"`,
		`envoy_http_ext_authz_ok{envoy_http_conn_manager_prefix="forward"`,
		`envoy_http_rbac_denied`,
		`envoy_http_downstream_rq_time_count{envoy_http_conn_manager_prefix="llm"`,
	} {
		eventually(t, soon, "the metrics of the Egress Proxies have nothing for "+series, func() bool {
			return total(metrics(t, "egress-proxy"), series) >= 1
		})
	}
}

// A turn that fails says why, and its session goes on.
func failures(t *testing.T) {
	// fails sends the message and waits for its turn to fail for the reason.
	fails := func(t *testing.T, session, text, reason string) turn {
		t.Helper()
		accepted := send(t, session, text)
		last := read(t, accepted.ID).end(t)
		failed := record(t, accepted.ID)
		if last.Type != "turn.failed" || !strings.Contains(last.Data, reason) || failed.State != "failed" || failed.Failure["reason"] != reason {
			t.Fatalf("the turn ended with %s %s and is recorded as %s for %v, not as failed for %s",
				last.Type, last.Data, failed.State, failed.Failure, reason)
		}
		if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
			t.Errorf("the session of the turn that failed did not go on: %q", next.said())
		}
		return failed
	}

	// The runner leaves the reason with its pod, where the Supervisor reads
	// it.
	t.Run("the model refuses", func(t *testing.T) {
		t.Parallel()
		fails(t, newAgent(t, harness(harnesses[0])).session(t), "fail the model is out of order", "harness_error")
	})
	// An agent is given the resources of its turn pods.
	t.Run("the pod takes more memory than it may", func(t *testing.T) {
		t.Parallel()
		limited := newAgent(t, fmt.Sprintf(`{"harness": %q, "resources": {"limits": {"memory": "512Mi"}}}`, harnesses[0]))
		var kept struct {
			Resources struct{ Limits map[string]string }
		}
		if request(t, "GET", "/v1/agents/"+limited.id, "", &kept); kept.Resources.Limits["memory"] != "512Mi" {
			t.Errorf("the agent has the resources %v", kept.Resources)
		}
		fails(t, limited.session(t), `run node -e "const a = []; for (;;) a.push(Buffer.alloc(50 * 1048576, 1))"`, "out_of_memory")
	})
	// A pod that is stopped while its agent works has the harness end the
	// turn first. The record keeps what the agent did until then.
	t.Run("the pod is deleted", func(t *testing.T) {
		t.Parallel()
		session := newAgent(t, harness(harnesses[0])).session(t)
		accepted := send(t, session, "run sleep 60")
		events := read(t, accepted.ID)
		events.until(t, "called its tool", func(next event) bool { return next.Type == "tool.call" })
		err := cluster.CoreV1().Pods(runtime).DeleteCollection(ctx, metav1.DeleteOptions{},
			metav1.ListOptions{LabelSelector: "aviary.io/turn=" + accepted.ID})
		if err != nil {
			t.Fatal(err)
		}
		last := events.end(t)
		lost := record(t, accepted.ID)
		if last.Type != "turn.failed" || lost.Failure["reason"] != "pod_lost" || lost.StopReason != "cancelled" || lost.tools() == "" {
			t.Errorf("the turn whose pod was deleted ended with %s %s and is recorded as %+v", last.Type, last.Data, lost)
		}
		if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
			t.Errorf("the session of the turn whose pod was deleted did not go on: %q", next.said())
		}
	})
}

// homesAtOnce is run by node, in a pod on each node. Every 20 ms by the
// clock, which is when the other pod does the same, it has the runner make
// the home of a session of an agent that is new at that moment. It prints
// the moments at which it began and ended, and the homes that the runner
// could not make.
const homesAtOnce = `const {spawnSync} = require('child_process');
const [store, count] = [process.argv[1], Number(process.argv[2])];
const failed = [];
let first, last;
for (let made = 0; made < count; made++) {
  const moment = Math.floor(Date.now() / 20) + 1;
  while (Date.now() < moment * 20) {}
  first ??= moment;
  last = moment;
  const home = store + '/projects/e2e-' + moment + '/agents/a/sessions/' + require('os').hostname() + '/home';
  const ended = spawnSync('/aviary/bin/aviary-runner', ['init-home', home], {encoding: 'utf8'});
  if (ended.status !== 0) failed.push(home + ': ' + ended.stderr.trim());
}
console.log(JSON.stringify({first, last, failed}));`

// The first turns of two sessions of a new agent may start at the same
// moment on different nodes, and each makes the home of its session on the
// volume that the nodes share. Neither fails over the directories of the
// agent that the other made first.
func homes(t *testing.T) {
	const count = 500
	supervisor, err := cluster.AppsV1().Deployments(system).Get(ctx, "aviary-supervisor", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var runner string
	var images map[string]struct{ Image string }
	for _, variable := range supervisor.Spec.Template.Spec.Containers[0].Env {
		switch variable.Name {
		case "AVIARY_RUNNER_IMAGE":
			runner = variable.Value
		case "AVIARY_HARNESSES":
			if err := json.Unmarshal([]byte(variable.Value), &images); err != nil {
				t.Fatal(err)
			}
		}
	}
	claims, err := cluster.CoreV1().PersistentVolumeClaims(runtime).List(ctx, metav1.ListOptions{})
	if err != nil || len(claims.Items) != 1 {
		t.Fatalf("the sessions of the installation are not on one volume: %v %v", claims, err)
	}
	all, err := cluster.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var nodes []string
	for _, node := range all.Items {
		if len(node.Spec.Taints) == 0 {
			nodes = append(nodes, node.Name)
		}
	}
	if len(nodes) < 2 {
		t.Fatalf("the cluster runs pods on %d nodes, and it takes two to make homes from at once", len(nodes))
	}

	// The pods are set up as those of turns are.
	var made []string
	for _, node := range nodes[:2] {
		pod, err := cluster.CoreV1().Pods(runtime).Create(ctx, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-homes-"},
			Spec: corev1.PodSpec{
				NodeName:                     node,
				RestartPolicy:                corev1.RestartPolicyNever,
				AutomountServiceAccountToken: new(false),
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot:   new(true),
					RunAsUser:      new(int64(10000)),
					RunAsGroup:     new(int64(10000)),
					SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
				Containers: []corev1.Container{{
					Name:    "homes",
					Image:   images[harnesses[0]].Image,
					Command: []string{"node", "-e", homesAtOnce, "/store", fmt.Sprint(count)},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: new(false),
						Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "store", MountPath: "/store"},
						{Name: "runner", MountPath: "/aviary/bin", ReadOnly: true},
					},
				}},
				Volumes: []corev1.Volume{
					{Name: "store", VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claims.Items[0].Name},
					}},
					{Name: "runner", VolumeSource: corev1.VolumeSource{
						Image: &corev1.ImageVolumeSource{Reference: runner},
					}},
				},
			},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, pod.Name)
		t.Cleanup(func() {
			cluster.CoreV1().Pods(runtime).Delete(ctx, pod.Name, metav1.DeleteOptions{GracePeriodSeconds: new(int64(0))})
		})
	}

	type result struct {
		First, Last int64
		Failed      []string
	}
	var results []result
	for _, name := range made {
		eventually(t, patience, "the pod "+name+" has not made its homes", func() bool {
			pod, err := cluster.CoreV1().Pods(runtime).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
		})
		printed, err := cluster.CoreV1().Pods(runtime).GetLogs(name, &corev1.PodLogOptions{}).Do(ctx).Raw()
		if err != nil {
			t.Fatal(err)
		}
		var made result
		if err := json.Unmarshal(printed, &made); err != nil {
			t.Fatalf("the pod %s did not get to the end: %s", name, printed)
		}
		if len(made.Failed) != 0 {
			t.Errorf("the pod %s could not make %d of %d homes, the first of them %s", name, len(made.Failed), count, made.Failed[0])
		}
		results = append(results, made)
	}
	if together := min(results[0].Last, results[1].Last) - max(results[0].First, results[1].First); together < count/2 {
		t.Errorf("the pods made their homes at the same moments only %d times of %d, so little was checked", together, count)
	}
}

// said reads on until the agent has said the text, and returns what it has
// said by then.
func said(t *testing.T, events *reader, text string) string {
	t.Helper()
	var said strings.Builder
	events.until(t, "said "+text, func(next event) bool {
		var data struct{ Text string }
		if next.Type == "message.delta" && json.Unmarshal([]byte(next.Data), &data) == nil {
			said.WriteString(data.Text)
		}
		return strings.Contains(said.String(), text)
	})
	return said.String()
}

// A turn that runs is stopped when it is cancelled. Its record keeps the
// answer as far as it got, and its session goes on.
func cancel(t *testing.T) {
	session := newAgent(t, harness(harnesses[0])).session(t)
	_, slow := words(40)
	accepted := send(t, session, slow)
	events := read(t, accepted.ID)
	said(t, events, "w2 ")

	// An agent without resources of its own has those of its harness, which
	// the installation sets.
	pod := pods(t, runtime, "aviary.io/turn="+accepted.ID)
	if len(pod) != 1 || pod[0].Spec.Containers[0].Resources.Requests.Memory().String() != "384Mi" {
		t.Errorf("the turn has %d pods, which ask for %v", len(pod), pod)
	}

	var cancelling turn
	if status := request(t, "POST", "/v1/turns/"+accepted.ID+"/cancel", "", &cancelling); status != http.StatusAccepted {
		t.Errorf("cancelling a turn that runs was answered with %d", status)
	}
	last := events.end(t)
	cut := record(t, accepted.ID)
	if last.Type != "turn.cancelled" || cut.State != "cancelled" || cut.StopReason != "cancelled" || cut.Failure != nil {
		t.Errorf("the cancelled turn ended with %s and is recorded as %+v", last.Type, cut)
	}
	if answer := cut.said(); !strings.Contains(answer, "w1 w2 ") || strings.Contains(answer, "w40") {
		t.Errorf("the record of the cancelled turn has the answer %q", answer)
	}
	eventually(t, soon, "the Job or the pod of the cancelled turn is still there", func() bool { return len(left(t, accepted.ID)) == 0 })
	if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
		t.Errorf("the session of the cancelled turn did not go on: %q", next.said())
	}
}

// Each Supervisor reports what it did itself, and the metrics of all of
// them together tell of the turns.
func supervisorMetrics(t *testing.T) {
	run(t, newAgent(t, harness(harnesses[0])).session(t), "say done")
	for _, series := range []string{
		`aviary_turns_ended_total{harness="` + harnesses[0] + `",reason="",state="completed"}`,
		`aviary_turn_tokens_total{harness="` + harnesses[0] + `",kind="input"}`,
		"aviary_turn_startup_duration_seconds_count",
		"aviary_turn_run_duration_seconds_count",
	} {
		eventually(t, soon, "the metrics of the Supervisors have nothing for "+series, func() bool {
			return total(metrics(t, "supervisor"), series) >= 1
		})
	}
}

// whole tells whether the record of a turn has the words once and in order.
// A harness may say something of its own before them.
func whole(record turn, words string) bool {
	_, answer, found := strings.Cut(record.said(), "w1 ")
	return found && "w1 "+answer == words
}

// A turn outlasts its Supervisors: all of them are replaced while the answer
// comes in. The runner goes on with one that comes after, and the answer is
// recorded whole and once.
func outlasts(t *testing.T) {
	session := newAgent(t, harness(harnesses[0])).session(t)
	answer, slow := words(30)
	accepted := send(t, session, slow)
	events := read(t, accepted.ID)
	said(t, events, "w2 ")

	old := map[string]bool{}
	for _, pod := range running(t, "supervisor") {
		old[pod.Name] = true
	}
	err := cluster.CoreV1().Pods(system).DeleteCollection(ctx, metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: "app.kubernetes.io/component=supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, patience, "the Supervisors were not replaced", func() bool {
		for _, pod := range pods(t, system, "app.kubernetes.io/component=supervisor") {
			if old[pod.Name] {
				return false
			}
		}
		return ready(t, "supervisor", 2)
	})
	if state := record(t, accepted.ID).State; state != "running" {
		t.Fatalf("the turn was %s before its Supervisors were gone, so nothing was checked", state)
	}
	if last := events.end(t); last.Type != "turn.completed" {
		t.Fatalf("the turn whose Supervisors were replaced ended with %s %s", last.Type, last.Data)
	}
	if kept := record(t, accepted.ID); !whole(kept, answer) {
		t.Errorf("the record of the turn whose Supervisors were replaced does not have the answer whole and once: %q", kept.said())
	}
}

// For a while there is no Supervisor at all. The runner finds the ones that
// come after: an attempt to connect that got no answer must not keep it from
// them.
func waits(t *testing.T) {
	session := newAgent(t, harness(harnesses[0])).session(t)
	answer, slow := words(40)
	accepted := send(t, session, slow)
	events := read(t, accepted.ID)
	said(t, events, "w2 ")

	scale := func(replicas int32) {
		t.Helper()
		scale := &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{Name: "aviary-supervisor", Namespace: system},
			Spec:       autoscalingv1.ScaleSpec{Replicas: replicas},
		}
		if _, err := cluster.AppsV1().Deployments(system).UpdateScale(ctx, "aviary-supervisor", scale, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	scale(0)
	// The installation is left as it was however the test ends.
	t.Cleanup(func() { scale(2) })
	eventually(t, patience, "the Supervisors did not stop", func() bool {
		return len(pods(t, system, "app.kubernetes.io/component=supervisor")) == 0
	})
	// Longer than the runner waits for an answer to an attempt.
	time.Sleep(8 * time.Second)
	scale(2)
	eventually(t, patience, "the Supervisors did not come back", func() bool { return ready(t, "supervisor", 2) })

	if last := events.end(t); last.Type != "turn.completed" {
		t.Fatalf("the turn that was without a Supervisor ended with %s %s", last.Type, last.Data)
	}
	if kept := record(t, accepted.ID); !whole(kept, answer) {
		t.Errorf("the record of the turn that was without a Supervisor does not have the answer whole and once: %q", kept.said())
	}
}
