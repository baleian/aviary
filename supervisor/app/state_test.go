package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/internal/driver"
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

// stateMetrics serves the state metrics of the store until the test ends, and
// returns their URL.
func stateMetrics(t *testing.T, st store.Store) string {
	t.Helper()
	on, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- serveStateOn(ctx, on, st) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("serveStateOn: %v", err)
			}
		case <-time.After(patience):
			t.Error("the state metrics did not stop")
		}
	})
	return "http://" + on.Addr().String() + "/metrics"
}

// scrape returns the status and the lines of a scrape.
func scrape(t *testing.T, url string) (int, []string) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, strings.Split(string(body), "\n")
}

// The state metrics count the sandboxes of the installation in each state,
// as the database has them at the scrape.
func TestStateMetricsCountSandboxes(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) {
		config.Idle = 300 * time.Millisecond
		// Sandboxes are looked at only every few sweeps.
		config.Sweep = 20 * time.Millisecond
	})
	s := aviary.supervisor()
	metrics := stateMetrics(t, aviary.records)
	reports := func(awake, asleep, lost int) bool {
		t.Helper()
		status, lines := scrape(t, metrics)
		if status != http.StatusOK {
			t.Fatalf("scrape: status %d, want 200", status)
		}
		for state, sandboxes := range map[string]int{"awake": awake, "asleep": asleep, "lost": lost} {
			if !slices.Contains(lines, fmt.Sprintf(`aviary_sandboxes{state=%q} %d`, state, sandboxes)) {
				return false
			}
		}
		return true
	}
	// Every state is reported from the start.
	if !reports(0, 0, 0) {
		t.Error("state metrics of an installation without sessions do not report 0 sandboxes in every state")
	}

	// A session without a sandbox is not counted.
	s.session()
	working, resting := s.session(), s.session()
	aviary.given(s.send(working, "one").ID).report(started("harness-session"))
	runner := aviary.given(s.send(resting, "two").ID)
	if !reports(2, 0, 0) {
		t.Error("state metrics do not report 2 awake sandboxes while two sessions run turns")
	}
	runner.report(started("harness-session"))
	runner.report(finished("end_turn", nil))
	eventually(t, patience, "the state metrics do not report the idle session's sandbox as asleep", func() bool {
		return reports(1, 1, 0)
	})
	// The sandbox is deleted from outside.
	runner.lose()
	eventually(t, patience, "the state metrics do not report the deleted sandbox as lost", func() bool {
		return reports(1, 0, 1)
	})
	// A session that has ended is not counted.
	if status, _ := s.request("DELETE", "/v1/sessions/"+resting, "", nil); status != http.StatusOK {
		t.Fatalf("end session: status %d, want 200", status)
	}
	if !reports(1, 0, 0) {
		t.Error("state metrics still count the sandbox of a session that has ended")
	}
}

// unreadable is a store whose database cannot be reached.
type unreadable struct{ store.Store }

func (unreadable) SandboxCounts(context.Context) (map[string]int, error) {
	return nil, errors.New("dial tcp db.example:5432: connection refused")
}

// A scrape fails while the database cannot be read, so that its state is not
// taken for zero. The response does not repeat the error, which may name the
// database.
func TestStateMetricsFailWithoutTheDatabase(t *testing.T) {
	t.Parallel()
	status, lines := scrape(t, stateMetrics(t, unreadable{memorystore.New()}))
	if body := strings.Join(lines, "\n"); status != http.StatusInternalServerError ||
		strings.Contains(body, "db.example") || strings.Contains(body, "aviary_sandboxes{") {
		t.Errorf("scrape without the database = %d %q, want 500 without the address or a count", status, body)
	}
}
