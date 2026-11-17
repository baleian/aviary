// Package events keeps the events of each turn where any Supervisor can read
// them to a client, and lets the Supervisors tell each other what happens to
// a turn. Both go through a log, which a plugin connects to.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

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

// cancelChannel is the channel on which a Supervisor tells the others that
// a turn was asked to stop.
const cancelChannel = "turns:cancel"

// answerChannel is the one on which it tells them that a request of a turn
// for approval was answered.
const answerChannel = "turns:answer"

// keptFor is how long the events of a turn are kept after its last event,
// for the clients that still read them. What the turn said is by then in the
// database.
const keptFor = time.Hour

// Streams are the events of the turns, in the log of the installation.
type Streams struct {
	log Log
}

// New returns the streams of the turns in the log.
func New(log Log) *Streams {
	return &Streams{log: log}
}

// Open connects to the log at address, a URL, with the plugin that connects
// to logs with the scheme of the address.
func Open(address string, plugins ...Plugin) (*Streams, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		// A URL that cannot be parsed is quoted in the error, password included.
		var quoting *url.Error
		if errors.As(err, &quoting) {
			err = quoting.Err
		}
		return nil, fmt.Errorf("parse the URL of the event log: %w", err)
	}
	var known []string
	for _, plugin := range plugins {
		if slices.Contains(plugin.Schemes(), parsed.Scheme) {
			log, err := plugin.Open(address)
			if err != nil {
				return nil, fmt.Errorf("connect to the event log: %w", err)
			}
			return New(log), nil
		}
		known = append(known, plugin.Schemes()...)
	}
	return nil, fmt.Errorf("the URL of the event log begins with %q, and this Supervisor was built with the plugins for: %s",
		parsed.Scheme, strings.Join(known, ", "))
}

func (s *Streams) Close() {
	s.log.Close()
}

// Append adds an event to the end of the turn's stream. The data is stored as
// JSON.
func (s *Streams) Append(ctx context.Context, turnID uuid.UUID, eventType string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.log.Append(ctx, turnID, eventType, string(encoded))
}

// Receive records the message with the number seq of the turn's runner, and
// adds its event to the end of the turn's stream. A message without an event
// has no eventType. A message that is recorded already is left out, and one
// that does not follow the last recorded one gets ErrGap.
func (s *Streams) Receive(ctx context.Context, turnID uuid.UUID, seq uint64, eventType string, data any) error {
	var encoded []byte
	if eventType != "" {
		var err error
		if encoded, err = json.Marshal(data); err != nil {
			return err
		}
	}
	return s.log.Receive(ctx, turnID, seq, eventType, string(encoded))
}

// Received returns the number of the last message of the turn's runner that
// is recorded, which is 0 if none is.
func (s *Streams) Received(ctx context.Context, turnID uuid.UUID) (uint64, error) {
	return s.log.Received(ctx, turnID)
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
	return s.log.Announce(ctx, cancelChannel, turnID)
}

// WatchCancels calls cancel for every turn that a Supervisor announces as
// asked to stop, until ctx is done. It misses what is announced while the
// log cannot be reached.
func (s *Streams) WatchCancels(ctx context.Context, cancel func(turnID uuid.UUID)) {
	s.log.Watch(ctx, cancelChannel, cancel)
}

// AnnounceAnswer tells every Supervisor that a request of the turn for
// approval was answered.
func (s *Streams) AnnounceAnswer(ctx context.Context, turnID uuid.UUID) error {
	return s.log.Announce(ctx, answerChannel, turnID)
}

// WatchAnswers calls answered for every turn that a Supervisor announces an
// answer for, until ctx is done. It misses what WatchCancels misses.
func (s *Streams) WatchAnswers(ctx context.Context, answered func(turnID uuid.UUID)) {
	s.log.Watch(ctx, answerChannel, answered)
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

// AppendLast adds the last event of the turn to its stream and has what the
// log holds of the turn deleted once it has been kept long enough.
func (s *Streams) AppendLast(ctx context.Context, turn store.Turn) error {
	eventType, data := Last(turn)
	if err := s.Append(ctx, turn.ID, eventType, data); err != nil {
		return err
	}
	return s.log.Expire(ctx, turn.ID, keptFor)
}

// Read returns the turn's events that follow the position after, oldest
// first. If there are none, it waits for the next for as long as wait and
// then returns nothing.
func (s *Streams) Read(ctx context.Context, turnID uuid.UUID, after string, wait time.Duration) ([]Event, error) {
	return s.log.Read(ctx, turnID, after, wait)
}

// IsID reports whether id has the form of an event ID.
func (s *Streams) IsID(id string) bool {
	return s.log.IsID(id)
}
