package server

import (
	"net/http"
	"slices"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/internal/access"
	"github.com/spechtlabs/sigil/examples/internal/deploy"
)

// The deploy roles the access roles map to. Reader and auditor grant no
// deploy role; admin grants both, since an admin can do everything a
// deployer and a release manager can.
const (
	roleDeployer       = "deployer"
	roleReleaseManager = "release_manager"
)

// fallbackOutcome is the answer a deployment request gives when the access
// stage fails: the DeployApproval kind's default. The deploy policy never
// ran, so there is no result to take it from.
var fallbackOutcome = deploy.NoRuleMatched

// newDecisionResponse renders a deploy evaluation and the HTTP status its
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

// fallbackResponse is the answer to a deployment request whose access stage
// failed: the kind's default decision, which the host acts on, and no trace,
// since the deploy policy didn't run.
func fallbackResponse(team, policyName string) DecisionResponse {
	return DecisionResponse{
		Team:     team,
		Policy:   policyName,
		Decision: fallbackOutcome.Decision(),
		Reason:   fallbackOutcome.Name(),
		Payload:  Payload{},
		Trace:    []CandidateResult{},
	}
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

// decisionStatus maps a decision to its HTTP status through the typed
// decision handles: approve is 200, review 202, and deny, or anything a
// newer kind adds, 403, so an unknown decision fails closed.
func decisionStatus(res *policy.Result) int {
	if _, ok := deploy.Approve.Match(res); ok {
		return http.StatusOK
	}
	if _, ok := deploy.Review.Match(res); ok {
		return http.StatusAccepted
	}
	return http.StatusForbidden
}

// grantResults renders an access result's outcome through the typed
// decision handles, one MatchAll per role, as a host consumes a collecting
// kind. The outcome is sorted by the kind's declaration order and then by
// position, so taking the roles in declaration order keeps outcome order.
func grantResults(res *policy.Result) []GrantResult {
	out := make([]GrantResult, 0, len(res.Outcome))
	for _, m := range access.Reader.MatchAll(res) {
		out = append(out, grant(res, access.Reader.Name(), m.Reason, m.Policy, m.Position, nil))
	}
	for _, m := range access.Deployer.MatchAll(res) {
		out = append(out, grant(res, access.Deployer.Name(), m.Reason, m.Policy, m.Position, ttl(m.Payload.TTL)))
	}
	for _, m := range access.ReleaseManager.MatchAll(res) {
		out = append(out, grant(res, access.ReleaseManager.Name(), m.Reason, m.Policy, m.Position, ttl(m.Payload.TTL)))
	}
	for _, m := range access.Admin.MatchAll(res) {
		out = append(out, grant(res, access.Admin.Name(), m.Reason, m.Policy, m.Position, ttl(m.Payload.TTL)))
	}
	for _, m := range access.Auditor.MatchAll(res) {
		out = append(out, grant(res, access.Auditor.Name(), m.Reason, m.Policy, m.Position, nil))
	}
	return out
}

// deployRoles derives the deploy policy's actor.roles from the granted
// roles: deployer and release_manager carry over, admin grants both, and
// reader and auditor grant nothing. Each role appears once, in a fixed order.
func deployRoles(grants []GrantResult) []string {
	var deployer, releaseManager bool
	for _, g := range grants {
		switch g.Role {
		case access.Deployer.Name():
			deployer = true
		case access.ReleaseManager.Name():
			releaseManager = true
		case access.Admin.Name():
			deployer, releaseManager = true, true
		}
	}

	roles := []string{}
	if deployer {
		roles = append(roles, roleDeployer)
	}
	if releaseManager {
		roles = append(roles, roleReleaseManager)
	}
	return roles
}

// grant builds one grant, with the location of the candidate that produced
// it. An outcome entry carries only its constructor's position, while the
// matching trace candidate knows the whole call chain.
func grant(res *policy.Result, role, reason, policyName string, pos policy.Position, lifetime *Duration) GrantResult {
	location := pos.String()
	i := slices.IndexFunc(res.Trace.Candidates, func(c policy.Candidate) bool {
		return c.Decision == role && c.Reason == reason && c.Policy == policyName && c.Position == pos
	})
	if i >= 0 {
		location = res.Trace.Candidates[i].Location()
	}
	return GrantResult{Role: role, Reason: reason, TTL: lifetime, Policy: policyName, Location: location}
}

// ttl makes a grant's time to live optional on the wire.
func ttl(d time.Duration) *Duration {
	w := Duration(d)
	return &w
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
