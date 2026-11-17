package resp_test

import (
	"context"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/valkey-io/valkey-go"

	"aviary/supervisor/events"
	"aviary/supervisor/events/eventstest"
	"aviary/supervisor/events/resp"
	"aviary/supervisor/internal/testenv"
)

var ctx = context.Background()

// open connects to the log of the tests.
func open(t *testing.T) events.Log {
	t.Helper()
	log, err := resp.Plugin{}.Open(testenv.EventsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(log.Close)
	return log
}

func TestLog(t *testing.T) {
	eventstest.Run(t, open)
}

func TestIsID(t *testing.T) {
	t.Parallel()
	log := open(t)
	for id, is := range map[string]bool{
		"0-0":             true,
		"1763280000000-3": true,
		"":                false,
		"1763280000000":   false,
		"1763280000000-":  false,
		"-3":              false,
		"1-2-3":           false,
		"$":               false,
	} {
		if log.IsID(id) != is {
			t.Errorf("IsID(%q) is %t", id, !is)
		}
	}
}

func TestOpenKeepsThePasswordToItself(t *testing.T) {
	t.Parallel()
	_, err := resp.Plugin{}.Open("valkey://default:secret@events:port")
	if err == nil {
		t.Fatal("the plugin took a URL without a port number")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("the error tells the password: %v", err)
	}
}

// Something else on a channel than a turn is passed over.
func TestWatchPassesOverWhatIsNoTurn(t *testing.T) {
	t.Parallel()
	log, channel := open(t), "tests:"+uuid.NewString()
	options, err := valkey.ParseURL(testenv.EventsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	options.DisableCache = true
	client, err := valkey.NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	watching, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	told := make(chan uuid.UUID, 100)
	go log.Watch(watching, channel, func(turn uuid.UUID) { told <- turn })
	// What is announced before the watcher listens is lost, so a turn is
	// announced until it is heard.
	heard := func() {
		t.Helper()
		turn := uuid.New()
		deadline := time.After(10 * time.Second)
		for {
			if err := log.Announce(ctx, channel, turn); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-told:
				if got == turn {
					return
				}
			case <-time.After(20 * time.Millisecond):
			case <-deadline:
				t.Fatal("the watcher was not told of the turn")
			}
		}
	}
	heard()
	// The watcher listens now, and goes on after what is no turn.
	if err := client.Do(ctx, client.B().Publish().Channel(channel).Message("not a turn").Build()).Error(); err != nil {
		t.Fatal(err)
	}
	heard()
}

// Without an answer from the server a read fails. A read that was tried
// again until the server is back would leave the reader of a turn's events
// waiting, with no word of what is wrong.
func TestReadFailsWithoutTheServer(t *testing.T) {
	t.Parallel()
	address, err := url.Parse(testenv.EventsURL(t))
	if err != nil {
		t.Fatal(err)
	}
	// The connections go through a relay that the test can break.
	relay, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := address.Host
	var mu sync.Mutex
	var connections []net.Conn
	go func() {
		for {
			incoming, err := relay.Accept()
			if err != nil {
				return
			}
			outgoing, err := net.Dial("tcp", server)
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
	log, err := resp.Plugin{}.Open(address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(log.Close)
	turn := uuid.New()
	if err := log.Append(ctx, turn, "message.delta", `{"text":"hello"}`); err != nil {
		t.Fatal(err)
	}
	if recorded, err := log.Read(ctx, turn, events.Start, 0); err != nil || len(recorded) != 1 {
		t.Fatalf("%d events were read through the relay: %v", len(recorded), err)
	}

	cut()
	for _, wait := range []time.Duration{time.Second, 0} {
		failed := make(chan error, 1)
		go func() {
			_, err := log.Read(ctx, turn, events.Start, wait)
			failed <- err
		}()
		select {
		case err := <-failed:
			if err == nil {
				t.Errorf("a read that waits %s returned no error without the server", wait)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("a read that waits %s goes on waiting without the server", wait)
		}
	}
}
