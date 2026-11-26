package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"aviary/supervisor/store"
)

// readTimeout limits how long a scrape waits for the store.
const readTimeout = 8 * time.Second

// Counts is what State reads from the store.
type Counts interface {
	SandboxCounts(ctx context.Context) (map[string]int, error)
	UnfinishedTurns(ctx context.Context) ([]store.UnfinishedTurn, error)
}

// State reports the state of an installation, which it reads from the store
// whenever it is scraped. Every State of an installation reports the same, so
// an installation serves one, apart from the metrics of its Supervisors.
type State struct {
	registry *prometheus.Registry
}

// NewState returns a State that reads counts. It reports every series of the
// harnesses, also those without turns.
func NewState(counts Counts, harnesses []string) *State {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		sandboxes{counts: counts, desc: prometheus.NewDesc(
			"aviary_sandboxes", "Number of sandboxes of sessions that have not ended, by state.", []string{"state"}, nil,
		)},
		turns{
			counts: counts, harnesses: harnesses,
			count: prometheus.NewDesc(
				"aviary_turns", "Number of turns that have not ended, by harness and state.", []string{"harness", "state"}, nil,
			),
			oldest: prometheus.NewDesc(
				"aviary_turns_oldest_age_seconds",
				"Time since the oldest turn of a harness and state left the queue or, for queued turns, since its message, in seconds. 0 without such a turn.",
				[]string{"harness", "state"}, nil,
			),
			asking: prometheus.NewDesc(
				"aviary_approvals_waiting", "Number of approval requests that wait for a person's answer, by harness.",
				[]string{"harness"}, nil,
			),
		},
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return &State{registry: registry}
}

// Handler serves the metrics. A scrape fails if the store cannot be read.
func (s *State) Handler() http.Handler {
	return promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{})
}

// unread fails the scrape because the store could not be read for the metric.
// The error may name the database, so it only goes to the log.
func unread(ctx context.Context, collected chan<- prometheus.Metric, metric *prometheus.Desc, what string, err error) {
	slog.ErrorContext(ctx, what, "error", err)
	collected <- prometheus.NewInvalidMetric(metric, errors.New(what+": the store could not be read"))
}

var sandboxStates = []string{store.SandboxAwake, store.SandboxAsleep, store.SandboxLost}

// sandboxes collects the number of sandboxes in each state.
type sandboxes struct {
	counts Counts
	desc   *prometheus.Desc
}

func (s sandboxes) Describe(descs chan<- *prometheus.Desc) {
	descs <- s.desc
}

func (s sandboxes) Collect(collected chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	counts, err := s.counts.SandboxCounts(ctx)
	if err != nil {
		unread(ctx, collected, s.desc, "count sandboxes", err)
		return
	}
	// Every state is reported, so that a state without sandboxes reads 0.
	for _, state := range sandboxStates {
		collected <- prometheus.MustNewConstMetric(s.desc, prometheus.GaugeValue, float64(counts[state]), state)
	}
}

// The states of an unfinished turn that the metrics tell apart. A turn that
// has left the queue is starting until its harness has the message. From then
// on it is running, or awaiting approval while a request of it waits for a
// person.
const (
	turnQueued           = "queued"
	turnStarting         = "starting"
	turnRunning          = "running"
	turnAwaitingApproval = "awaiting_approval"
)

var turnStates = []string{turnQueued, turnStarting, turnRunning, turnAwaitingApproval}

func stateOf(turn store.UnfinishedTurn) string {
	switch {
	case turn.State == store.TurnQueued:
		return turnQueued
	case turn.Asking > 0:
		return turnAwaitingApproval
	case turn.Started:
		return turnRunning
	}
	return turnStarting
}

// turns collects the number of unfinished turns of each harness in each
// state, the age of the oldest of them, and the number of their approval
// requests that wait for a person.
type turns struct {
	counts                Counts
	harnesses             []string
	count, oldest, asking *prometheus.Desc
}

func (t turns) Describe(descs chan<- *prometheus.Desc) {
	descs <- t.count
	descs <- t.oldest
	descs <- t.asking
}

func (t turns) Collect(collected chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	unfinished, err := t.counts.UnfinishedTurns(ctx)
	if err != nil {
		unread(ctx, collected, t.count, "list unfinished turns", err)
		return
	}
	type group struct{ harness, state string }
	count, oldest, asking := map[group]int{}, map[group]time.Duration{}, map[string]int{}
	for _, harness := range t.harnesses {
		asking[harness] = 0
		for _, state := range turnStates {
			count[group{harness, state}] = 0
		}
	}
	for _, turn := range unfinished {
		of := group{turn.Harness, stateOf(turn)}
		count[of]++
		oldest[of] = max(oldest[of], turn.Age)
		asking[turn.Harness] += turn.Asking
	}
	for of, turns := range count {
		collected <- prometheus.MustNewConstMetric(t.count, prometheus.GaugeValue, float64(turns), of.harness, of.state)
		collected <- prometheus.MustNewConstMetric(t.oldest, prometheus.GaugeValue, oldest[of].Seconds(), of.harness, of.state)
	}
	for harness, requests := range asking {
		collected <- prometheus.MustNewConstMetric(t.asking, prometheus.GaugeValue, float64(requests), harness)
	}
}
