// Package metrics holds what the Supervisor reports of the turns, in the form
// that Prometheus reads.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"aviary/supervisor/store"
)

// Metrics counts and times the turns. Of several Supervisors, each reports
// what it did and what it holds itself, so the numbers of all of them add
// up. None of them reports what is only in the database, such as the number
// of turns that wait: every Supervisor would report the same number.
type Metrics struct {
	registry *prometheus.Registry
	queue    prometheus.Histogram
	running  *prometheus.GaugeVec
	ended    *prometheus.CounterVec
	waited   *prometheus.HistogramVec
	startup  *prometheus.HistogramVec
	ran      *prometheus.HistogramVec
	tokens   *prometheus.CounterVec
	refused  *prometheus.CounterVec
	approved *prometheus.HistogramVec
}

func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		queue: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "aviary_turn_queue_length",
			Help:    "How many turns waited to run when a turn was accepted, that turn among them. It is zero for a turn that did not have to wait.",
			Buckets: []float64{0, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000},
		}),
		running: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "aviary_turns_running",
			Help: "The turns that run and whose runners are connected to this Supervisor.",
		}, []string{"harness"}),
		ended: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "aviary_turns_ended_total",
			Help: "The turns that have ended, by how they ended. The reason is set for a failed turn.",
		}, []string{"harness", "state", "reason"}),
		waited: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aviary_turn_queue_duration_seconds",
			Help:    "How long a turn waited to run, from its message to its pod being asked for.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600},
		}, []string{"harness"}),
		startup: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aviary_turn_startup_duration_seconds",
			Help:    "How long a turn took to start, from its pod being asked for to its harness having the message.",
			Buckets: []float64{0.5, 1, 2, 3, 5, 10, 20, 30, 60, 90, 120},
		}, []string{"harness"}),
		ran: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aviary_turn_run_duration_seconds",
			Help:    "How long a turn ran, from its harness having the message to the turn having ended.",
			Buckets: []float64{1, 2, 5, 10, 30, 60, 120, 300, 600, 1800, 3600, 7200, 14400, 43200, 86400},
		}, []string{"harness", "state"}),
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "aviary_turn_tokens_total",
			Help: "The token usage of the turns as their harnesses reported it. It is not a measurement: harnesses differ in what they count.",
		}, []string{"harness", "kind"}),
		refused: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "aviary_messages_refused_total",
			Help: "The messages that were not accepted as turns, by the reason.",
		}, []string{"reason"}),
		approved: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aviary_approval_wait_seconds",
			Help:    "How long a request of a harness for approval waited, from the harness asking to the harness having its answer, by the kind of the option that it was given. cancelled: its turn was told to stop before anybody answered.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600},
		}, []string{"harness", "outcome"}),
	}
	m.registry.MustRegister(
		m.queue, m.running, m.ended, m.waited, m.startup, m.ran, m.tokens, m.refused, m.approved,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler serves the metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Accepted records that a message was accepted as a turn when so many turns
// waited to run, the new one among them.
func (m *Metrics) Accepted(waiting int) {
	m.queue.Observe(float64(waiting))
}

// Connected records that the runner of a turn of the harness has connected
// to this Supervisor. The function that it returns records that the runner
// is gone.
func (m *Metrics) Connected(harness string) (gone func()) {
	running := m.running.WithLabelValues(harness)
	running.Inc()
	return running.Dec
}

// Dispatched records that a turn of the harness left the queue after it had
// waited for so long.
func (m *Metrics) Dispatched(harness string, waited time.Duration) {
	m.waited.WithLabelValues(harness).Observe(waited.Seconds())
}

// Started records that the harness got the message of a turn so long after
// the pod of the turn was asked for.
func (m *Metrics) Started(harness string, startup time.Duration) {
	m.startup.WithLabelValues(harness).Observe(startup.Seconds())
}

// Ended records how a turn of the harness ended.
func (m *Metrics) Ended(harness string, turn store.Turn) {
	var reason string
	if turn.Failure != nil {
		reason = turn.Failure.Reason
	}
	m.ended.WithLabelValues(harness, turn.State, reason).Inc()
	if turn.StartedAt != nil && turn.EndedAt != nil {
		m.ran.WithLabelValues(harness, turn.State).Observe(turn.EndedAt.Sub(*turn.StartedAt).Seconds())
	}
	if usage := turn.Usage; usage != nil {
		m.tokens.WithLabelValues(harness, "input").Add(float64(usage.InputTokens))
		m.tokens.WithLabelValues(harness, "output").Add(float64(usage.OutputTokens))
		for kind, tokens := range map[string]*int64{
			"thought": usage.ThoughtTokens, "cached_read": usage.CachedReadTokens, "cached_write": usage.CachedWriteTokens,
		} {
			if tokens != nil {
				m.tokens.WithLabelValues(harness, kind).Add(float64(*tokens))
			}
		}
	}
}

// Approved records that a harness got its answer to a request for approval
// after it had waited for so long. The outcome is the kind of the option that
// it was given, or cancelled. The kinds are those of the Agent Client
// Protocol: a runner is not trusted to make up more of them.
func (m *Metrics) Approved(harness, outcome string, waited time.Duration) {
	switch outcome {
	case "allow_once", "allow_always", "reject_once", "reject_always", "cancelled":
	default:
		outcome = "other"
	}
	m.approved.WithLabelValues(harness, outcome).Observe(waited.Seconds())
}

// Refused records that a message was not accepted as a turn.
func (m *Metrics) Refused(reason string) {
	m.refused.WithLabelValues(reason).Inc()
}
