package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"aviary/supervisor/events"
)

// keepalive is how long an event stream may stay silent before a comment is
// sent. The comment keeps proxies from closing the connection as idle, and
// detects clients that went away.
const keepalive = 15 * time.Second

// streamEvents sends the turn's events as server-sent events, up to the one
// that reports how the turn ended. A client that reconnects sends the ID of
// the last event it received in Last-Event-ID and gets the ones after it.
func (a *api) streamEvents(w http.ResponseWriter, r *http.Request) error {
	turnID, err := pathID(r, "tid")
	if err != nil {
		return err
	}
	turn, err := a.store.Turn(r.Context(), turnID)
	if err != nil {
		return err
	}
	after := r.Header.Get("Last-Event-ID")
	switch {
	case after == "":
		after = events.Start
	case !a.events.IsID(after):
		return invalid("Last-Event-ID is not an event ID")
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		select {
		case <-a.closing:
			cancel()
		case <-ctx.Done():
		}
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	response := http.NewResponseController(w)
	// The status is sent, so errors only end the stream. The client
	// reconnects and continues after the last event it received.
	for {
		if err := response.Flush(); err != nil {
			return nil
		}
		// A turn that has ended gets no more events, so there is nothing to
		// wait for.
		wait := keepalive
		if turn.EndedAt != nil {
			wait = time.Millisecond
		}
		batch, err := a.events.Read(ctx, turnID, after, wait)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			slog.ErrorContext(ctx, "read turn events", "turn", turnID, "error", err)
			return nil
		}
		for _, event := range batch {
			if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data); err != nil {
				return nil
			}
			if events.IsLast(event.Type) {
				return nil
			}
			after = event.ID
		}
		if len(batch) > 0 {
			continue
		}
		// If the turn has ended, its last event is missing from the stream:
		// it was not written, or it has expired. It is then made from the
		// turn's record.
		if turn, err = a.store.Turn(ctx, turnID); err != nil {
			slog.ErrorContext(ctx, "read turn", "turn", turnID, "error", err)
			return nil
		}
		if turn.EndedAt == nil {
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return nil
			}
			continue
		}
		eventType, data := events.Last(turn)
		encoded, err := json.Marshal(data)
		if err != nil {
			slog.ErrorContext(ctx, "encode turn event", "turn", turnID, "error", err)
			return nil
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, encoded)
		return nil
	}
}
