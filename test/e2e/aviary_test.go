package e2e

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	ateapi "go.ate.dev/substrate/api/ateapi/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The tests under "turns" run in parallel, each with its own sessions. The
// last two take the Supervisors down, which would break the others' open
// requests, so they run last.
func TestAviary(t *testing.T) {
	if !t.Run("the installation runs turns", installation) {
		t.Fatal("installation runs no turns; not running the other tests")
	}
	together := t.Run("turns", func(t *testing.T) {
		for _, harness := range harnesses {
			t.Run(harness+" session", func(t *testing.T) {
				t.Parallel()
				session(t, harness)
			})
			t.Run(harness+" agent asks for approval", func(t *testing.T) {
				t.Parallel()
				approval(t, harness)
			})
			t.Run("replaced "+harness+" agent runs its next turn as it then is", func(t *testing.T) {
				t.Parallel()
				replaces(t, harness)
			})
			t.Run("idle "+harness+" session sleeps", func(t *testing.T) {
				t.Parallel()
				sleeps(t, harness)
			})
			t.Run(harness+" turn awaiting approval sleeps until it is answered", func(t *testing.T) {
				t.Parallel()
				waitsAsleep(t, harness)
			})
			t.Run(harness+" agent uses the tools of MCP servers", func(t *testing.T) {
				t.Parallel()
				mcpServers(t, harness)
			})
		}
		tests := map[string]func(*testing.T){
			"a sandbox reaches only the hosts that it may": egress,
			"an agent uses its provider":                   providers,
			"the models of a provider are listed":          models,
			"a provider keeps its key in a secret store":   secretStore,
			"a failed turn reports its reason":             failures,
			"an idle session loses its sandbox":            loses,
			"a message carries files into its sandbox":     attaches,
			"a turn is cancelled":                          cancel,
			"a session is ended":                           ended,
			"an agent is deleted":                          deletes,
			"a client watches the sessions of a project":   watches,
			"the Supervisors report turn metrics":          supervisorMetrics,
		}
		if identity {
			tests["the API authenticates and authorizes callers"] = callers
		}
		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				test(t)
			})
		}
	})
	if !together {
		t.Fatal("not running the tests that replace the Supervisors, to keep their logs of the failed turns")
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
	if !ready(t, "supervisor", 2) {
		t.Fatal("the Supervisor does not have 2 ready pods; run `make e2e` to install Aviary")
	}
	// The sandboxes of an earlier run may still be being deleted.
	eventually(t, soon, "the cluster still has sandboxes from earlier runs; run `make e2e` to create a new cluster", func() bool {
		return sandboxCount(t) == 0
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
				t.Fatalf("no %s turn completed; the last one ended with %s %s", name, last.Type, last.Data)
			}
			t.Logf("%s turn ended with %s %s; retrying", name, last.Type, last.Data)
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
		t.Errorf("first turn usage = %v, want 11 input and 7 output tokens per model response", first.Usage)
	}
	// The session carries its newest turn, in the list of sessions too.
	var listed struct {
		Sessions []struct {
			ID       string `json:"id"`
			LastTurn struct {
				ID    string `json:"id"`
				State string `json:"state"`
			} `json:"last_turn"`
		}
	}
	if status := request(t, "GET", "/v1/agents/"+agent.id+"/sessions", "", &listed); status != http.StatusOK || len(listed.Sessions) != 1 ||
		listed.Sessions[0].ID != session || listed.Sessions[0].LastTurn.ID != first.ID || listed.Sessions[0].LastTurn.State != "completed" {
		t.Errorf("list of sessions: got %d %+v, want the session with its completed turn", status, listed.Sessions)
	}

	t.Run("continues", func(t *testing.T) {
		t.Parallel()
		if second := run(t, session, "recall"); !strings.Contains(second.said(), write) {
			t.Errorf("second turn did not recall the first: said %q", second.said())
		}
		third := run(t, session, "run cat "+file)
		if !strings.Contains(third.said(), content) || !strings.Contains(third.tools(), content) {
			t.Errorf("third turn did not read the file written in the first: said %q, tool calls %s", third.said(), third.tools())
		}
		// The agent's instructions are sent in every turn.
		if fourth := run(t, session, "find "+word); !strings.Contains(fourth.said(), "present") {
			t.Errorf("model was not sent the agent's instructions: said %q", fourth.said())
		}
		var list struct{ Turns []turn }
		if status := request(t, "GET", "/v1/sessions/"+session+"/turns?limit=2", "", &list); status != http.StatusOK ||
			len(list.Turns) != 2 || list.Turns[0].Seq != 4 || list.Turns[1].Seq != 3 || list.Turns[1].said() != third.said() {
			t.Errorf("listed turns = %+v, want turns 4 and 3", list.Turns)
		}
		if actorOf(t, session) == nil {
			t.Error("session has no sandbox after four turns")
		}
	})
	t.Run("is isolated", func(t *testing.T) {
		t.Parallel()
		other := agent.session(t)
		if recalled := run(t, other, "recall"); !strings.Contains(recalled.said(), "nothing") {
			t.Errorf("other session recalled %q", recalled.said())
		}
		listed := run(t, other, "run ls -a")
		if listed.tools() == "" || strings.Contains(listed.said()+listed.tools(), file) {
			t.Errorf("ls in the other session: said %q, tool calls %s; want no file of the first session", listed.said(), listed.tools())
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
			t.Errorf("approval request = %+v, turn state = %s, pending approvals = %+v", asked, waiting.State, waiting.Approvals)
		}
		var option string
		for _, offered := range asked.Options {
			if offered.Kind == kind {
				option = offered.ID
			}
		}
		answer := fmt.Sprintf(`{"option_id": %q}`, option)
		if status := request(t, "POST", "/v1/turns/"+accepted.ID+"/approvals/"+asked.ID, answer, nil); status != http.StatusAccepted {
			t.Fatalf("answer %s to approval request %+v: got status %d, want 202", answer, asked, status)
		}
		// A harness ends a turn after a refusal in its own way, but the turn
		// does not fail.
		if last := events.end(t); last.Type != "turn.completed" {
			t.Fatalf("turn for %q ended with %s %s, want turn.completed", text, last.Type, last.Data)
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
			t.Errorf("turn for %q: approval items = %v, pending approvals = %+v", text, kept, ended.Approvals)
		}
		return ended
	}

	allowed, refused := unique("allowed"), unique("refused")
	answered("escalate touch "+allowed, "allow_once")
	answered("escalate touch "+refused, "reject_once")
	// A harness may run a command that only reads without asking, and this
	// one writes the listing too.
	listed := answered("escalate ls | tee listing", "allow_once")
	if files := listed.said() + listed.tools(); !strings.Contains(files, allowed) || strings.Contains(files, refused) {
		t.Errorf("files after one allowed and one rejected command: %q", files)
	}
	for _, outcome := range []string{"allow_once", "reject_once"} {
		series := fmt.Sprintf(`aviary_approval_wait_seconds_count{harness=%q,outcome=%q}`, name, outcome)
		eventually(t, soon, "Supervisor metrics have no "+series, func() bool {
			return total(metrics(t, "supervisor"), series) >= 1
		})
	}
}

// An agent that is replaced runs the next turn of its sessions as it then is,
// also in a sandbox that is awake: here with other instructions, and asking a
// person before it acts. The harness is started again for that, and still has
// the conversation.
func replaces(t *testing.T, name string) {
	before, after := unique("plum"), unique("pear")
	project := newProject(t)
	models := provider(t, project, mockKey)
	agent := agentOf(t, project, models, fmt.Sprintf(`{"harness": %q, "instructions": "The word of the day is %s."}`, name, before))
	session := agent.session(t)
	if first := run(t, session, "find "+before); !strings.Contains(first.said(), "present") {
		t.Fatalf("model was not sent the agent's instructions: said %q", first.said())
	}

	body := fmt.Sprintf(`{"name": "Renamed", "instructions": "The word of the day is %s.", "approval": "ask", "model": {"provider": %q, "name": "mock"}}`,
		after, models)
	var replaced struct{ Name, Harness, Approval string }
	if status := request(t, "PUT", "/v1/agents/"+agent.id, body, &replaced); status != http.StatusOK ||
		replaced.Name != "Renamed" || replaced.Harness != name || replaced.Approval != "ask" {
		t.Fatalf("replace agent: got %d %+v, want 200 with the agent as it was replaced", status, replaced)
	}
	if second := run(t, session, "find "+after); !strings.Contains(second.said(), "present") {
		t.Errorf("model was not sent the replaced instructions: said %q", second.said())
	}
	accepted := send(t, session, "escalate touch "+unique("replaced"))
	events := read(t, accepted.ID)
	var asked approvalRequest
	events.until(t, "asked for approval", func(next event) bool {
		return next.Type == "approval.requested" && json.Unmarshal([]byte(next.Data), &asked) == nil
	})
	var option string
	for _, offered := range asked.Options {
		if offered.Kind == "allow_once" {
			option = offered.ID
		}
	}
	answer := fmt.Sprintf(`{"option_id": %q}`, option)
	if status := request(t, "POST", "/v1/turns/"+accepted.ID+"/approvals/"+asked.ID, answer, nil); status != http.StatusAccepted {
		t.Fatalf("answer %s to approval request %+v: got status %d, want 202", answer, asked, status)
	}
	if last := events.end(t); last.Type != "turn.completed" {
		t.Fatalf("turn of the replaced agent ended with %s %s, want turn.completed", last.Type, last.Data)
	}
	if recalled := run(t, session, "recall"); !strings.Contains(recalled.said(), "find "+before) {
		t.Errorf("the harness does not have the conversation from before the agent was replaced: said %q", recalled.said())
	}
}

// An idle session sleeps: its sandbox leaves its worker, and a snapshot keeps
// it. The next message wakes it, where the agent still has the conversation,
// its files and the packages that it installed.
func sleeps(t *testing.T, name string) {
	session := newAgent(t, harness(name)).session(t)
	content, file := unique("content"), unique("note")+".txt"
	write := fmt.Sprintf("run printf %s > %s", content, file)
	run(t, session, write)
	// pip fetches the package from mock-llm, which the sandboxes may reach.
	// The check reads what the model said, since not every harness keeps the
	// command output in its tool call.
	if installed := run(t, session, "run pip install "+probeURL); !strings.Contains(installed.said(), "Successfully installed aviary-probe-1.0") {
		t.Fatalf("agent did not install the package: said %q", installed.said())
	}
	before := actorOf(t, session)
	if before == nil || before.State != ateapi.ActorState_ACTOR_STATE_RUNNING {
		t.Fatalf("sandbox of the session = %+v, want it running", before)
	}
	if state := sandboxState(t, session); state != "awake" {
		t.Errorf("session says that its sandbox is %q, want awake", state)
	}
	// Other tests have sandboxes too, so the state metrics are only checked
	// to count this one.
	counted := func(state string) float64 {
		return total(metrics(t, "state-metrics"), `aviary_sandboxes{state="`+state+`"}`)
	}
	if awake := counted("awake"); awake < 1 {
		t.Errorf("state metrics count %v awake sandboxes while the session is awake", awake)
	}
	asleep(t, session)
	eventually(t, soon, "the session does not say that its sandbox is asleep", func() bool {
		return sandboxState(t, session) == "asleep"
	})
	if asleep := counted("asleep"); asleep < 1 {
		t.Errorf("state metrics count %v asleep sandboxes while the session is asleep", asleep)
	}

	if recalled := run(t, session, "recall"); !strings.Contains(recalled.said(), write) {
		t.Errorf("woken session did not recall the conversation: said %q", recalled.said())
	}
	if state := sandboxState(t, session); state != "awake" {
		t.Errorf("woken session says that its sandbox is %q, want awake", state)
	}
	if read := run(t, session, "run cat "+file); !strings.Contains(read.tools(), content) {
		t.Errorf("woken session did not read its file: tool calls %s", read.tools())
	}
	if shown := run(t, session, "run pip show aviary-probe"); !strings.Contains(shown.said(), "Location: /home/.local/lib/python3") {
		t.Errorf("woken session lost the installed package: said %q", shown.said())
	}
	if after := actorOf(t, session); after == nil || after.UID != before.UID {
		t.Errorf("sandbox before sleeping = %s, after = %+v, want the same sandbox", before.UID, after)
	}
}

// A message carries files: they are uploaded for the session, and are in the
// home of its sandbox when the turn of the message starts, whatever their
// names and sizes. The model is told of them in the order of the message.
// They can be downloaded as they were uploaded.
func attaches(t *testing.T) {
	session := newAgent(t, harness(harnesses[0])).session(t)
	// Megabytes that do not compress, and a name that needs care.
	large := make([]byte, 8<<20)
	if _, err := rand.Read(large); err != nil {
		t.Fatal(err)
	}
	const report = "보고서 2026.txt"
	ids := map[string]string{}
	for name, content := range map[string]string{"data.bin": string(large), report: "to do"} {
		var uploaded struct {
			ID   string
			Size int64
		}
		path := "/v1/sessions/" + session + "/attachments?name=" + url.QueryEscape(name)
		if status := request(t, "POST", path, content, &uploaded); status != http.StatusCreated || uploaded.Size != int64(len(content)) {
			t.Fatalf("upload %s: got %d %+v, want 201 with its %d bytes", name, status, uploaded, len(content))
		}
		ids[name] = uploaded.ID
	}
	message, err := json.Marshal(map[string]any{
		"text": "run sha256sum /home/.attachments/1/data.bin", "attachments": []string{ids["data.bin"], ids[report]},
	})
	if err != nil {
		t.Fatal(err)
	}
	var accepted turn
	if status := request(t, "POST", "/v1/sessions/"+session+"/messages", string(message), &accepted); status != http.StatusAccepted {
		t.Fatalf("message with attachments: got status %d, want 202", status)
	}
	if last := read(t, accepted.ID).end(t); last.Type != "turn.completed" {
		t.Fatalf("turn of a message with attachments ended with %s %s, want turn.completed", last.Type, last.Data)
	}
	sum := sha256.Sum256(large)
	if summed := record(t, accepted.ID); !strings.Contains(summed.tools(), hex.EncodeToString(sum[:])) {
		t.Errorf("the file in the sandbox is not the one that was uploaded: tool calls %s", summed.tools())
	}
	// The files stay for the turns that follow.
	if shown := run(t, session, `run cat "/home/.attachments/1/`+report+`"`); !strings.Contains(shown.tools(), "to do") {
		t.Errorf("agent did not read the file of the earlier message: tool calls %s", shown.tools())
	}

	// The mock model says what came with its instruction, and a harness names
	// a file by its path.
	var again [2]string
	for i, name := range []string{"second.txt", "first.txt"} {
		var uploaded struct{ ID string }
		if status := request(t, "POST", "/v1/sessions/"+session+"/attachments?name="+name, "a note", &uploaded); status != http.StatusCreated {
			t.Fatalf("upload %s: got %d, want 201", name, status)
		}
		again[i] = uploaded.ID
	}
	if message, err = json.Marshal(map[string]any{"text": "attached", "attachments": again}); err != nil {
		t.Fatal(err)
	}
	if status := request(t, "POST", "/v1/sessions/"+session+"/messages", string(message), &accepted); status != http.StatusAccepted {
		t.Fatalf("second message with attachments: got status %d, want 202", status)
	}
	if last := read(t, accepted.ID).end(t); last.Type != "turn.completed" {
		t.Fatalf("turn of the second message with attachments ended with %s %s, want turn.completed", last.Type, last.Data)
	}
	told := record(t, accepted.ID).said()
	second, first := strings.Index(told, "/home/.attachments/3/second.txt"), strings.Index(told, "/home/.attachments/3/first.txt")
	if second < 0 || first < second {
		t.Errorf("the model was told %q, want the files of the message in its order", told)
	}

	response, err := call(ctx, administrator, "GET", "/v1/attachments/"+ids["data.bin"], "")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	downloaded, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(downloaded, large) {
		t.Errorf("download: got %d with %d bytes, %v; want 200 with the %d that were uploaded", response.StatusCode, len(downloaded), err, len(large))
	}
}

// A turn that waits for approval has its sandbox put to sleep after the idle
// time, as an idle session has, and the answer wakes it where it was: the
// snapshots keep the processes.
func waitsAsleep(t *testing.T, name string) {
	session := newAgent(t, fmt.Sprintf(`{"harness": %q, "approval": "ask"}`, name)).session(t)
	accepted := send(t, session, "escalate touch "+unique("asleep"))
	var asked approvalRequest
	read(t, accepted.ID).until(t, "asked for approval", func(next event) bool {
		return next.Type == "approval.requested" && json.Unmarshal([]byte(next.Data), &asked) == nil
	})
	waiting := actorOf(t, session)
	asleep(t, session)
	// Other tests have turns too, so the state metrics are only checked to
	// count this one.
	for _, series := range []string{
		fmt.Sprintf(`aviary_turns{harness=%q,state="awaiting_approval"}`, name),
		fmt.Sprintf(`aviary_approvals_waiting{harness=%q}`, name),
	} {
		if counted := total(metrics(t, "state-metrics"), series); counted < 1 {
			t.Errorf("state metrics report %v for %s while a turn of %s awaits approval", counted, series, name)
		}
	}

	var option string
	for _, offered := range asked.Options {
		if offered.Kind == "allow_once" {
			option = offered.ID
		}
	}
	answer := fmt.Sprintf(`{"option_id": %q}`, option)
	if status := request(t, "POST", "/v1/turns/"+accepted.ID+"/approvals/"+asked.ID, answer, nil); status != http.StatusAccepted {
		t.Fatalf("answer %s to approval request %+v: got status %d, want 202", answer, asked, status)
	}
	// A new reader gives the turn the full patience from the answer.
	if last := read(t, accepted.ID).end(t); last.Type != "turn.completed" {
		t.Fatalf("turn approved after a long wait ended with %s %s, want turn.completed", last.Type, last.Data)
	}
	if woken := actorOf(t, session); waiting == nil || woken == nil || woken.UID != waiting.UID {
		t.Errorf("sandbox of the turn = %+v, then %+v; want the same one, woken", waiting, woken)
	}
}

// fetch is the instruction that has the agent get a URL in its sandbox and
// print the response status, or that it failed.
const fetch = `run node -e "fetch(process.argv[1]).then(r => console.log('status', r.status), () => console.log('failed'))" `

// A sandbox holds no model API key, and reaches only the hosts that it may:
// those of the installation, and the provider of its turn, into whose
// requests the egress gateway puts the key.
func egress(t *testing.T) {
	project := newProject(t)
	agent := agentOf(t, project, provider(t, project, mockKey), harness(harnesses[0]))
	t.Run("to the model", func(t *testing.T) {
		t.Parallel()
		environment := run(t, agent.session(t), "run env")
		// mock-llm accepts only its own key, so the gateway put it in.
		seen := environment.said() + environment.tools()
		if !strings.Contains(seen, "AVIARY_LLM_BASE_URL="+mockURL) || !strings.Contains(seen, "AVIARY_LLM_API_KEY=aviary") || strings.Contains(seen, mockKey) {
			t.Errorf("harness environment = %s, want the provider's URL and a placeholder in place of its key", seen)
		}
	})
	t.Run("to an allowed host", func(t *testing.T) {
		t.Parallel()
		allowed := run(t, agent.session(t), fetch+"http://"+mockHost+"/healthz")
		if !strings.Contains(allowed.said(), "status 200") {
			t.Errorf("fetch of an allowed host: said %q, want status 200", allowed.said())
		}
	})
	t.Run("to a disallowed host", func(t *testing.T) {
		t.Parallel()
		refused := run(t, agent.session(t), fetch+"http://example.com/")
		if said := refused.said(); !strings.Contains(said, "failed") && !strings.Contains(said, "status 403") {
			t.Errorf("fetch of a disallowed host: said %q, want it failed or refused", said)
		}
	})
	t.Run("at an address", func(t *testing.T) {
		t.Parallel()
		// Not even at the address of a host that the sandbox may reach.
		model, err := cluster.CoreV1().Services("aviary-e2e").Get(ctx, "mock-llm", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		direct := run(t, agent.session(t), fetch+"http://"+model.Spec.ClusterIP+"/healthz")
		if said := direct.said(); strings.Contains(said, "status 200") || !strings.Contains(said, "failed") && !strings.Contains(said, "status 403") {
			t.Errorf("fetch at an address: said %q, want it failed or refused", said)
		}
	})
}

// A turn's model requests go to the provider of its agent, with that
// provider's key. A replaced key applies from the next request.
func providers(t *testing.T) {
	project := newProject(t)
	// Two providers of the same model server, one with a key that it does
	// not accept.
	right, wrong := provider(t, project, mockKey), provider(t, project, "a-key-that-the-model-does-not-know")
	able := agentOf(t, project, right, harness(harnesses[0]))
	unable := agentOf(t, project, wrong, harness(harnesses[0]))

	if completed := run(t, able.session(t), "say done"); !strings.Contains(completed.said(), "done") {
		t.Errorf("the agent of the provider with the right key said %q", completed.said())
	}

	session := unable.session(t)
	refused := send(t, session, "say done")
	if last := read(t, refused.ID).end(t); last.Type != "turn.failed" || !strings.Contains(last.Data, "harness_error") {
		t.Fatalf("turn with a wrong provider key ended with %s %s, want turn.failed with harness_error", last.Type, last.Data)
	}

	body := fmt.Sprintf(`{"name": "mock", "url": %q, "api_key": {"value": %q}}`, mockURL, mockKey)
	if status := request(t, "PUT", "/v1/providers/"+wrong, body, nil); status != http.StatusOK {
		t.Fatalf("replace provider: got status %d, want 200", status)
	}
	if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
		t.Errorf("after the provider key was replaced, the agent said %q", next.said())
	}

	// A provider at localhost is reached at the host that sandboxes.localhost
	// names.
	local := create(t, "/v1/projects/"+project+"/providers", fmt.Sprintf(`{"name": "local", "url": "http://localhost/v1", "api_key": {"value": %q}}`, mockKey))
	if here := run(t, agentOf(t, project, local, harness(harnesses[0])).session(t), "say done"); !strings.Contains(here.said(), "done") {
		t.Errorf("the agent of a provider at localhost said %q", here.said())
	}
}

// The models of a provider are listed, whether it is registered or not: the
// Supervisor asks its endpoint, and the answer is the endpoint's models and
// status, or why there was none.
func models(t *testing.T) {
	project := newProject(t)
	type listed struct {
		Reached bool
		Status  int
		Reason  string
		Models  []string
	}
	for name, test := range map[string]struct {
		url, key string
		reached  bool
		// status and models are the answer of an endpoint that was reached,
		// and reason is part of why another was not.
		status int
		models string
		reason string
	}{
		"with its key":                        {mockURL, mockKey, true, http.StatusOK, "mock", ""},
		"with a key that the model refuses":   {mockURL, "a-key-that-the-model-does-not-know", true, http.StatusUnauthorized, "", ""},
		"at localhost":                        {"http://localhost/v1", mockKey, true, http.StatusOK, "mock", ""},
		"at a host that is not reached":       {"https://model.example.com/v1", mockKey, false, 0, "", "cannot be reached"},
		"at a port where nothing listens":     {"https://mock-closed.aviary-e2e.svc.cluster.local/v1", mockKey, false, 0, "", "cannot be reached"},
		"under a path that the model has not": {"http://" + mockHost + "/v2", mockKey, true, http.StatusNotFound, "", ""},
	} {
		var got listed
		body := fmt.Sprintf(`{"url": %q, "api_key": {"value": %q}}`, test.url, test.key)
		if status := request(t, "POST", "/v1/projects/"+project+"/providers/models", body, &got); status != http.StatusOK {
			t.Errorf("an endpoint %s: got status %d, want 200", name, status)
			continue
		}
		if got.Reached != test.reached || got.Status != test.status || strings.Join(got.Models, ",") != test.models ||
			!strings.Contains(got.Reason, test.reason) || (got.Reason == "") != test.reached {
			t.Errorf("an endpoint %s: %+v, want reached %t, status %d, the models %q and a reason with %q", name, got, test.reached, test.status, test.models, test.reason)
		}
	}
	var registered struct{ Providers []struct{ ID string } }
	if status := request(t, "GET", "/v1/projects/"+project+"/providers", "", &registered); status != http.StatusOK || len(registered.Providers) != 0 {
		t.Errorf("providers after the listings: status %d, %v", status, registered.Providers)
	}

	// A registered provider is asked with the key that it has.
	var got listed
	if status := request(t, "GET", "/v1/providers/"+provider(t, project, mockKey)+"/models", "", &got); status != http.StatusOK || !got.Reached || strings.Join(got.Models, ",") != "mock" {
		t.Errorf("the models of a provider: status %d, %+v, want the model of the mock", status, got)
	}
	got = listed{}
	if status := request(t, "GET", "/v1/providers/"+provider(t, project, "a-key-that-the-model-does-not-know")+"/models", "", &got); status != http.StatusOK || got.Status != http.StatusUnauthorized || len(got.Models) != 0 {
		t.Errorf("the models of a provider with a key that the model refuses: status %d, %+v", status, got)
	}
}

// A provider's key may be kept in a secret store of the project. A Supervisor
// reads it when the egress gateway asks for it for a model request, and
// caches it for the configured time, after which model requests carry the
// store's current key.
func secretStore(t *testing.T) {
	project := newProject(t)
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
		t.Fatal("secret store returned no role ID or secret ID")
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
		t.Fatalf("turn with a wrong key in the secret store ended with %s %s, want turn.failed with harness_error", last.Type, last.Data)
	}

	keep(mockKey)
	time.Sleep(remembered)
	next := run(t, session, "say done")
	if !strings.Contains(next.said(), "done") {
		t.Errorf("after the key in the secret store was changed, the agent said %q", next.said())
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
			t.Fatalf("turn ended with %s %s, state %s, failure %v; want failed with reason %s",
				last.Type, last.Data, failed.State, failed.Failure, reason)
		}
		if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
			t.Errorf("session did not continue after the failed turn: said %q", next.said())
		}
		return failed
	}

	// The model refuses the request, and the runner reports the harness's
	// error.
	t.Run("the model refuses", func(t *testing.T) {
		t.Parallel()
		fails(t, newAgent(t, harness(harnesses[0])).session(t), "fail the model is out of order", "harness_error")
	})
	// Deleting the sandbox fails the running turn. The turn keeps what the
	// agent did until then, and the next turn gets a new sandbox.
	t.Run("the sandbox is lost", func(t *testing.T) {
		t.Parallel()
		session := newAgent(t, harness(harnesses[0])).session(t)
		accepted := send(t, session, "run sleep 60")
		events := read(t, accepted.ID)
		events.until(t, "called its tool", func(next event) bool { return next.Type == "tool.call" })
		had := actorOf(t, session)
		if had == nil {
			t.Fatal("session has no sandbox")
		}
		deleteSandbox(t, session)
		last := events.end(t)
		lost := record(t, accepted.ID)
		if last.Type != "turn.failed" || lost.Failure["reason"] != "sandbox_lost" || lost.tools() == "" {
			t.Errorf("turn ended with %s %s, record = %+v; want failed with sandbox_lost and its tool calls kept", last.Type, last.Data, lost)
		}
		if state := sandboxState(t, session); state != "lost" {
			t.Errorf("session says that its sandbox is %q, want lost", state)
		}
		if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
			t.Errorf("session did not continue after its sandbox was deleted: said %q", next.said())
		}
		if has := actorOf(t, session); has == nil || has.UID == had.UID {
			t.Errorf("sandbox after deleting %s = %+v, want a new one", had.UID, has)
		}
	})
}

// A session whose sandbox is deleted while no turn runs says that the sandbox
// is lost, and its next turn gets a new one.
func loses(t *testing.T) {
	session := newAgent(t, harness(harnesses[0])).session(t)
	run(t, session, "say ready")
	had := actorOf(t, session)
	if had == nil {
		t.Fatal("session has no sandbox")
	}
	deleteSandbox(t, session)
	eventually(t, look+soon, "the session does not say that its sandbox is lost", func() bool {
		return sandboxState(t, session) == "lost"
	})
	if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
		t.Errorf("session did not continue after its sandbox was deleted: said %q", next.said())
	}
	if has := actorOf(t, session); has == nil || has.UID == had.UID {
		t.Errorf("sandbox after deleting %s = %+v, want a new one", had.UID, has)
	}
	if state := sandboxState(t, session); state != "awake" {
		t.Errorf("session with a new sandbox says that its sandbox is %q, want awake", state)
	}
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
		t.Errorf("cancel running turn: got status %d, want 202", status)
	}
	last := events.end(t)
	cut := record(t, accepted.ID)
	if last.Type != "turn.cancelled" || cut.State != "cancelled" || cut.StopReason != "cancelled" || cut.Failure != nil {
		t.Errorf("cancelled turn ended with %s, record = %+v", last.Type, cut)
	}
	if answer := cut.said(); !strings.Contains(answer, "w1 w2 ") || strings.Contains(answer, "w40") {
		t.Errorf("cancelled turn answer = %q, want the partial answer", answer)
	}
	if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
		t.Errorf("session did not continue after the cancelled turn: said %q", next.said())
	}
}

// Ending a session deletes its sandbox with its home, keeps its turns, and
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
		t.Errorf("end session with a running turn: got %d %s, want 409 session_busy", status, refusal.Code)
	}
	if last := events.end(t); last.Type != "turn.completed" {
		t.Fatalf("turn ended with %s %s, want turn.completed", last.Type, last.Data)
	}
	if actorOf(t, session) == nil {
		t.Fatal("session has no sandbox")
	}

	var over struct {
		ID      string     `json:"id"`
		EndedAt *time.Time `json:"ended_at"`
	}
	if status := request(t, "DELETE", path, "", &over); status != http.StatusOK || over.ID != session || over.EndedAt == nil {
		t.Fatalf("end session: got %d %+v, want 200 with ended_at", status, over)
	}
	eventually(t, soon, "the ended session's sandbox still exists", func() bool {
		return actorOf(t, session) == nil
	})
	if status := request(t, "POST", path+"/messages", `{"text": "say more"}`, &refusal); status != http.StatusConflict || refusal.Code != "session_ended" {
		t.Errorf("message to an ended session: got %d %s, want 409 session_ended", status, refusal.Code)
	}
	var list struct{ Turns []turn }
	if status := request(t, "GET", path+"/turns", "", &list); status != http.StatusOK || len(list.Turns) != 1 || !whole(list.Turns[0], answer) {
		t.Errorf("turns of the ended session: got %d %+v, want its one turn with the whole answer", status, list.Turns)
	}
}

// A client that watches a project is sent each of its sessions when the
// session changes, whichever Supervisor the client reaches.
func watches(t *testing.T) {
	project := newProject(t)
	changes := watch(t, project)
	session := agentOf(t, project, provider(t, project, mockKey), harness(harnesses[0])).session(t)
	done := run(t, session, "say done")
	deadline := time.After(soon)
	for {
		select {
		case sent, open := <-changes:
			switch {
			case !open:
				t.Fatal("the stream of changes ended before the session was sent with its completed turn")
			case sent.ID != session:
				t.Errorf("session %s was sent, want only %s", sent.ID, session)
			case sent.LastTurn.ID == done.ID && sent.LastTurn.State == "completed":
				if sent.Sandbox != "awake" {
					t.Errorf("the session was sent with the sandbox %q, want awake", sent.Sandbox)
				}
				return
			}
		case <-deadline:
			t.Fatalf("after %s, the session was not sent with its completed turn", soon)
		}
	}
}

// Deleting an agent ends its sessions and deletes their sandboxes. The
// conversations stay readable, and the agent's slug is free again.
func deletes(t *testing.T) {
	project := newProject(t)
	models := provider(t, project, mockKey)
	agent := agentOf(t, project, models, harness(harnesses[0]))
	path := "/v1/agents/" + agent.id
	session := agent.session(t)
	run(t, session, "say done")
	if actorOf(t, session) == nil {
		t.Fatal("session has no sandbox")
	}

	var deleted struct {
		ID        string     `json:"id"`
		Slug      string     `json:"slug"`
		DeletedAt *time.Time `json:"deleted_at"`
	}
	if status := request(t, "DELETE", path, "", &deleted); status != http.StatusOK || deleted.ID != agent.id || deleted.DeletedAt == nil {
		t.Fatalf("delete agent: got %d %+v, want 200 with deleted_at", status, deleted)
	}
	var over struct {
		EndedAt   *time.Time `json:"ended_at"`
		EndReason string     `json:"end_reason"`
	}
	if status := request(t, "GET", "/v1/sessions/"+session, "", &over); status != http.StatusOK || over.EndedAt == nil || over.EndReason != "agent_deleted" {
		t.Errorf("session of the deleted agent: got %d %+v, want it ended with the reason agent_deleted", status, over)
	}
	eventually(t, soon, "the sandbox of the deleted agent's session still exists", func() bool {
		return actorOf(t, session) == nil
	})
	var list struct{ Turns []turn }
	if status := request(t, "GET", "/v1/sessions/"+session+"/turns", "", &list); status != http.StatusOK ||
		len(list.Turns) != 1 || !strings.Contains(list.Turns[0].said(), "done") {
		t.Errorf("turns of the deleted agent's session: got %d %+v, want its one turn", status, list.Turns)
	}
	var refusal struct{ Code string }
	if status := request(t, "POST", path+"/sessions", "", &refusal); status != http.StatusConflict || refusal.Code != "agent_deleted" {
		t.Errorf("session of a deleted agent: got %d %s, want 409 agent_deleted", status, refusal.Code)
	}
	body := fmt.Sprintf(`{"slug": %q, "harness": %q, "model": {"provider": %q, "name": "mock"}}`, deleted.Slug, harnesses[0], models)
	if next := create(t, "/v1/projects/"+project+"/agents", body); next == agent.id {
		t.Errorf("agent created with the slug of the deleted one has its ID %s", next)
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
		eventually(t, soon, "Supervisor metrics have no "+series, func() bool {
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
			t.Fatalf("GET /v1/me as %s: got %d %+v", user, status, answer)
		}
		return answer
	}
	if alice := who(administrator); !alice.Admin || alice.DisplayName != "Alice Admin" || len(alice.Groups) != 1 {
		t.Errorf("administrator = %+v", alice)
	}
	bob := who(member)
	if carol := who(teammate); bob.Admin || carol.Admin || carol.ID == bob.ID || len(carol.Groups) != 2 {
		t.Errorf("users = %+v and %+v", bob, carol)
	}
	if dave := who(stranger); dave.Admin || len(dave.Groups) != 0 {
		t.Errorf("user in no group = %+v", dave)
	}

	// The member and the team are members of the first of two projects.
	theirs, other := newProject(t), newProject(t)
	agent := agentOf(t, theirs, provider(t, theirs, mockKey), harness(harnesses[0]))
	create(t, "/v1/projects/"+theirs+"/members", fmt.Sprintf(`{"user": %q, "role": "user"}`, member))
	create(t, "/v1/projects/"+theirs+"/members", fmt.Sprintf(`{"group": %q, "role": "viewer"}`, team))

	var listed struct{ Projects []struct{ ID string } }
	if status := requestAs(t, member, "GET", "/v1/projects", "", &listed); status != http.StatusOK {
		t.Fatalf("list projects as the member: got status %d, want 200", status)
	}
	var seen []string
	for _, project := range listed.Projects {
		seen = append(seen, project.ID)
	}
	if !slices.Contains(seen, theirs) || slices.Contains(seen, other) {
		t.Errorf("projects listed for the member of %s but not %s = %v", theirs, other, seen)
	}

	var session struct {
		ID        string `json:"id"`
		CreatedBy string `json:"created_by"`
	}
	if status := requestAs(t, member, "POST", "/v1/agents/"+agent.id+"/sessions", "", &session); status != http.StatusCreated || session.CreatedBy != bob.ID {
		t.Fatalf("create session as the member: got %d %+v", status, session)
	}
	t.Cleanup(func() { end(t, session.ID) })
	var accepted turn
	if status := requestAs(t, member, "POST", "/v1/sessions/"+session.ID+"/messages", `{"text": "say done"}`, &accepted); status != http.StatusAccepted {
		t.Fatalf("send message as the member: got status %d, want 202", status)
	}
	if last := read(t, accepted.ID).end(t); last.Type != "turn.completed" {
		t.Fatalf("member's turn ended with %s %s, want turn.completed", last.Type, last.Data)
	}
	var sent struct {
		CreatedBy string `json:"created_by"`
	}
	if status := requestAs(t, member, "GET", "/v1/turns/"+accepted.ID, "", &sent); status != http.StatusOK || sent.CreatedBy != bob.ID {
		t.Errorf("get turn as the member %s: got status %d, created_by %q", bob.ID, status, sent.CreatedBy)
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
			t.Errorf("%s %s as %q: got status %d, want %d", try.method, try.path, try.user, status, try.status)
		}
	}
	// An invalid token counts as none.
	refused, err := call(ctx, "", "GET", "/v1/me", "", "Authorization", "Bearer not-a-token")
	if err != nil {
		t.Fatal(err)
	}
	refused.Body.Close()
	if refused.StatusCode != http.StatusUnauthorized {
		t.Errorf("request with an invalid token: got status %d, want 401", refused.StatusCode)
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
		t.Fatalf("turn state after the Supervisors were replaced = %s, want running", state)
	}
	if last := events.end(t); last.Type != "turn.completed" {
		t.Fatalf("turn ended with %s %s after the Supervisors were replaced, want turn.completed", last.Type, last.Data)
	}
	if kept := record(t, accepted.ID); !whole(kept, answer) {
		t.Errorf("recorded answer = %q, want every word once and in order", kept.said())
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
		t.Fatalf("turn that ran without a Supervisor ended with %s %s, want turn.completed", last.Type, last.Data)
	}
	if kept := record(t, accepted.ID); !whole(kept, answer) {
		t.Errorf("recorded answer = %q, want every word once and in order", kept.said())
	}
}

// An agent uses the tools of the MCP servers that its session has on, which
// mock-llm serves. The harness sends each request with the server's headers,
// and the egress gateway puts the server's token in place of a placeholder. A
// harness that does not support a server's transport says so in the turn, and
// a server that the session turns off is gone from its next turn.
func mcpServers(t *testing.T, harness string) {
	project := newProject(t)
	server := func(slug, url, transport string) string {
		return create(t, "/v1/projects/"+project+"/mcp-servers", fmt.Sprintf(`{"slug": %q, "url": %q, "transport": %q,
			"headers": [{"name": "authorization", "secret": {"value": "Bearer %s"}}, {"name": "x-probe", "value": "from %s"}]}`,
			slug, url, transport, mockKey, slug))
	}
	web, feed := server("web", "http://"+mcpHost+"/mcp", "http"), server("feed", "http://"+sseHost+"/sse", "sse")
	// The servers' tools were listed as they were registered.
	for _, id := range []string{web, feed} {
		var registered struct{ Tools []struct{ Name string } }
		if status := request(t, "GET", "/v1/mcp-servers/"+id, "", &registered); status != http.StatusOK || len(registered.Tools) != 1 || registered.Tools[0].Name != "headers" {
			t.Errorf("tools of server %s: status %d, %+v", id, status, registered.Tools)
		}
	}
	agent := agentOf(t, project, provider(t, project, mockKey), fmt.Sprintf(`{"harness": %q, "mcp_servers": [%q, %q]}`, harness, web, feed))
	session := agent.session(t)

	used := run(t, session, `use web headers {"names": ["x-probe"]}`)
	if said := used.said(); !strings.Contains(said, "x-probe: from web") {
		t.Errorf("the tool of the server said %q", said)
	}
	if called := mcpCalls(t, used); !slices.Contains(called, web+" web headers") {
		t.Errorf("calls of the tools of MCP servers: %v", called)
	}

	fed := run(t, session, `use feed headers {"names": ["x-probe"]}`)
	if unsupported := itemsOf(t, fed, "mcp_unsupported"); len(unsupported) > 0 {
		if said := fed.said(); !strings.Contains(said, "offers no tool headers of the MCP server feed") {
			t.Errorf("a harness that does not support the server: the agent said %q", said)
		}
	} else if said := fed.said(); !strings.Contains(said, "x-probe: from feed") {
		t.Errorf("the tool of the server of SSE said %q", said)
	}

	if status := request(t, "PUT", "/v1/sessions/"+session+"/mcp-servers/"+web, `{"enabled": false}`, nil); status != http.StatusOK {
		t.Fatalf("turn the server off: status %d", status)
	}
	// A harness may still offer the server's tool from earlier in the
	// session, and fail the call.
	if said := run(t, session, `use web headers {"names": ["x-probe"]}`).said(); strings.Contains(said, "x-probe: from web") {
		t.Errorf("after the server was turned off, the turn reached it: %q", said)
	}
}

// itemsOf returns the items of the turn of the type.
func itemsOf(t *testing.T, of turn, kind string) []map[string]any {
	t.Helper()
	var found []map[string]any
	for _, encoded := range of.Items {
		var item map[string]any
		if err := json.Unmarshal(encoded, &item); err != nil {
			t.Fatal(err)
		}
		if item["type"] == kind {
			found = append(found, item)
		}
	}
	return found
}

// mcpCalls returns the calls of the tools of MCP servers in the turn, each as
// the server's ID, its slug and the tool.
func mcpCalls(t *testing.T, of turn) []string {
	t.Helper()
	var called []string
	for _, item := range itemsOf(t, of, "tool_call") {
		if mcp, found := item["mcp"].(map[string]any); found {
			called = append(called, fmt.Sprintf("%v %v %v", mcp["server"], mcp["slug"], mcp["tool"]))
		}
	}
	return called
}
