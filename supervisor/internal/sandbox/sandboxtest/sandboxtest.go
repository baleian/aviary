// Package sandboxtest provides an in-memory fake of the session sandboxes,
// with a fake runner in each.
package sandboxtest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/internal/sandbox/runnertest"
)

// Service implements driver.Sandboxes in memory. A sandbox is made asleep, as
// Substrate makes an actor, and unless KeepProcesses is called its snapshot
// does not keep its processes: a sandbox that is put to sleep or reverted
// wakes up with a runner that has no turn.
type Service struct {
	*runnertest.Runners
	// server serves the runner of each sandbox under /{name}/.
	server *httptest.Server

	mu        sync.Mutex
	sandboxes map[string]*made
	// slow keeps woken sandboxes starting.
	slow bool
	// draining are the sandboxes whose workers go away.
	draining map[string]bool
	// room is how many sandboxes the workers hold at once, or 0 for any
	// number.
	room int
	// keeps makes the snapshots keep the processes.
	keeps bool
}

type made struct {
	request sandbox.Request
	uid     string
	state   sandbox.State
	message string
	created time.Time
	// woken counts the resumes, and durable the durable suspensions.
	woken, durable int
	// operating is the number of resumes that find another operation in
	// progress.
	operating int
	// home outlives the runners.
	home   *runnertest.Home
	runner *runnertest.Runner
}

// New returns a Service without sandboxes.
func New(t testing.TB) *Service {
	s := &Service{Runners: runnertest.New(t), sandboxes: map[string]*made{}, draining: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/{sandbox}/", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("sandbox")
		s.mu.Lock()
		var runner *runnertest.Runner
		if made := s.sandboxes[name]; made != nil && made.state == sandbox.Running {
			runner = made.runner
		}
		s.mu.Unlock()
		if runner == nil {
			http.Error(w, "sandbox is not running", http.StatusServiceUnavailable)
			return
		}
		http.StripPrefix("/"+name, runner).ServeHTTP(w, r)
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func (s *Service) Create(_ context.Context, request sandbox.Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := sandbox.Name(request.Session)
	if m := s.sandboxes[name]; m != nil {
		m.request.Egress = request.Egress
		return nil
	}
	home := &runnertest.Home{}
	s.sandboxes[name] = &made{
		request: request, uid: uuid.NewString(), state: sandbox.Asleep, created: time.Now(),
		home: home, runner: s.Runners.Runner(name, home),
	}
	return nil
}

func (s *Service) SetEgress(_ context.Context, name string, egress sandbox.Egress) error {
	return s.change(name, func(m *made) { m.request.Egress = egress })
}

func (s *Service) Sandbox(_ context.Context, name string) (sandbox.Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.sandboxes[name]
	if m == nil {
		return sandbox.Sandbox{}, fmt.Errorf("sandbox %s: %w", name, sandbox.ErrNotFound)
	}
	// A starting sandbox becomes ready when it is read, unless slow is set.
	if m.state == sandbox.Starting && !s.slow {
		m.state = sandbox.Running
	}
	return s.seen(name, m), nil
}

func (s *Service) Sandboxes(context.Context) ([]sandbox.Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := []sandbox.Sandbox{}
	for name, m := range s.sandboxes {
		all = append(all, s.seen(name, m))
	}
	return all, nil
}

func (s *Service) seen(name string, m *made) sandbox.Sandbox {
	return sandbox.Sandbox{Name: name, UID: m.uid, Session: m.request.Session.String(), State: m.state, Message: m.message, Created: m.created}
}

// Resume wakes the sandbox up. It runs at once unless slow is set, which
// keeps it starting. A crashed sandbox is not woken up.
func (s *Service) Resume(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.sandboxes[name]
	switch {
	case m == nil:
		return fmt.Errorf("sandbox %s: %w", name, sandbox.ErrNotFound)
	case m.state == sandbox.Crashed:
		return fmt.Errorf("sandbox %s: %w", name, sandbox.ErrCrashed)
	case m.state != sandbox.Asleep:
		return nil
	case m.operating > 0:
		m.operating--
		return fmt.Errorf("sandbox %s: %w", name, sandbox.ErrOperating)
	case s.room > 0 && s.awake() >= s.room:
		return fmt.Errorf("sandbox %s: %w", name, sandbox.ErrNoWorker)
	}
	m.woken++
	m.state = sandbox.Running
	if s.slow {
		m.state = sandbox.Starting
	}
	return nil
}

// Operate has the next attempts to wake the sandbox find another operation
// on it in progress: so many of them.
func (s *Service) Operate(name string, attempts int) {
	s.change(name, func(m *made) { m.operating = attempts })
}

// Room makes the workers hold at most n sandboxes at once, or any number if n
// is 0: a sandbox that wakes up beyond them finds no worker with room.
func (s *Service) Room(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.room = n
}

// awake counts the sandboxes that hold a place on a worker. The caller holds
// mu.
func (s *Service) awake() int {
	awake := 0
	for _, m := range s.sandboxes {
		if m.state == sandbox.Running || m.state == sandbox.Starting {
			awake++
		}
	}
	return awake
}

// Suspend puts the sandbox to sleep. Its runner goes, which cuts off the
// event streams of its turn.
func (s *Service) Suspend(_ context.Context, name string, durable bool) error {
	return s.change(name, func(m *made) {
		if durable {
			m.durable++
		}
		s.sleep(name, m)
	})
}

// Revert puts the sandbox to sleep as its last snapshot has it, which the
// fake takes to be one without a turn: it wakes up with a runner that has
// none.
func (s *Service) Revert(_ context.Context, name string) error {
	return s.change(name, func(m *made) {
		m.message = ""
		s.sleep(name, m)
		m.runner = s.Runners.Runner(name, m.home)
	})
}

// sleep cuts off the event streams of the sandbox's runner, which goes unless
// the snapshots keep the processes.
func (s *Service) sleep(name string, m *made) {
	m.state = sandbox.Asleep
	m.runner.Gone()
	if !s.keeps {
		m.runner = s.Runners.Runner(name, m.home)
	}
}

// KeepProcesses makes the snapshots keep the processes, as memory snapshots
// do: a sandbox that is put to sleep wakes up with its runner and the turn
// that the runner has.
func (s *Service) KeepProcesses() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keeps = true
}

// Delete deletes the sandbox, with its runner and its home, if uid is empty
// or the sandbox's.
func (s *Service) Delete(_ context.Context, name, uid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil && (uid == "" || uid == m.uid) {
		m.runner.Gone()
		delete(s.sandboxes, name)
		delete(s.draining, name)
	}
	return nil
}

// Draining returns the sandboxes that Drain marked, while they run.
func (s *Service) Draining(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for name := range s.draining {
		if m := s.sandboxes[name]; m != nil && m.state == sandbox.Running {
			names = append(names, name)
		}
	}
	return names, nil
}

// Runner returns a client of the sandbox's runner.
func (s *Service) Runner(name string) *sandbox.Runner {
	return sandbox.RunnerAt(s.server.URL + "/" + name)
}

func (s *Service) change(name string, change func(*made)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.sandboxes[name]
	if m == nil {
		return fmt.Errorf("sandbox %s: %w", name, sandbox.ErrNotFound)
	}
	change(m)
	return nil
}

// Made returns the request of each sandbox by name, with its latest egress.
func (s *Service) Made() map[string]sandbox.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := map[string]sandbox.Request{}
	for name, m := range s.sandboxes {
		requests[name] = m.request
	}
	return requests
}

// Slow keeps woken sandboxes starting, as when the worker is slow to restore
// them. Slow(false) lets them run when they are next read.
func (s *Service) Slow(slow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slow = slow
}

// Age makes the sandbox older by the duration.
func (s *Service) Age(name string, by time.Duration) {
	_ = s.change(name, func(m *made) { m.created = m.created.Add(-by) })
}

// Restart replaces the sandbox's runner with one that has no turn, as when
// the sandbox crashed and was reverted and woken up again.
func (s *Service) Restart(name string) {
	_ = s.change(name, func(m *made) {
		m.runner.Gone()
		m.runner = s.Runners.Runner(name, m.home)
	})
}

// Crash makes the sandbox crash: its runner goes, and it does not run again
// until it is reverted.
func (s *Service) Crash(name, why string) {
	_ = s.change(name, func(m *made) {
		m.state, m.message = sandbox.Crashed, why
		m.runner.Gone()
		m.runner = s.Runners.Runner(name, m.home)
	})
}

// Drain marks the sandbox's worker as going away.
func (s *Service) Drain(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.draining[name] = true
}

// Lose deletes the sandbox, as if something else had deleted it.
func (s *Service) Lose(name string) {
	_ = s.Delete(context.Background(), name, "")
}

// Home returns the home of the sandbox, or nil if there is no such sandbox.
func (s *Service) Home(name string) *runnertest.Home {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil {
		return m.home
	}
	return nil
}

// UID returns the UID of the sandbox, or "" if there is no such sandbox.
func (s *Service) UID(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil {
		return m.uid
	}
	return ""
}

// Terminate leaves the sandbox being deleted: lost, and still listed.
func (s *Service) Terminate(name string) {
	_ = s.change(name, func(m *made) { m.state = sandbox.Lost })
}

// Woken returns how often the sandbox was woken up.
func (s *Service) Woken(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil {
		return m.woken
	}
	return 0
}

// Durable returns how often the sandbox was put to sleep durably.
func (s *Service) Durable(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil {
		return m.durable
	}
	return 0
}

// State returns the state of the sandbox without reading it, so that a
// starting sandbox stays starting.
func (s *Service) State(name string) sandbox.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil {
		return m.state
	}
	return ""
}
