// Package dispatch is where a routed alert leaves alertrouter: the
// [Notification] the router built from a policy's decision, and the
// [Notifier] that delivers it.
//
// The router makes a decision for every firing alert, the team policy's or
// the kind's fallback, and hands each to a Notifier, so no alert ends
// without one. A drop is dispatched too: it says the alert was looked at and
// deliberately silenced, which is worth a log line of its own when someone
// later asks why nobody was paged.
//
// The example ships [LogNotifier], which writes one structured log line per
// notification instead of calling a pager or a chat service. A real
// deployment would put a PagerDuty or Slack client behind the same
// interface; the router doesn't change.
package dispatch

import (
	"context"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
)

// NoDestination is the destination of a notification that goes nowhere, a
// drop, in logs and metrics.
const NoDestination = "-"

// Notification is one decision to deliver: which alert, whose it is, what
// the policy decided and why, and where it goes. Target is set for a page,
// Channel for a notification; a drop has neither.
type Notification struct {
	// Team is the team that owns the alert, or empty when no team does.
	Team string
	// AlertName is the alert's name, its alertname label.
	AlertName string
	// Fingerprint identifies the alert across webhooks, as Alertmanager
	// computes it. It is empty for an alert posted on its own.
	Fingerprint string
	// Decision and Reason are the decision, page, drop or notify, and why.
	Decision string
	Reason   string
	// Target is whom a page goes to.
	Target string
	// Channel is where a notification is posted.
	Channel string
}

// Notifier delivers notifications. The router calls Notify once per firing
// alert, from the request's goroutine, with the request's context, which
// carries the alert's span. An error is logged and returned to the caller
// for that alert, with its advice, so it says what to check; the alert's
// decision stands.
type Notifier interface {
	Notify(ctx context.Context, n Notification) humane.Error
}

// NotifierFunc adapts a function to a [Notifier], the way
// [net/http.HandlerFunc] adapts one to a handler.
type NotifierFunc func(ctx context.Context, n Notification) humane.Error

// LogNotifier is the example's Notifier: it logs each notification instead
// of delivering it. It has no state and is safe for concurrent use.
type LogNotifier struct{}

// Destination is where the notification goes: the paged target, the channel,
// or [NoDestination] for a drop.
func (n Notification) Destination() string {
	switch {
	case n.Target != "":
		return n.Target
	case n.Channel != "":
		return n.Channel
	}
	return NoDestination
}

// Notify calls f.
func (f NotifierFunc) Notify(ctx context.Context, n Notification) humane.Error {
	return f(ctx, n)
}

// NewLogNotifier returns a Notifier that logs every notification.
func NewLogNotifier() *LogNotifier {
	return &LogNotifier{}
}

// Notify writes one "notification dispatched" line at info level through
// [telemetry.FromContext], so it carries the trace and span ids of the
// alert's span and leads straight to the evaluation that decided it, and
// records the destination on that span, so the trace says where the alert
// went as well as why. It never fails.
func (*LogNotifier) Notify(ctx context.Context, n Notification) humane.Error {
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("alertrouter.destination", n.Destination()))
	telemetry.FromContext(ctx).InfoContext(ctx, "notification dispatched",
		zap.String("team", n.Team),
		zap.String("alertname", n.AlertName),
		zap.String("fingerprint", n.Fingerprint),
		zap.String("decision", n.Decision),
		zap.String("reason", n.Reason),
		zap.String("destination", n.Destination()),
	)
	return nil
}
