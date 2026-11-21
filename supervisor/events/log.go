package events

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Start is the position before a turn's first event.
const Start = ""

// ErrGap is returned for a runner message whose predecessor is not recorded.
var ErrGap = errors.New("previous message is not recorded")

// Event is an event of a turn. IDs order the events of a turn, and a reader
// resumes after an ID.
type Event struct {
	ID   string
	Type string
	Data string // JSON
}

// Log keeps the events of turns while clients read them, and carries
// announcements between Supervisors. All Supervisors of an installation share
// one log. Plugins implement it.
type Log interface {
	// Append appends an event to the turn's events.
	Append(ctx context.Context, turnID uuid.UUID, eventType, data string) error
	// Receive atomically records message seq of the turn's runner and, if
	// eventType is not empty, appends its event. Sequence numbers start at 1.
	// A message that is already recorded is ignored, even when several
	// Supervisors receive it at once, and one that does not follow the last
	// recorded one returns ErrGap.
	Receive(ctx context.Context, turnID uuid.UUID, seq uint64, eventType, data string) error
	// Received returns the sequence number of the last recorded message of
	// the turn's runner, or 0 if there is none.
	Received(ctx context.Context, turnID uuid.UUID) (uint64, error)
	// Read returns the events of the turn after the position after, which is
	// Start or an event ID, oldest first. It may return only the first few.
	// If none follow, it waits up to wait for one, and returns none if
	// nothing arrives.
	Read(ctx context.Context, turnID uuid.UUID, after string, wait time.Duration) ([]Event, error)
	// IsID reports whether id has the form of an event ID of this log.
	IsID(id string) bool
	// Expire deletes the turn's data from the log after the given duration,
	// or at once if it is zero.
	Expire(ctx context.Context, turnID uuid.UUID, after time.Duration) error
	// Follow grants follower the lease on the turn for the given duration,
	// and reports whether it did. It grants the lease if no one holds it, or
	// renews it if follower does. While a lease is held, no other follower
	// gets it, even when several ask at once.
	Follow(ctx context.Context, turnID, follower uuid.UUID, lease time.Duration) (bool, error)
	// Leave releases follower's lease on the turn, so that the next follower
	// that asks gets it at once. It does nothing if follower does not hold
	// the lease.
	Leave(ctx context.Context, turnID, follower uuid.UUID) error
	// Announce announces id, the ID of a turn or a session, to the watchers
	// of channel.
	Announce(ctx context.Context, channel string, id uuid.UUID) error
	// Watch calls announced for every ID announced on channel, until ctx is
	// done. It may miss announcements while the log is unreachable, and
	// resumes when it is reachable again.
	Watch(ctx context.Context, channel string, announced func(id uuid.UUID))
	// Close closes the connection to the log.
	Close()
}

// Plugin connects to the event logs whose URLs have one of its schemes.
type Plugin interface {
	// Schemes returns the URL schemes that the plugin handles.
	Schemes() []string
	// Open connects to the log at address. Errors must not include the
	// address, which may contain a password.
	Open(address string) (Log, error)
}
