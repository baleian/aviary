package events

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Start is the position before a turn's first event.
const Start = ""

// ErrGap is returned for a message of a runner that follows one that is not
// recorded.
var ErrGap = errors.New("the message before this one is not recorded")

// Event is one event of a turn. Its ID orders the turn's events and names the
// position from which a reader continues.
type Event struct {
	ID   string
	Type string
	Data string // JSON
}

// Log is where the events of the turns are kept while clients read them,
// and through which the Supervisors tell each other what happens to a turn.
// Every Supervisor of an installation has the same log: what one of them
// writes, the others read. A plugin gives the Supervisor a log of one kind.
type Log interface {
	// Append adds an event to the end of the turn's events.
	Append(ctx context.Context, turnID uuid.UUID, eventType, data string) error
	// Receive records the message with the number seq of the turn's runner
	// and, if the message has an event, adds the event to the end of the
	// turn's events: both or neither. The first message has the number 1. A
	// message that is recorded already is left out, also when several
	// Supervisors receive it at once, and one that does not follow the last
	// recorded one gets ErrGap.
	Receive(ctx context.Context, turnID uuid.UUID, seq uint64, eventType, data string) error
	// Received returns the number of the last message of the turn's runner
	// that is recorded, which is 0 if none is.
	Received(ctx context.Context, turnID uuid.UUID) (uint64, error)
	// Read returns events of the turn that follow the position after, oldest
	// first: all of them or the first of them. The position is Start or the
	// ID of an event. If no event follows, Read waits for the next for as
	// long as wait and then returns nothing.
	Read(ctx context.Context, turnID uuid.UUID, after string, wait time.Duration) ([]Event, error)
	// IsID reports whether id has the form of the IDs that the log gives
	// its events.
	IsID(id string) bool
	// Expire has what the log holds of the turn deleted when the time has
	// passed, which is at once if it is zero.
	Expire(ctx context.Context, turnID uuid.UUID, after time.Duration) error
	// Follow gives the turn to the Supervisor that asks to follow it, which
	// names itself with follower, for as long as the lease, and reports
	// whether it did. A turn is given to one Supervisor at a time: to one
	// that asks while nobody has it, and again to the one that has it, for
	// another lease. It is not given to another before the lease is over,
	// also when several ask at once.
	Follow(ctx context.Context, turnID, follower uuid.UUID, lease time.Duration) (bool, error)
	// Leave gives the turn up for the Supervisor that follows it: the next
	// that asks is given it, without waiting for the lease to be over. It
	// changes nothing if another Supervisor follows the turn, or none.
	Leave(ctx context.Context, turnID, follower uuid.UUID) error
	// Announce tells the Supervisors that watch the channel of the turn.
	Announce(ctx context.Context, channel string, turnID uuid.UUID) error
	// Watch calls announced for every turn that is announced on the channel,
	// until ctx is done. It may miss what is announced while the log cannot
	// be reached, and goes on when it can be reached again.
	Watch(ctx context.Context, channel string, announced func(turnID uuid.UUID))
	// Close ends the connection to the log.
	Close()
}

// Plugin connects the Supervisor to the logs of one kind. The address of a
// log is a URL, and its scheme names the kind.
type Plugin interface {
	// Schemes are the schemes of the addresses that the plugin connects to.
	Schemes() []string
	// Open connects to the log at the address. An error does not quote the
	// address, which may hold a password.
	Open(address string) (Log, error)
}
