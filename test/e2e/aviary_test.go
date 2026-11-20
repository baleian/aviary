package e2e

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
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
			t.Run("an agent of "+harness+" that is to ask", func(t *testing.T) {
				t.Parallel()
				approval(t, harness)
			})
			t.Run("a session of "+harness+" that is idle sleeps", func(t *testing.T) {
				t.Parallel()
				sleeps(t, harness)
			})
			t.Run("an agent of "+harness+" that waits for a person sleeps", func(t *testing.T) {
				t.Parallel()
				waitsAsleep(t, harness)
			})
		}
		tests := map[string]func(*testing.T){
			"a turn that fails says why":       failures,
			"a turn is cancelled":              cancel,
			"a session is ended":               ended,
			"the Supervisors report the turns": supervisorMetrics,
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

// The cluster is new. A node fetches the template of a harness before it
// runs the first sandbox of that harness, and until it has, a turn does not
// start in time. The first turns are tried until one of each harness
// completes.
func installation(t *testing.T) {
	if !ready(t, "supervisor", 2) {
		t.Fatal("the installation does not run two Supervisors: run `make e2e`, which installs it")
	}
	// The sandboxes of the last sessions of an earlier run may still be on
	// their way out.
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

// A session keeps its conversation and its files from one turn to the next,
// in a sandbox of its own, and no other session sees them.
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

// An agent that is to ask runs a command when a person has allowed it and
// not when a person has refused it. Its turn waits for the answer, which
// the API takes.
func approval(t *testing.T, name string) {
	session := newAgent(t, fmt.Sprintf(`{"harness": %q, "approval": "ask"}`, name)).session(t)
	// answered sends the message and has a person answer what the harness
	// asks with the option of the kind. It returns the turn as it ended.
	answered := func(text, kind string) turn {
		t.Helper()
		accepted := send(t, session, text)
		events := read(t, accepted.ID)
		var asked approvalRequest
		events.until(t, "asked for approval", func(next event) bool {
			return next.Type == "approval.requested" && json.Unmarshal([]byte(next.Data), &asked) == nil
		})
		// The request tells the command. The turn runs while it waits, and
		// shows the request to whoever reads the turn.
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
		// How a harness ends a turn after a refusal is up to it, but the
		// turn does not fail.
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

// A session that is not at work sleeps, and goes on as it was when it is
// sent its next message: what ran in its sandbox runs on, in the same
// sandbox, and the clock of the sandbox is right.
func sleeps(t *testing.T, name string) {
	session := newAgent(t, harness(name)).session(t)
	// A process in the background counts the seconds in a file.
	count := "run setsid nohup sh -c 'echo $$ > counter.pid; while sleep 1; do echo >> counted; done' " +
		"> /dev/null 2>&1 < /dev/null & sleep 2; echo counter-$(cat counter.pid)"
	first := run(t, session, count)
	counter := regexp.MustCompile(`counter-(\d+)`).FindStringSubmatch(first.said() + first.tools())
	if counter == nil {
		t.Fatalf("the counter did not start: the agent said %q, and its tools %s", first.said(), first.tools())
	}
	before := sandboxes(t, session)
	asleep(t, session)

	check := "run kill -0 $(cat counter.pid) && echo alive-$(cat counter.pid); echo counted-$(wc -l < counted); " +
		"sleep 3; echo counted-$(wc -l < counted); echo clock-$(date +%s)"
	woken := run(t, session, check)
	told := woken.said() + woken.tools()
	if !strings.Contains(told, "alive-"+counter[1]) {
		t.Errorf("the counter %s does not run after the session slept: the agent said %q", counter[1], woken.said())
	}
	counts := regexp.MustCompile(`counted-(\d+)`).FindAllStringSubmatch(told, -1)
	if len(counts) < 2 || counts[0][1] == counts[len(counts)-1][1] {
		t.Errorf("the counter does not count after the session slept: the agent said %q", woken.said())
	}
	clock := regexp.MustCompile(`clock-(\d+)`).FindStringSubmatch(told)
	if clock == nil {
		t.Fatalf("the agent told no clock: it said %q", woken.said())
	}
	if seconds, _ := strconv.ParseInt(clock[1], 10, 64); time.Since(time.Unix(seconds, 0)).Abs() > 20*time.Second {
		t.Errorf("after the session slept, the clock of its sandbox is %s off", time.Since(time.Unix(seconds, 0)).Round(time.Second))
	}
	if recalled := run(t, session, "recall"); !strings.Contains(recalled.said(), "while sleep 1") {
		t.Errorf("after the session slept, the agent did not know what was said: %q", recalled.said())
	}
	if after := sandboxes(t, session); !slices.Equal(after, before) {
		t.Errorf("the session had the sandboxes %v before it slept, and %v after", before, after)
	}
}

// A turn that waits for a person sleeps with its session for as long as it
// waits, and goes on when it is answered.
func waitsAsleep(t *testing.T, name string) {
	session := newAgent(t, fmt.Sprintf(`{"harness": %q, "approval": "ask"}`, name)).session(t)
	accepted := send(t, session, "escalate touch "+unique("woken"))
	events := read(t, accepted.ID)
	var asked approvalRequest
	events.until(t, "asked for approval", func(next event) bool {
		return next.Type == "approval.requested" && json.Unmarshal([]byte(next.Data), &asked) == nil
	})
	asleep(t, session)
	if waiting := record(t, accepted.ID); waiting.State != "running" || len(waiting.Approvals) != 1 {
		t.Fatalf("the turn whose session sleeps is %s, with the requests %+v", waiting.State, waiting.Approvals)
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
	if last := events.end(t); last.Type != "turn.completed" {
		t.Fatalf("the turn that was answered while its session slept ended with %s %s", last.Type, last.Data)
	}
	var given bool
	for _, encoded := range record(t, accepted.ID).Items {
		var item struct{ Type, ID, Outcome string }
		if json.Unmarshal(encoded, &item) == nil && item.Type == "approval" {
			given = item.ID == asked.ID && item.Outcome == "selected"
		}
	}
	if !given {
		t.Errorf("the harness did not get the answer that it was given while its session slept")
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

	// The runner tells the reason, as its harness gave it.
	t.Run("the model refuses", func(t *testing.T) {
		t.Parallel()
		fails(t, newAgent(t, harness(harnesses[0])).session(t), "fail the model is out of order", "harness_error")
	})
	// A sandbox that goes while its agent works takes the turn with it. The
	// record keeps what the agent did until then, and the next turn of the
	// session gets a new sandbox.
	t.Run("the sandbox is lost", func(t *testing.T) {
		t.Parallel()
		session := newAgent(t, harness(harnesses[0])).session(t)
		accepted := send(t, session, "run sleep 60")
		events := read(t, accepted.ID)
		events.until(t, "called its tool", func(next event) bool { return next.Type == "tool.call" })
		had := sandboxes(t, session)
		if len(had) != 1 {
			t.Fatalf("the session of a turn that runs has the sandboxes %v", had)
		}
		if status, answer := sandboxService(t, "DELETE", "sandboxes/"+had[0]); status != http.StatusNoContent {
			t.Fatalf("the sandbox service answered the deleting of a sandbox with %d %s", status, answer)
		}
		last := events.end(t)
		lost := record(t, accepted.ID)
		if last.Type != "turn.failed" || lost.Failure["reason"] != "sandbox_lost" || lost.tools() == "" {
			t.Errorf("the turn whose sandbox was deleted ended with %s %s and is recorded as %+v", last.Type, last.Data, lost)
		}
		if next := run(t, session, "say done"); !strings.Contains(next.said(), "done") {
			t.Errorf("the session of the turn whose sandbox was deleted did not go on: %q", next.said())
		}
		if has := sandboxes(t, session); len(has) != 1 || has[0] == had[0] {
			t.Errorf("the session whose sandbox %v was deleted has the sandboxes %v", had, has)
		}
	})
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

// A session is ended: its sandbox goes with all that the agent had in it,
// what was said stays, and the session takes no more messages. It is not
// ended while a turn of it runs.
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
	if kept := sandboxes(t, session); len(kept) != 1 {
		t.Fatalf("the session that was not ended has the sandboxes %v", kept)
	}

	var over struct {
		ID      string     `json:"id"`
		EndedAt *time.Time `json:"ended_at"`
	}
	if status := request(t, "DELETE", path, "", &over); status != http.StatusOK || over.ID != session || over.EndedAt == nil {
		t.Fatalf("ending the session was answered with %d and %+v", status, over)
	}
	eventually(t, soon, "the sandbox service still has the sandbox of the session that was ended", func() bool {
		return len(sandboxes(t, session)) == 0
	})
	if status := request(t, "POST", path+"/messages", `{"text": "say more"}`, &refusal); status != http.StatusConflict || refusal.Code != "session_ended" {
		t.Errorf("a message to a session that has ended was answered with %d %s", status, refusal.Code)
	}
	var list struct{ Turns []turn }
	if status := request(t, "GET", path+"/turns", "", &list); status != http.StatusOK || len(list.Turns) != 1 || !whole(list.Turns[0], answer) {
		t.Errorf("the turns of the session that has ended are read with %d as %+v", status, list.Turns)
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

// An installation with an identity provider takes a request only with a
// token of the provider, tells its administrators and its users by the
// groups that the provider gives them, and holds a member of a project to
// what the member may do there.
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

	// The administrator makes two projects, and of one of them a user and a
	// group the members.
	theirs, other := create(t, "/v1/projects", `{"name": "theirs"}`), create(t, "/v1/projects", `{"name": "other"}`)
	agent := agentOf(t, theirs, provider(t, theirs), harness(harnesses[0]))
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

	// The member runs a turn of a session of its own.
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
	// What is no token of the provider is as good as none.
	refused, err := call(ctx, "", "GET", "/v1/me", "", "Authorization", "Bearer not-a-token")
	if err != nil {
		t.Fatal(err)
	}
	refused.Body.Close()
	if refused.StatusCode != http.StatusUnauthorized {
		t.Errorf("a request with something else than a token was answered with %d", refused.StatusCode)
	}
}

// whole tells whether the record of a turn has the words once and in order.
// A harness may say something of its own before them.
func whole(record turn, words string) bool {
	_, answer, found := strings.Cut(record.said(), "w1 ")
	return found && "w1 "+answer == words
}

// A turn outlasts its Supervisors: all of them are replaced while the answer
// comes in. The one that followed the turn is not heard of any more, and
// after a while one that came after takes the turn up. The answer takes
// longer than that, so it reads the rest from the runner as it comes, from
// where the other stopped: the answer is recorded whole and once.
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

// For a while there is no Supervisor at all, and the turn ends in its
// sandbox with nobody to tell. The runner keeps what it has told nobody: a
// Supervisor that comes after reads it, and records the turn as it ended.
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
	// The installation is left as it was however the test ends.
	t.Cleanup(func() { scale(2) })
	eventually(t, patience, "the Supervisors did not stop", func() bool {
		return len(pods(t, system, "app.kubernetes.io/component=supervisor")) == 0
	})
	// The model says a word every half second, so by then it has said them
	// all.
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
