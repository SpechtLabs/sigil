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
const Namespace = "alertrouter"

// The error kinds alertrouter_evaluation_errors_total counts. A conflict is a
// defect in a policy, such as a team rule that pages someone else than the
// platform's rule for the same reason, while a failed assert is usually a bad
// input, so they are alerted on separately. A timeout is an evaluation that
// ran past the evaluation timeout, alertrouter's failure to answer in time.
// A request the client canceled isn't a kind: nothing failed, so it isn't
// counted here, and alertrouter_requests_total{code="499"} records it.
const (
	ErrorKindAssertion = "assertion"
	ErrorKindRuntime   = "runtime"
	ErrorKindConflict  = "conflict"
	ErrorKindTimeout   = "timeout"
)

// The alert statuses alertrouter_alerts_received_total counts, as
// Alertmanager sends them.
const (
	AlertFiring   = "firing"
	AlertResolved = "resolved"
)

// The outcomes alertrouter_alerts_routed_total counts, one per firing alert:
// the team's policy decided it, no team owns it, the router couldn't read it,
// or its evaluation failed. Every outcome but routed ends in the fallback
// decision, so each still ends in a notification.
const (
	OutcomeRouted  = "routed"
	OutcomeUnowned = "unowned"
	OutcomeInvalid = "invalid"
	OutcomeFailed  = "failed"
)

// NoTeam is the team label of an alert no team in the directory owns. The
// alert's own team label is chosen by whoever wrote the alert rule, so it
// never becomes a label value: every distinct one would be a new series.
const NoTeam = "-"

// The results alertrouter_policy_reloads_total counts.
const (
	reloadSuccess = "success"
	reloadFailure = "failure"
)

// The label names shared by several metrics.
const (
	labelTeam     = "team"
	labelPolicy   = "policy"
	labelDecision = "decision"
	labelMethod   = "method"
	labelRoute    = "route"
)

var (
	// evaluationBuckets suit a policy evaluation, which takes microseconds.
	evaluationBuckets = []float64{.00005, .0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1}

	// batchBuckets suit a webhook's alert count, from a single alert up to
	// the thousand a webhook may carry.
	batchBuckets = []float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000}
)

// Metrics holds the service's Prometheus collectors on a registry the service
// owns, so tests can build as many as they like without colliding in the
// global default registry, and /metrics exposes exactly what is registered
// here. Its methods are safe for concurrent use.
type Metrics struct {
	registry *prometheus.Registry

	// Alerts and their routing.
	received      *prometheus.CounterVec
	routed        *prometheus.CounterVec
	batchSize     prometheus.Histogram
	notifications *prometheus.CounterVec

	// Policy decisions.
	decisions          *prometheus.CounterVec
	evaluationDuration *prometheus.HistogramVec
	evaluationErrors   *prometheus.CounterVec

	// The policy bundle.
	reloads          *prometheus.CounterVec
	lastReload       prometheus.Gauge
	reloadSuccessful prometheus.Gauge
	loadedInfo       *prometheus.GaugeVec

	// HTTP requests.
	requests        *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

// NewMetrics creates the service's collectors, the Go runtime and process
// collectors among them, and registers them on a fresh registry.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		received: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "alerts_received_total",
			Help:      "Alerts received, by status as Alertmanager sent it (firing, resolved). Only firing alerts are routed.",
		}, []string{"status"}),
		routed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "alerts_routed_total",
			Help:      "Firing alerts routed, by team and outcome: routed by the team's policy, unowned by any team, invalid, or failed in evaluation. Every outcome ends in a notification; team is - for an unowned alert.",
		}, []string{labelTeam, "outcome"}),
		batchSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "webhook_batch_size",
			Help:      "Alerts per Alertmanager webhook received.",
			Buckets:   batchBuckets,
		}),
		notifications: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "notifications_total",
			Help:      "Notifications dispatched, by decision and destination: the paged target, the channel posted to, or - for a drop.",
		}, []string{labelDecision, "destination"}),
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "decisions_total",
			Help:      "Decisions a team's policy made, by team, evaluated policy, decision and reason. A failed evaluation made no decision and counts only in alertrouter_evaluation_errors_total, and an unowned or invalid alert is never evaluated.",
		}, []string{labelTeam, labelPolicy, labelDecision, "reason"}),
		evaluationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "evaluation_duration_seconds",
			Help:      "Time spent evaluating a team's policy against one alert.",
			Buckets:   evaluationBuckets,
		}, []string{labelTeam}),
		evaluationErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "evaluation_errors_total",
			Help:      "Evaluations that failed, by team and kind of failure (assertion, runtime, conflict, timeout). A request the client canceled isn't a failure and isn't counted.",
		}, []string{labelTeam, "kind"}),
		reloads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "policy_reloads_total",
			Help:      "Attempts to load the team policy bundle, by result. A failure keeps the previous bundle serving.",
		}, []string{"result"}),
		lastReload: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "policy_last_reload_timestamp_seconds",
			Help:      "Unix time of the last successful load of the team policy bundle; 0 before the first one.",
		}),
		reloadSuccessful: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "policy_last_reload_successful",
			Help:      "Whether the latest attempt to load the team policy bundle succeeded (1) or failed (0). A failed bundle stays 0 until a load succeeds, while the previous bundle keeps serving.",
		}),
		loadedInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "policy_loaded_info",
			Help:      "One series per team policy currently serving, always 1, with the fingerprint and source of the bundle it was loaded from.",
		}, []string{labelTeam, labelPolicy, "fingerprint", "source"}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "requests_total",
			Help:      "HTTP requests answered, by status code, method and route template.",
		}, []string{"code", labelMethod, labelRoute}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "request_duration_seconds",
			Help:      "Time spent answering an HTTP request, by method and route template.",
			Buckets:   prometheus.DefBuckets,
		}, []string{labelMethod, labelRoute}),
	}
	m.register()

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
	var method, route string
	timer := prometheus.NewTimer(prometheus.ObserverFunc(func(seconds float64) {
		m.requestDuration.WithLabelValues(method, route).Observe(seconds)
	}))
	return func(code, reqMethod, reqRoute string) {
		method, route = reqMethod, reqRoute
		timer.ObserveDuration()
		m.requests.WithLabelValues(code, method, route).Inc()
	}
}

// ObserveBatch records the number of alerts one webhook carried.
func (m *Metrics) ObserveBatch(size int) {
	m.batchSize.Observe(float64(size))
}

// ObserveReceived counts one alert received with status, AlertFiring or
// AlertResolved. The caller maps whatever the sender wrote to one of the two,
// so the label stays bounded.
func (m *Metrics) ObserveReceived(status string) {
	m.received.WithLabelValues(status).Inc()
}

// ObserveRouted counts one firing alert routed for team with outcome, one of
// the Outcome constants. team is NoTeam for an unowned alert.
func (m *Metrics) ObserveRouted(team, outcome string) {
	m.routed.WithLabelValues(team, outcome).Inc()
}

// ObserveNotification counts one notification dispatched with decision to
// destination: the paged target, the channel posted to, or "-" for a drop.
// Both come from the team directory and the policies rather than from the
// alert, which keeps them bounded as long as no policy copies an alert label
// into a target or channel.
func (m *Metrics) ObserveNotification(decision, destination string) {
	m.notifications.WithLabelValues(decision, destination).Inc()
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

// ObserveEvaluationError counts one failed evaluation of team's policy, of
// kind, one of the ErrorKind constants.
func (m *Metrics) ObserveEvaluationError(team, kind string) {
	m.evaluationErrors.WithLabelValues(team, kind).Inc()
}

// PrepareReloads creates both results of the reload counter, so a
// failure-rate alert has a series to divide by before the first failure.
// Series that exist already keep their values.
func (m *Metrics) PrepareReloads() {
	m.reloads.WithLabelValues(reloadSuccess)
	m.reloads.WithLabelValues(reloadFailure)
}

// ObserveReloadFailure counts a load that was rejected and marks the latest
// load as failed. The last-reload time stays where the last good load left
// it, and so do the loaded-policy series: the bundle they describe still
// serves.
func (m *Metrics) ObserveReloadFailure() {
	m.reloads.WithLabelValues(reloadFailure).Inc()
	m.reloadSuccessful.Set(0)
}

// ObserveReloadSuccess counts a successful load at the given time, marks the
// latest load as successful, and replaces the loaded-policy series with the
// policies that now serve, from the bundle with fingerprint read from source,
// so a team that was dropped disappears from the gauge and a changed bundle
// shows its new fingerprint.
func (m *Metrics) ObserveReloadSuccess(at time.Time, source, fingerprint string, loaded []LoadedPolicy) {
	m.reloads.WithLabelValues(reloadSuccess).Inc()
	m.lastReload.Set(float64(at.UnixNano()) / float64(time.Second))
	m.reloadSuccessful.Set(1)

	m.loadedInfo.Reset()
	for _, p := range loaded {
		m.loadedInfo.WithLabelValues(p.Team, p.Policy, fingerprint, source).Set(1)
	}
}

// LoadedPolicy is one team policy a bundle serves, for the loaded-policy
// gauge.
type LoadedPolicy struct {
	// Team is the team the policy serves, the gauge's team label.
	Team string
	// Policy is the policy's name, the gauge's policy label.
	Policy string
}

// register puts every collector on the service's registry, the Go runtime
// and process collectors among them.
func (m *Metrics) register() {
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.received,
		m.routed,
		m.batchSize,
		m.notifications,
		m.decisions,
		m.evaluationDuration,
		m.evaluationErrors,
		m.reloads,
		m.lastReload,
		m.reloadSuccessful,
		m.loadedInfo,
		m.requests,
		m.requestDuration,
	)
}
