package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"aviary/supervisor/internal/driver"
	"aviary/supervisor/store"
	memorystore "aviary/supervisor/store/memory"
)

// stateMetrics serves the state metrics of the store until the test ends, and
// returns their URL. The harnesses are those of install.
func stateMetrics(t *testing.T, st store.Store) string {
	t.Helper()
	on, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- serveStateOn(ctx, on, st, []string{"codex", "opencode"}) }()
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
	response, err := httpClient.Get(url)
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

// The state metrics count the turns that have not ended by harness and by how
// far they have come, and the requests that wait for a person. They report
// every series of the harnesses from the start.
func TestStateMetricsCountTurns(t *testing.T) {
	t.Parallel()
	aviary := install(t, func(config *driver.Config) { config.MaxConcurrentTurns = 3 })
	s := aviary.supervisor()
	metrics := stateMetrics(t, aviary.records)
	// sample returns the sample of the series, or -1 if there is none.
	sample := func(series string) float64 {
		t.Helper()
		status, lines := scrape(t, metrics)
		if status != http.StatusOK {
			t.Fatalf("scrape: status %d, want 200", status)
		}
		for _, line := range lines {
			if sample, found := strings.CutPrefix(line, series+" "); found {
				value, err := strconv.ParseFloat(sample, 64)
				if err != nil {
					t.Fatalf("%s: %v", line, err)
				}
				return value
			}
		}
		return -1
	}
	reported := func(metric, harness, state string) float64 {
		t.Helper()
		return sample(fmt.Sprintf(`%s{harness=%q,state=%q}`, metric, harness, state))
	}
	states := []string{"queued", "starting", "running", "awaiting_approval"}
	for _, harness := range []string{"codex", "opencode"} {
		if requests := sample(fmt.Sprintf(`aviary_approvals_waiting{harness=%q}`, harness)); requests != 0 {
			t.Errorf("without turns, waiting approval requests of %s = %v, want 0", harness, requests)
		}
		for _, state := range states {
			if turns, age := reported("aviary_turns", harness, state), reported("aviary_turns_oldest_age_seconds", harness, state); turns != 0 || age != 0 {
				t.Errorf("without turns, %s turns of %s = %v with the oldest at %v, want 0 and 0", state, harness, turns, age)
			}
		}
	}

	// Three turns take the room to run: one whose harness does not have the
	// message yet, one that runs, and one that waits for a person. A fourth
	// is queued.
	aviary.given(s.send(s.session(), "one").ID)
	aviary.given(s.send(s.session(), "two").ID).report(started("harness-session"))
	third := s.send(s.session(`{"harness": "codex"}`), "three")
	asking := aviary.given(third.ID)
	asking.report(started("harness-session"))
	asking.report(asks("1", "rm -r build"))
	s.send(s.session(), "four")
	expected := map[[2]string]float64{
		{"opencode", "starting"}: 1, {"opencode", "running"}: 1, {"codex", "awaiting_approval"}: 1, {"opencode", "queued"}: 1,
	}
	eventually(t, soon, "the state metrics do not count the four turns by harness and state", func() bool {
		for _, harness := range []string{"codex", "opencode"} {
			for _, state := range states {
				if reported("aviary_turns", harness, state) != expected[[2]string{harness, state}] {
					return false
				}
			}
		}
		return true
	})
	// Only a state with turns has an oldest one.
	if age := reported("aviary_turns_oldest_age_seconds", "opencode", "queued"); age <= 0 {
		t.Errorf("age of the oldest queued turn = %v, want more than 0", age)
	}
	if age := reported("aviary_turns_oldest_age_seconds", "codex", "queued"); age != 0 {
		t.Errorf("age of the oldest queued turn of a harness without one = %v, want 0", age)
	}

	// A turn can have several requests waiting, and one that has its answer
	// no longer waits.
	asking.report(asks("2", "git push"))
	eventually(t, soon, "the state metrics do not count both requests of the turn", func() bool {
		return sample(`aviary_approvals_waiting{harness="codex"}`) == 2 && reported("aviary_turns", "codex", "awaiting_approval") == 1
	})
	if status, _ := s.approve(third.ID, "1", `{"option_id": "never"}`); status != http.StatusAccepted {
		t.Fatalf("approve: status %d, want 202", status)
	}
	if requests := sample(`aviary_approvals_waiting{harness="codex"}`); requests != 1 || sample(`aviary_approvals_waiting{harness="opencode"}`) != 0 {
		t.Errorf("waiting approval requests of codex after an answer = %v, want 1, and none of opencode", requests)
	}
}

// unreadable is a store whose database cannot be reached for the sandboxes or
// for the turns.
type unreadable struct {
	store.Store
	turns bool
}

var errUnreachable = errors.New("dial tcp db.example:5432: connection refused")

func (u unreadable) SandboxCounts(ctx context.Context) (map[string]int, error) {
	if u.turns {
		return u.Store.SandboxCounts(ctx)
	}
	return nil, errUnreachable
}

func (u unreadable) UnfinishedTurns(ctx context.Context) ([]store.UnfinishedTurn, error) {
	if u.turns {
		return nil, errUnreachable
	}
	return u.Store.UnfinishedTurns(ctx)
}

// A scrape fails while the database cannot be read, so that its state is not
// taken for zero. The response does not repeat the error, which may name the
// database.
func TestStateMetricsFailWithoutTheDatabase(t *testing.T) {
	t.Parallel()
	for _, turns := range []bool{false, true} {
		status, lines := scrape(t, stateMetrics(t, unreadable{Store: memorystore.New(), turns: turns}))
		if body := strings.Join(lines, "\n"); status != http.StatusInternalServerError ||
			strings.Contains(body, "db.example") || strings.Contains(body, "aviary_sandboxes{") || strings.Contains(body, "aviary_turns{") {
			t.Errorf("scrape without the database = %d %q, want 500 without the address or a count", status, body)
		}
	}
}
