package app

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/events"
	"aviary/supervisor/internal/driver"
	"aviary/supervisor/store"
)

// items decodes the items of a turn.
func items(t *testing.T, record turn) []map[string]any {
	t.Helper()
	items := []map[string]any{}
	for _, encoded := range record.Items {
		var item map[string]any
		if err := json.Unmarshal(encoded, &item); err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	return items
}

// A message runs as a turn: the Supervisor creates the session's sandbox,
// dispatches the turn to its runner, streams and records the runner's
// events, and ends the turn when the runner reports its end.
func TestTurn(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	s := aviary.supervisor()
	session := s.session(`{"harness": "codex", "instructions": "Be brief."}`)

	accepted := s.send(session, "hello")
	watching := s.watch(accepted.ID)
	if accepted.State != "queued" || accepted.Seq != 1 || accepted.Text != "hello" || accepted.SessionID != session {
		t.Errorf("accepted turn = %+v", accepted)
	}
	runner := aviary.given(accepted.ID)
	if image := aviary.service.Made()[runner.Sandbox].Pod.Harness.Image; image != "registry.example.com/harness-codex:1.0" {
		t.Errorf("sandbox image = %s, want the codex harness", image)
	}
	if runner.Text != "hello" || runner.Instructions != "Be brief." || runner.Approval != "" {
		t.Errorf("runner turn = %+v", runner.Turn)
	}
	if state := s.turn(accepted.ID).State; state != "running" {
		t.Errorf("turn state = %s, want running", state)
	}
	// While a turn runs, the session token is accepted for model and forward
	// proxy requests, which are attributed to the turn.
	token := aviary.token(runner.Sandbox)
	for _, forward := range []bool{false, true} {
		origin := s.origin(token, forward)
		if origin["turn"] != accepted.ID || origin["session"] != session || origin["agent"] == "" || origin["project"] == "" {
			t.Errorf("origin of the running turn's token = %v", origin)
		}
		// Without an identity provider, the message has no sender.
		_, user := origin["user"]
		if _, subject := origin["subject"]; user || subject {
			t.Errorf("origin has a sender without an identity provider: %v", origin)
		}
		// Only model requests name a provider.
		if _, provider := origin["provider"]; provider == forward {
			t.Errorf("forward %t: provider = %q", forward, origin["provider"])
		}
	}
	if origin := s.origin("another-token", false); origin != nil {
		t.Errorf("origin of an unknown token = %v, want nil", origin)
	}

	runner.report(started("harness-session-1"))
	runner.report(said("Hello "))
	runner.report(said("there"))
	runner.report(message{"tool.call", map[string]string{"id": "t1", "title": "ls", "kind": "execute", "status": "pending"}})
	runner.report(message{"tool.update", map[string]string{"id": "t1", "status": "completed", "output": "a.txt"}})
	// The harness names its session, which is passed on. An event that
	// clients do not know is not.
	runner.report(message{"session.update", map[string]string{"title": "Greeting"}})
	runner.report(message{"harness.note", map[string]string{"text": "for the runner only"}})
	// The session takes no message while its turn runs.
	var refusal struct{ Code string }
	if status, _ := s.request("POST", "/v1/sessions/"+session+"/messages", `{"text": "again"}`, &refusal); status != http.StatusConflict || refusal.Code != "session_busy" {
		t.Errorf("send to a busy session = %d %s, want 409 session_busy", status, refusal.Code)
	}
	runner.report(finished("end_turn", &store.Usage{TotalTokens: 18, InputTokens: 11, OutputTokens: 7}))

	events := watching()
	expected := []string{"turn.started", "message.delta", "message.delta", "tool.call", "tool.update", "session.update", "turn.completed"}
	if !slices.Equal(types(events), expected) {
		t.Fatalf("event types = %v, want %v", types(events), expected)
	}
	if named := events[len(events)-2].Data; named != `{"title":"Greeting"}` {
		t.Errorf("session.update data = %s, want the title", named)
	}
	var last struct {
		StopReason string           `json:"stop_reason"`
		Usage      map[string]int64 `json:"usage"`
	}
	if err := json.Unmarshal([]byte(events[len(events)-1].Data), &last); err != nil {
		t.Fatal(err)
	}
	usage := map[string]int64{"total_tokens": 18, "input_tokens": 11, "output_tokens": 7}
	if last.StopReason != "end_turn" || !maps.Equal(last.Usage, usage) {
		t.Errorf("last event data = %s", events[len(events)-1].Data)
	}

	record := s.turn(accepted.ID)
	if record.State != "completed" || record.StartedAt == nil || record.EndedAt == nil || record.StopReason != "end_turn" ||
		!maps.Equal(record.Usage, usage) || record.Failure != nil {
		t.Errorf("unexpected turn record %+v", record)
	}
	said := items(t, record)
	if len(said) != 2 || said[0]["type"] != "message" || said[0]["text"] != "Hello there" ||
		said[1]["type"] != "tool_call" || said[1]["title"] != "ls" || said[1]["status"] != "completed" || said[1]["output"] != "a.txt" {
		t.Errorf("turn items = %v", said)
	}
	if origin := s.origin(token, true); origin != nil {
		t.Errorf("origin of an ended turn's token = %v, want nil", origin)
	}
	// The title is the client's to set: the session keeps its own.
	var kept struct{ Title string }
	if status, _ := s.request("GET", "/v1/sessions/"+session, "", &kept); status != http.StatusOK || kept.Title != "" {
		t.Errorf("session after its harness named it = %d with the title %q, want its own empty title", status, kept.Title)
	}

	// The next turn runs in the same sandbox, where the harness keeps its
	// session.
	next := s.send(session, "and again")
	if next.Seq != 2 {
		t.Errorf("second turn seq = %d, want 2", next.Seq)
	}
	again := aviary.given(next.ID)
	if again.Text != "and again" || again.Sandbox != runner.Sandbox || len(aviary.service.Made()) != 1 {
		t.Errorf("next runner turn = %+v, sandboxes %v; want the same sandbox", again.Turn, aviary.service.Made())
	}
}

// elsewhere is an event log in which another Supervisor holds the lease on
// every turn, so that the Supervisors of the test follow none.
type elsewhere struct{ events.Log }

func (elsewhere) Follow(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error) {
	return false, nil
}

// A turn that has left the queue runs, as the API tells it, also before a
// Supervisor follows it.
func TestATurnThatLeftTheQueueIsRunning(t *testing.T) {
	t.Parallel()
	aviary := install(t)
	aviary.log = elsewhere{aviary.log}
	s := aviary.supervisor()
	session := s.session()

	accepted := s.send(session, "hello")
	eventually(t, soon, "the turn is not dispatched", func() bool {
		stored, err := aviary.records.Turn(context.Background(), uuid.MustParse(accepted.ID))
		if err != nil {
			t.Fatal(err)
		}
		return stored.State == store.TurnDispatching
	})
	if record := s.turn(accepted.ID); record.State != "running" || record.StartedAt != nil || record.EndedAt != nil {
		t.Errorf("dispatched turn = %+v, want running, and neither started nor ended", record)
	}
	var listed struct{ Turns []turn }
	if status, _ := s.request("GET", "/v1/sessions/"+session+"/turns", "", &listed); status != http.StatusOK ||
		len(listed.Turns) != 1 || listed.Turns[0].State != "running" {
		t.Errorf("turns of the session = %d %+v, want the turn as running", status, listed.Turns)
	}
	var read struct {
		LastTurn turn `json:"last_turn"`
	}
	if status, _ := s.request("GET", "/v1/sessions/"+session, "", &read); status != http.StatusOK || read.LastTurn.State != "running" {
		t.Errorf("newest turn of the session = %d %+v, want it as running", status, read.LastTurn)
	}
	// It is told to stop as a turn that runs is.
	var stopping turn
	if status, _ := s.request("POST", "/v1/turns/"+accepted.ID+"/cancel", "", &stopping); status != http.StatusAccepted ||
		stopping.State != "running" || stopping.CancelRequestedAt == nil {
		t.Errorf("cancel dispatched turn = %d %+v, want 202 with the turn as running and asked to stop", status, stopping)
	}
}

// positions returns the queue positions of the turn.queued events.
func positions(t *testing.T, events []event) []int {
	t.Helper()
	positions := []int{}
	for _, event := range events {
		if event.Type != "turn.queued" {
			continue
		}
		var data struct{ Position int }
		if err := json.Unmarshal([]byte(event.Data), &data); err != nil {
			t.Fatal(err)
		}
		positions = append(positions, data.Position)
	}
	return positions
}

// Turns beyond the concurrency limit are queued in order, and are told their
// position whenever it changes.
func TestQueue(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) { config.MaxConcurrentTurns = 1 })
	s := aviary.supervisor()
	// told returns the distinct positions that a turn was told.
	told := func(events []event) []int { return slices.Compact(positions(t, events)) }
	ending := func(events []event) string { return events[len(events)-1].Type }

	lost := s.session()
	first := s.send(lost, "one")
	running := aviary.given(first.ID)
	running.report(started("harness-session"))
	second, third, fourth := s.send(s.session(), "two"), s.send(s.session(), "three"), s.send(s.session(), "four")
	for _, waiting := range []turn{second, third, fourth} {
		if state := s.turn(waiting.ID).State; state != "queued" {
			t.Errorf("turn beyond the limit is %s, want queued", state)
		}
	}

	// A queued turn is cancelled at once and never runs.
	var cancelled turn
	if status, _ := s.request("POST", "/v1/turns/"+second.ID+"/cancel", "", &cancelled); status != http.StatusOK || cancelled.State != "cancelled" {
		t.Errorf("cancel queued turn = %d %+v, want 200 cancelled", status, cancelled)
	}
	if events := s.follow(second.ID); ending(events) != "turn.cancelled" || len(types(events)) != 1 || !slices.Equal(told(events), []int{1}) {
		t.Errorf("events of the cancelled queued turn = %v", events)
	}
	if record := s.turn(second.ID); record.StartedAt != nil || record.EndedAt == nil || len(record.Items) != 0 || record.Failure != nil {
		t.Errorf("turn that never ran = %+v", record)
	}

	// The running turn loses its sandbox and fails, and the next one runs.
	running.lose()
	if events := s.follow(first.ID); ending(events) != "turn.failed" {
		t.Errorf("turn without a sandbox ended with %s, want turn.failed", ending(events))
	}
	if record := s.turn(first.ID); record.State != "failed" || record.Failure["reason"] != "sandbox_lost" {
		t.Errorf("turn without a sandbox = %+v, want failed with sandbox_lost", record)
	}
	s.run(third.ID, "done")
	s.run(fourth.ID, "done")

	// The positions went down when the cancelled turn left the queue and
	// when the running turn ended.
	for _, waited := range []struct {
		turn   turn
		places []int
	}{{third, []int{2, 1}}, {fourth, []int{3, 2, 1}}} {
		events := s.follow(waited.turn.ID)
		if ending(events) != "turn.completed" || !slices.Equal(told(events), waited.places) {
			t.Errorf("turn %q ended with %s, queue positions %v, want %v",
				waited.turn.Text, ending(events), told(events), waited.places)
		}
	}

	// The session of the failed turn continues in a new sandbox.
	next := s.send(lost, "again")
	s.run(next.ID, "done")
	if events := s.follow(next.ID); ending(events) != "turn.completed" || next.Seq != 2 {
		t.Errorf("turn %d after the failure ended with %s, want turn 2 completed", next.Seq, ending(events))
	}
}

// The queue is limited in length and in waiting time.
func TestQueueLimits(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) {
		config.MaxConcurrentTurns = 1
		config.MaxQueuedTurns = 1
		config.MaxQueueWait = time.Second
	})
	s := aviary.supervisor()

	running := s.send(s.session(), "one")
	aviary.given(running.ID)
	waiting := s.send(s.session(), "two")
	var refusal struct{ Code string }
	status, header := s.request("POST", "/v1/sessions/"+s.session()+"/messages", `{"text": "three"}`, &refusal)
	if status != http.StatusTooManyRequests || refusal.Code != "queue_full" || header.Get("Retry-After") == "" {
		t.Errorf("send to a full queue = %d %s, Retry-After %q; want 429 queue_full with Retry-After",
			status, refusal.Code, header.Get("Retry-After"))
	}

	events := s.follow(waiting.ID)
	if !slices.Equal(types(events), []string{"turn.expired"}) {
		t.Errorf("events of the expired turn = %v", events)
	}
	if record := s.turn(waiting.ID); record.State != "expired" || record.EndedAt == nil || record.StartedAt != nil {
		t.Errorf("expired turn = %+v", record)
	}
	if status, _ := s.request("POST", "/v1/turns/"+waiting.ID+"/cancel", "", &refusal); status != http.StatusConflict || refusal.Code != "turn_ended" {
		t.Errorf("cancel expired turn = %d %s, want 409 turn_ended", status, refusal.Code)
	}
	if given := aviary.service.Given(); len(given) != 1 {
		t.Errorf("runners were given %v, want only the running turn", given)
	}
}
