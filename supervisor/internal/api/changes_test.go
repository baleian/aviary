package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/events"
	memorylog "aviary/supervisor/events/memory"
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

// A stream of changes ends by itself, so that the client opens the next one
// with a token and permissions that are checked again.
func TestAStreamOfChangesEndsByItself(t *testing.T) {
	life := streamLife
	streamLife = 200 * time.Millisecond
	t.Cleanup(func() { streamLife = life })
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	server := httptest.NewServer(New(ctx, memorystore.New(), events.New(memorylog.New()), nil, nil, nil, Callers{}))
	t.Cleanup(server.Close)
	created, err := http.Post(server.URL+"/v1/projects", "application/json", strings.NewReader(`{"slug": "a-project"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer created.Body.Close()
	var project struct{ ID string }
	if err := json.NewDecoder(created.Body).Decode(&project); err != nil || project.ID == "" {
		t.Fatalf("create project: status %d, ID %q, %v", created.StatusCode, project.ID, err)
	}

	opened := time.Now()
	stream, err := http.Get(server.URL + "/v1/projects/" + project.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK || stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream of changes: status %d, content type %q", stream.StatusCode, stream.Header.Get("Content-Type"))
	}
	sent, err := io.ReadAll(stream.Body)
	if open := time.Since(opened); err != nil || len(sent) != 0 || open < streamLife || open > 10*streamLife {
		t.Errorf("stream ended after %s with %q, %v; want it to end by itself after %s", open, sent, err, streamLife)
	}
}

// A client that does not read its stream loses it, and the other clients
// keep theirs.
func TestAStreamThatFallsBehindIsEnded(t *testing.T) {
	t.Parallel()
	watching, project := newChanges(), uuid.New()
	every := func(store.Session) bool { return true }
	slow, read := watching.add(project, every), watching.add(project, every)
	for range behind + 1 {
		for _, stream := range watching.of(project) {
			watching.send(project, stream, []byte("{}"))
		}
		<-read.sessions
	}
	for range behind {
		if _, open := <-slow.sessions; !open {
			t.Fatalf("the stream ended before it was %d sessions behind", behind)
		}
	}
	if _, open := <-slow.sessions; open {
		t.Errorf("the stream is open although it was more than %d sessions behind", behind)
	}
	if open := watching.of(project); len(open) != 1 || open[0] != read {
		t.Errorf("open streams = %v, want only the one that was read", open)
	}
}

// A Supervisor without streams does not keep the sessions that change.
func TestChangesWithoutStreamsAreDropped(t *testing.T) {
	t.Parallel()
	watching, project := newChanges(), uuid.New()
	watching.announced(uuid.New())
	if taken := watching.take(); len(taken) != 0 {
		t.Errorf("changed sessions without a stream = %v, want none", taken)
	}
	stream, session := watching.add(project, func(store.Session) bool { return true }), uuid.New()
	watching.announced(session)
	watching.announced(session)
	if taken := watching.take(); len(taken) != 1 || taken[0] != session {
		t.Errorf("changed sessions = %v, want %s once", taken, session)
	}
	watching.remove(project, stream)
	watching.announced(session)
	if taken := watching.take(); len(taken) != 0 {
		t.Errorf("changed sessions after the stream was closed = %v, want none", taken)
	}
}
