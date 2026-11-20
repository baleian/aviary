// Package events keeps the events of running turns in a log that all
// Supervisors share, so that any of them can stream a turn's events to a
// client. The Supervisors also send each other announcements through the log,
// which a plugin connects to.
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

	"aviary/supervisor/store"
)

// Events that the Supervisor writes. All other events come from the runner.
const (
	// TurnQueued reports the position of a queued turn.
	TurnQueued = "turn.queued"
	// TurnStarted reports that the harness has received the message.
	TurnStarted = "turn.started"
	// TurnCompleted, TurnFailed, TurnCancelled and TurnExpired report how
	// the turn ended. One of them is the last event of every turn.
	TurnCompleted = "turn.completed"
	TurnFailed    = "turn.failed"
	TurnCancelled = "turn.cancelled"
	TurnExpired   = "turn.expired"
)

// cancelChannel carries the announcements of cancel requests.
const cancelChannel = "turns:cancel"

// answerChannel carries the announcements of answers to approval requests.
const answerChannel = "turns:answer"

// keptFor is how long the events of a turn are kept after it ends, for
// clients that are still reading. By then its items are in the database.
const keptFor = time.Hour

// Streams are the event streams of the turns in a Log.
type Streams struct {
	log Log
}

func New(log Log) *Streams {
	return &Streams{log: log}
}

// Open connects to the log at address with the plugin for the URL's scheme.
func Open(address string, plugins ...Plugin) (*Streams, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		// url.Error quotes the URL, password included.
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

// Append appends an event to the turn's stream, with data encoded as JSON.
func (s *Streams) Append(ctx context.Context, turnID uuid.UUID, eventType string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.log.Append(ctx, turnID, eventType, string(encoded))
}

// Receive records message seq of the turn's runner and, if eventType is not
// empty, appends its event to the turn's stream. A message that is already
// recorded is ignored, and one that does not follow the last recorded one
// returns ErrGap.
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

// Received returns the sequence number of the last recorded message of the
// turn's runner, or 0 if there is none.
func (s *Streams) Received(ctx context.Context, turnID uuid.UUID) (uint64, error) {
	return s.log.Received(ctx, turnID)
}

// Follow acquires or renews follower's lease on the turn, and reports whether
// follower holds it. The follower drives the turn and records its outcome,
// and renews the lease before it expires; otherwise it is taken to be gone,
// and the next Supervisor that asks takes the turn over.
//
// The lease avoids duplicate work but does not guarantee exclusivity: a log
// that loses its data can give a followed turn to another Supervisor. What
// the Supervisors record of a turn is recorded once, however many of them
// record it.
func (s *Streams) Follow(ctx context.Context, turnID, follower uuid.UUID, lease time.Duration) (bool, error) {
	return s.log.Follow(ctx, turnID, follower, lease)
}

// Leave releases follower's lease on the turn when it is done with the turn
// or stops, so that the next Supervisor that asks can follow it at once.
func (s *Streams) Leave(ctx context.Context, turnID, follower uuid.UUID) error {
	return s.log.Leave(ctx, turnID, follower)
}

// AppendQueued appends a TurnQueued event with the turn's position in the
// queue, which is 1 for the next turn to run.
func (s *Streams) AppendQueued(ctx context.Context, turnID uuid.UUID, position int) error {
	return s.Append(ctx, turnID, TurnQueued, struct {
		Position int `json:"position"`
	}{position})
}

// AnnounceCancel tells every Supervisor that the turn was asked to stop,
// since the turn's runner may be connected to any of them.
func (s *Streams) AnnounceCancel(ctx context.Context, turnID uuid.UUID) error {
	return s.log.Announce(ctx, cancelChannel, turnID)
}

// WatchCancels calls cancel for every turn announced by AnnounceCancel, until
// ctx is done. It misses announcements while the log is unreachable.
func (s *Streams) WatchCancels(ctx context.Context, cancel func(turnID uuid.UUID)) {
	s.log.Watch(ctx, cancelChannel, cancel)
}

// AnnounceAnswer tells every Supervisor that an approval request of the turn
// was answered.
func (s *Streams) AnnounceAnswer(ctx context.Context, turnID uuid.UUID) error {
	return s.log.Announce(ctx, answerChannel, turnID)
}

// WatchAnswers calls answered for every turn announced by AnnounceAnswer,
// until ctx is done. Like WatchCancels, it misses announcements while the log
// is unreachable.
func (s *Streams) WatchAnswers(ctx context.Context, answered func(turnID uuid.UUID)) {
	s.log.Watch(ctx, answerChannel, answered)
}

// Last returns the last event of an ended turn.
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

// AppendLast appends the last event of the turn, and schedules the deletion
// of the turn's data in the log.
func (s *Streams) AppendLast(ctx context.Context, turn store.Turn) error {
	eventType, data := Last(turn)
	if err := s.Append(ctx, turn.ID, eventType, data); err != nil {
		return err
	}
	return s.log.Expire(ctx, turn.ID, keptFor)
}

// Read returns the turn's events after the position after, oldest first. If
// there are none, it waits up to wait for one, and returns none if nothing
// arrives.
func (s *Streams) Read(ctx context.Context, turnID uuid.UUID, after string, wait time.Duration) ([]Event, error) {
	return s.log.Read(ctx, turnID, after, wait)
}

// IsID reports whether id has the form of an event ID.
func (s *Streams) IsID(id string) bool {
	return s.log.IsID(id)
}
