// Package routing defines the AlertRouting kind: the contract every alert
// policy is checked against. It is the running example of the documentation's
// Getting Started path, so the policies under examples/alert-routing/policies
// read exactly like the docs.
//
// The package shows how a host declares a kind in Go. [Input] and its nested
// structs carry `policy:` tags that name what a policy reads, so the Sigil
// field alert.firing_for is [Alert.FiringFor], a duration. [Severity] is a
// named string type the kind registers as an enum, so alert.severity is one
// of critical, warning and info, written bare in a policy. [Page], [Drop] and
// [Notify] are the decision handles, each with its reasons and payload type,
// and [Kind] ties them together with the Severity enum, the precedence and
// the default decision. The host reads a result through the same handles:
// [policy.Decision.Match] hands back a [PageData] or a [NotifyData], not a
// map.
//
// The Go types here are the source of truth. `sigilc export` writes them out
// as policies/alert_routing.sigil for the tooling that runs without this
// code, and TestKindFileIsCurrent fails when that copy is stale. The
// package's tests also run every team's policy tests through
// [github.com/spechtlabs/sigil/pkg/policytest.Run], with platform.paging
// required from the platform's documents the way the service loads them.
package routing

import (
	"slices"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// DefaultChannel is where a notification is posted when the notifying rule
// doesn't name a channel, which makes it where the kind's default decision,
// notify(reason: unrouted), posts. alertrouter posts an alert no policy ran
// for there too. It repeats the default of [NotifyData.Channel], which a
// struct tag can only spell as a literal; the package's test keeps the two
// equal.
const DefaultChannel = "#alerts"

// Severity is how bad an alert says it is. [Kind] declares it as the enum
// Severity, so a policy writes alert.severity == critical, and a misspelled
// severity is a compile error instead of a comparison that never matches.
type Severity string

// The severities, in the order the kind declares them.
const (
	Critical Severity = "critical"
	Warning  Severity = "warning"
	Info     Severity = "info"
)

// Severities lists every severity [Kind] declares, in declaration order.
var Severities = []Severity{Critical, Warning, Info}

// Input is everything an alert policy can read: the alert itself and the
// team that owns it.
type Input struct {
	Alert Alert `policy:"alert" json:"alert"`
	// Team is the owning team, which alertrouter looks up in its directory
	// by the alert's team label. A policy reads it to page the team's
	// on-call or post to its channel without naming either.
	Team Team `policy:"team" json:"team"`
}

// Alert is one firing alert, as the monitoring system reports it.
type Alert struct {
	// Name is the alert's name, Alertmanager's alertname label.
	Name string `policy:"name" json:"name"`
	// Severity is the alert's severity label. A severity outside
	// [Severities] fails the evaluation when a rule reads it, so parse it
	// with [ParseSeverity] first.
	Severity Severity `policy:"severity" json:"severity"`
	// Labels are all of the alert's labels, the ones above included. The
	// policies read env and component.
	Labels map[string]string `policy:"labels" json:"labels"`
	// FiringFor is how long the alert has been firing. The platform pages
	// for a warning that fires longer than the team's page_after.
	FiringFor time.Duration `policy:"firing_for" json:"firing_for"`
}

// Team is the team that owns an alert.
type Team struct {
	Name string `policy:"name" json:"name"`
	// Oncall is the paging target of the team's on-call rotation.
	Oncall string `policy:"oncall" json:"oncall"`
	// Channel is the chat channel the team reads its notifications in.
	Channel string `policy:"channel" json:"channel"`
}

// PageData is the payload of a page: whom to page.
type PageData struct {
	Target string `policy:"target" json:"target"`
}

// NotifyData is the payload of a notification: where to post it.
type NotifyData struct {
	// Channel is #alerts when the notifying rule doesn't set it, which is
	// where the kind's default decision posts an alert no rule routed.
	Channel string `policy:"channel,default=\"#alerts\"" json:"channel"`
}

// The decisions, declared with their reasons. Drop carries only a reason.
var (
	Page   = policy.NewDecision[PageData]("page", "critical_alert", "sustained")
	Drop   = policy.NewDecision[policy.None]("drop", "muted", "not_production")
	Notify = policy.NewDecision[NotifyData]("notify", "routine", "unrouted")
)

// The reasons Go code names, as typed handles: the kind's options rank them
// and pick the default, and the router answers with that default when no
// policy ran for an alert. A misspelled reason panics here at start-up, with a
// did-you-mean hint, instead of compiling into a comparison that never matches.
var (
	CriticalAlert = Page.Reason("critical_alert")
	Sustained     = Page.Reason("sustained")

	Muted         = Drop.Reason("muted")
	NotProduction = Drop.Reason("not_production")

	Routine  = Notify.Reason("routine")
	Unrouted = Notify.Reason("unrouted")
)

// Kind is the AlertRouting contract, version 1. Decisions are listed in
// precedence order: a page beats a drop beats a notification, so muting an
// alert silences its notifications but never a page. The reasons of every
// decision are ranked too, so two rules of the same decision never conflict
// over the reason; they still conflict when they disagree on the payload,
// which is why a team never adds a page rule of its own. When no rule fires,
// the alert is posted to #alerts rather than lost.
var Kind = policy.NewKind[Input]("AlertRouting",
	policy.WithVersion(1),
	policy.WithEnum(Critical, Warning, Info),
	policy.WithDecisions(Page, Drop, Notify),
	policy.WithReasonPrecedence(CriticalAlert, Sustained),
	policy.WithReasonPrecedence(Muted, NotProduction),
	policy.WithReasonPrecedence(Routine, Unrouted),
	policy.WithDefault(Unrouted),
)

// ParseSeverity returns the severity s names, and false when s is not one of
// [Severities]. A policy that reads a severity outside them fails with a
// runtime error, so alertrouter parses the label before any policy runs.
// The match is exact, as in a policy: "Critical" is not critical.
func ParseSeverity(s string) (Severity, bool) {
	sev := Severity(s)
	if !slices.Contains(Severities, sev) {
		return "", false
	}
	return sev, true
}
