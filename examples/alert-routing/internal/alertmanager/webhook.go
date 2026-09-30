// Package alertmanager reads what Prometheus Alertmanager sends to a webhook
// receiver, version 4 of its payload, and turns each alert into the
// AlertRouting kind's [routing.Alert].
//
// Alertmanager describes an alert by its labels only, so [Convert] reads the
// kind's fields out of well-known labels: the name from alertname, the
// severity from severity, and the owning team, which the router looks up
// itself, from team. Everything else stays a label, which is how a policy
// reads env or component. The alert's firing time is the only field that
// isn't a label: [Convert] derives firing_for from startsAt and the time the
// router received the batch.
package alertmanager

import (
	"fmt"
	"maps"
	"strings"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
)

// Version is the webhook payload version this package reads.
const Version = "4"

// MaxAlerts is the most alerts one webhook may carry. Alertmanager's
// max_alerts receiver setting truncates a group to fewer; a batch larger
// than this isn't one Alertmanager configured sensibly sent, and routing
// it would hold the request for as long as its size made it take.
const MaxAlerts = 1000

// The statuses of a webhook and of each alert in it.
const (
	StatusFiring   = "firing"
	StatusResolved = "resolved"
)

// The labels the router reads. Every other label reaches the policy
// untouched in alert.labels.
const (
	// LabelAlertName is the alert's name, set by Prometheus from the
	// alerting rule's name.
	LabelAlertName = "alertname"
	// LabelSeverity is the alert's severity, one of the kind's Severity
	// values.
	LabelSeverity = "severity"
	// LabelTeam names the team that owns the alert, which picks the policy
	// <team>.alerts and the team input from the directory.
	LabelTeam = "team"
)

// Webhook is the body of one Alertmanager webhook notification: a group of
// alerts that share GroupLabels, sent to one receiver.
type Webhook struct {
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	// Version is the payload version, "4" for every Alertmanager since 0.9.
	Version string `json:"version"`
	// GroupKey identifies the group the alerts belong to.
	GroupKey string `json:"groupKey"`
	// Status is firing when at least one alert of the group fires.
	Status   string `json:"status"`
	Receiver string `json:"receiver"`
	// ExternalURL is the Alertmanager that sent the notification.
	ExternalURL string `json:"externalURL"`
	// Alerts are the group's alerts, firing and resolved.
	Alerts []Alert `json:"alerts"`
	// TruncatedAlerts is how many alerts Alertmanager left out because of
	// the receiver's max_alerts.
	TruncatedAlerts int `json:"truncatedAlerts"`
}

// Alert is one alert of a webhook.
type Alert struct {
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	// Status is firing or resolved.
	Status       string `json:"status"`
	GeneratorURL string `json:"generatorURL"`
	// Fingerprint identifies the alert by its labels, stable across
	// notifications.
	Fingerprint string `json:"fingerprint"`
}

// Validate reports the first thing that makes w a payload the router can't
// read: a version other than [Version], more than [MaxAlerts] alerts, or an
// alert whose status is neither firing nor resolved. The alerts' labels
// aren't checked here; [Convert] checks those one alert at a time, so one
// broken alert doesn't cost the rest of the batch their routing.
func (w Webhook) Validate() humane.Error {
	if w.Version != Version {
		return humane.New(fmt.Sprintf("the webhook has version %q, not %q", w.Version, Version),
			"send Alertmanager's webhook payload version 4, which every Alertmanager since 0.9 sends")
	}
	if len(w.Alerts) > MaxAlerts {
		return humane.New(fmt.Sprintf("the webhook carries %d alerts, more than the %d the router takes at once", len(w.Alerts), MaxAlerts),
			fmt.Sprintf("set max_alerts on the Alertmanager receiver to %d or fewer", MaxAlerts))
	}
	for i, a := range w.Alerts {
		if !a.Firing() && a.Status != StatusResolved {
			return humane.New(fmt.Sprintf("alert %d has the status %q", i, a.Status),
				`set each alert's status to "firing" or "resolved", as Alertmanager does`)
		}
	}
	return nil
}

// Firing reports whether the alert fires. A resolved alert needs no route.
func (a Alert) Firing() bool {
	return a.Status == StatusFiring
}

// Convert turns a into the kind's alert, as of now, the time the router
// received it. The name and the severity come from the alertname and
// severity labels, and both are required: an alert without a name can't be
// muted by name, and one without a known severity would fail the evaluation
// the moment a rule compared it. firing_for is now minus startsAt, and zero
// for an alert that starts in the future because the two clocks disagree.
// The labels are copied, all of them, so the result doesn't share a's map.
func Convert(a Alert, now time.Time) (routing.Alert, humane.Error) {
	name := a.Labels[LabelAlertName]
	if name == "" {
		return routing.Alert{}, humane.New("the alert has no "+LabelAlertName+" label",
			"send alerts from a Prometheus alerting rule, which sets alertname to the rule's name")
	}

	raw, ok := a.Labels[LabelSeverity]
	if !ok {
		return routing.Alert{}, humane.New(fmt.Sprintf("alert %s has no %s label", name, LabelSeverity),
			"add a severity label to the alerting rule: "+severityList())
	}
	severity, ok := routing.ParseSeverity(raw)
	if !ok {
		return routing.Alert{}, humane.New(fmt.Sprintf("alert %s has the severity %q, which the AlertRouting kind doesn't declare", name, raw),
			"set the alerting rule's severity label to "+severityList()+", in lower case")
	}

	if a.StartsAt.IsZero() {
		return routing.Alert{}, humane.New(fmt.Sprintf("alert %s has no startsAt", name),
			"send the alert as Alertmanager does, with the time it started firing; without it the router can't tell a fresh alert from a sustained one")
	}

	labels := make(map[string]string, len(a.Labels))
	maps.Copy(labels, a.Labels)

	return routing.Alert{
		Name:      name,
		Severity:  severity,
		Labels:    labels,
		FiringFor: max(now.Sub(a.StartsAt), 0),
	}, nil
}

// severityList renders the kind's severities for advice: "critical, warning
// or info".
func severityList() string {
	names := make([]string, len(routing.Severities))
	for i, s := range routing.Severities {
		names[i] = string(s)
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
