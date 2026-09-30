package fixture

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExamplesDir is the examples module's root seen from a suite's package
// directory, test/<suite>, which is where `go test` runs it.
const ExamplesDir = "../.."

// The teams the example serves, as the default team directory lists them.
const (
	TeamCheckout = "checkout"
	TeamPayments = "payments"

	CheckoutOncall  = "checkout-primary"
	CheckoutChannel = "#checkout-alerts"
	PaymentsOncall  = "payments-primary"
	PaymentsChannel = "#payments-alerts"

	// DefaultChannel is where the kind's default decision posts.
	DefaultChannel = "#alerts"
)

// The severities the AlertRouting kind declares.
const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
)

// The decisions and reasons of the AlertRouting kind.
const (
	DecisionPage   = "page"
	DecisionDrop   = "drop"
	DecisionNotify = "notify"

	ReasonCriticalAlert = "critical_alert"
	ReasonSustained     = "sustained"
	ReasonMuted         = "muted"
	ReasonNotProduction = "not_production"
	ReasonRoutine       = "routine"
	ReasonUnrouted      = "unrouted"
)

// What became of one alert of a webhook batch.
const (
	StatusRouted   = "routed"
	StatusResolved = "resolved"
	StatusUnowned  = "unowned"
	StatusInvalid  = "invalid"
	StatusFailed   = "failed"
)

// The labels the router reads from an Alertmanager alert, and the ones the
// policies read.
const (
	LabelAlertName = "alertname"
	LabelSeverity  = "severity"
	LabelTeam      = "team"
	LabelEnv       = "env"
	LabelComponent = "component"

	EnvProduction = "production"
	EnvStaging    = "staging"
)

// The alerts the specs send most often. CheckoutMuted is the alert the
// checkout team mutes.
const (
	CheckoutErrorRate = "CheckoutErrorRate"
	CheckoutLatency   = "CheckoutLatencyHigh"
	CheckoutMuted     = "CheckoutCanaryLatency"
)

// The statuses of a webhook and of each alert in it, as Alertmanager sends
// them.
const (
	AlertFiring   = "firing"
	AlertResolved = "resolved"
)

// Mutator changes one aspect of a request built by FiringAlert.
type Mutator func(*RouteRequest)

// FiringAlert returns a production alert called name at severity that has
// fired for a minute, with each mutator applied in turn. Every call builds a
// fresh label map, so a mutator never leaks into the next spec.
func FiringAlert(name, severity string, mutators ...Mutator) RouteRequest {
	r := RouteRequest{Alert: Alert{
		Name:      name,
		Severity:  severity,
		Labels:    map[string]string{LabelEnv: EnvProduction},
		FiringFor: "1m",
	}}

	for _, mutate := range mutators {
		mutate(&r)
	}

	return r
}

// FiringFor sets how long the alert has fired, a duration string.
func FiringFor(d string) Mutator {
	return func(r *RouteRequest) { r.Alert.FiringFor = d }
}

// Env sets the alert's env label, which decides whether it is a production
// alert.
func Env(env string) Mutator {
	return Label(LabelEnv, env)
}

// Label sets one label of the alert.
func Label(key, value string) Mutator {
	return func(r *RouteRequest) { r.Alert.Labels[key] = value }
}

// NoLabel removes one label of the alert.
func NoLabel(key string) Mutator {
	return func(r *RouteRequest) { delete(r.Alert.Labels, key) }
}

// Severity sets the alert's severity, which need not be one the kind
// declares.
func Severity(severity string) Mutator {
	return func(r *RouteRequest) { r.Alert.Severity = severity }
}

// JSON renders the request. The request holds only strings and a string
// map, which always marshal, so there is no error to return, and the suites
// can build their tables while Ginkgo constructs the spec tree.
func (r RouteRequest) JSON() string {
	return mustMarshal(r)
}

// JSONWithField renders the request with one more field in the alert, which
// the service must reject rather than ignore: a misspelled field would
// otherwise evaluate a policy against a zero value.
func (r RouteRequest) JSONWithField(key string, value any) string {
	return mustMarshal(map[string]any{"alert": map[string]any{
		"name":       r.Alert.Name,
		"severity":   r.Alert.Severity,
		"labels":     r.Alert.Labels,
		"firing_for": r.Alert.FiringFor,
		key:          value,
	}})
}

// NewWebhook returns an Alertmanager webhook, version 4, carrying alerts, as
// the receiver alertrouter gets it. The batch is firing when any alert in it
// fires.
func NewWebhook(alerts ...WebhookAlert) Webhook {
	status := AlertResolved
	for _, a := range alerts {
		if a.Status == AlertFiring {
			status = AlertFiring
		}
	}

	return Webhook{
		Version:           "4",
		GroupKey:          `{}:{alertname=~".+"}`,
		Status:            status,
		Receiver:          "alertrouter",
		GroupLabels:       map[string]string{},
		CommonLabels:      map[string]string{},
		CommonAnnotations: map[string]string{},
		ExternalURL:       "http://alertmanager:9093",
		Alerts:            append([]WebhookAlert{}, alerts...),
	}
}

// Firing returns a firing alert that started at startsAt, identified by
// fingerprint, with labels built by [AlertLabels] or by hand. The router
// derives firing_for from startsAt and its own clock, so a spec against the
// wall clock picks a start well clear of any threshold it tests.
func Firing(fingerprint string, labels map[string]string, startsAt time.Time) WebhookAlert {
	return WebhookAlert{
		Status:       AlertFiring,
		Labels:       labels,
		Annotations:  map[string]string{"summary": "sent by the test suite"},
		StartsAt:     startsAt.UTC(),
		GeneratorURL: "http://prometheus:9090/graph",
		Fingerprint:  fingerprint,
	}
}

// Resolved returns an alert that fired for an hour and resolved at endsAt,
// which the router acknowledges without asking a policy.
func Resolved(fingerprint string, labels map[string]string, endsAt time.Time) WebhookAlert {
	a := Firing(fingerprint, labels, endsAt.Add(-time.Hour))
	a.Status = AlertResolved
	a.EndsAt = endsAt.UTC()
	return a
}

// AlertLabels returns the labels of a production alert owned by team, with
// more labels as key, value pairs. An empty team leaves the team label out,
// and a pair with an empty value removes that label, so a spec can build an
// alert without an env or a severity.
func AlertLabels(team, name, severity string, pairs ...string) map[string]string {
	labels := map[string]string{
		LabelAlertName: name,
		LabelSeverity:  severity,
		LabelEnv:       EnvProduction,
	}
	if team != "" {
		labels[LabelTeam] = team
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			delete(labels, pairs[i])
			continue
		}
		labels[pairs[i]] = pairs[i+1]
	}
	return labels
}

// SlowPolicy returns a checkout.alerts document that pages like every team
// policy must, and then runs far past any evaluation timeout before it
// decides anything else: a quantifier nested in another walks n² pairs of a
// list, and the condition never holds, so nothing cuts the walk short. The
// list is n strings long: 10,000 makes a hundred million steps, which take
// well over a second unbounded, from a document of about 100 KB.
func SlowPolicy(n int) string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("%q", fmt.Sprintf("n%d", i))
	}

	return "policy checkout.alerts: AlertRouting@1\n\n" +
		"use platform.paging\n\n" +
		"paging()\n\n" +
		"let names = [" + strings.Join(names, ", ") + "]\n\n" +
		"when any a in names: any b in names: a == alert.name and b == alert.name {\n" +
		"  notify(reason: routine, channel: \"#never\")\n" +
		"}\n"
}

// mustMarshal renders v, which the callers build from strings, string slices
// and string maps only. Those always marshal, so there is no error to
// return.
func mustMarshal(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
