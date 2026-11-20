// Package eventstest is a conformance test suite for implementations of
// events.Log. The tests of a plugin run it against a log of their kind.
package eventstest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/events"
)

var ctx = context.Background()

// all returns the events of the turn that the log holds.
func all(t *testing.T, log events.Log, turn uuid.UUID) []events.Event {
	t.Helper()
	var all []events.Event
	after := events.Start
	for {
		batch, err := log.Read(ctx, turn, after, 0)
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

// words returns the event data {"text":"w<i> "} for each i from from to to.
func words(from, to int) []string {
	var words []string
	for i := from; i <= to; i++ {
		words = append(words, fmt.Sprintf(`{"text":"w%d "}`, i))
	}
	return words
}

// Run tests the log that open connects to. Each call of open returns a
// connection to the same log, as each Supervisor of an installation has its
// own. The log may be shared with other tests, so each test uses turns of its
// own.
func Run(t *testing.T, open func(t *testing.T) events.Log) {
	t.Run("receive records each message once and in order", func(t *testing.T) {
		t.Parallel()
		log, turn := open(t), uuid.New()
		if received, err := log.Received(ctx, turn); err != nil || received != 0 {
			t.Fatalf("Received of a new turn = %d, %v; want 0", received, err)
		}
		for _, step := range []struct {
			seq      uint64
			text     string
			gap      bool
			received uint64
		}{
			{seq: 1, text: "one", received: 1},
			// A runner resends a message whose recording it did not hear of.
			{seq: 1, text: "one", received: 1},
			// A message without an event, such as the one that ends the
			// turn.
			{seq: 2, received: 2},
			{seq: 4, text: "four", gap: true, received: 2},
			{seq: 3, text: "three", received: 3},
			{seq: 2, received: 3},
		} {
			var err error
			if step.text == "" {
				err = log.Receive(ctx, turn, step.seq, "", "")
			} else {
				err = log.Receive(ctx, turn, step.seq, "message.delta", step.text)
			}
			if errors.Is(err, events.ErrGap) != step.gap || err != nil && !step.gap {
				t.Fatalf("message %d: %v", step.seq, err)
			}
			if received, err := log.Received(ctx, turn); err != nil || received != step.received {
				t.Fatalf("after message %d: Received = %d, %v", step.seq, received, err)
			}
		}
		recorded := all(t, log, turn)
		if expected := []string{"message.delta one", "message.delta three"}; !slices.Equal(texts(recorded), expected) {
			t.Errorf("events = %q, want %q", texts(recorded), expected)
		}
		for _, event := range recorded {
			if !log.IsID(event.ID) {
				t.Errorf("IsID(%q) = false", event.ID)
			}
		}
		for _, none := range []string{"an event", "$", "*", "-", "+", "(1"} {
			if log.IsID(none) {
				t.Errorf("IsID(%q) = true", none)
			}
		}
	})

	// A runner that loses its Supervisor resends the messages whose recording
	// it did not hear of, while that Supervisor may still be recording them.
	t.Run("concurrent receive records each message once", func(t *testing.T) {
		t.Parallel()
		turn, answer := uuid.New(), words(1, 300)
		var receiving sync.WaitGroup
		for range 3 {
			log := open(t)
			receiving.Go(func() {
				for i, word := range answer {
					if err := log.Receive(ctx, turn, uint64(i+1), "message.delta", word); err != nil {
						t.Errorf("message %d: %v", i+1, err)
						return
					}
				}
			})
		}
		receiving.Wait()
		var recorded []string
		for _, event := range all(t, open(t), turn) {
			recorded = append(recorded, event.Data)
		}
		if !slices.Equal(recorded, answer) {
			t.Errorf("recorded %d events for %d messages: %q", len(recorded), len(answer), recorded)
		}
	})

	t.Run("read resumes after an ID and waits for new events", func(t *testing.T) {
		t.Parallel()
		log, turn, said := open(t), uuid.New(), words(1, 2500)
		for _, word := range said {
			if err := log.Append(ctx, turn, "message.delta", word); err != nil {
				t.Fatal(err)
			}
		}
		// Reading in batches returns all events in order, with or without
		// waiting.
		for _, wait := range []time.Duration{0, time.Millisecond} {
			var read []string
			after := events.Start
			for {
				batch, err := log.Read(ctx, turn, after, wait)
				if err != nil {
					t.Fatal(err)
				}
				if len(batch) == 0 {
					break
				}
				for _, event := range batch {
					read = append(read, event.Data)
				}
				after = batch[len(batch)-1].ID
			}
			if !slices.Equal(read, said) {
				t.Errorf("wait %s: read %d events, want %d", wait, len(read), len(said))
			}
		}
		last := all(t, log, turn)[len(said)-1].ID

		// A waiting reader gets the next event as soon as it is appended,
		// also through another connection.
		go func() {
			time.Sleep(50 * time.Millisecond)
			if err := open(t).Append(ctx, turn, "turn.completed", "{}"); err != nil {
				t.Error(err)
			}
		}()
		began := time.Now()
		batch, err := log.Read(ctx, turn, last, 10*time.Second)
		if err != nil || len(batch) != 1 || batch[0].Type != "turn.completed" || time.Since(began) > 5*time.Second {
			t.Errorf("waiting Read = %v after %s, %v; want turn.completed promptly", batch, time.Since(began), err)
		}

		// Without events, Read waits for the full duration, or returns at
		// once if wait is 0.
		began = time.Now()
		if batch, err := log.Read(ctx, uuid.New(), events.Start, 100*time.Millisecond); err != nil || len(batch) != 0 ||
			time.Since(began) < 100*time.Millisecond {
			t.Errorf("Read of a turn without events = %v after %s, %v; want nothing after the full wait", batch, time.Since(began), err)
		}
		began = time.Now()
		if batch, err := log.Read(ctx, uuid.New(), events.Start, 0); err != nil || len(batch) != 0 || time.Since(began) > 5*time.Second {
			t.Errorf("Read without wait = %v after %s, %v; want nothing at once", batch, time.Since(began), err)
		}
	})

	t.Run("expire deletes the turn after the duration", func(t *testing.T) {
		t.Parallel()
		log := open(t)
		// held reports whether the log holds anything of the turn.
		held := func(turn uuid.UUID) bool {
			t.Helper()
			received, err := log.Received(ctx, turn)
			if err != nil {
				t.Fatal(err)
			}
			return received != 0 || len(all(t, log, turn)) != 0
		}
		begin := func() uuid.UUID {
			t.Helper()
			turn := uuid.New()
			if err := log.Receive(ctx, turn, 1, "message.delta", `{"text":"hello"}`); err != nil {
				t.Fatal(err)
			}
			return turn
		}

		kept, soon, gone := begin(), begin(), begin()
		if err := log.Expire(ctx, kept, time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := log.Expire(ctx, soon, 200*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if err := log.Expire(ctx, gone, 0); err != nil {
			t.Fatal(err)
		}
		if held(gone) || !held(soon) || !held(kept) {
			t.Errorf("held: expired at once %v, expiring soon %v, kept %v; want false, true, true",
				held(gone), held(soon), held(kept))
		}
		deadline := time.Now().Add(10 * time.Second)
		for held(soon) {
			if time.Now().After(deadline) {
				t.Fatal("turn not deleted after it expired")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !held(kept) {
			t.Error("turn deleted before it expired")
		}
		// Expiring a turn that the log does not hold is not an error.
		if err := log.Expire(ctx, uuid.New(), time.Hour); err != nil {
			t.Errorf("Expire of an unknown turn: %v", err)
		}
	})

	t.Run("follow grants the lease to one follower at a time", func(t *testing.T) {
		t.Parallel()
		log, turn := open(t), uuid.New()
		one, other := uuid.New(), uuid.New()
		follows := func(log events.Log, follower uuid.UUID, lease time.Duration) bool {
			t.Helper()
			given, err := log.Follow(ctx, turn, follower, lease)
			if err != nil {
				t.Fatal(err)
			}
			return given
		}
		if !follows(log, one, time.Minute) {
			t.Fatal("Follow of an unfollowed turn = false")
		}
		// The holder renews the lease through any connection, and others are
		// refused.
		if !follows(open(t), one, time.Minute) {
			t.Error("holder could not renew the lease")
		}
		if follows(open(t), other, time.Minute) {
			t.Error("another follower got a held lease")
		}
		// Leases are per turn.
		if given, err := log.Follow(ctx, uuid.New(), other, time.Minute); err != nil || !given {
			t.Errorf("Follow of another turn = false, %v", err)
		}

		// Once a lease expires, another Supervisor gets the turn.
		if !follows(log, one, 20*time.Millisecond) {
			t.Fatal("holder could not renew the lease")
		}
		time.Sleep(100 * time.Millisecond)
		if !follows(open(t), other, time.Minute) {
			t.Error("expired lease was not granted to another follower")
		}
		if follows(log, one, time.Minute) {
			t.Error("former holder got the lease back")
		}

		// Leave by a Supervisor that does not hold the lease does nothing.
		// Leave by the holder releases it at once.
		if err := open(t).Leave(ctx, turn, one); err != nil {
			t.Fatal(err)
		}
		if follows(log, one, time.Minute) {
			t.Error("Leave by a non-holder released the lease")
		}
		if err := log.Leave(ctx, turn, other); err != nil {
			t.Fatal(err)
		}
		if !follows(open(t), one, time.Minute) {
			t.Error("lease was not granted after the holder left")
		}
	})

	t.Run("concurrent follow grants the lease once", func(t *testing.T) {
		t.Parallel()
		turn := uuid.New()
		var given atomic.Int32
		var asked sync.WaitGroup
		for range 8 {
			log := open(t)
			asked.Go(func() {
				mine, err := log.Follow(ctx, turn, uuid.New(), time.Minute)
				if err != nil {
					t.Error(err)
				}
				if mine {
					given.Add(1)
				}
			})
		}
		asked.Wait()
		if given.Load() != 1 {
			t.Errorf("%d of 8 concurrent followers got the lease, want 1", given.Load())
		}
	})

	t.Run("announce reaches the watchers of the channel", func(t *testing.T) {
		t.Parallel()
		asked, connected := open(t), open(t)
		channel, other := "tests:"+uuid.NewString(), "tests:"+uuid.NewString()
		watching, stop := context.WithCancel(ctx)
		told := make(chan uuid.UUID, 100)
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			connected.Watch(watching, channel, func(turn uuid.UUID) { told <- turn })
		}()

		// Announcements made before the watcher subscribes are lost, so each
		// turn is announced until it is heard.
		for range 2 {
			turn, heard := uuid.New(), false
			deadline := time.After(10 * time.Second)
			for !heard {
				if err := asked.Announce(ctx, channel, turn); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-told:
					heard = got == turn
				case <-time.After(20 * time.Millisecond):
				case <-deadline:
					t.Fatal("announcement not received")
				}
			}
		}
		// Announcements on another channel are not delivered.
		stranger := uuid.New()
		if err := asked.Announce(ctx, other, stranger); err != nil {
			t.Fatal(err)
		}
		for moment, passed := time.After(100*time.Millisecond), false; !passed; {
			select {
			case turn := <-told:
				if turn == stranger {
					t.Error("received an announcement from another channel")
				}
			case <-moment:
				passed = true
			}
		}

		stop()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("Watch did not return after its context was canceled")
		}
	})
}
