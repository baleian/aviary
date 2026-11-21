package metrics

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/store"
)

// counts is the store of an installation with the turns.
type counts []store.UnfinishedTurn

func (counts) SandboxCounts(context.Context) (map[string]int, error) { return nil, nil }

func (c counts) UnfinishedTurns(context.Context) ([]store.UnfinishedTurn, error) { return c, nil }

// Turns are counted by harness and by how far they have come, with the age of
// the oldest of each kind, and so are the requests that wait for a person.
func TestStateCountsTurns(t *testing.T) {
	state := NewState(counts{
		{Harness: "opencode", State: store.TurnQueued, Age: 90 * time.Second},
		{Harness: "opencode", State: store.TurnQueued, Age: 30 * time.Second},
		// Starting: dispatched, and followed before the harness has the
		// message.
		{Harness: "opencode", State: store.TurnDispatching, Age: 2 * time.Second},
		{Harness: "opencode", State: store.TurnRunning, Age: 5 * time.Second},
		{Harness: "opencode", State: store.TurnRunning, Started: true, Age: 10 * time.Minute},
		{Harness: "codex", State: store.TurnRunning, Started: true, Asking: 2, Age: 45 * time.Second},
		{Harness: "codex", State: store.TurnRunning, Started: true, Asking: 1, Age: 20 * time.Second},
		// A harness that is no longer configured.
		{Harness: "retired", State: store.TurnRunning, Started: true, Age: time.Second},
	}, []string{"codex", "opencode"})
	scraped := httptest.NewRecorder()
	state.Handler().ServeHTTP(scraped, httptest.NewRequest("GET", "/metrics", nil))
	lines := strings.Split(scraped.Body.String(), "\n")
	for _, line := range []string{
		`aviary_turns{harness="opencode",state="queued"} 2`,
		`aviary_turns_oldest_age_seconds{harness="opencode",state="queued"} 90`,
		`aviary_turns{harness="opencode",state="starting"} 2`,
		`aviary_turns_oldest_age_seconds{harness="opencode",state="starting"} 5`,
		`aviary_turns{harness="opencode",state="running"} 1`,
		`aviary_turns_oldest_age_seconds{harness="opencode",state="running"} 600`,
		`aviary_turns{harness="opencode",state="awaiting_approval"} 0`,
		`aviary_turns_oldest_age_seconds{harness="opencode",state="awaiting_approval"} 0`,
		`aviary_turns{harness="codex",state="awaiting_approval"} 2`,
		`aviary_turns_oldest_age_seconds{harness="codex",state="awaiting_approval"} 45`,
		`aviary_approvals_waiting{harness="codex"} 3`,
		`aviary_approvals_waiting{harness="opencode"} 0`,
		`aviary_turns{harness="codex",state="running"} 0`,
		`aviary_turns{harness="retired",state="running"} 1`,
	} {
		if !slices.Contains(lines, line) {
			t.Errorf("metrics lack %s", line)
		}
	}
	// Only the configured harnesses have a series for every state.
	if slices.ContainsFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, `aviary_turns{harness="retired",state="queued"}`)
	}) {
		t.Error("metrics report queued turns of a harness that is not configured")
	}
}
