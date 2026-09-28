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

// The stages of a deployment request, as deploygate_evaluation_errors_total
// labels them: the access policy runs first and decides the actor's roles,
// then the team's deploy policy.
const (
	StageAccess = "access"
	StageDeploy = "deploy"
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
	labelKind   = "kind"
	labelReason = "reason"
)

// evaluationBuckets suit a policy evaluation, which takes microseconds.
var evaluationBuckets = []float64{.00005, .0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1}

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

	// Access grants.
	grants                   *prometheus.CounterVec
	accessEvaluationDuration *prometheus.HistogramVec

	// The policy bundles.
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
		}, []string{labelTeam, labelPolicy, "decision", labelReason}),
		evaluationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "evaluation_duration_seconds",
			Help:      "Time spent evaluating a team's deploy policy against one request.",
			Buckets:   evaluationBuckets,
		}, []string{labelTeam}),
		evaluationErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "evaluation_errors_total",
			Help:      "Evaluations that failed, by team, kind of failure (assertion, runtime, conflict) and stage (access, deploy).",
		}, []string{labelTeam, labelKind, "stage"}),
		grants: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "access_grants_total",
			Help:      "Roles the access policy granted, by team, role and reason.",
		}, []string{labelTeam, "role", labelReason}),
		accessEvaluationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "access_evaluation_duration_seconds",
			Help:      "Time spent evaluating the access policy against one request.",
			Buckets:   evaluationBuckets,
		}, []string{labelTeam}),
		reloads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "policy_reloads_total",
			Help:      "Attempts to load a policy bundle, by the kind it implements and result. A failure keeps the previous bundle serving.",
		}, []string{labelKind, "result"}),
		lastReload: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "policy_last_reload_timestamp_seconds",
			Help:      "Unix time of the last successful load of any policy bundle.",
		}),
		loadedInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "policy_loaded_info",
			Help:      "One series per policy currently serving, always 1. team is empty for a policy that serves every team, such as access.main.",
		}, []string{labelKind, labelTeam, labelPolicy, "source"}),
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
		m.grants,
		m.accessEvaluationDuration,
		m.reloads,
		m.lastReload,
		m.loadedInfo,
		m.requests,
		m.requestDuration,
	)

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

// AccessTimer starts timing one evaluation of the access policy for team.
// Calling ObserveDuration on it records the evaluation and returns how long
// it took.
func (m *Metrics) AccessTimer(team string) *prometheus.Timer {
	return prometheus.NewTimer(m.accessEvaluationDuration.WithLabelValues(team))
}

// ObserveGrant counts one role the access policy granted for team.
func (m *Metrics) ObserveGrant(team, role, reason string) {
	m.grants.WithLabelValues(team, role, reason).Inc()
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
// ErrorKind constants, in stage, StageAccess or StageDeploy.
func (m *Metrics) ObserveEvaluationError(stage, team, kind string) {
	m.evaluationErrors.WithLabelValues(team, kind, stage).Inc()
}

// PrepareReloads creates both reload results for a policy kind, so a
// failure-rate alert has a series to divide by before the first failure.
func (m *Metrics) PrepareReloads(kind string) {
	m.reloads.WithLabelValues(kind, reloadSuccess)
	m.reloads.WithLabelValues(kind, reloadFailure)
}

// ObserveReloadFailure counts a load of kind's bundle that was rejected.
func (m *Metrics) ObserveReloadFailure(kind string) {
	m.reloads.WithLabelValues(kind, reloadFailure).Inc()
}

// ObserveReloadSuccess counts a successful load of kind's bundle at the given
// time and replaces that kind's loaded-policy series with the policies that
// now serve, so a team that was dropped from the configuration disappears
// from the gauge while the other kind's series stay.
func (m *Metrics) ObserveReloadSuccess(kind string, at time.Time, source string, loaded []LoadedPolicy) {
	m.reloads.WithLabelValues(kind, reloadSuccess).Inc()
	m.lastReload.Set(float64(at.UnixNano()) / float64(time.Second))

	m.loadedInfo.DeletePartialMatch(prometheus.Labels{labelKind: kind})
	for _, p := range loaded {
		m.loadedInfo.WithLabelValues(kind, p.Team, p.Policy, source).Set(1)
	}
}

// LoadedPolicy is one policy a bundle serves, for the loaded-policy gauge.
// Team is empty for a policy that isn't a team's, such as access.main.
type LoadedPolicy struct {
	Team   string
	Policy string
}
