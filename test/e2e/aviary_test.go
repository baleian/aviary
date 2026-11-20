package e2e

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The tests under "turns" run in parallel, each with its own sessions. The
// last two take the Supervisors down, which would break the others' open
// requests, so they run last.
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
			t.Run("an agent of "+harness+" that is to ask", func(t *testing.T) {
				t.Parallel()
				approval(t, harness)
			})
			t.Run("a session of "+harness+" that is idle sleeps", func(t *testing.T) {
				t.Parallel()
				sleeps(t, harness)
			})
			t.Run("a turn of "+harness+" that waits for a person keeps its session awake", func(t *testing.T) {
				t.Parallel()
				keepsAwake(t, harness)
			})
		}
		tests := map[string]func(*testing.T){
			"a sandbox reaches the outside only through the proxy": egress,
			"an agent uses the provider that it names":             providers,
			"a provider keeps its key in a secret store":           secretStore,
			"a turn that fails says why":                           failures,
			"a turn is cancelled":                                  cancel,
			"a session is ended":                                   ended,
			"the Supervisors report the turns":                     supervisorMetrics,
		}
		if identity {
			tests["a caller does what the installation lets them"] = callers
		}
		for name, test := range tests {
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

// unique returns prefix with a random suffix.
func unique(prefix string) string {
	return prefix + "-" + strings.ToLower(rand.Text())[:8]
}

func harness(name string) string {
	return fmt.Sprintf(`{"harness": %q}`, name)
}

// ready reports whether count pods of the component are running, all of them
// ready.
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

// In a new cluster, the first sandbox of a harness may take longer to start
// than a turn waits for it, so the first turns are retried until one of each
// harness completes.
func installation(t *testing.T) {
	for _, component := range []string{"supervisor", "egress-proxy"} {
		if !ready(t, component, 2) {
			t.Fatalf("the installation does not run two of %s: run `make e2e`, which installs it", component)
		}
	}
	// The sandboxes of an earlier run may still be terminating.
	eventually(t, soon, "the cluster holds sandboxes that are not of these tests: run `make e2e`, which makes a new cluster", func() bool {
		return len(sandboxes(t, "")) == 0
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

// A session keeps its conversation and its files across turns in its own
// sandbox, and other sessions do not see them.
func session(t *testing.T, name string) {
	word, content, file := unique("plum"), unique("content"), unique("note")+".txt"
	agent := newAgent(t, fmt.Sprintf(`{"harness": %q, "instructions": "The word of the day is %s."}`, name, word))
	session := agent.session(t)
	write := fmt.Sprintf("run printf %s > %s", content, file)
	first := run(t, session, write)
	// A harness reports the usage of one model response or of all of them,
	// and every mock-llm response reports 11 input and 7 output tokens.
	answers := first.Usage["total_tokens"] / 18
	if answers < 1 || first.Usage["input_tokens"] != 11*answers || first.Usage["output_tokens"] != 7*answers {
		t.Errorf("the first turn records the usage %v", first.Usage)
	}

	t.Run("goes on", func(t *testing.T) {
		t.Parallel()
		if second := run(t, session, "recall"); !strings.Contains(second.said(), write) {
			t.Errorf("the second turn did not know what was said in the first: %q", second.said())
		}
		third := run(t, session, "run cat "+file)
		if !strings.Contains(third.said(), content) || !strings.Contains(third.tools(), content) {
			t.Errorf("the third turn did not read the file of the first: it said %q and its tools %s", third.said(), third.tools())
		}
		// The agent's instructions are sent in every turn.
		if fourth := run(t, session, "find "+word); !strings.Contains(fourth.said(), "present") {
			t.Errorf("the model was not sent the instructions of the agent: %q", fourth.said())
		}
		var list struct{ Turns []turn }
		if status := request(t, "GET", "/v1/sessions/"+session+"/turns?limit=2", "", &list); status != http.StatusOK ||
			len(list.Turns) != 2 || list.Turns[0].Seq != 4 || list.Turns[1].Seq != 3 || list.Turns[1].said() != third.said() {
			t.Errorf("the last two turns of the session are listed as %+v", list.Turns)
		}
		if kept := sandboxes(t, session); len(kept) != 1 {
			t.Errorf("after four turns, the session has the sandboxes %v", kept)
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

// An agent with approval "ask" runs a command only if a person allows it. The
// turn waits for the answer, which is given through the API.
func approval(t *testing.T, name string) {
	session := newAgent(t, fmt.Sprintf(`{"harness": %q, "approval": "ask"}`, name)).session(t)
	// answered sends text, answers the approval request with the option of
	// kind, and returns the ended turn.
	answered := func(text, kind string) turn {
		t.Helper()
		accepted := send(t, session, text)
		events := read(t, accepted.ID)
		var asked approvalRequest
		events.until(t, "asked for approval", func(next event) bool {
			return next.Type == "approval.requested" && json.Unmarshal([]byte(next.Data), &asked) == nil
		})
		// The request names the command. While it waits, the turn is running
		// and lists the request.
		command := strings.TrimPrefix(text, "escalate ")
		waiting := record(t, accepted.ID)
		if !strings.Contains(string(asked.ToolCall.Input), command) || waiting.State != "running" ||
			len(waiting.Approvals) != 1 || waiting.Approvals[0].ID != asked.ID {
			t.Errorf("the harness asked %+v, and the turn is %s with the requests %+v", asked, waiting.State, waiting.Approvals)
		}
		var option string
		for _, offered := range asked.Options {
			if offered.Kind == kind {
				option = offered.ID
			}
		}
		answer := fmt.Sprintf(`{"option_id": %q}`, option)
		if status := request(t, "POST", "/v1/turns/"+accepted.ID+"/approvals/"+asked.ID, answer, nil); status != http.StatusAccepted {
			t.Fatalf("the answer %s to the request %+v was answered with %d", answer, asked, status)
		}
		// A harness ends a turn after a refusal in its own way, but the turn
		// does not fail.
		if last := events.end(t); last.Type != "turn.completed" {
			t.Fatalf("the turn of the message %q ended with %s %s", text, last.Type, last.Data)
		}
		ended := record(t, accepted.ID)
		var kept []map[string]any
		for _, encoded := range ended.Items {
			var item map[string]any
			if json.Unmarshal(encoded, &item) == nil && item["type"] == "approval" {
				kept = append(kept, item)
			}
		}
		if len(kept) != 1 || kept[0]["id"] != asked.ID || kept[0]["outcome"] != "selected" || kept[0]["option_id"] != option ||
			len(ended.Approvals) != 0 {
			t.Errorf("the record of the turn of %q keeps the requests %v, and shows %+v as waiting", text, kept, ended.Approvals)
		}
		return ended
	}

	allowed, refused := unique("allowed"), unique("refused")
	answered("escalate touch "+allowed, "allow_once")
	answered("escalate touch "+refused, "reject_once")
	listed := answered("escalate ls", "allow_once")
	if files := listed.said() + listed.tools(); !strings.Contains(files, allowed) || strings.Contains(files, refused) {
		t.Errorf("after one command was allowed and one refused, the files are %q", files)
	}
	for _, outcome := range []string{"allow_once", "reject_once"} {
		series := fmt.Sprintf(`aviary_approval_wait_seconds_count{harness=%q,outcome=%q}`, name, outcome)
		eventually(t, soon, "the metrics of the Supervisors have nothing for "+series, func() bool {
			return total(metrics(t, "supervisor"), series) >= 1
		})
	}
}

// An idle session sleeps: its pod is deleted and its home volume is kept. The
// next message wakes it in a new pod, where the agent still has the
// conversation, its files and the packages that it installed.
func sleeps(t *testing.T, name string) {
	session := newAgent(t, harness(name)).session(t)
	content, file := unique("content"), unique("note")+".txt"
	write := fmt.Sprintf("run printf %s > %s", content, file)
	run(t, session, write)
	// pip fetches the package through the Egress Proxy. The check reads what
	// the model said, since not every harness keeps the command output in
	// its tool call.
	if installed := run(t, session, "run pip install "+probeURL); !strings.Contains(installed.said(), "Successfully installed aviary-probe-1.0") {
		t.Fatalf("the agent did not install the package: it said %q", installed.said())
	}
	before, awake := sandboxesOf(t, session), sessionPods(t, session)
	if len(before) != 1 || len(awake) != 1 {
		t.Fatalf("the session that has run a turn has %d sandboxes and %d pods", len(before), len(awake))
	}
	asleep(t, session)
	eventually(t, soon, "the pod of the sandbox that sleeps is not gone", func() bool {
		return len(sessionPods(t, session)) == 0
	})

	if recalled := run(t, session, "recall"); !strings.Contains(recalled.said(), write) {
		t.Errorf("after the session slept, the agent did not know what was said: %q", recalled.said())
	}
	if read := run(t, session, "run cat "+file); !strings.Contains(read.tools(), content) {
		t.Errorf("after the session slept, the agent did not read its file: its tools %s", read.tools())
	}
	if shown := run(t, session, "run pip show aviary-probe"); !strings.Contains(shown.said(), "Location: /home/.local/lib/python3") {
		t.Errorf("after the session slept, the package that the agent installed is gone: it said %q", shown.said())
	}
	after, woken := sandboxesOf(t, session), sessionPods(t, session)
	if len(after) != 1 || after[0].Metadata.UID != before[0].Metadata.UID {
		t.Errorf("the session had the sandbox %s before it slept, and %v after", before[0].Metadata.UID, after)
	}
	if len(woken) != 1 || woken[0].UID == awake[0].UID {
		t.Errorf("the session that slept in the pod %s was woken in %d pods, not in one new pod", awake[0].UID, len(woken))
	}
}

// A turn that waits for approval keeps its sandbox from sleeping, and goes on
// when it is answered.
func keepsAwake(t *testing.T, name string) {
	session := newAgent(t, fmt.Sprintf(`{"harness": %q, "approval": "ask"}`, name)).session(t)
	accepted := send(t, session, "escalate touch "+unique("awake"))
	var asked approvalRequest
	read(t, accepted.ID).until(t, "asked for approval", func(next event) bool {
		return next.Type == "approval.requested" && json.Unmarshal([]byte(next.Data), &asked) == nil
	})
	waiting := sessionPods(t, session)
	// Past the idle time, and long enough for every Supervisor to have looked
	// for idle sessions since.
	time.Sleep(idle + look + 5*time.Second)
	found, still := sandboxesOf(t, session), sessionPods(t, session)
	if len(found) != 1 || found[0].Spec.OperatingMode == "Suspended" || len(waiting) != 1 || len(still) != 1 || still[0].UID != waiting[0].UID {
		t.Fatalf("the sandbox of the turn that waits for a person was put to sleep, or its pod replaced: %v", found)
	}

	var option string
	for _, offered := range asked.Options {
		if offered.Kind == "allow_once" {
			option = offered.ID
		}
	}
	answer := fmt.Sprintf(`{"option_id": %q}`, option)
	if status := request(t, "POST", "/v1/turns/"+accepted.ID+"/approvals/"+asked.ID, answer, nil); status != http.StatusAccepted {
		t.Fatalf("the answer %s to the request %+v was answered with %d", answer, asked, status)
	}
	// A new reader gives the turn the full patience from the answer.
	if last := read(t, accepted.ID).end(t); last.Type != "turn.completed" {
		t.Fatalf("the turn that was answered after a long wait ended with %s %s", last.Type, last.Data)
	}
}

// Instructions that have the agent run a command in its sandbox. fetch gets a
// URL through the Egress Proxy, as the sandbox is configured to, and prints
// the response status. connect opens a TCP connection to port 80 of an
// address, bypassing the proxy, and prints whether it connected.
const (
	fetch   = `run node -e "fetch(process.argv[1]).then(r => console.log('status', r.status), () => console.log('failed'))" `
	connect = `run node -e "const s = require('net').connect({host: process.argv[1], port: 80, timeout: 3000}); ` +
		`s.on('connect', () => { console.log('connected'); s.destroy() }); ` +
		`s.on('timeout', () => { console.log('no answer'); s.destroy() }); s.on('error', () => console.log('no answer'))" `
)

// A sandbox holds no model API key and reaches the outside only through the
// Egress Proxy, which allows only the listed hosts and logs every request
// with its turn.
func egress(t *testing.T) {
	project := create(t, "/v1/projects", `{"name": "e2e"}`)
	keyed := provider(t, project, mockKey)
	agent := agentOf(t, project, keyed, harness(harnesses[0]))

	t.Run("requests", func(t *testing.T) {
		t.Run("to the model", func(t *testing.T) {
			t.Parallel()
			session := agent.session(t)
			environment := run(t, session, "run env")
			seen := environment.said() + environment.tools()
			if !strings.Contains(seen, "AVIARY_LLM_BASE_URL=http://aviary-egress-proxy") {
				t.Errorf("the harness does not have the Egress Proxy as its model endpoint: %s", seen)
			}
			if strings.Contains(seen, mockKey) {
				t.Error("the sandbox holds the key of the model endpoint")
			}
			// mock-llm accepts only its own key, so the proxy added it. The
			// access log line identifies the origin of the request.
			line := logged(t, environment.ID, "llm", mockHost, 200)
			for field, value := range map[string]string{"session": session, "agent": agent.id, "project": project, "provider": keyed} {
				if line[field] != value {
					t.Errorf("the model request of the turn is recorded with the %s %v, not %s: %v", field, line[field], value, line)
				}
			}
			// With authentication, the line also identifies the caller.
			_, user := line["user"].(string)
			if _, subject := line["subject"].(string); user != identity || subject != identity {
				t.Errorf("the model request of the turn is recorded as that of the user %v, the subject %v", line["user"], line["subject"])
			}
		})
		t.Run("to an allowed host", func(t *testing.T) {
			t.Parallel()
			allowed := run(t, agent.session(t), fetch+"http://"+mockHost+"/healthz")
			if !strings.Contains(allowed.said(), "status 200") {
				t.Errorf("the agent did not reach an allowed host through the proxy: %q", allowed.said())
			}
			logged(t, allowed.ID, "forward", mockHost, 200)
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
			// Not even to the address of a host that the proxy allows.
			model, err := cluster.CoreV1().Services("aviary-e2e").Get(ctx, "mock-llm", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			direct := run(t, agent.session(t), connect+model.Spec.ClusterIP)
			if !strings.Contains(direct.said(), "no answer") {
				t.Errorf("the sandbox connected to an address without the proxy: %q", direct.said())
			}
		})
	})

	// The proxies' metrics count these requests: those allowed on both
	// listeners, the refused one, and the durations of the model requests.
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

// A turn's model requests go to the provider of its agent, with that
// provider's key. A replaced key applies from the next request.
func providers(t *testing.T) {
	project := create(t, "/v1/projects", `{"name": "e2e"}`)
	// Two providers of the same model server, one with a key that it does
	// not accept.
	right, wrong := provider(t, project, mockKey), provider(t, project, "a-key-that-the-model-does-not-know")
	able := agentOf(t, project, right, harness(harnesses[0]))
	unable := agentOf(t, project, wrong, harness(harnesses[0]))

	completed := run(t, able.session(t), "say done")
	if line := logged(t, completed.ID, "llm", mockHost, 200); line["provider"] != right {
		t.Errorf("the model request of the agent of the provider %s is recorded as %v", right, line)
	}

	session := unable.session(t)
	refused := send(t, session, "say done")
	if last := read(t, refused.ID).end(t); last.Type != "turn.failed" || !strings.Contains(last.Data, "harness_error") {
		t.Fatalf("the turn of an agent whose provider has a wrong key ended with %s %s", last.Type, last.Data)
	}
	if line := logged(t, refused.ID, "llm", mockHost, 401); line["provider"] != wrong {
		t.Errorf("the model request of the agent of the provider %s is recorded as %v", wrong, line)
	}

	body := fmt.Sprintf(`{"name": "mock", "url": %q, "api_key": {"value": %q}}`, mockURL, mockKey)
	if status := request(t, "PUT", "/v1/providers/"+wrong, body, nil); status != http.StatusOK {
		t.Fatalf("the provider was replaced with %d", status)
	}
	if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
		t.Errorf("with the key of its provider replaced, the agent said %q", next.said())
	}
}

// A provider's key may be kept in a secret store of the project. A Supervisor
// reads it when the Egress Proxy asks about a model request, and caches it
// for the configured time, after which model requests carry the store's
// current key.
func secretStore(t *testing.T) {
	project := create(t, "/v1/projects", `{"name": "e2e"}`)
	// The store's owner gives Aviary an AppRole that may read the secrets
	// under a path of its own.
	name := unique("e2e")
	owner(t, "POST", "/v1/sys/auth/approle", map[string]string{"type": "approle"}, http.StatusBadRequest)
	owner(t, "PUT", "/v1/sys/policies/acl/"+name, map[string]string{
		"policy": fmt.Sprintf(`path "secret/data/%s/*" { capabilities = ["read"] }`, name),
	})
	owner(t, "POST", "/v1/auth/approle/role/"+name, map[string]string{"token_policies": name})
	roleID, _ := owner(t, "GET", "/v1/auth/approle/role/"+name+"/role-id", nil)["role_id"].(string)
	secretID, _ := owner(t, "POST", "/v1/auth/approle/role/"+name+"/secret-id", nil)["secret_id"].(string)
	if roleID == "" || secretID == "" {
		t.Fatal("the secret store gave the role no IDs")
	}
	// keep writes key where the provider's key reference points.
	keep := func(key string) {
		t.Helper()
		owner(t, "POST", "/v1/secret/data/"+name+"/model", map[string]any{"data": map[string]string{"api_key": key}})
	}
	keep("a-key-that-the-model-does-not-know")
	registered := fmt.Sprintf(`{"name": "e2e", "kind": "kv-v2", "settings": {"address": %q, "mount": "secret", "role_id": %q}, "credential": {"value": %q}}`,
		storeAddress, roleID, secretID)
	kept := create(t, "/v1/projects/"+project+"/secret-stores", registered)
	elsewhere := create(t, "/v1/projects/"+project+"/providers",
		fmt.Sprintf(`{"name": "mock", "url": %q, "api_key": {"store": %q, "path": %q, "key": "api_key"}}`, mockURL, kept, name+"/model"))
	session := agentOf(t, project, elsewhere, harness(harnesses[0])).session(t)

	refused := send(t, session, "say done")
	if last := read(t, refused.ID).end(t); last.Type != "turn.failed" || !strings.Contains(last.Data, "harness_error") {
		t.Fatalf("the turn of an agent whose secret store holds a key that the model does not take ended with %s %s", last.Type, last.Data)
	}
	if line := logged(t, refused.ID, "llm", mockHost, 401); line["provider"] != elsewhere {
		t.Errorf("the model request of the agent of the provider %s is recorded as %v", elsewhere, line)
	}

	keep(mockKey)
	time.Sleep(remembered)
	next := run(t, session, "say done")
	if !strings.Contains(next.said(), "done") {
		t.Errorf("with the key in its secret store changed, the agent said %q", next.said())
	}
	if line := logged(t, next.ID, "llm", mockHost, 200); line["provider"] != elsewhere {
		t.Errorf("the model request of the agent of the provider %s is recorded as %v", elsewhere, line)
	}
}

// A failed turn reports its reason, and its session goes on.
func failures(t *testing.T) {
	// fails sends text, checks that its turn fails for reason, and that the
	// session goes on.
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

	// The model, not the Egress Proxy, refuses the request, and the runner
	// reports the harness's error.
	t.Run("the model refuses", func(t *testing.T) {
		t.Parallel()
		refused := fails(t, newAgent(t, harness(harnesses[0])).session(t), "fail the model is out of order", "harness_error")
		logged(t, refused.ID, "llm", mockHost, 401)
	})
	// Deleting the sandbox fails the running turn. The turn keeps what the
	// agent did until then, and the next turn gets a new sandbox.
	t.Run("the sandbox is lost", func(t *testing.T) {
		t.Parallel()
		session := newAgent(t, harness(harnesses[0])).session(t)
		accepted := send(t, session, "run sleep 60")
		events := read(t, accepted.ID)
		events.until(t, "called its tool", func(next event) bool { return next.Type == "tool.call" })
		had := sandboxesOf(t, session)
		if len(had) != 1 {
			t.Fatalf("the session of a turn that runs has the sandboxes %v", had)
		}
		if err := cluster.CoreV1().RESTClient().Delete().AbsPath(sandboxAPI, had[0].Metadata.Name).Do(ctx).Error(); err != nil {
			t.Fatal(err)
		}
		last := events.end(t)
		lost := record(t, accepted.ID)
		if last.Type != "turn.failed" || lost.Failure["reason"] != "sandbox_lost" || lost.tools() == "" {
			t.Errorf("the turn whose sandbox was deleted ended with %s %s and is recorded as %+v", last.Type, last.Data, lost)
		}
		if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
			t.Errorf("the session of the turn whose sandbox was deleted did not go on: %q", next.said())
		}
		if has := sandboxesOf(t, session); len(has) != 1 || has[0].Metadata.UID == had[0].Metadata.UID {
			t.Errorf("the session whose sandbox %s was deleted has the sandboxes %v", had[0].Metadata.UID, has)
		}
	})
}

// said reads events until the agent has said text, and returns what it said
// so far.
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

// Cancelling a running turn stops it. The turn keeps the answer as far as it
// got, and the session goes on.
func cancel(t *testing.T) {
	session := newAgent(t, harness(harnesses[0])).session(t)
	_, slow := words(40)
	accepted := send(t, session, slow)
	events := read(t, accepted.ID)
	said(t, events, "w2 ")

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
	if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
		t.Errorf("the session of the cancelled turn did not go on: %q", next.said())
	}
}

// Ending a session deletes its sandbox and home volume, keeps its turns, and
// refuses further messages. A session with a running turn cannot be ended.
func ended(t *testing.T) {
	session := newAgent(t, harness(harnesses[0])).session(t)
	path := "/v1/sessions/" + session
	answer, slow := words(8)
	accepted := send(t, session, slow)
	events := read(t, accepted.ID)
	said(t, events, "w2 ")
	var refusal struct{ Code string }
	if status := request(t, "DELETE", path, "", &refusal); status != http.StatusConflict || refusal.Code != "session_busy" {
		t.Errorf("ending a session whose turn runs was answered with %d %s", status, refusal.Code)
	}
	if last := events.end(t); last.Type != "turn.completed" {
		t.Fatalf("the turn of a session that was not ended ended with %s %s", last.Type, last.Data)
	}
	kept := sandboxes(t, session)
	if len(kept) != 1 {
		t.Fatalf("the session that was not ended has the sandboxes %v", kept)
	}
	// agent-sandbox names a volume claim <template>-<sandbox>.
	volume := "home-" + kept[0]
	if _, err := cluster.CoreV1().PersistentVolumeClaims(runtime).Get(ctx, volume, metav1.GetOptions{}); err != nil {
		t.Fatalf("the volume of the session that was not ended: %v", err)
	}

	var over struct {
		ID      string     `json:"id"`
		EndedAt *time.Time `json:"ended_at"`
	}
	if status := request(t, "DELETE", path, "", &over); status != http.StatusOK || over.ID != session || over.EndedAt == nil {
		t.Fatalf("ending the session was answered with %d and %+v", status, over)
	}
	eventually(t, soon, "the sandbox of the session that was ended is still there", func() bool {
		return len(sandboxes(t, session)) == 0
	})
	eventually(t, patience, "the volume of the session that was ended is still there", func() bool {
		_, err := cluster.CoreV1().PersistentVolumeClaims(runtime).Get(ctx, volume, metav1.GetOptions{})
		return apierrors.IsNotFound(err)
	})
	if status := request(t, "POST", path+"/messages", `{"text": "say more"}`, &refusal); status != http.StatusConflict || refusal.Code != "session_ended" {
		t.Errorf("a message to a session that has ended was answered with %d %s", status, refusal.Code)
	}
	var list struct{ Turns []turn }
	if status := request(t, "GET", path+"/turns", "", &list); status != http.StatusOK || len(list.Turns) != 1 || !whole(list.Turns[0], answer) {
		t.Errorf("the turns of the session that has ended are read with %d as %+v", status, list.Turns)
	}
}

// Each Supervisor reports only its own work, so the metrics are summed over
// all of them.
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

// With an identity provider, the API accepts only the provider's tokens, tells
// admins and users apart by their groups, and limits project members to what
// their role allows.
func callers(t *testing.T) {
	type me struct {
		ID          string   `json:"id"`
		Subject     string   `json:"subject"`
		Username    string   `json:"username"`
		DisplayName string   `json:"display_name"`
		Groups      []string `json:"groups"`
		Admin       bool     `json:"admin"`
	}
	who := func(user string) me {
		t.Helper()
		var answer me
		if status := requestAs(t, user, "GET", "/v1/me", "", &answer); status != http.StatusOK || answer.ID == "" || answer.Subject == "" || answer.Username != user {
			t.Fatalf("the installation answered %s with %d, as %+v", user, status, answer)
		}
		return answer
	}
	if alice := who(administrator); !alice.Admin || alice.DisplayName != "Alice Admin" || len(alice.Groups) != 1 {
		t.Errorf("the administrator is %+v", alice)
	}
	bob := who(member)
	if carol := who(teammate); bob.Admin || carol.Admin || carol.ID == bob.ID || len(carol.Groups) != 2 {
		t.Errorf("the users are %+v and %+v", bob, carol)
	}
	if dave := who(stranger); dave.Admin || len(dave.Groups) != 0 {
		t.Errorf("the user that is in no group is %+v", dave)
	}

	// The member and the team are members of the first of two projects.
	theirs, other := create(t, "/v1/projects", `{"name": "theirs"}`), create(t, "/v1/projects", `{"name": "other"}`)
	agent := agentOf(t, theirs, provider(t, theirs, mockKey), harness(harnesses[0]))
	create(t, "/v1/projects/"+theirs+"/members", fmt.Sprintf(`{"user": %q, "role": "user"}`, member))
	create(t, "/v1/projects/"+theirs+"/members", fmt.Sprintf(`{"group": %q, "role": "viewer"}`, team))

	var listed struct{ Projects []struct{ ID string } }
	if status := requestAs(t, member, "GET", "/v1/projects", "", &listed); status != http.StatusOK {
		t.Fatalf("the projects of the member were answered with %d", status)
	}
	var seen []string
	for _, project := range listed.Projects {
		seen = append(seen, project.ID)
	}
	if !slices.Contains(seen, theirs) || slices.Contains(seen, other) {
		t.Errorf("the member of the project %s, and not of %s, is listed the projects %v", theirs, other, seen)
	}

	var session struct {
		ID        string `json:"id"`
		CreatedBy string `json:"created_by"`
	}
	if status := requestAs(t, member, "POST", "/v1/agents/"+agent.id+"/sessions", "", &session); status != http.StatusCreated || session.CreatedBy != bob.ID {
		t.Fatalf("a session of the member was answered with %d, as %+v", status, session)
	}
	t.Cleanup(func() { end(t, session.ID) })
	var accepted turn
	if status := requestAs(t, member, "POST", "/v1/sessions/"+session.ID+"/messages", `{"text": "say done"}`, &accepted); status != http.StatusAccepted {
		t.Fatalf("a message of the member was answered with %d", status)
	}
	if last := read(t, accepted.ID).end(t); last.Type != "turn.completed" {
		t.Fatalf("the turn of the member ended with %s %s", last.Type, last.Data)
	}
	var sent struct {
		CreatedBy string `json:"created_by"`
	}
	if status := requestAs(t, member, "GET", "/v1/turns/"+accepted.ID, "", &sent); status != http.StatusOK || sent.CreatedBy != bob.ID {
		t.Errorf("the turn of the member %s was answered with %d, as sent by %q", bob.ID, status, sent.CreatedBy)
	}
	// The access log line names the user, and the subject that the identity
	// provider knows the user by.
	if line := logged(t, accepted.ID, "llm", mockHost, 200); line["user"] != bob.ID || line["subject"] != bob.Subject {
		t.Errorf("the model request of the turn of the member %s, the subject %s, is recorded as %v", bob.ID, bob.Subject, line)
	}

	for _, try := range []struct {
		user, method, path string
		status             int
	}{
		{"", "GET", "/v1/me", http.StatusUnauthorized},
		{"", "GET", "/v1/projects", http.StatusUnauthorized},
		{stranger, "GET", "/v1/projects", http.StatusForbidden},
		{member, "POST", "/v1/projects", http.StatusForbidden},
		{member, "GET", "/v1/projects/" + other, http.StatusNotFound},
		{member, "POST", "/v1/projects/" + theirs + "/agents", http.StatusForbidden},
		{teammate, "GET", "/v1/agents/" + agent.id, http.StatusOK},
		{teammate, "POST", "/v1/agents/" + agent.id + "/sessions", http.StatusForbidden},
		{teammate, "GET", "/v1/turns/" + accepted.ID, http.StatusForbidden},
	} {
		if status := requestAs(t, try.user, try.method, try.path, "", nil); status != try.status {
			t.Errorf("%s %s of %q was answered with %d, not %d", try.method, try.path, try.user, status, try.status)
		}
	}
	// An invalid token counts as none.
	refused, err := call(ctx, "", "GET", "/v1/me", "", "Authorization", "Bearer not-a-token")
	if err != nil {
		t.Fatal(err)
	}
	refused.Body.Close()
	if refused.StatusCode != http.StatusUnauthorized {
		t.Errorf("a request with something else than a token was answered with %d", refused.StatusCode)
	}
}

// whole reports whether the turn said words once and in order. A harness may
// say something of its own first.
func whole(record turn, words string) bool {
	_, answer, found := strings.Cut(record.said(), "w1 ")
	return found && "w1 "+answer == words
}

// All Supervisors are replaced while the answer streams in, which takes longer
// than a new Supervisor needs to take the turn up. The new one reads the rest
// of the answer from the runner where the old one stopped, so the answer is
// recorded whole and once.
func outlasts(t *testing.T) {
	session := newAgent(t, harness(harnesses[0])).session(t)
	answer, slow := words(120)
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

// While no Supervisor runs, the turn ends in its sandbox. The runner keeps
// the events that nobody has read, and a Supervisor that starts later reads
// them and records the turn as it ended.
func waits(t *testing.T) {
	session := newAgent(t, harness(harnesses[0])).session(t)
	const count = 40
	answer, slow := words(count)
	accepted := send(t, session, slow)
	events := read(t, accepted.ID)
	said(t, events, "w2 ")
	saying := time.Now()

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
	// Restore the Supervisors however the test ends.
	t.Cleanup(func() { scale(2) })
	eventually(t, patience, "the Supervisors did not stop", func() bool {
		return len(pods(t, system, "app.kubernetes.io/component=supervisor")) == 0
	})
	// The model says a word every half second, so by then it has said all
	// of them.
	time.Sleep(time.Until(saying.Add(count*time.Second/2 + 5*time.Second)))
	scale(2)
	eventually(t, patience, "the Supervisors did not come back", func() bool { return ready(t, "supervisor", 2) })

	if last := events.end(t); last.Type != "turn.completed" {
		t.Fatalf("the turn that was without a Supervisor ended with %s %s", last.Type, last.Data)
	}
	if kept := record(t, accepted.ID); !whole(kept, answer) {
		t.Errorf("the record of the turn that was without a Supervisor does not have the answer whole and once: %q", kept.said())
	}
}
