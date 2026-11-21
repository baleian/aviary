package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

const (
	// settle is how long a changed session waits to be sent, so that changes
	// that follow each other at once are sent as one.
	settle = 50 * time.Millisecond
	// behind is how many sessions may wait to be written to a stream of
	// changes. A client that falls further behind loses its stream.
	behind = 64
)

// streamLife is how long a stream of changes stays open, or up to a quarter
// longer. Opening the next one checks the client's token and permissions
// again. It is a variable for tests.
var streamLife = 5 * time.Minute

// changes sends the sessions that have changed to the streams of their
// projects.
type changes struct {
	// wake signals that changed is not empty.
	wake chan struct{}

	mu sync.Mutex
	// streams are the open streams of each project.
	streams map[uuid.UUID]map[*changeStream]struct{}
	// changed are the sessions that have changed and are not sent yet.
	changed map[uuid.UUID]struct{}
}

// changeStream is the stream of a client that watches a project.
type changeStream struct {
	// reads reports whether the client may read the session.
	reads func(store.Session) bool
	// sessions are the sessions to write, encoded. It is closed if the
	// client falls behind.
	sessions chan []byte
}

func newChanges() *changes {
	return &changes{
		wake:    make(chan struct{}, 1),
		streams: map[uuid.UUID]map[*changeStream]struct{}{},
		changed: map[uuid.UUID]struct{}{},
	}
}

// add opens a stream of the project's sessions that reads allows.
func (c *changes) add(projectID uuid.UUID, reads func(store.Session) bool) *changeStream {
	stream := &changeStream{reads: reads, sessions: make(chan []byte, behind)}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streams[projectID] == nil {
		c.streams[projectID] = map[*changeStream]struct{}{}
	}
	c.streams[projectID][stream] = struct{}{}
	return stream
}

// remove removes the stream, and reports whether it was still open.
func (c *changes) remove(projectID uuid.UUID, stream *changeStream) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, open := c.streams[projectID][stream]
	delete(c.streams[projectID], stream)
	if len(c.streams[projectID]) == 0 {
		delete(c.streams, projectID)
	}
	return open
}

// of returns the open streams of the project.
func (c *changes) of(projectID uuid.UUID) []*changeStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Collect(maps.Keys(c.streams[projectID]))
}

// announced notes that the session has changed. Without open streams there
// is no one to send it to.
func (c *changes) announced(sessionID uuid.UUID) {
	c.mu.Lock()
	watched := len(c.streams) > 0
	if watched {
		c.changed[sessionID] = struct{}{}
	}
	c.mu.Unlock()
	if !watched {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
		// A pending wake-up covers this one.
	}
}

// take returns the sessions that have changed since it last did.
func (c *changes) take() []uuid.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()
	taken := slices.Collect(maps.Keys(c.changed))
	clear(c.changed)
	return taken
}

// send gives the encoded session to the stream. A stream with no room is
// ended instead: its client opens another, and reads the sessions again.
// Only one goroutine may call send.
func (c *changes) send(projectID uuid.UUID, stream *changeStream, session []byte) {
	select {
	case stream.sessions <- session:
	default:
		if c.remove(projectID, stream) {
			close(stream.sessions)
		}
	}
}

// sendChanges sends the sessions that the Supervisors announce as changed to
// the streams of their projects, until ctx is done.
func (a *api) sendChanges(ctx context.Context) {
	go a.events.WatchChanges(ctx, a.changes.announced)
	for {
		select {
		case <-a.changes.wake:
		case <-ctx.Done():
			return
		}
		select {
		case <-time.After(settle):
		case <-ctx.Done():
			return
		}
		for _, sessionID := range a.changes.take() {
			// A session that is not sent is seen when its stream ends and
			// the client reads the sessions again.
			if err := a.sendChange(ctx, sessionID); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "send session change", "session", sessionID, "error", err)
			}
		}
	}
}

// sendChange reads the session and sends it to the streams of its project.
func (a *api) sendChange(ctx context.Context, sessionID uuid.UUID) error {
	session, err := a.store.Session(ctx, sessionID)
	if err != nil {
		return err
	}
	agent, err := a.store.Agent(ctx, session.AgentID)
	if err != nil {
		return err
	}
	streams := a.changes.of(agent.ProjectID)
	if len(streams) == 0 {
		return nil
	}
	records, err := a.sessionRecords(ctx, []store.Session{session})
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(records[0])
	if err != nil {
		return err
	}
	for _, stream := range streams {
		if stream.reads(session) {
			a.changes.send(agent.ProjectID, stream, encoded)
		}
	}
	return nil
}

// streamChanges sends the sessions of the project that the caller may read
// as server-sent events, each when it or its newest turn has changed. The
// stream starts with the changes that follow its opening, so a client reads
// the sessions after it has opened the stream, and again whenever it opens
// another.
func (a *api) streamChanges(w http.ResponseWriter, r *http.Request) error {
	projectID, err := pathID(r, "pid")
	if err != nil {
		return err
	}
	if _, err := a.store.Project(r.Context(), projectID); err != nil {
		return err
	}
	// A caller reads its own sessions with sessions.run, and everyone's
	// with sessions.view_all.
	reads := func(store.Session) bool { return true }
	switch who := callerOf(r); {
	case holds(r, sessionsViewAll):
	case holds(r, sessionsRun):
		reads = func(session store.Session) bool { return session.CreatedBy == who.id }
	default:
		reads = func(store.Session) bool { return false }
	}
	// The stream is open before the response begins, so that it has every
	// change that follows what the client reads next.
	stream := a.changes.add(projectID, reads)
	defer a.changes.remove(projectID, stream)
	// Streams that were opened together, as after a Supervisor has stopped,
	// do not end together.
	ends := time.NewTimer(streamLife + rand.N(streamLife/4))
	defer ends.Stop()
	idle := time.NewTicker(keepalive)
	defer idle.Stop()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	response := http.NewResponseController(w)
	// The status is sent, so errors only end the stream.
	for {
		if err := response.Flush(); err != nil {
			return nil
		}
		select {
		case session, open := <-stream.sessions:
			if !open {
				return nil
			}
			if _, err := fmt.Fprintf(w, "event: session\ndata: %s\n\n", session); err != nil {
				return nil
			}
			idle.Reset(keepalive)
		case <-idle.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return nil
			}
		case <-ends.C:
			return nil
		case <-a.closing:
			return nil
		case <-r.Context().Done():
			return nil
		}
	}
}
