package telemetry

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Namespace prefixes every metric the service exports, including the HTTP
// request metrics, so one dashboard selector finds them.
const Namespace = "deploygate"

// The error kinds deploygate_evaluation_errors_total counts. A conflict is a
// defect in a policy while a failed assert is usually a bad input, so they are
// alerted on separately.
const (
	ErrorKindAssertion = "assertion"
	ErrorKindRuntime   = "runtime"
	ErrorKindConflict  = "conflict"
)

// The results deploygate_policy_reloads_total counts.
const (
	reloadSuccess = "success"
	reloadFailure = "failure"
)

// The label names shared by several metrics.
const (
	labelTeam   = "team"
	labelPolicy = "policy"
)

// Metrics holds the service's Prometheus collectors on a registry the service
// owns, so tests can build as many as they like without colliding in the
// global default registry, and /metrics exposes exactly what is registered
// here.
type Metrics struct {
	registry *prometheus.Registry

	// Deploy decisions.
	decisions          *prometheus.CounterVec
	evaluationDuration *prometheus.HistogramVec
	evaluationErrors   *prometheus.CounterVec

	// The team bundle.
	reloads    *prometheus.CounterVec
	lastReload prometheus.Gauge
	loadedInfo *prometheus.GaugeVec

	// HTTP requests.
	requests        *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

// NewMetrics creates the service's collectors, the Go runtime and process
// collectors among them, and registers them on a fresh registry.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "decisions_total",
			Help:      "Deploy decisions returned, by team, evaluated policy, decision and reason. Failed evaluations count with their fallback decision.",
		}, []string{labelTeam, labelPolicy, "decision", "reason"}),
		evaluationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "evaluation_duration_seconds",
			Help:      "Time spent evaluating a team's policy against one request.",
			Buckets:   []float64{.00005, .0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1},
		}, []string{labelTeam}),
		evaluationErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "evaluation_errors_total",
			Help:      "Evaluations that failed, by team and kind of failure (assertion, runtime, conflict).",
		}, []string{labelTeam, "kind"}),
		reloads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "policy_reloads_total",
			Help:      "Attempts to load the team policy bundle, by result. A failure keeps the previous bundle serving.",
		}, []string{"result"}),
		lastReload: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "policy_last_reload_timestamp_seconds",
			Help:      "Unix time of the last successful load of the team policy bundle.",
		}),
		loadedInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "policy_loaded_info",
			Help:      "One series per policy currently serving, always 1.",
		}, []string{labelTeam, labelPolicy, "source"}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "requests_total",
			Help:      "HTTP requests answered, by status code, method and route template.",
		}, []string{"code", "method", "url"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "request_duration_seconds",
			Help:      "Time spent answering an HTTP request, by status code, method and route template.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"code", "method", "url"}),
	}

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.decisions,
		m.evaluationDuration,
		m.evaluationErrors,
		m.reloads,
		m.lastReload,
		m.loadedInfo,
		m.requests,
		m.requestDuration,
	)

	// Both results exist from the start, so a failure-rate alert has a series
	// to divide by before the first failure happens.
	m.reloads.WithLabelValues(reloadSuccess)
	m.reloads.WithLabelValues(reloadFailure)

	return m
}

// Registry returns the registry the service's collectors live on, for callers
// that want to add their own.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// RequestTimer starts timing one HTTP request. Call the function it returns
// once the request is answered, with its labels, which are only known then.
// The caller keeps the labels bounded: every value becomes a series.
func (m *Metrics) RequestTimer() func(code, method, route string) {
	var labels []string
	timer := prometheus.NewTimer(prometheus.ObserverFunc(func(seconds float64) {
		m.requestDuration.WithLabelValues(labels...).Observe(seconds)
	}))
	return func(code, method, route string) {
		labels = []string{code, method, route}
		timer.ObserveDuration()
		m.requests.WithLabelValues(labels...).Inc()
	}
}

// EvaluationTimer starts timing one evaluation of team's policy. Calling
// ObserveDuration on it records the evaluation and returns how long it took.
func (m *Metrics) EvaluationTimer(team string) *prometheus.Timer {
	return prometheus.NewTimer(m.evaluationDuration.WithLabelValues(team))
}

// ObserveDecision counts one decision a team's policy returned.
func (m *Metrics) ObserveDecision(team, policyName, decision, reason string) {
	m.decisions.WithLabelValues(team, policyName, decision, reason).Inc()
}

// ObserveEvaluationError counts one failed evaluation of kind, one of the
// ErrorKind constants.
func (m *Metrics) ObserveEvaluationError(team, kind string) {
	m.evaluationErrors.WithLabelValues(team, kind).Inc()
}

// ObserveReloadFailure counts a load of the team bundle that was rejected.
func (m *Metrics) ObserveReloadFailure() {
	m.reloads.WithLabelValues(reloadFailure).Inc()
}

// ObserveReloadSuccess counts a successful load at the given time and replaces
// the loaded-policy series with the policies that now serve, so a team that
// was dropped from the configuration disappears from the gauge.
func (m *Metrics) ObserveReloadSuccess(at time.Time, source string, teamPolicies map[string]string) {
	m.reloads.WithLabelValues(reloadSuccess).Inc()
	m.lastReload.Set(float64(at.UnixNano()) / float64(time.Second))

	m.loadedInfo.Reset()
	for team, policyName := range teamPolicies {
		m.loadedInfo.WithLabelValues(team, policyName, source).Set(1)
	}
}
