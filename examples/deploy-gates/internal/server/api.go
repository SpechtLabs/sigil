package server

import (
	"time"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/access"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
)

// DeploymentRequest is the body of POST /api/v1/teams/{team}/deployments.
// It carries no roles: the access policy grants them, and the handler feeds
// them into the deploy policy's actor.roles.
type DeploymentRequest struct {
	Release ReleaseRequest `json:"release"`
	// Service is the deploy policy's service as it is. The handler refuses
	// a tier the kind doesn't declare with 400.
	Service     deploy.Service `json:"service"`
	Actor       ActorRequest   `json:"actor"`
	Environment string         `json:"environment"`
}

// ReleaseRequest is the release being shipped, as the API receives it.
type ReleaseRequest struct {
	// Soak is how long the release has soaked, a duration string. The
	// handler refuses a negative one with 400.
	Soak   Duration `json:"soak"`
	Hotfix bool     `json:"hotfix"`
}

// ActorRequest is who asks for a deployment: their identity as the identity
// provider describes it, and the regions they are cleared for. The access
// policy reads name, groups and clearance; the deploy policy reads groups as
// the actor's teams, and regions.
type ActorRequest struct {
	Name      string   `json:"name"`
	Groups    []string `json:"groups"`
	Clearance string   `json:"clearance"`
	Regions   []string `json:"regions"`
}

// AccessRequest is the body of POST /api/v1/access/grants: the access
// policy's input as it is. Team must not be empty.
type AccessRequest struct {
	Actor       access.Actor `json:"actor"`
	Team        string       `json:"team"`
	Environment string       `json:"environment"`
}

// DecisionResponse is what a deployment request returns: the decision the
// host acts on, the trace that explains it, and the roles the access stage
// granted. When an evaluation fails, with HTTP 422 for a failed input assert
// the caller has to fix, 500 for a failure of the policy or 503 for an
// evaluation that ran out of time, the decision fields hold the fallback,
// deny, and Error says what went wrong.
type DecisionResponse struct {
	// Team is the team from the path.
	Team string `json:"team"`
	// Policy is the team's root policy, <team>.production.
	Policy string `json:"policy"`
	// Decision and Reason are the outcome: approve, review or deny, and why.
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	// Payload is the winning decision's payload: bake for an approval,
	// approvers for a review, empty for a deny.
	Payload Payload `json:"payload"`
	// Trace lists every candidate the deploy policy produced, the winner
	// marked. It is empty when no rule fired, so the kind's default decided,
	// and when the deploy policy didn't run.
	Trace []CandidateResult `json:"trace"`
	// Access is the access stage that ran before the deploy policy.
	Access *AccessBlock `json:"access,omitempty"`
	// Error is set when an evaluation failed, and Asserts or Conflict
	// explain the failure when it was a failed assert or a conflict.
	Error    *ErrorResponse  `json:"error,omitempty"`
	Asserts  []AssertResult  `json:"asserts,omitempty"`
	Conflict *ConflictResult `json:"conflict,omitempty"`
}

// AccessBlock is the access stage of a deployment request: the policy that
// ran and the roles it granted, in outcome order.
type AccessBlock struct {
	Policy string        `json:"policy"`
	Grants []GrantResult `json:"grants"`
}

// AccessResponse is what POST /api/v1/access/grants returns: 200 when at
// least one role is granted, 403 when none is, and 422, 500 or 503 with Error
// when the evaluation failed, in which case Grants is empty.
type AccessResponse struct {
	Policy      string `json:"policy"`
	Team        string `json:"team"`
	Environment string `json:"environment"`
	// Grants are the roles granted, in outcome order, never null.
	Grants []GrantResult `json:"grants"`
	// Trace lists every candidate the access policy produced.
	Trace []CandidateResult `json:"trace"`
	// Error, Asserts and Conflict are set as in [DecisionResponse].
	Error    *ErrorResponse  `json:"error,omitempty"`
	Asserts  []AssertResult  `json:"asserts,omitempty"`
	Conflict *ConflictResult `json:"conflict,omitempty"`
}

// GrantResult is one role the access policy granted. TTL is set for the
// roles that expire.
type GrantResult struct {
	Role   string    `json:"role"`
	Reason string    `json:"reason"`
	TTL    *Duration `json:"ttl,omitempty"`
	// Policy is the policy whose rule granted the role.
	Policy string `json:"policy"`
	// Location is where the grant was made, with the call chain that led
	// there when the trace has it.
	Location string `json:"location"`
}

// ConflictResult names the candidates that can't fire together, such as an
// admin and a release_manager grant, which the AccessGrant kind declares
// exclusive.
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
	// reached it, such as `payments/production.sigil:10:3 →
	// deploy/production.sigil:16:5`.
	Location string `json:"location"`
	// Conditions are the source text of the conditions that held on the
	// way to the constructor, outermost first.
	Conditions []string `json:"conditions,omitempty"`
	Payload    Payload  `json:"payload"`
	// Winner marks a candidate that made the outcome.
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
// POST /api/v1/policies/reload: every kind the service serves.
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
	Source   string           `json:"source"`
	Policies []PolicyResponse `json:"policies"`
}

// PolicyResponse is one served policy and, for a team's policy, the team.
type PolicyResponse struct {
	Team   string `json:"team,omitempty"`
	Policy string `json:"policy"`
}

// StatusResponse is the body of the health endpoints.
type StatusResponse struct {
	// Status is ok for /healthz, and ready or not ready for /readyz.
	Status string `json:"status"`
	// LoadedAt is the latest successful load of either bundle, set once
	// /readyz reports ready.
	LoadedAt *time.Time `json:"loaded_at,omitempty"`
}

// AccessInput is the access policy's input for a deployment to team.
func (r *DeploymentRequest) AccessInput(team string) access.Input {
	return access.Input{
		Actor:       access.Actor{Name: r.Actor.Name, Groups: r.Actor.Groups, Clearance: r.Actor.Clearance},
		Team:        team,
		Environment: r.Environment,
	}
}

// DeployInput is the deploy policy's input, with the roles the access stage
// granted. The actor's groups are its teams.
func (r *DeploymentRequest) DeployInput(roles []string) deploy.Input {
	return deploy.Input{
		Release: deploy.Release{
			Soak:   time.Duration(r.Release.Soak),
			Hotfix: r.Release.Hotfix,
		},
		Service: r.Service,
		Actor: deploy.Actor{
			Name:    r.Actor.Name,
			Teams:   r.Actor.Groups,
			Roles:   roles,
			Regions: r.Actor.Regions,
		},
		Environment: r.Environment,
	}
}
