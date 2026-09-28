package server

import (
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/internal/deploy"
)

// DeploymentRequest is the body of POST /api/v1/teams/{team}/deployments. It
// mirrors deploy.Input field for field; only the release's soak differs,
// because a duration travels as a string.
type DeploymentRequest struct {
	Release     ReleaseRequest `json:"release"`
	Service     deploy.Service `json:"service"`
	Actor       deploy.Actor   `json:"actor"`
	Environment string         `json:"environment"`
}

// ReleaseRequest is the release being shipped, as the API receives it.
type ReleaseRequest struct {
	Soak   Duration `json:"soak"`
	Hotfix bool     `json:"hotfix"`
}

// DecisionResponse is what an evaluation returns: the decision the host acts
// on, and the trace that explains it. On a failed evaluation (HTTP 422) the
// decision fields hold the fallback, the kind's default, and Error says what
// went wrong.
type DecisionResponse struct {
	Team     string            `json:"team"`
	Policy   string            `json:"policy"`
	Decision string            `json:"decision"`
	Reason   string            `json:"reason"`
	Payload  Payload           `json:"payload"`
	Trace    []CandidateResult `json:"trace"`
	Error    *ErrorResponse    `json:"error,omitempty"`
	Asserts  []AssertResult    `json:"asserts,omitempty"`
}

// Payload is a decision's payload by field name, with durations rendered as
// strings. It stays untyped because the trace holds candidates of every
// decision, each with its own payload struct.
type Payload map[string]any

// CandidateResult is one decision constructor that fired, as the trace
// reports it.
type CandidateResult struct {
	Decision   string   `json:"decision"`
	Reason     string   `json:"reason"`
	Policy     string   `json:"policy"`
	Location   string   `json:"location"`
	Conditions []string `json:"conditions,omitempty"`
	Payload    Payload  `json:"payload"`
	Winner     bool     `json:"winner"`
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
// POST /api/v1/policies/reload.
type PoliciesResponse struct {
	Kind     string           `json:"kind"`
	Version  int              `json:"version"`
	LoadedAt time.Time        `json:"loaded_at"`
	Source   string           `json:"source"`
	Policies []PolicyResponse `json:"policies"`
}

// PolicyResponse is one served team and the policy it evaluates.
type PolicyResponse struct {
	Team   string `json:"team"`
	Policy string `json:"policy"`
}

// StatusResponse is the body of the health endpoints.
type StatusResponse struct {
	Status   string     `json:"status"`
	LoadedAt *time.Time `json:"loaded_at,omitempty"`
}

// Input converts the request into the kind's input.
func (r *DeploymentRequest) Input() deploy.Input {
	return deploy.Input{
		Release: deploy.Release{
			Soak:   time.Duration(r.Release.Soak),
			Hotfix: r.Release.Hotfix,
		},
		Service:     r.Service,
		Actor:       r.Actor,
		Environment: r.Environment,
	}
}

// newDecisionResponse renders an evaluation result and the HTTP status its
// decision maps to. The winner's payload is read through the typed decision
// handles, which is how a host is meant to consume a result; trace candidates
// carry payloads of every decision, so they are rendered generically.
func newDecisionResponse(team, policyName string, res *policy.Result) (DecisionResponse, int) {
	return DecisionResponse{
		Team:     team,
		Policy:   policyName,
		Decision: res.Decision,
		Reason:   res.Reason,
		Payload:  decisionPayload(res),
		Trace:    traceCandidates(res),
	}, decisionStatus(res)
}

// decisionPayload renders the winning decision's payload from its typed
// struct.
func decisionPayload(res *policy.Result) Payload {
	if data, ok := deploy.Approve.Match(res); ok {
		return Payload{"bake": Duration(data.Bake)}
	}
	if data, ok := deploy.Review.Match(res); ok {
		return Payload{"approvers": data.Approvers}
	}
	return renderPayload(res.Payload)
}

// traceCandidates renders the trace and marks the candidates that made the
// outcome. The trace is sorted so the winner comes first, and a constructor
// can fire once per call chain, so each outcome entry claims the first
// candidate that matches it and no more.
func traceCandidates(res *policy.Result) []CandidateResult {
	claimed := make([]bool, len(res.Outcome))
	out := make([]CandidateResult, 0, len(res.Trace.Candidates))
	for _, c := range res.Trace.Candidates {
		winner := false
		for i, e := range res.Outcome {
			if !claimed[i] && e.Decision == c.Decision && e.Reason == c.Reason && e.Policy == c.Policy && e.Position == c.Position {
				claimed[i], winner = true, true
				break
			}
		}

		var conditions []string
		for _, cond := range c.Conditions {
			conditions = append(conditions, cond.Text)
		}

		out = append(out, CandidateResult{
			Decision:   c.Decision,
			Reason:     c.Reason,
			Policy:     c.Policy,
			Location:   c.Location(),
			Conditions: conditions,
			Payload:    renderPayload(c.Payload),
			Winner:     winner,
		})
	}
	return out
}

// assertResults renders the asserts of an assertion error.
func assertResults(err *policy.AssertionError) []AssertResult {
	if err == nil {
		return nil
	}
	out := make([]AssertResult, 0, len(err.Failures))
	for _, f := range err.Failures {
		a := AssertResult{Reason: f.Reason, Policy: f.Policy, Location: f.Location()}
		if f.Cause != nil {
			a.Cause = f.Cause.Error()
		}
		out = append(out, a)
	}
	return out
}

// renderPayload copies an untyped payload with every duration as a string.
// It never returns nil, so a decision without a payload renders as {}.
func renderPayload(in map[string]any) Payload { //nolint:emptyinterface // policy.Result's payloads are untyped maps by design
	out := make(Payload, len(in))
	for k, v := range in {
		out[k] = renderValue(v)
	}
	return out
}

// renderValue makes one payload value JSON-ready: durations become strings,
// and lists and maps are walked, since a payload field can be a list of
// durations as well as a duration.
func renderValue(v any) any { //nolint:emptyinterface // walks the untyped payload values policy.Result hands out
	switch val := v.(type) {
	case time.Duration:
		return Duration(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = renderValue(item)
		}
		return out
	case map[string]any:
		return renderPayload(val)
	default:
		return v
	}
}
