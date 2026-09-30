package server

import (
	"time"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
)

// The statuses of one alert in a [WebhookResponse]. Every firing alert ends
// routed, unowned, invalid or failed, and each of those ends in a
// notification; a resolved alert is only acknowledged.
const (
	// StatusRouted is an alert the owning team's policy decided.
	StatusRouted = "routed"
	// StatusResolved is an alert that stopped firing. It isn't evaluated.
	StatusResolved = "resolved"
	// StatusUnowned is an alert without a team label, or whose team isn't
	// in the directory. It goes to the kind's default without an evaluation.
	StatusUnowned = "unowned"
	// StatusInvalid is an alert the router can't read, such as one without
	// a severity it knows. It goes to the kind's default too.
	StatusInvalid = "invalid"
	// StatusFailed is an alert whose evaluation failed or ran out of time.
	// It goes to the fallback decision the evaluation returned.
	StatusFailed = "failed"
)

// RouteRequest is the body of POST /api/v1/teams/{team}/route: one firing
// alert of the team in the path.
type RouteRequest struct {
	Alert AlertRequest `json:"alert"`
}

// AlertRequest is one alert as the route endpoint receives it. The handler
// refuses an empty name, a severity the kind doesn't declare and a negative
// firing_for with 422.
type AlertRequest struct {
	Name     string            `json:"name"`
	Severity string            `json:"severity"`
	Labels   map[string]string `json:"labels"`
	// FiringFor is how long the alert has been firing, a duration string
	// such as "12m".
	FiringFor Duration `json:"firing_for"`
}

// RouteResponse is how one alert was routed: the decision the dispatcher
// acts on, where it goes, and the trace that explains it. When the
// evaluation failed, the decision fields hold the fallback, and Error says
// what went wrong, with Asserts or Conflict when it was a failed assert or a
// conflict.
type RouteResponse struct {
	// Team is the owning team, left out when no team in the directory owns
	// the alert; the error then names the team label it carried.
	Team string `json:"team,omitempty"`
	// Policy is the team's root policy, <team>.alerts, empty when no policy
	// ran because no team owns the alert.
	Policy string `json:"policy"`
	// Decision and Reason are the outcome: page, drop or notify, and why.
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	// Target is whom a page goes to, set for a page only.
	Target string `json:"target,omitempty"`
	// Channel is where a notification is posted, set for notify only.
	Channel string `json:"channel,omitempty"`
	// Trace lists every candidate the policy produced, the winner marked. It
	// is empty when no rule fired, so the kind's default decided, and when
	// no policy ran.
	Trace []CandidateResult `json:"trace"`
	// Error, Asserts and Conflict explain a failed evaluation.
	Error    *ErrorResponse  `json:"error,omitempty"`
	Asserts  []AssertResult  `json:"asserts,omitempty"`
	Conflict *ConflictResult `json:"conflict,omitempty"`
}

// WebhookResponse is the body of POST /api/v1/alerts: how many alerts the
// webhook carried, how many a team's policy routed, and each alert's result
// in the webhook's order.
type WebhookResponse struct {
	Received int           `json:"received"`
	Routed   int           `json:"routed"`
	Results  []AlertResult `json:"results"`
}

// AlertResult is one alert of a webhook: which alert, its status, and, for a
// firing alert, the [RouteResponse] fields, inline.
type AlertResult struct {
	Fingerprint string `json:"fingerprint"`
	AlertName   string `json:"alertname"`
	// Status is one of the Status constants.
	Status string `json:"status"`
	// Error says why an alert wasn't routed by its policy: no team owns it,
	// it can't be read, or its evaluation failed. It is a plain message
	// rather than an [ErrorResponse] so a batch of results stays readable;
	// it hides the embedded RouteResponse's Error, and a failed alert's
	// Asserts and Conflict still explain the failure in full.
	Error string `json:"error,omitempty"`
	// RouteResponse is how a firing alert was routed, nil for a resolved
	// one, which leaves its fields out.
	*RouteResponse
}

// ConflictResult names the candidates that can't fire together, such as two
// pages of the same reason to different targets.
type ConflictResult struct {
	Candidates []CandidateResult `json:"candidates"`
}

// Payload is a decision's payload by field name, with durations rendered as
// strings. It stays untyped because the trace holds candidates of every
// decision, each with its own payload struct.
type Payload map[string]any

// CandidateResult is one decision constructor that fired, as the trace
// reports it.
type CandidateResult struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	// Policy is the policy the constructor is written in.
	Policy string `json:"policy"`
	// Location is the constructor's position with the call chain that
	// reached it, such as `checkout/alerts.sigil:8:1 →
	// platform/routing.sigil:10:5`.
	Location string `json:"location"`
	// Conditions are the source text of the conditions that held on the
	// way to the constructor, outermost first.
	Conditions []string `json:"conditions,omitempty"`
	Payload    Payload  `json:"payload"`
	// Winner marks the candidate that made the outcome.
	Winner bool `json:"winner"`
}

// AssertResult is one assert that didn't hold.
type AssertResult struct {
	Reason   string `json:"reason"`
	Policy   string `json:"policy"`
	Location string `json:"location"`
	// Cause is set when the assert couldn't be checked because its condition
	// raised a runtime error.
	Cause string `json:"cause,omitempty"`
}

// PoliciesResponse is the body of GET /api/v1/policies and of a successful
// POST /api/v1/policies/reload. It has the shape of deploygate's, one entry
// per kind the service serves, which for alertrouter is AlertRouting alone.
type PoliciesResponse struct {
	Kinds []KindPolicies `json:"kinds"`
}

// KindPolicies is one kind's loaded bundle.
type KindPolicies struct {
	Kind string `json:"kind"`
	// Version is the kind's contract version.
	Version  int       `json:"version"`
	LoadedAt time.Time `json:"loaded_at"`
	// Source is the directory the bundle was read from, or embedded.
	Source string `json:"source"`
	// Fingerprint is the hash of the bundle's `.sigil` files that serves.
	Fingerprint string           `json:"fingerprint"`
	Policies    []PolicyResponse `json:"policies"`
}

// PolicyResponse is one served team policy.
type PolicyResponse struct {
	Team   string `json:"team"`
	Policy string `json:"policy"`
}

// TeamsResponse is the body of GET /api/v1/teams: the team directory, sorted
// by name.
type TeamsResponse struct {
	Teams []routing.Team `json:"teams"`
}

// StatusResponse is the body of the health endpoints.
type StatusResponse struct {
	// Status is ok for /healthz, and ready or not ready for /readyz.
	Status string `json:"status"`
	// LoadedAt is the latest successful load of the bundle, set once /readyz
	// reports ready.
	LoadedAt *time.Time `json:"loaded_at,omitempty"`
}

// RoutingAlert is the kind's alert the request describes. Its severity is
// only valid when the caller checked it with [routing.ParseSeverity], which
// the handler does first.
func (r *AlertRequest) RoutingAlert() routing.Alert {
	return routing.Alert{
		Name:      r.Name,
		Severity:  routing.Severity(r.Severity),
		Labels:    r.Labels,
		FiringFor: time.Duration(r.FiringFor),
	}
}
