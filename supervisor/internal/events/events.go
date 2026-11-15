// Package events keeps the events of each turn in a Valkey stream, so that
// any Supervisor can read them to a client.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/valkey-io/valkey-go"

	"aviary/supervisor/internal/store"
)

// The events that the Supervisor writes itself. The others come from the
// runner of the turn.
const (
	// TurnQueued reports that the turn was accepted and waits to run.
	TurnQueued = "turn.queued"
	// TurnStarted reports that the harness has been given the message.
	TurnStarted = "turn.started"
	// TurnCompleted, TurnFailed, TurnCancelled and TurnExpired report how
	// the turn ended. One of them is the last event of every turn.
	TurnCompleted = "turn.completed"
	TurnFailed    = "turn.failed"
	TurnCancelled = "turn.cancelled"
	TurnExpired   = "turn.expired"
)

// cancelChannel is the Valkey channel on which a Supervisor tells the others
// that a turn was asked to stop.
const cancelChannel = "turns:cancel"

// keptFor is how long the events of a turn are kept after its last event,
// for the clients that still read them. What the turn said is by then in the
// database.
const keptFor = time.Hour

// Start is the position before a turn's first event.
const Start = "0-0"

// readBatch is the most events that one Read returns.
const readBatch = 100

// Event is one event of a turn. Its ID orders the turn's events and names the
// position from which a reader continues.
type Event struct {
	ID   string
	Type string
	Data string // JSON
}

type Streams struct {
	client valkey.Client
}

// Open connects to the Valkey at address, a URL such as
// valkey://user:password@host:6379.
func Open(address string) (*Streams, error) {
	options, err := valkey.ParseURL(address)
	if err != nil {
		// A URL that cannot be parsed is quoted in the error, password included.
		var quoting *url.Error
		if errors.As(err, &quoting) {
			err = quoting.Err
		}
		return nil, fmt.Errorf("parse Valkey URL: %w", err)
	}
	// The Supervisor does not cache on the client side, and not every managed
	// Valkey supports it.
	options.DisableCache = true
	// The client would repeat a read for as long as Valkey is unreachable. A
	// read that fails at once lets the caller report it.
	options.DisableRetry = true
	client, err := valkey.NewClient(options)
	if err != nil {
		return nil, fmt.Errorf("connect to Valkey: %w", err)
	}
	return &Streams{client: client}, nil
}

func (s *Streams) Close() {
	s.client.Close()
}

// Append adds an event to the end of the turn's stream. The data is stored as
// JSON.
func (s *Streams) Append(ctx context.Context, turnID uuid.UUID, eventType string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.client.Do(ctx, s.client.B().Xadd().Key(key(turnID)).Id("*").
		FieldValue().FieldValue("type", eventType).FieldValue("data", string(encoded)).Build()).Error()
}

// AppendQueued tells a turn that waits to run its position in the queue,
// which is 1 for the turn that runs next.
func (s *Streams) AppendQueued(ctx context.Context, turnID uuid.UUID, position int) error {
	return s.Append(ctx, turnID, TurnQueued, struct {
		Position int `json:"position"`
	}{position})
}

// AnnounceCancel tells every Supervisor that the turn was asked to stop. The
// runner of the turn is connected to one of them.
func (s *Streams) AnnounceCancel(ctx context.Context, turnID uuid.UUID) error {
	return s.client.Do(ctx, s.client.B().Publish().Channel(cancelChannel).Message(turnID.String()).Build()).Error()
}

// WatchCancels calls cancel for every turn that a Supervisor announces as
// asked to stop, until ctx is done. It misses what is announced while Valkey
// cannot be reached.
func (s *Streams) WatchCancels(ctx context.Context, cancel func(turnID uuid.UUID)) {
	for ctx.Err() == nil {
		err := s.client.Receive(ctx, s.client.B().Subscribe().Channel(cancelChannel).Build(),
			func(message valkey.PubSubMessage) {
				if turnID, err := uuid.Parse(message.Message); err == nil {
					cancel(turnID)
				}
			})
		if ctx.Err() != nil {
			return
		}
		slog.WarnContext(ctx, "watch for cancelled turns", "error", err)
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
		}
	}
}

// Last returns the last event of a turn that has ended.
func Last(turn store.Turn) (eventType string, data any) {
	switch turn.State {
	case store.TurnCompleted:
		return TurnCompleted, struct {
			StopReason *string      `json:"stop_reason"`
			Usage      *store.Usage `json:"usage,omitempty"`
		}{turn.StopReason, turn.Usage}
	case store.TurnCancelled:
		return TurnCancelled, struct{}{}
	case store.TurnExpired:
		return TurnExpired, struct{}{}
	}
	var failure store.Failure
	if turn.Failure != nil {
		failure = *turn.Failure
	}
	return TurnFailed, struct {
		Reason  string `json:"reason,omitempty"`
		Message string `json:"message,omitempty"`
	}{failure.Reason, failure.Message}
}

// IsLast reports whether an event of this type is the last of its turn.
func IsLast(eventType string) bool {
	switch eventType {
	case TurnCompleted, TurnFailed, TurnCancelled, TurnExpired:
		return true
	}
	return false
}

// AppendLast adds the last event of the turn to its stream and sets the
// stream to be deleted once it has been kept long enough.
func (s *Streams) AppendLast(ctx context.Context, turn store.Turn) error {
	eventType, data := Last(turn)
	if err := s.Append(ctx, turn.ID, eventType, data); err != nil {
		return err
	}
	return s.client.Do(ctx, s.client.B().Expire().Key(key(turn.ID)).Seconds(int64(keptFor.Seconds())).Build()).Error()
}

// Read returns the turn's events that follow the position after, oldest
// first. If there are none, it waits for the next for as long as wait and
// then returns nothing.
func (s *Streams) Read(ctx context.Context, turnID uuid.UUID, after string, wait time.Duration) ([]Event, error) {
	streams, err := s.client.Do(ctx, s.client.B().Xread().Count(readBatch).Block(wait.Milliseconds()).
		Streams().Key(key(turnID)).Id(after).Build()).AsXRead()
	if valkey.IsValkeyNil(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries := streams[key(turnID)]
	batch := make([]Event, len(entries))
	for i, entry := range entries {
		batch[i] = Event{ID: entry.ID, Type: entry.FieldValues["type"], Data: entry.FieldValues["data"]}
	}
	return batch, nil
}

// IsID reports whether id has the form of an event ID.
func IsID(id string) bool {
	millis, sequence, found := strings.Cut(id, "-")
	if !found {
		return false
	}
	_, millisErr := strconv.ParseUint(millis, 10, 64)
	_, sequenceErr := strconv.ParseUint(sequence, 10, 64)
	return millisErr == nil && sequenceErr == nil
}

// key names the turn's stream. A turn has no other key, so the streams can
// be spread over a Valkey cluster.
func key(turnID uuid.UUID) string {
	return "turn:" + turnID.String()
}
