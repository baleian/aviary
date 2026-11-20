// Package sandboxtest has the sandboxes of the sessions for tests, as the
// cluster keeps them, in memory, with a runner in each that the test plays.
package sandboxtest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/internal/sandbox"
	"aviary/supervisor/internal/sandbox/runnertest"
)

// Service is the sandboxes, as the driver asks for them.
type Service struct {
	*runnertest.Runners
	// server serves the runners: the runner of a sandbox under its name.
	server *httptest.Server

	mu        sync.Mutex
	sandboxes map[string]*made
	// slow is whether a sandbox that is made or woken stays starting.
	slow bool
}

type made struct {
	request sandbox.Request
	state   sandbox.State
	created time.Time
	// version counts the changes of the sandbox.
	version int
	// expires is when the sandbox is deleted, as it was told, and woken how
	// often it was woken.
	expires time.Time
	woken   int
	// sleepy is whether it is put to sleep just before it is next kept.
	sleepy bool
	runner *runnertest.Runner
}

// New returns the sandboxes of a test, of which there are none yet.
func New(t testing.TB) *Service {
	s := &Service{Runners: runnertest.New(t), sandboxes: map[string]*made{}}
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
			http.Error(w, "the sandbox does not run", http.StatusServiceUnavailable)
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
	if s.sandboxes[name] != nil {
		return nil
	}
	m := &made{request: request, state: sandbox.Starting, created: time.Now(), version: 1, runner: s.Runner(name)}
	if request.Kept > 0 {
		m.expires = time.Now().Add(request.Kept)
	}
	s.sandboxes[name] = m
	return nil
}

func (s *Service) Sandbox(_ context.Context, name string) (sandbox.Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.sandboxes[name]
	if m == nil {
		return sandbox.Sandbox{}, fmt.Errorf("the sandbox %s: %w", name, sandbox.ErrNotFound)
	}
	// A sandbox takes a moment to run: until it is looked at.
	if m.state == sandbox.Starting && !s.slow {
		m.state = sandbox.Running
		m.version++
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
	seen := sandbox.Sandbox{Name: name, Session: m.request.Session.String(), State: m.state, Created: m.created, Version: strconv.Itoa(m.version)}
	if m.state == sandbox.Running {
		seen.Address = s.server.URL + "/" + name
	}
	return seen
}

// Delete deletes the sandbox. Its runner is gone with its pod.
func (s *Service) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil {
		m.runner.Gone()
		delete(s.sandboxes, name)
	}
	return nil
}

// Suspend puts the sandbox to sleep, if it is of the version, or of any
// without one. Its runner is gone with its pod, and those who read the
// events of its turn are cut off.
func (s *Service) Suspend(_ context.Context, name, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.sandboxes[name]
	switch {
	case m == nil:
		return fmt.Errorf("the sandbox %s: %w", name, sandbox.ErrNotFound)
	case version != "" && version != strconv.Itoa(m.version):
		return fmt.Errorf("the sandbox %s: %w", name, sandbox.ErrChanged)
	}
	s.suspend(name, m)
	return nil
}

func (s *Service) suspend(name string, m *made) {
	m.state = sandbox.Asleep
	m.version++
	m.runner.Gone()
	m.runner = s.Runner(name)
}

// Resume wakes the sandbox, of the harness, with a runner that has no turn.
func (s *Service) Resume(_ context.Context, name string, harness sandbox.Harness) error {
	return s.change(name, func(m *made) {
		m.request.Harness = harness
		if m.state == sandbox.Asleep {
			m.state = sandbox.Starting
			m.woken++
		}
	})
}

func (s *Service) Keep(_ context.Context, name string, _ uuid.UUID, until time.Time) (sandbox.Sandbox, error) {
	var kept sandbox.Sandbox
	err := s.change(name, func(m *made) {
		if m.sleepy {
			m.sleepy = false
			s.suspend(name, m)
		}
		m.expires = until
		kept = s.seen(name, m)
	})
	return kept, err
}

func (s *Service) change(name string, change func(*made)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.sandboxes[name]
	if m == nil {
		return fmt.Errorf("the sandbox %s: %w", name, sandbox.ErrNotFound)
	}
	m.version++
	change(m)
	return nil
}

// Made returns the sandboxes that there are: what each was made for, by its
// name, with the harness that it was last woken with.
func (s *Service) Made() map[string]sandbox.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := map[string]sandbox.Request{}
	for name, m := range s.sandboxes {
		requests[name] = m.request
	}
	return requests
}

// Slow has the sandboxes that are made or woken stay starting, as they do on
// a node that does not have their image yet, or run once they are looked at
// again.
func (s *Service) Slow(slow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slow = slow
}

// SleepBeforeKeep has the sandbox put to sleep just before it is next kept
// for a turn, as by a Supervisor that found its session not at work just
// before the turn came.
func (s *Service) SleepBeforeKeep(name string) {
	_ = s.change(name, func(m *made) { m.sleepy = true })
}

// Age makes the sandbox so much older.
func (s *Service) Age(name string, by time.Duration) {
	_ = s.change(name, func(m *made) { m.created = m.created.Add(-by) })
}

// Restart has the runner of the sandbox started again, as the pod of one
// that was gone: it has no turn.
func (s *Service) Restart(name string) {
	_ = s.change(name, func(m *made) {
		m.runner.Gone()
		m.runner = s.Runner(name)
	})
}

// Lose loses the sandbox, with its volume, as if it had expired.
func (s *Service) Lose(name string) {
	_ = s.Delete(context.Background(), name)
}

// Expires returns when the sandbox is deleted, as it was told.
func (s *Service) Expires(name string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil {
		return m.expires
	}
	return time.Time{}
}

// Woken returns how often the sandbox was woken.
func (s *Service) Woken(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil {
		return m.woken
	}
	return 0
}

// State returns what the sandbox does, as the test sees it: it is not
// looked at for that.
func (s *Service) State(name string) sandbox.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil {
		return m.state
	}
	return ""
}
