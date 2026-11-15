package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/events"
	"aviary/supervisor/internal/store"
	"aviary/supervisor/internal/testenv"
)

var ctx = context.Background()

// said is the data of the event with which an agent says the text.
func said(text string) any {
	return map[string]string{"text": text}
}

// all returns the events of the turn that Valkey holds.
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

// texts returns what the events say, each as its type and its data.
func texts(batch []events.Event) []string {
	texts := []string{}
	for _, event := range batch {
		texts = append(texts, event.Type+" "+event.Data)
	}
	return texts
}

// words returns so many words, from w1 on, each with a space after it.
func words(from, to int) []string {
	var words []string
	for i := from; i <= to; i++ {
		words = append(words, fmt.Sprintf("w%d ", i))
	}
	return words
}

func TestReceive(t *testing.T) {
	t.Parallel()
	streams, turn := testenv.Events(t), uuid.New()
	if received, err := streams.Received(ctx, turn); err != nil || received != 0 {
		t.Fatalf("of a turn that has not begun, message %d is recorded: %v", received, err)
	}
	for _, step := range []struct {
		seq      uint64
		text     string
		gap      bool
		received uint64
	}{
		{seq: 1, text: "one", received: 1},
		// A runner sends a message again that it did not hear was recorded.
		{seq: 1, text: "one", received: 1},
		// A message without an event, such as the one that ends the turn.
		{seq: 2, received: 2},
		{seq: 4, text: "four", gap: true, received: 2},
		{seq: 3, text: "three", received: 3},
		{seq: 2, received: 3},
	} {
		var err error
		if step.text == "" {
			err = streams.Receive(ctx, turn, step.seq, "", nil)
		} else {
			err = streams.Receive(ctx, turn, step.seq, "message.delta", said(step.text))
		}
		if errors.Is(err, events.ErrGap) != step.gap || err != nil && !step.gap {
			t.Fatalf("message %d: %v", step.seq, err)
		}
		if received, err := streams.Received(ctx, turn); err != nil || received != step.received {
			t.Fatalf("after message %d, message %d is the last that is recorded: %v", step.seq, received, err)
		}
	}
	recorded := all(t, streams, turn)
	expected := []string{`message.delta {"text":"one"}`, `message.delta {"text":"three"}`}
	if !slices.Equal(texts(recorded), expected) {
		t.Errorf("the events are %q, not %q", texts(recorded), expected)
	}
	for _, event := range recorded {
		if !events.IsID(event.ID) {
			t.Errorf("the event has the ID %q", event.ID)
		}
	}
}

// A runner that lost its Supervisor sends again what it did not hear was
// recorded, while the Supervisor that it lost may still be recording it.
func TestReceiveFromSupervisorsAtOnce(t *testing.T) {
	t.Parallel()
	turn, answer := uuid.New(), words(1, 300)
	var receiving sync.WaitGroup
	for range 3 {
		streams := testenv.Events(t)
		receiving.Go(func() {
			for i, word := range answer {
				if err := streams.Receive(ctx, turn, uint64(i+1), "message.delta", said(word)); err != nil {
					t.Errorf("message %d: %v", i+1, err)
					return
				}
			}
		})
	}
	receiving.Wait()
	var recorded []string
	for _, event := range all(t, testenv.Events(t), turn) {
		var data struct{ Text string }
		if err := json.Unmarshal([]byte(event.Data), &data); err != nil {
			t.Fatal(err)
		}
		recorded = append(recorded, data.Text)
	}
	if !slices.Equal(recorded, answer) {
		t.Errorf("%d events are recorded of %d messages that three Supervisors each got: %q", len(recorded), len(answer), recorded)
	}
}

func TestRead(t *testing.T) {
	t.Parallel()
	streams, turn := testenv.Events(t), uuid.New()
	for _, word := range words(1, 250) {
		if err := streams.Append(ctx, turn, "message.delta", said(word)); err != nil {
			t.Fatal(err)
		}
	}
	// A reader gets the events in batches and goes on from the last of each.
	after := events.Start
	for _, expected := range []int{100, 100, 50, 0} {
		batch, err := streams.Read(ctx, turn, after, time.Millisecond)
		if err != nil || len(batch) != expected {
			t.Fatalf("a read returned %d events, not %d: %v", len(batch), expected, err)
		}
		if len(batch) > 0 {
			after = batch[len(batch)-1].ID
		}
	}

	// A reader that waits gets the next event when it comes.
	go func() {
		time.Sleep(50 * time.Millisecond)
		if err := streams.Append(ctx, turn, events.TurnCompleted, struct{}{}); err != nil {
			t.Error(err)
		}
	}()
	began := time.Now()
	batch, err := streams.Read(ctx, turn, after, 10*time.Second)
	if err != nil || len(batch) != 1 || batch[0].Type != events.TurnCompleted || time.Since(began) > 5*time.Second {
		t.Errorf("a reader that waited got %v after %s: %v", batch, time.Since(began), err)
	}

	// A reader of a turn without events waits for as long as it said.
	began = time.Now()
	if batch, err := streams.Read(ctx, uuid.New(), events.Start, 100*time.Millisecond); err != nil || len(batch) != 0 ||
		time.Since(began) < 100*time.Millisecond {
		t.Errorf("a reader of a turn without events got %v after %s: %v", batch, time.Since(began), err)
	}
}

func TestItems(t *testing.T) {
	t.Parallel()
	streams, turn, client := testenv.Events(t), uuid.New(), testenv.Valkey(t)
	// More events than are read at once, with a tool call among them.
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
	// An event that cannot be read. The turn has its items all the same.
	err := client.Do(ctx, client.B().Xadd().Key("turn:"+turn.String()).Id("*").
		FieldValue().FieldValue("type", "message.delta").FieldValue("data", `{"text": `).Build()).Error()
	if err != nil {
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
		t.Fatalf("the turn has %d items, not a message, a tool call and a message", len(items))
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
			t.Errorf("item %d is %.200s", i+1, items[i])
		}
	}

	if items, err := streams.Items(ctx, uuid.New()); err != nil || len(items) != 0 {
		t.Errorf("a turn without events has the items %s: %v", items, err)
	}
}

func TestAppendLast(t *testing.T) {
	t.Parallel()
	streams, client := testenv.Events(t), testenv.Valkey(t)
	// kept returns for how many seconds more Valkey keeps the key.
	kept := func(key string) int64 {
		seconds, err := client.Do(ctx, client.B().Ttl().Key(key).Build()).AsInt64()
		if err != nil {
			t.Fatal(err)
		}
		return seconds
	}

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
		t.Errorf("the events of the turn that ended are %q, not %q", recorded, expected)
	}
	// What Valkey holds of a turn goes when its readers have had their time.
	for _, key := range []string{"turn:" + ran.ID.String(), "{turn:" + ran.ID.String() + "}:received"} {
		if seconds := kept(key); seconds <= 0 || seconds > 3600 {
			t.Errorf("Valkey keeps %s for %d seconds", key, seconds)
		}
	}

	// A turn that never ran has only its last event.
	waited := store.Turn{ID: uuid.New(), State: store.TurnExpired}
	if err := streams.AppendLast(ctx, waited); err != nil {
		t.Fatal(err)
	}
	if recorded := texts(all(t, streams, waited.ID)); !slices.Equal(recorded, []string{`turn.expired {}`}) {
		t.Errorf("the events of the turn that never ran are %q", recorded)
	}
	if seconds := kept("turn:" + waited.ID.String()); seconds <= 0 || seconds > 3600 {
		t.Errorf("Valkey keeps the events of the turn that never ran for %d seconds", seconds)
	}
}

// A request to cancel a turn reaches one Supervisor, and the runner of the
// turn is connected to another.
func TestWatchCancels(t *testing.T) {
	t.Parallel()
	asked, connected, client := testenv.Events(t), testenv.Events(t), testenv.Valkey(t)
	watching, stop := context.WithCancel(ctx)
	told := make(chan uuid.UUID, 100)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		connected.WatchCancels(watching, func(turn uuid.UUID) { told <- turn })
	}()

	// What is announced before the other Supervisor listens is lost, so a
	// turn is announced until it is heard.
	announce := func(turn uuid.UUID) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for heard := false; !heard; {
			if err := asked.AnnounceCancel(ctx, turn); err != nil {
				t.Fatal(err)
			}
			select {
			case heard = <-heardOf(told, turn):
			case <-deadline:
				t.Fatal("the other Supervisor was not told of the turn to cancel")
			}
		}
	}
	announce(uuid.New())
	// Something else on the channel is passed over.
	if err := client.Do(ctx, client.B().Publish().Channel("turns:cancel").Message("not a turn").Build()).Error(); err != nil {
		t.Fatal(err)
	}
	announce(uuid.New())

	stop()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Error("the Supervisor went on watching after it was stopped")
	}
}

// heardOf tells whether the turn is among those that were told of within a
// moment. The tests share the channel, so the turns of others come by.
func heardOf(told <-chan uuid.UUID, turn uuid.UUID) <-chan bool {
	heard := make(chan bool, 1)
	go func() {
		moment := time.After(20 * time.Millisecond)
		for {
			select {
			case other := <-told:
				if other == turn {
					heard <- true
					return
				}
			case <-moment:
				heard <- false
				return
			}
		}
	}()
	return heard
}

// Without an answer from Valkey a read fails. A read that was tried again
// until Valkey is back would leave the reader of a turn's events waiting,
// with no word of what is wrong.
func TestReadFailsWithoutValkey(t *testing.T) {
	t.Parallel()
	address, err := url.Parse(testenv.ValkeyURL(t))
	if err != nil {
		t.Fatal(err)
	}
	// The connections go through a relay that the test can break.
	relay, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	valkey := address.Host
	var mu sync.Mutex
	var connections []net.Conn
	go func() {
		for {
			incoming, err := relay.Accept()
			if err != nil {
				return
			}
			outgoing, err := net.Dial("tcp", valkey)
			if err != nil {
				incoming.Close()
				continue
			}
			mu.Lock()
			connections = append(connections, incoming, outgoing)
			mu.Unlock()
			go io.Copy(outgoing, incoming)
			go io.Copy(incoming, outgoing)
		}
	}()
	cut := func() {
		relay.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, connection := range connections {
			connection.Close()
		}
	}
	t.Cleanup(cut)

	address.Host = relay.Addr().String()
	streams, err := events.Open(address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(streams.Close)
	turn := uuid.New()
	if err := streams.Append(ctx, turn, "message.delta", said("hello")); err != nil {
		t.Fatal(err)
	}
	if recorded := all(t, streams, turn); len(recorded) != 1 {
		t.Fatalf("%d events were read through the relay", len(recorded))
	}

	cut()
	failed := make(chan error, 1)
	go func() {
		_, err := streams.Read(ctx, turn, events.Start, time.Second)
		failed <- err
	}()
	select {
	case err := <-failed:
		if err == nil {
			t.Error("a read without Valkey returned no error")
		}
	case <-time.After(10 * time.Second):
		t.Error("a read without Valkey goes on waiting")
	}
}
