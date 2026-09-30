package fixture

import (
	"net/http"
	"time"

	"github.com/onsi/ginkgo/v2"

	request "github.com/spechtlabs/sigil/examples/alert-routing/requests"
)

// PaymentsMuted is the alert the payments team mutes.
const PaymentsMuted = "PaymentsSettlementBatchSlow"

// badRequestStart is when the alerts of the bad requests started. The
// service refuses the whole payload before reading it, so any time does.
var badRequestStart = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// Route is a route as a spec expects it: the decision, the reason, and the
// target of a page or the channel of a notification, empty otherwise.
type Route struct {
	Decision string
	Reason   string
	Target   string
	Channel  string
}

// RouteCase is one alert sent to one team's route endpoint and the route the
// team's policy must choose. The integration and end-to-end suites run the
// same cases, so the in-process server and the container can't disagree.
type RouteCase struct {
	Request RouteRequest
	Want    Route
	// Name is the spec's description.
	Name string
	// Team is the team the alert is sent for, which picks the policy
	// <team>.alerts.
	Team string
}

// BadRequestCase is a body the service must refuse before evaluating
// anything, and the status it must refuse it with.
type BadRequestCase struct {
	// Name is the spec's description.
	Name string
	// Body is the raw request body, sent byte for byte.
	Body string
	// Status is the status the body must be refused with.
	Status int
}

// ResultCase is one alert of a webhook batch and what must become of it:
// its status, the team it was routed for, and the route. A resolved alert
// needs no route and names no team.
type ResultCase struct {
	Want        Route
	Fingerprint string
	Status      string
	// Team is the owning team the result names, empty for an unowned
	// alert.
	Team string
}

// Batch is a webhook and what must become of each of its alerts.
type Batch struct {
	Webhook Webhook
	Results []ResultCase
}

// ManifestCase is one case of requests/cases.json: a sample request and
// what alertrouter must answer to it. Err is set instead when the manifest
// doesn't read, so the table still has an entry to fail.
type ManifestCase struct {
	Err  error
	Case request.Case
}

// Case is a table case with a description, which every case type here is,
// so [Entries] can turn any of their lists into a table.
type Case interface {
	Description() string
}

// The routes the specs expect most often.
var (
	// Unrouted is the kind's default: nothing matched, or the router
	// couldn't ask a policy, and the alert still reaches #alerts.
	Unrouted = Route{Decision: DecisionNotify, Reason: ReasonUnrouted, Channel: DefaultChannel}

	checkoutCritical  = Route{Decision: DecisionPage, Reason: ReasonCriticalAlert, Target: CheckoutOncall}
	checkoutSustained = Route{Decision: DecisionPage, Reason: ReasonSustained, Target: CheckoutOncall}
	paymentsCritical  = Route{Decision: DecisionPage, Reason: ReasonCriticalAlert, Target: PaymentsOncall}
	paymentsSustained = Route{Decision: DecisionPage, Reason: ReasonSustained, Target: PaymentsOncall}
	muted             = Route{Decision: DecisionDrop, Reason: ReasonMuted}
	notProduction     = Route{Decision: DecisionDrop, Reason: ReasonNotProduction}
)

// RouteCases returns every decision and reason the checkout and payments
// policies can reach, and the combinations whose outcome says something
// about the kind: a page beats a drop, so muting never silences a page, and
// the team's own threshold decides when a warning becomes one.
func RouteCases() []RouteCase {
	return append(checkoutCases(), paymentsCases()...)
}

// RouteBadRequestCases returns bodies the route endpoint must refuse: 400
// for a body that isn't a request, and 422 for a request whose alert the
// kind can't represent: no name, a severity it doesn't declare, or a
// firing time before the alert started.
func RouteBadRequestCases() []BadRequestCase {
	valid := FiringAlert(CheckoutLatency, SeverityWarning)
	return []BadRequestCase{
		{Name: "malformed JSON", Body: `{"alert": {"name": "CheckoutLatencyHigh"`, Status: http.StatusBadRequest},
		{Name: "an unknown field in the alert", Body: valid.JSONWithField("team", TeamPayments), Status: http.StatusBadRequest},
		{Name: "a field outside the alert", Body: `{"alert": ` + mustMarshal(valid.Alert) + `, "severity": "critical"}`, Status: http.StatusBadRequest},
		{Name: "a duration that doesn't parse", Body: FiringAlert(CheckoutLatency, SeverityWarning, FiringFor("twelve minutes")).JSON(), Status: http.StatusBadRequest},
		{Name: "a number where a duration belongs", Body: `{"alert": {"name": "CheckoutLatencyHigh", "severity": "warning", "labels": {}, "firing_for": 720000000000}}`, Status: http.StatusBadRequest},
		{Name: "a severity the kind doesn't declare", Body: FiringAlert(CheckoutLatency, "urgent").JSON(), Status: http.StatusUnprocessableEntity},
		{Name: "a severity in the wrong case", Body: FiringAlert(CheckoutLatency, "Critical").JSON(), Status: http.StatusUnprocessableEntity},
		{Name: "no severity", Body: FiringAlert(CheckoutLatency, "").JSON(), Status: http.StatusUnprocessableEntity},
		{Name: "no name", Body: FiringAlert("", SeverityWarning).JSON(), Status: http.StatusUnprocessableEntity},
		{Name: "a negative firing time", Body: FiringAlert(CheckoutLatency, SeverityWarning, FiringFor("-5m")).JSON(), Status: http.StatusUnprocessableEntity},
	}
}

// WebhookBadRequestCases returns webhook bodies the service must refuse with
// 400 as a whole, before routing any alert: Alertmanager would retry them
// forever, but a payload the router can't read won't get better.
func WebhookBadRequestCases() []BadRequestCase {
	firing := Firing("a1", AlertLabels(TeamCheckout, CheckoutErrorRate, SeverityCritical), badRequestStart)

	wrongVersion := NewWebhook(firing)
	wrongVersion.Version = "3"

	badStatus := NewWebhook(firing)
	badStatus.Alerts[0].Status = "pending"

	tooMany := NewWebhook()
	for range 1001 {
		tooMany.Alerts = append(tooMany.Alerts, firing)
	}

	return []BadRequestCase{
		{Name: "malformed JSON", Body: `{"version": "4", "alerts": [`, Status: http.StatusBadRequest},
		{Name: "a payload version other than 4", Body: mustMarshal(wrongVersion), Status: http.StatusBadRequest},
		{Name: "an alert that is neither firing nor resolved", Body: mustMarshal(badStatus), Status: http.StatusBadRequest},
		{Name: "more alerts than one webhook may carry", Body: mustMarshal(tooMany), Status: http.StatusBadRequest},
	}
}

// MixedBatch returns a webhook with one alert for every status a result can
// have, sent at now, and what must become of each. Every firing alert ends
// in a route, the ones no policy decided included: those take the kind's
// default, so no alert is ever silently lost.
func MixedBatch(now time.Time) Batch {
	return Batch{
		Webhook: NewWebhook(
			Firing("checkout-critical", AlertLabels(TeamCheckout, CheckoutErrorRate, SeverityCritical), now.Add(-time.Minute)),
			Firing("payments-warning", AlertLabels(TeamPayments, "PaymentsLatencyHigh", SeverityWarning), now.Add(-time.Minute)),
			Firing("checkout-staging", AlertLabels(TeamCheckout, CheckoutErrorRate, SeverityCritical, LabelEnv, EnvStaging), now.Add(-time.Minute)),
			Firing("payments-sustained", AlertLabels(TeamPayments, "PaymentsLatencyHigh", SeverityWarning), now.Add(-time.Hour)),
			Resolved("checkout-resolved", AlertLabels(TeamCheckout, CheckoutLatency, SeverityWarning), now),
			Firing("no-team", AlertLabels("", "NodeDiskFull", SeverityCritical), now.Add(-time.Minute)),
			Firing("unknown-team", AlertLabels("marketing", "CampaignBounceRate", SeverityWarning), now.Add(-time.Minute)),
			Firing("bad-severity", AlertLabels(TeamCheckout, CheckoutLatency, "urgent"), now.Add(-time.Minute)),
			Firing("no-name", AlertLabels(TeamPayments, "", SeverityCritical, LabelAlertName, ""), now.Add(-time.Minute)),
		),
		Results: []ResultCase{
			{Fingerprint: "checkout-critical", Status: StatusRouted, Team: TeamCheckout, Want: checkoutCritical},
			{
				Fingerprint: "payments-warning", Status: StatusRouted, Team: TeamPayments,
				Want: Route{Decision: DecisionNotify, Reason: ReasonRoutine, Channel: PaymentsChannel},
			},
			{Fingerprint: "checkout-staging", Status: StatusRouted, Team: TeamCheckout, Want: notProduction},
			{Fingerprint: "payments-sustained", Status: StatusRouted, Team: TeamPayments, Want: paymentsSustained},
			{Fingerprint: "checkout-resolved", Status: StatusResolved},
			{Fingerprint: "no-team", Status: StatusUnowned, Want: Unrouted},
			{Fingerprint: "unknown-team", Status: StatusUnowned, Want: Unrouted},
			{Fingerprint: "bad-severity", Status: StatusInvalid, Team: TeamCheckout, Want: Unrouted},
			{Fingerprint: "no-name", Status: StatusInvalid, Team: TeamPayments, Want: Unrouted},
		},
	}
}

// ManifestCases returns every case of requests/cases.json, in the
// manifest's order. A manifest that doesn't read is one case carrying the
// error, which fails its spec instead of the tree's construction.
func ManifestCases() []ManifestCase {
	cases, herr := request.Cases()
	if herr != nil {
		return []ManifestCase{{Err: herr, Case: request.Case{Name: request.CasesFile}}}
	}

	out := make([]ManifestCase, 0, len(cases))
	for _, c := range cases {
		out = append(out, ManifestCase{Case: c})
	}
	return out
}

// Firing returns how many results of the batch are for firing alerts, each
// of which must end in exactly one notification.
func (b Batch) Firing() int {
	n := 0
	for _, r := range b.Results {
		if r.Status != StatusResolved {
			n++
		}
	}
	return n
}

// Entries turns cases into Ginkgo table entries, one per case, described by
// the case and passing it whole to the table's body, so both suites build
// every table the same way and a new table is one call.
func Entries[C Case](cases []C) []ginkgo.TableEntry {
	entries := make([]ginkgo.TableEntry, 0, len(cases))
	for _, c := range cases {
		entries = append(entries, ginkgo.Entry(c.Description(), c))
	}

	return entries
}

// Description is the spec's description, for [Entries].
func (c RouteCase) Description() string { return c.Name }

// Description is the spec's description, for [Entries].
func (c BadRequestCase) Description() string { return c.Name }

// Description is the spec's description, for [Entries].
func (c ResultCase) Description() string { return c.Status + " " + c.Fingerprint }

// Description is the spec's description, for [Entries]: the scenario's name
// and what it shows.
func (c ManifestCase) Description() string {
	if c.Case.Description == "" {
		return c.Case.Name
	}
	return c.Case.Name + ": " + c.Case.Description
}

// checkoutCases are checkout's routes: the platform's paging and routing
// with a ten-minute threshold and one muted alert, and the team's own
// channel for payments info alerts.
func checkoutCases() []RouteCase {
	return []RouteCase{
		{
			Name: "a critical production alert pages checkout's on-call",
			Team: TeamCheckout, Request: FiringAlert(CheckoutErrorRate, SeverityCritical),
			Want: checkoutCritical,
		},
		{
			Name: "a fresh warning goes to checkout's channel",
			Team: TeamCheckout, Request: FiringAlert(CheckoutLatency, SeverityWarning, FiringFor("4m")),
			Want: Route{Decision: DecisionNotify, Reason: ReasonRoutine, Channel: CheckoutChannel},
		},
		{
			Name: "a warning that has fired for checkout's ten minutes pages",
			Team: TeamCheckout, Request: FiringAlert(CheckoutLatency, SeverityWarning, FiringFor("10m")),
			Want: checkoutSustained,
		},
		{
			Name: "a muted warning is dropped",
			Team: TeamCheckout, Request: FiringAlert(CheckoutMuted, SeverityWarning, FiringFor("4m")),
			Want: muted,
		},
		{
			// A page beats a drop, so the mute only silences the
			// notification.
			Name: "muting never silences a sustained page",
			Team: TeamCheckout, Request: FiringAlert(CheckoutMuted, SeverityWarning, FiringFor("12m")),
			Want: checkoutSustained,
		},
		{
			Name: "muting never silences a critical page",
			Team: TeamCheckout, Request: FiringAlert(CheckoutMuted, SeverityCritical),
			Want: checkoutCritical,
		},
		{
			Name: "a critical staging alert is dropped",
			Team: TeamCheckout, Request: FiringAlert(CheckoutErrorRate, SeverityCritical, Env(EnvStaging)),
			Want: notProduction,
		},
		{
			// Both drops fire, and the kind ranks muted first.
			Name: "a muted staging alert is dropped as muted",
			Team: TeamCheckout, Request: FiringAlert(CheckoutMuted, SeverityWarning, Env(EnvStaging)),
			Want: muted,
		},
		{
			Name: "an alert without an env label isn't a production alert",
			Team: TeamCheckout, Request: FiringAlert(CheckoutErrorRate, SeverityCritical, NoLabel(LabelEnv)),
			Want: notProduction,
		},
		{
			Name: "payments info goes to checkout's payments channel",
			Team: TeamCheckout, Request: FiringAlert("PaymentsRetryRate", SeverityInfo, Label(LabelComponent, "payments")),
			Want: Route{Decision: DecisionNotify, Reason: ReasonRoutine, Channel: "#checkout-payments"},
		},
		{
			Name: "any other info alert falls back to #alerts",
			Team: TeamCheckout, Request: FiringAlert("CheckoutPodRestarted", SeverityInfo),
			Want: Unrouted,
		},
	}
}

// paymentsCases are payments' routes: the same platform policies with a
// five-minute threshold, and the team's own channel for ledger info alerts.
func paymentsCases() []RouteCase {
	return []RouteCase{
		{
			Name: "a critical production alert pages payments' on-call",
			Team: TeamPayments, Request: FiringAlert("PaymentsErrorRate", SeverityCritical),
			Want: paymentsCritical,
		},
		{
			Name: "a fresh warning goes to payments' channel",
			Team: TeamPayments, Request: FiringAlert("PaymentsLatencyHigh", SeverityWarning, FiringFor("4m")),
			Want: Route{Decision: DecisionNotify, Reason: ReasonRoutine, Channel: PaymentsChannel},
		},
		{
			// Six minutes is sustained for payments and fresh for checkout.
			Name: "a warning pages after payments' shorter five minutes",
			Team: TeamPayments, Request: FiringAlert("PaymentsLatencyHigh", SeverityWarning, FiringFor("6m")),
			Want: paymentsSustained,
		},
		{
			Name: "payments' muted warning is dropped",
			Team: TeamPayments, Request: FiringAlert(PaymentsMuted, SeverityWarning, FiringFor("4m")),
			Want: muted,
		},
		{
			Name: "a staging warning is dropped",
			Team: TeamPayments, Request: FiringAlert("PaymentsLatencyHigh", SeverityWarning, Env(EnvStaging)),
			Want: notProduction,
		},
		{
			Name: "ledger info goes to payments' ledger channel",
			Team: TeamPayments, Request: FiringAlert("LedgerReconciliationLag", SeverityInfo, Label(LabelComponent, "ledger")),
			Want: Route{Decision: DecisionNotify, Reason: ReasonRoutine, Channel: "#payments-ledger"},
		},
		{
			// The payments channel rule is checkout's, not payments'.
			Name: "payments component info falls back to #alerts for payments",
			Team: TeamPayments, Request: FiringAlert("PaymentsRetryRate", SeverityInfo, Label(LabelComponent, "payments")),
			Want: Unrouted,
		},
	}
}
