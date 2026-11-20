// Package memory implements an events.Log in the memory of the process, for
// tests. The Supervisors of a test share one Log, as those of an installation
// share theirs.
package memory

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/events"
)

// batch is the maximum number of events that Read returns.
const batch = 100

// Log is an event log in memory. Create it with New; the zero value is not
// usable.
type Log struct {
	mu       sync.Mutex
	turns    map[uuid.UUID]*turn
	followed map[uuid.UUID]lease
	// watchers holds the callbacks of each channel.
	watchers map[string]map[*func(uuid.UUID)]struct{}
}

type lease struct {
	follower uuid.UUID
	until    time.Time
}

type turn struct {
	events   []events.Event
	received uint64
	// added is closed and replaced when an event is added, to wake waiting
	// readers.
	added chan struct{}
	// expiry deletes the turn when it expires.
	expiry *time.Timer
}

func New() *Log {
	return &Log{
		turns: map[uuid.UUID]*turn{}, followed: map[uuid.UUID]lease{},
		watchers: map[string]map[*func(uuid.UUID)]struct{}{},
	}
}

// Close does nothing, since the Supervisors of a test share the log.
func (*Log) Close() {}

// of returns the data of the turn, and creates it if needed. The caller must
// hold l.mu.
func (l *Log) of(turnID uuid.UUID) *turn {
	held, found := l.turns[turnID]
	if !found {
		held = &turn{added: make(chan struct{})}
		l.turns[turnID] = held
	}
	return held
}

// add appends an event and wakes waiting readers. The caller must hold the
// lock of the log.
func (t *turn) add(eventType, data string) {
	t.events = append(t.events, events.Event{ID: strconv.Itoa(len(t.events) + 1), Type: eventType, Data: data})
	close(t.added)
	t.added = make(chan struct{})
}

func (l *Log) Append(_ context.Context, turnID uuid.UUID, eventType, data string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.of(turnID).add(eventType, data)
	return nil
}

func (l *Log) Receive(_ context.Context, turnID uuid.UUID, seq uint64, eventType, data string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	held := l.of(turnID)
	switch {
	case seq <= held.received:
		return nil
	case seq > held.received+1:
		return events.ErrGap
	}
	if eventType != "" {
		held.add(eventType, data)
	}
	held.received = seq
	return nil
}

func (l *Log) Received(_ context.Context, turnID uuid.UUID) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if held, found := l.turns[turnID]; found {
		return held.received, nil
	}
	return 0, nil
}

func (l *Log) Read(ctx context.Context, turnID uuid.UUID, after string, wait time.Duration) ([]events.Event, error) {
	// An event ID is the event's 1-based position among the turn's events.
	read, _ := strconv.Atoi(after)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		l.mu.Lock()
		held := l.of(turnID)
		if read < len(held.events) {
			next := held.events[read:min(len(held.events), read+batch)]
			l.mu.Unlock()
			return append([]events.Event(nil), next...), nil
		}
		added := held.added
		l.mu.Unlock()
		if wait <= 0 {
			return nil, nil
		}
		select {
		case <-added:
		case <-deadline.C:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (*Log) IsID(id string) bool {
	number, err := strconv.Atoi(id)
	return err == nil && number > 0
}

func (l *Log) Expire(_ context.Context, turnID uuid.UUID, after time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	held, found := l.turns[turnID]
	if !found {
		return nil
	}
	if held.expiry != nil {
		held.expiry.Stop()
	}
	if after <= 0 {
		delete(l.turns, turnID)
		return nil
	}
	held.expiry = time.AfterFunc(after, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.turns[turnID] == held {
			delete(l.turns, turnID)
		}
	})
	return nil
}

func (l *Log) Follow(_ context.Context, turnID, follower uuid.UUID, length time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if held, found := l.followed[turnID]; found && held.follower != follower && held.until.After(now) {
		return false, nil
	}
	l.followed[turnID] = lease{follower: follower, until: now.Add(length)}
	return true, nil
}

func (l *Log) Leave(_ context.Context, turnID, follower uuid.UUID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.followed[turnID].follower == follower {
		delete(l.followed, turnID)
	}
	return nil
}

func (l *Log) Announce(_ context.Context, channel string, turnID uuid.UUID) error {
	l.mu.Lock()
	watching := make([]*func(uuid.UUID), 0, len(l.watchers[channel]))
	for watcher := range l.watchers[channel] {
		watching = append(watching, watcher)
	}
	l.mu.Unlock()
	for _, watcher := range watching {
		(*watcher)(turnID)
	}
	return nil
}

func (l *Log) Watch(ctx context.Context, channel string, announced func(turnID uuid.UUID)) {
	l.mu.Lock()
	if l.watchers[channel] == nil {
		l.watchers[channel] = map[*func(uuid.UUID)]struct{}{}
	}
	l.watchers[channel][&announced] = struct{}{}
	l.mu.Unlock()
	<-ctx.Done()
	l.mu.Lock()
	delete(l.watchers[channel], &announced)
	l.mu.Unlock()
}
