// Package sandboxtest provides an in-memory fake of the session sandboxes,
// with a fake runner in each.
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

// Service implements driver.Sandboxes in memory.
type Service struct {
	*runnertest.Runners
	// server serves the runner of each sandbox under /{name}/.
	server *httptest.Server

	mu        sync.Mutex
	sandboxes map[string]*made
	// slow keeps created and woken sandboxes starting.
	slow bool
}

type made struct {
	request sandbox.Request
	state   sandbox.State
	created time.Time
	// version is incremented by every change, like a resourceVersion.
	version int
	// expires is the requested shutdown time, and woken counts the resumes.
	expires time.Time
	woken   int
	// sleepy puts the sandbox to sleep right before the next Keep.
	sleepy bool
	runner *runnertest.Runner
}

// New returns a Service without sandboxes.
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
	// A starting sandbox becomes ready when it is read, unless slow is set.
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

// Delete deletes the sandbox, and its runner with it.
func (s *Service) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.sandboxes[name]; m != nil {
		m.runner.Gone()
		delete(s.sandboxes, name)
	}
	return nil
}

// Suspend puts the sandbox to sleep if version is empty or current. Its
// runner goes with the pod, which cuts off the event streams of its turn.
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

// Resume wakes the sandbox with the pod and a runner that has no turn.
func (s *Service) Resume(_ context.Context, name string, pod sandbox.Pod) error {
	return s.change(name, func(m *made) {
		m.request.Pod = pod
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

// Made returns the request of each sandbox by name, with the pod that it
// last woke with.
func (s *Service) Made() map[string]sandbox.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := map[string]sandbox.Request{}
	for name, m := range s.sandboxes {
		requests[name] = m.request
	}
	return requests
}

// Slow keeps created and woken sandboxes starting, as on a node that is
// still pulling the image. Slow(false) lets them run when they are next read.
func (s *Service) Slow(slow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slow = slow
}

// SleepBeforeKeep puts the sandbox to sleep right before its next Keep, as
// another Supervisor that finds the session idle would.
func (s *Service) SleepBeforeKeep(name string) {
	_ = s.change(name, func(m *made) { m.sleepy = true })
}

// Age makes the sandbox older by the duration.
func (s *Service) Age(name string, by time.Duration) {
	_ = s.change(name, func(m *made) { m.created = m.created.Add(-by) })
}

// Restart replaces the sandbox's runner with one that has no turn, as a
// recreated pod would.
func (s *Service) Restart(name string) {
	_ = s.change(name, func(m *made) {
		m.runner.Gone()
		m.runner = s.Runner(name)
	})
}

// Lose deletes the sandbox, as if it had expired.
func (s *Service) Lose(name string) {
	_ = s.Delete(context.Background(), name)
}

// Expires returns the requested shutdown time of the sandbox.
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
