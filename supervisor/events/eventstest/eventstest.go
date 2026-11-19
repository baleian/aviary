// Package eventstest tests a log of the events of the turns: what holds of
// every log, whatever plugin connects to it. The tests of a plugin run it
// against a log of the plugin's kind.
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

// texts returns what the events say, each as its type and its data.
func texts(batch []events.Event) []string {
	texts := []string{}
	for _, event := range batch {
		texts = append(texts, event.Type+" "+event.Data)
	}
	return texts
}

// words returns so many words, from w1 on, each as the data of an event.
func words(from, to int) []string {
	var words []string
	for i := from; i <= to; i++ {
		words = append(words, fmt.Sprintf(`{"text":"w%d "}`, i))
	}
	return words
}

// Run tests the log that open connects to. Every call of open is one more
// connection to the same log, as every Supervisor of an installation has
// one. The log may hold what other tests keep in it: a test keeps to turns
// of its own.
func Run(t *testing.T, open func(t *testing.T) events.Log) {
	t.Run("a message of a runner is recorded once and in its order", func(t *testing.T) {
		t.Parallel()
		log, turn := open(t), uuid.New()
		if received, err := log.Received(ctx, turn); err != nil || received != 0 {
			t.Fatalf("of a turn that has not begun, message %d is recorded: %v", received, err)
		}
		for _, step := range []struct {
			seq      uint64
			text     string
			gap      bool
			received uint64
		}{
			{seq: 1, text: "one", received: 1},
			// A runner sends a message again that it did not hear was
			// recorded.
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
				t.Fatalf("after message %d, message %d is the last that is recorded: %v", step.seq, received, err)
			}
		}
		recorded := all(t, log, turn)
		if expected := []string{"message.delta one", "message.delta three"}; !slices.Equal(texts(recorded), expected) {
			t.Errorf("the events are %q, not %q", texts(recorded), expected)
		}
		for _, event := range recorded {
			if !log.IsID(event.ID) {
				t.Errorf("the event has the ID %q, which the log says is none", event.ID)
			}
		}
		for _, none := range []string{"an event", "$", "*", "-", "+", "(1"} {
			if log.IsID(none) {
				t.Errorf("the log says that %q is the ID of an event", none)
			}
		}
	})

	// A runner that lost its Supervisor sends again what it did not hear was
	// recorded, while the Supervisor that it lost may still be recording it.
	t.Run("a message that several Supervisors get at once is recorded once", func(t *testing.T) {
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
			t.Errorf("%d events are recorded of %d messages that three Supervisors each got: %q", len(recorded), len(answer), recorded)
		}
	})

	t.Run("a reader goes on from where it was, and waits for what comes", func(t *testing.T) {
		t.Parallel()
		log, turn, said := open(t), uuid.New(), words(1, 2500)
		for _, word := range said {
			if err := log.Append(ctx, turn, "message.delta", word); err != nil {
				t.Fatal(err)
			}
		}
		// A reader gets the events in order, some at a time, whether it
		// waits for more or not.
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
				t.Errorf("a reader that waits %s read %d events of %d", wait, len(read), len(said))
			}
		}
		last := all(t, log, turn)[len(said)-1].ID

		// A reader that waits gets the next event when it comes, which may
		// be from another Supervisor.
		go func() {
			time.Sleep(50 * time.Millisecond)
			if err := open(t).Append(ctx, turn, "turn.completed", "{}"); err != nil {
				t.Error(err)
			}
		}()
		began := time.Now()
		batch, err := log.Read(ctx, turn, last, 10*time.Second)
		if err != nil || len(batch) != 1 || batch[0].Type != "turn.completed" || time.Since(began) > 5*time.Second {
			t.Errorf("a reader that waited got %v after %s: %v", batch, time.Since(began), err)
		}

		// A reader of a turn without events waits for as long as it said,
		// and one that does not wait is answered at once.
		began = time.Now()
		if batch, err := log.Read(ctx, uuid.New(), events.Start, 100*time.Millisecond); err != nil || len(batch) != 0 ||
			time.Since(began) < 100*time.Millisecond {
			t.Errorf("a reader of a turn without events got %v after %s: %v", batch, time.Since(began), err)
		}
		began = time.Now()
		if batch, err := log.Read(ctx, uuid.New(), events.Start, 0); err != nil || len(batch) != 0 || time.Since(began) > 5*time.Second {
			t.Errorf("a reader that does not wait got %v after %s: %v", batch, time.Since(began), err)
		}
	})

	t.Run("what the log holds of a turn goes when its time has passed", func(t *testing.T) {
		t.Parallel()
		log := open(t)
		// held tells whether the log holds anything of the turn.
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
			t.Errorf("at once, the log holds the turn that was to go at once: %v, the one that was to go soon: %v, and the one to keep: %v",
				held(gone), held(soon), held(kept))
		}
		deadline := time.Now().Add(10 * time.Second)
		for held(soon) {
			if time.Now().After(deadline) {
				t.Fatal("the log still holds a turn whose time has passed")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !held(kept) {
			t.Error("the log does not hold a turn whose time has not passed")
		}
		// A turn of which nothing is held has nothing to delete.
		if err := log.Expire(ctx, uuid.New(), time.Hour); err != nil {
			t.Errorf("a turn without events: %v", err)
		}
	})

	// A request to cancel a turn reaches one Supervisor, and the runner of
	// the turn is connected to another.
	t.Run("a turn is followed by one Supervisor at a time", func(t *testing.T) {
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
			t.Fatal("a turn that nobody follows was not given to the Supervisor that asked")
		}
		// It is given again to the Supervisor that has it, through any
		// connection, and to no other.
		if !follows(open(t), one, time.Minute) {
			t.Error("the Supervisor that follows the turn was not given it again")
		}
		if follows(open(t), other, time.Minute) {
			t.Error("another Supervisor was given a turn that is followed")
		}
		// Another turn is nothing to this one.
		if given, err := log.Follow(ctx, uuid.New(), other, time.Minute); err != nil || !given {
			t.Errorf("another turn was not given to the Supervisor that asked: %v", err)
		}

		// A Supervisor whose lease is over is taken to be gone.
		if !follows(log, one, 20*time.Millisecond) {
			t.Fatal("the Supervisor that follows the turn was not given it again")
		}
		time.Sleep(100 * time.Millisecond)
		if !follows(open(t), other, time.Minute) {
			t.Error("the turn of a Supervisor that is gone was not given to the next")
		}
		if follows(log, one, time.Minute) {
			t.Error("the Supervisor that was gone was given the turn again")
		}
	})

	t.Run("a turn that several Supervisors ask for at once is given to one", func(t *testing.T) {
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
			t.Errorf("of eight Supervisors that asked for a turn at once, %d were given it", given.Load())
		}
	})

	t.Run("what one Supervisor announces, the others are told", func(t *testing.T) {
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

		// What is announced before the other Supervisor listens is lost, so
		// a turn is announced until it is heard.
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
					t.Fatal("the other Supervisor was not told of the turn")
				}
			}
		}
		// What is announced on another channel is not for this watcher.
		stranger := uuid.New()
		if err := asked.Announce(ctx, other, stranger); err != nil {
			t.Fatal(err)
		}
		for moment, passed := time.After(100*time.Millisecond), false; !passed; {
			select {
			case turn := <-told:
				if turn == stranger {
					t.Error("the Supervisor was told of a turn that was announced on another channel")
				}
			case <-moment:
				passed = true
			}
		}

		stop()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("the Supervisor went on watching after it was stopped")
		}
	})
}
