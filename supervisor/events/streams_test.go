package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/events"
	"aviary/supervisor/events/memory"
	"aviary/supervisor/store"
)

var ctx = context.Background()

// said returns the data of a message.delta event.
func said(text string) any {
	return map[string]string{"text": text}
}

// all returns the events of the turn that the log holds.
func all(t *testing.T, streams *events.Streams, turn uuid.UUID) []events.Event {
	t.Helper()
	var all []events.Event
	after := events.Start
	for {
		batch, err := streams.Read(ctx, turn, after, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 {
			return all
		}
		all = append(all, batch...)
		after = batch[len(batch)-1].ID
	}
}

// texts returns the type and the data of each event.
func texts(batch []events.Event) []string {
	texts := []string{}
	for _, event := range batch {
		texts = append(texts, event.Type+" "+event.Data)
	}
	return texts
}

// words returns "w<i> " for each i from from to to.
func words(from, to int) []string {
	var words []string
	for i := from; i <= to; i++ {
		words = append(words, fmt.Sprintf("w%d ", i))
	}
	return words
}

// A turn that is rewound has only the event that says so, and takes the
// messages of its runner from the first again.
func TestRewind(t *testing.T) {
	t.Parallel()
	streams, turn := events.New(memory.New()), uuid.New()
	for seq, text := range []string{"one", "two"} {
		if err := streams.Receive(ctx, turn, uint64(seq+1), "message.delta", said(text)); err != nil {
			t.Fatal(err)
		}
	}
	if err := streams.Rewind(ctx, turn); err != nil {
		t.Fatal(err)
	}
	if received, err := streams.Received(ctx, turn); err != nil || received != 0 {
		t.Errorf("Received = %d, %v; want 0", received, err)
	}
	if err := streams.Receive(ctx, turn, 1, "message.delta", said("one")); err != nil {
		t.Errorf("Receive of the first message after Rewind: %v", err)
	}
	expected := []string{`turn.rewound {}`, `message.delta {"text":"one"}`}
	if recorded := all(t, streams, turn); !slices.Equal(texts(recorded), expected) {
		t.Errorf("events = %q, want %q", texts(recorded), expected)
	}
	// Rewinding a turn that the log does not hold is not an error.
	if err := streams.Rewind(ctx, uuid.New()); err != nil {
		t.Errorf("Rewind of an unknown turn: %v", err)
	}
}

// Events are stored as JSON, in the order of their sequence numbers.
func TestReceive(t *testing.T) {
	t.Parallel()
	streams, turn := events.New(memory.New()), uuid.New()
	for seq, step := range []struct {
		eventType string
		data      any
	}{
		{"message.delta", said("one")},
		// A message without an event, such as the one that ends the turn.
		{"", nil},
		{"tool.call", map[string]any{"id": "t1", "title": "ls"}},
	} {
		if err := streams.Receive(ctx, turn, uint64(seq+1), step.eventType, step.data); err != nil {
			t.Fatalf("message %d: %v", seq+1, err)
		}
	}
	if err := streams.Receive(ctx, turn, 5, "message.delta", said("five")); !errors.Is(err, events.ErrGap) {
		t.Errorf("Receive after a gap = %v, want ErrGap", err)
	}
	if received, err := streams.Received(ctx, turn); err != nil || received != 3 {
		t.Errorf("Received = %d, %v; want 3", received, err)
	}
	if err := streams.Append(ctx, turn, events.TurnStarted, struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := streams.AppendQueued(ctx, turn, 2); err != nil {
		t.Fatal(err)
	}
	recorded := all(t, streams, turn)
	expected := []string{`message.delta {"text":"one"}`, `tool.call {"id":"t1","title":"ls"}`, `turn.started {}`, `turn.queued {"position":2}`}
	if !slices.Equal(texts(recorded), expected) {
		t.Errorf("events = %q, want %q", texts(recorded), expected)
	}
	for _, event := range recorded {
		if !streams.IsID(event.ID) {
			t.Errorf("IsID(%q) = false", event.ID)
		}
	}
	if streams.IsID("not an ID") {
		t.Error(`IsID("not an ID") = true`)
	}
	// Data that cannot be encoded as JSON is refused.
	if err := streams.Receive(ctx, turn, 4, "message.delta", func() {}); err == nil {
		t.Error("Receive of data that cannot be encoded as JSON succeeded")
	}
}

func TestItems(t *testing.T) {
	t.Parallel()
	log := memory.New()
	streams, turn := events.New(log), uuid.New()
	// More events than one read returns, with a tool call in between.
	before, after := words(1, 1200), words(1201, 2500)
	seq := uint64(0)
	receive := func(eventType string, data any) {
		seq++
		if err := streams.Receive(ctx, turn, seq, eventType, data); err != nil {
			t.Fatal(err)
		}
	}
	for _, word := range before {
		receive("message.delta", said(word))
	}
	receive("tool.call", map[string]string{"id": "t1", "title": "ls"})
	// An event that cannot be decoded, which Items skips.
	if err := log.Append(ctx, turn, "message.delta", `{"text": `); err != nil {
		t.Fatal(err)
	}
	for _, word := range after {
		receive("message.delta", said(word))
	}

	items, err := streams.Items(ctx, turn)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want a message, a tool call and a message", len(items))
	}
	for i, expected := range []map[string]string{
		{"type": "message", "text": strings.Join(before, "")},
		{"type": "tool_call", "id": "t1", "title": "ls"},
		{"type": "message", "text": strings.Join(after, "")},
	} {
		var item map[string]string
		if err := json.Unmarshal(items[i], &item); err != nil {
			t.Fatal(err)
		}
		if len(item) != len(expected) || item["type"] != expected["type"] || item["text"] != expected["text"] ||
			item["id"] != expected["id"] || item["title"] != expected["title"] {
			t.Errorf("unexpected item %d: %.200s", i+1, items[i])
		}
	}

	if items, err := streams.Items(ctx, uuid.New()); err != nil || len(items) != 0 {
		t.Errorf("Items of a turn without events = %s, %v", items, err)
	}
}

// expiring is a log that records the durations passed to Expire.
type expiring struct {
	events.Log
	mu   sync.Mutex
	kept map[uuid.UUID]time.Duration
}

func (e *expiring) Expire(ctx context.Context, turnID uuid.UUID, after time.Duration) error {
	e.mu.Lock()
	e.kept[turnID] = after
	e.mu.Unlock()
	return e.Log.Expire(ctx, turnID, after)
}

func TestAppendLast(t *testing.T) {
	t.Parallel()
	log := &expiring{Log: memory.New(), kept: map[uuid.UUID]time.Duration{}}
	streams := events.New(log)

	reason := "end_turn"
	ran := store.Turn{ID: uuid.New(), State: store.TurnCompleted, StopReason: &reason}
	if err := streams.Receive(ctx, ran.ID, 1, "message.delta", said("hello")); err != nil {
		t.Fatal(err)
	}
	if err := streams.AppendLast(ctx, ran); err != nil {
		t.Fatal(err)
	}
	expected := []string{`message.delta {"text":"hello"}`, `turn.completed {"stop_reason":"end_turn"}`}
	if recorded := texts(all(t, streams, ran.ID)); !slices.Equal(recorded, expected) {
		t.Errorf("events = %q, want %q", recorded, expected)
	}
	// A turn that never ran has only its last event.
	waited := store.Turn{ID: uuid.New(), State: store.TurnExpired}
	if err := streams.AppendLast(ctx, waited); err != nil {
		t.Fatal(err)
	}
	if recorded := texts(all(t, streams, waited.ID)); !slices.Equal(recorded, []string{`turn.expired {}`}) {
		t.Errorf("events of a turn that never ran = %q", recorded)
	}
	// The turn's data expires after a grace period for its readers.
	for _, turn := range []uuid.UUID{ran.ID, waited.ID} {
		if kept := log.kept[turn]; kept <= 0 || kept > time.Hour {
			t.Errorf("Expire after %s, want a positive duration of at most an hour", kept)
		}
	}
}

// Cancels, answers and changes are announced to the other Supervisors on
// separate channels.
func TestAnnounce(t *testing.T) {
	t.Parallel()
	log := memory.New()
	asked, connected := events.New(log), events.New(log)
	watching, stop := context.WithCancel(ctx)
	defer stop()
	cancelled, answered, changed := make(chan uuid.UUID, 10), make(chan uuid.UUID, 10), make(chan uuid.UUID, 10)
	go connected.WatchCancels(watching, func(turn uuid.UUID) { cancelled <- turn })
	go connected.WatchAnswers(watching, func(turn uuid.UUID) { answered <- turn })
	go connected.WatchChanges(watching, func(session uuid.UUID) { changed <- session })

	// Announcements made before the watcher subscribes are lost, so the ID
	// is announced until it is heard.
	heard := func(announce func(context.Context, uuid.UUID) error, on chan uuid.UUID, others ...chan uuid.UUID) {
		t.Helper()
		id := uuid.New()
		deadline := time.After(10 * time.Second)
		for {
			if err := announce(ctx, id); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-on:
				if got != id {
					t.Errorf("unexpected announced ID %s", got)
				}
				for _, other := range others {
					select {
					case got := <-other:
						t.Fatalf("%s announced on the wrong channel", got)
					default:
					}
				}
				return
			case <-time.After(20 * time.Millisecond):
			case <-deadline:
				t.Fatal("announcement not received")
			}
		}
	}
	heard(asked.AnnounceCancel, cancelled, answered, changed)
	heard(asked.AnnounceAnswer, answered, cancelled, changed)
	heard(asked.AnnounceChange, changed, cancelled, answered)
}

// kind is a plugin that opens logs in memory.
type kind struct {
	schemes []string
	opened  *[]string
}

func (k kind) Schemes() []string { return k.schemes }

func (k kind) Open(address string) (events.Log, error) {
	if strings.Contains(address, "unreachable") {
		return nil, errors.New("no log is there")
	}
	*k.opened = append(*k.opened, k.schemes[0]+" "+address)
	return memory.New(), nil
}

// Open selects the plugin by the URL scheme.
func TestOpen(t *testing.T) {
	t.Parallel()
	var opened []string
	plugins := []events.Plugin{kind{[]string{"first", "firsts"}, &opened}, kind{[]string{"second"}, &opened}}
	for _, address := range []string{"first://host:1", "firsts://host:2", "second://user:password@host:3"} {
		streams, err := events.Open(address, plugins...)
		if err != nil {
			t.Fatalf("%s: %v", address, err)
		}
		if err := streams.Append(ctx, uuid.New(), events.TurnStarted, struct{}{}); err != nil {
			t.Errorf("Append to %s: %v", address, err)
		}
		streams.Close()
	}
	if expected := []string{"first first://host:1", "first firsts://host:2", "second second://user:password@host:3"}; !slices.Equal(opened, expected) {
		t.Errorf("opened = %q, want %q", opened, expected)
	}

	// Errors say why without revealing the password.
	for address, why := range map[string]string{
		"third://default:secret@host:6379":        "first, firsts, second",
		"host:6379":                               "first, firsts, second",
		"first://default:secret@host:port":        "parse",
		"second://default:secret@unreachable:637": "no log is there",
	} {
		_, err := events.Open(address, plugins...)
		if err == nil || !strings.Contains(err.Error(), why) || strings.Contains(err.Error(), "secret") {
			t.Errorf("Open(%s) error = %v", address, err)
		}
	}
	if _, err := events.Open("first://host:1"); err == nil {
		t.Error("Open without plugins succeeded")
	}
}
