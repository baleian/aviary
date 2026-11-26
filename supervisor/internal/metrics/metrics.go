// Package metrics defines the Supervisor's Prometheus metrics.
package metrics

import (
	"net/http"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"aviary/supervisor/store"
)

// Metrics counts and times the turns. Each Supervisor reports only its own
// work, so that the metrics of all replicas add up. State that exists only in
// the database, such as the number of queued turns, is left to State, since
// every replica would report the same value.
type Metrics struct {
	registry   *prometheus.Registry
	queue      prometheus.Histogram
	followed   *prometheus.GaugeVec
	ended      *prometheus.CounterVec
	waited     *prometheus.HistogramVec
	startup    *prometheus.HistogramVec
	ran        *prometheus.HistogramVec
	tokens     *prometheus.CounterVec
	refused    *prometheus.CounterVec
	approved   *prometheus.HistogramVec
	secretRead *prometheus.HistogramVec
}

// Reasons for Refused.
const (
	QueueFull   = "queue_full"
	SessionBusy = "session_busy"
)

var (
	refusals = []string{QueueFull, SessionBusy}
	// The states in which a turn that ran can end.
	ranStates = []string{store.TurnCompleted, store.TurnFailed, store.TurnCancelled}
	failures  = []string{
		store.FailureHarness, store.FailureStartup, store.FailureResume, store.FailureTimedOut,
		store.FailureSandboxLost, store.FailureSessionExpired, store.FailureRunner,
	}
	tokenKinds = []string{"input", "output", "thought", "cached_read", "cached_write"}
	// The kinds of the options of the Agent Client Protocol, and the outcomes
	// recorded besides them.
	approvalOutcomes = []string{"allow_once", "allow_always", "reject_once", "reject_always", "cancelled", "other"}
	readOutcomes     = []string{"read", "failed"}
)

// New returns the metrics with every series of the harnesses and the secret
// store kinds at zero. A series that appears with its first event would not
// count that event in rate() or increase().
func New(harnesses, secretStoreKinds []string) *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		queue: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "aviary_turn_queue_length",
			Help:    "Number of queued turns when a turn is accepted, including that turn.",
			Buckets: []float64{0, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000},
		}),
		followed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "aviary_turns_followed",
			Help: "Number of turns that this Supervisor follows.",
		}, []string{"harness"}),
		ended: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "aviary_turns_ended_total",
			Help: "Number of turns that have ended, by state and, for failed turns, reason.",
		}, []string{"harness", "state", "reason"}),
		waited: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aviary_turn_queue_duration_seconds",
			Help:    "Time that a turn waited in the queue, from its message until dispatch, in seconds.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600},
		}, []string{"harness"}),
		startup: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aviary_turn_startup_duration_seconds",
			Help:    "Time from a turn's dispatch until its harness had the message, in seconds.",
			Buckets: []float64{0.5, 1, 2, 3, 5, 10, 20, 30, 60, 90, 120},
		}, []string{"harness"}),
		ran: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aviary_turn_run_duration_seconds",
			Help:    "Time from a turn's harness having the message until the turn ended, in seconds.",
			Buckets: []float64{1, 2, 5, 10, 30, 60, 120, 300, 600, 1800, 3600, 7200, 14400, 43200, 86400},
		}, []string{"harness", "state"}),
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "aviary_turn_tokens_total",
			Help: "Number of tokens used by turns, as reported by their harnesses. Not a measurement: harnesses differ in what they count.",
		}, []string{"harness", "kind"}),
		refused: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "aviary_messages_refused_total",
			Help: "Number of messages that were not accepted as turns, by reason.",
		}, []string{"reason"}),
		approved: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aviary_approval_wait_seconds",
			Help:    "Time that an approval request waited, from the harness asking until it had the answer, in seconds. outcome is the kind of the selected option, or cancelled if the turn was cancelled before anyone answered.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600},
		}, []string{"harness", "outcome"}),
		secretRead: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aviary_secret_read_seconds",
			Help:    "Time to read a value from a project's secret store, in seconds, by store kind and outcome. Values cached by this Supervisor are not counted.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 3},
		}, []string{"kind", "outcome"}),
	}
	m.registry.MustRegister(
		m.queue, m.followed, m.ended, m.waited, m.startup, m.ran, m.tokens, m.refused, m.approved, m.secretRead,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	for _, reason := range refusals {
		m.refused.WithLabelValues(reason)
	}
	for _, harness := range harnesses {
		m.followed.WithLabelValues(harness)
		m.waited.WithLabelValues(harness)
		m.startup.WithLabelValues(harness)
		for _, state := range []string{store.TurnCompleted, store.TurnCancelled, store.TurnExpired} {
			m.ended.WithLabelValues(harness, state, "")
		}
		for _, reason := range failures {
			m.ended.WithLabelValues(harness, store.TurnFailed, reason)
		}
		for _, state := range ranStates {
			m.ran.WithLabelValues(harness, state)
		}
		for _, kind := range tokenKinds {
			m.tokens.WithLabelValues(harness, kind)
		}
		for _, outcome := range approvalOutcomes {
			m.approved.WithLabelValues(harness, outcome)
		}
	}
	for _, kind := range secretStoreKinds {
		for _, outcome := range readOutcomes {
			m.secretRead.WithLabelValues(kind, outcome)
		}
	}
	return m
}

// Handler serves the metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Accepted records an accepted turn and the number of queued turns, including
// it.
func (m *Metrics) Accepted(queued int) {
	m.queue.Observe(float64(queued))
}

// Following counts a turn that this Supervisor follows, until the returned
// function is called.
func (m *Metrics) Following(harness string) (left func()) {
	followed := m.followed.WithLabelValues(harness)
	followed.Inc()
	return followed.Dec
}

// Dispatched records how long a dispatched turn was queued.
func (m *Metrics) Dispatched(harness string, waited time.Duration) {
	m.waited.WithLabelValues(harness).Observe(waited.Seconds())
}

// Started records how long a turn took from dispatch until its harness had
// the message.
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

// Approved records how long an approval request waited for its answer. The
// outcome is the kind of the chosen option, or "cancelled". Kinds other than
// those of the Agent Client Protocol are recorded as "other", since the
// runner is not trusted.
func (m *Metrics) Approved(harness, outcome string, waited time.Duration) {
	if !slices.Contains(approvalOutcomes, outcome) {
		outcome = "other"
	}
	m.approved.WithLabelValues(harness, outcome).Observe(waited.Seconds())
}

// SecretRead records the duration and outcome of a read from a secret store
// of the kind.
func (m *Metrics) SecretRead(kind string, took time.Duration, err error) {
	outcome := "read"
	if err != nil {
		outcome = "failed"
	}
	m.secretRead.WithLabelValues(kind, outcome).Observe(took.Seconds())
}

// Refused records that a message was not accepted as a turn.
func (m *Metrics) Refused(reason string) {
	m.refused.WithLabelValues(reason).Inc()
}
