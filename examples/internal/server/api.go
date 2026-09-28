package server

import (
	"time"

	"github.com/spechtlabs/sigil/examples/internal/access"
	"github.com/spechtlabs/sigil/examples/internal/deploy"
)

// DeploymentRequest is the body of POST /api/v1/teams/{team}/deployments.
// It carries no roles: the access policy grants them, and the handler feeds
// them into the deploy policy's actor.roles.
type DeploymentRequest struct {
	Release     ReleaseRequest `json:"release"`
	Service     deploy.Service `json:"service"`
	Actor       ActorRequest   `json:"actor"`
	Environment string         `json:"environment"`
}

// ReleaseRequest is the release being shipped, as the API receives it.
type ReleaseRequest struct {
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
// policy's input as it is.
type AccessRequest struct {
	Actor       access.Actor `json:"actor"`
	Team        string       `json:"team"`
	Environment string       `json:"environment"`
}

// DecisionResponse is what a deployment request returns: the decision the
// host acts on, the trace that explains it, and the roles the access stage
// granted. When an evaluation fails (HTTP 409 or 422) the decision fields hold
// the fallback, deny, and Error says what went wrong.
type DecisionResponse struct {
	Team     string            `json:"team"`
	Policy   string            `json:"policy"`
	Decision string            `json:"decision"`
	Reason   string            `json:"reason"`
	Payload  Payload           `json:"payload"`
	Trace    []CandidateResult `json:"trace"`
	Access   *AccessBlock      `json:"access,omitempty"`
	Error    *ErrorResponse    `json:"error,omitempty"`
	Asserts  []AssertResult    `json:"asserts,omitempty"`
	Conflict *ConflictResult   `json:"conflict,omitempty"`
}

// AccessBlock is the access stage of a deployment request: the policy that
// ran and the roles it granted, in outcome order.
type AccessBlock struct {
	Policy string        `json:"policy"`
	Grants []GrantResult `json:"grants"`
}

// AccessResponse is what POST /api/v1/access/grants returns: 200 when at
// least one role is granted, 403 when none is, and 409 or 422 with Error when
// the evaluation failed, in which case Grants is empty.
type AccessResponse struct {
	Policy      string            `json:"policy"`
	Team        string            `json:"team"`
	Environment string            `json:"environment"`
	Grants      []GrantResult     `json:"grants"`
	Trace       []CandidateResult `json:"trace"`
	Error       *ErrorResponse    `json:"error,omitempty"`
	Asserts     []AssertResult    `json:"asserts,omitempty"`
	Conflict    *ConflictResult   `json:"conflict,omitempty"`
}

// GrantResult is one role the access policy granted. TTL is set for the
// roles that expire.
type GrantResult struct {
	Role     string    `json:"role"`
	Reason   string    `json:"reason"`
	TTL      *Duration `json:"ttl,omitempty"`
	Policy   string    `json:"policy"`
	Location string    `json:"location"`
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
// POST /api/v1/policies/reload: every kind the service serves.
type PoliciesResponse struct {
	Kinds []KindPolicies `json:"kinds"`
}

// KindPolicies is one kind's loaded bundle.
type KindPolicies struct {
	Kind     string           `json:"kind"`
	Version  int              `json:"version"`
	LoadedAt time.Time        `json:"loaded_at"`
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
	Status   string     `json:"status"`
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
