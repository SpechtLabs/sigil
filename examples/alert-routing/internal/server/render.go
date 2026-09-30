package server

import (
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
)

// fallbackOutcome is the answer for an alert no policy decided: the
// AlertRouting kind's default, which posts the alert to the default channel
// rather than losing it.
var fallbackOutcome = routing.Unrouted

// newRouteResponse renders an evaluation for team's policy. The winner's
// payload is read through the typed decision handles, which is how a host is
// meant to consume a result; trace candidates carry payloads of every
// decision, so they are rendered generically.
func newRouteResponse(team, policyName string, res *policy.Result) RouteResponse {
	resp := RouteResponse{
		Team:     team,
		Policy:   policyName,
		Decision: res.Decision,
		Reason:   res.Reason,
		Trace:    traceCandidates(res),
	}
	if page, ok := routing.Page.Match(res); ok {
		resp.Target = page.Target
	}
	if note, ok := routing.Notify.Match(res); ok {
		resp.Channel = note.Channel
	}
	return resp
}

// fallbackResponse is the answer for an alert no policy ran for, an unowned
// or an invalid one: the kind's default, which the dispatcher acts on, and no
// trace. policyName is empty when no team owns the alert.
func fallbackResponse(team, policyName string) RouteResponse {
	return RouteResponse{
		Team:     team,
		Policy:   policyName,
		Decision: fallbackOutcome.Decision(),
		Reason:   fallbackOutcome.Name(),
		Channel:  routing.DefaultChannel,
		Trace:    []CandidateResult{},
	}
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
		out = append(out, candidateResult(c, winner))
	}
	return out
}

// candidateResult renders one candidate.
func candidateResult(c policy.Candidate, winner bool) CandidateResult {
	var conditions []string
	if len(c.Conditions) > 0 {
		conditions = make([]string, 0, len(c.Conditions))
	}
	for _, cond := range c.Conditions {
		conditions = append(conditions, cond.Text)
	}
	return CandidateResult{
		Decision:   c.Decision,
		Reason:     c.Reason,
		Policy:     c.Policy,
		Location:   c.Location(),
		Conditions: conditions,
		Payload:    renderPayload(c.Payload),
		Winner:     winner,
	}
}

// conflictResult renders the candidates of a conflict, both sides of it.
func conflictResult(err *policy.ConflictError) *ConflictResult {
	if err == nil {
		return nil
	}
	out := &ConflictResult{Candidates: make([]CandidateResult, 0, len(err.Candidates))}
	for _, c := range err.Candidates {
		out.Candidates = append(out.Candidates, candidateResult(c, false))
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
// durations as well as a duration. AlertRouting's payloads are strings
// today; a kind version that adds a duration field needs no change here.
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
