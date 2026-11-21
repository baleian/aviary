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
}

// State reports the state of an installation, which it reads from the store
// whenever it is scraped. Every State of an installation reports the same, so
// an installation serves one, apart from the metrics of its Supervisors.
type State struct {
	registry *prometheus.Registry
}

// NewState returns a State that reads counts.
func NewState(counts Counts) *State {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		sandboxes{counts: counts, desc: prometheus.NewDesc(
			"aviary_sandboxes", "Number of sandboxes of sessions that have not ended, by state.", []string{"state"}, nil,
		)},
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return &State{registry: registry}
}

// Handler serves the metrics. A scrape fails if the store cannot be read.
func (s *State) Handler() http.Handler {
	return promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{})
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
		// The error may name the database, so it stays in the log.
		slog.ErrorContext(ctx, "count sandboxes", "error", err)
		collected <- prometheus.NewInvalidMetric(s.desc, errors.New("the sandboxes could not be counted"))
		return
	}
	// Every state is reported, so that a state without sandboxes reads 0.
	for _, state := range sandboxStates {
		collected <- prometheus.MustNewConstMetric(s.desc, prometheus.GaugeValue, float64(counts[state]), state)
	}
}
