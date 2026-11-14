package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"aviary/supervisor/internal/events"
)

// keepalive is how long an event stream stays silent before a comment line
// is sent. The line keeps proxies from closing the connection as idle, and
// writing it is how a client that went away is noticed.
const keepalive = 15 * time.Second

// streamEvents sends the turn's events as server-sent events and keeps the
// response open for those to come. A client that reconnects sends the ID of
// the last event it received in Last-Event-ID and gets the ones after it.
func (a *api) streamEvents(w http.ResponseWriter, r *http.Request) error {
	turnID, err := pathID(r, "tid")
	if err != nil {
		return err
	}
	if _, err := a.store.Turn(r.Context(), turnID); err != nil {
		return err
	}
	after := r.Header.Get("Last-Event-ID")
	switch {
	case after == "":
		after = events.Start
	case !events.IsID(after):
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
	// From here the status is sent, so a failure can only end the stream. The
	// client reconnects and continues after the last event it received.
	for {
		if err := response.Flush(); err != nil {
			return nil
		}
		batch, err := a.events.Read(ctx, turnID, after, keepalive)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			slog.ErrorContext(ctx, "read turn events", "turn", turnID, "error", err)
			return nil
		}
		if len(batch) == 0 {
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return nil
			}
			continue
		}
		for _, event := range batch {
			if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data); err != nil {
				return nil
			}
			after = event.ID
		}
	}
}
