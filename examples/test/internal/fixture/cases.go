package fixture

import "net/http"

// The roles of the AccessGrant kind, as grants and derived roles name them.
const (
	RoleReader         = "reader"
	RoleDeployer       = "deployer"
	RoleReleaseManager = "release_manager"
	RoleAdmin          = "admin"
	RoleAuditor        = "auditor"
)

// The payloads the deploy cases share.
const (
	bothApprovers = `{"approvers": ["payments-leads", "security-leads"]}`
	noPayload     = `{}`
)

// GrantRef is a grant as a spec expects it: the role, the reason, and the
// time to live, empty for a role that doesn't expire.
type GrantRef struct {
	Role   string
	Reason string
	TTL    string
}

// DecisionCase is one deploy request and what the service must answer: the
// roles the access stage grants and the decision the deploy stage makes with
// them. The integration and end-to-end suites run the same cases, so the
// in-process server and the container can't disagree.
type DecisionCase struct {
	Request  DeployRequest
	Name     string
	Team     string
	Decision string
	Reason   string
	// Payload is the exact payload JSON, durations as strings.
	Payload string
	// Grants is the access stage's outcome, in outcome order.
	Grants []GrantRef
	// Roles is what the grants became: the deploy policy's actor.roles.
	Roles  []string
	Status int
}

// AccessCase is one access request and the roles the access policy must
// grant, in outcome order. An empty outcome is a 403.
type AccessCase struct {
	Request AccessRequest
	Name    string
	Grants  []GrantRef
	Status  int
}

// BadRequestCase is a body the service must refuse with 400.
type BadRequestCase struct {
	Name string
	Body string
}

// The groups of the actors whose access evaluation fails. The specs send
// them to the access endpoint and through a deployment alike.
var (
	// BreakGlassPlatform trips the kind's exclusive line: break glass grants
	// admin and the platform group grants release_manager.
	BreakGlassPlatform = []string{"break-glass", "platform"}

	// ComplianceMember is in the team and in compliance, so they would
	// audit their own deploys; the guardrails' separation-of-duties assert
	// refuses that.
	ComplianceMember = []string{TeamPayments, "compliance"}
)

// The grants the specs expect most often.
var (
	memberReader   = GrantRef{Role: RoleReader, Reason: "team_member"}
	stagingReader  = GrantRef{Role: RoleReader, Reason: "everyone_in_staging"}
	memberDeployer = GrantRef{Role: RoleDeployer, Reason: "team_member", TTL: "8h"}
	oncallDeployer = GrantRef{Role: RoleDeployer, Reason: "oncall", TTL: "2h"}
	platformRM     = GrantRef{Role: RoleReleaseManager, Reason: "platform_member", TTL: "4h"}
	clearanceAdmin = GrantRef{Role: RoleAdmin, Reason: "clearance", TTL: "1h"}
	breakGlass     = GrantRef{Role: RoleAdmin, Reason: "break_glass", TTL: "15m"}
	member         = []GrantRef{memberReader, memberDeployer}
)

// DecisionCases returns every decision and reason the payments and checkout
// policies can reach through the API, with the roles that lead there and the
// status the decision maps to.
func DecisionCases() []DecisionCase {
	cases := reviewCases()
	cases = append(cases, approveCases()...)
	return append(cases, denyCases()...)
}

// AccessCases returns a request for every rule of access.main, and for the
// combinations whose outcome says something about the kind: a collecting
// kind grants every role that fires, and admin and release_manager never
// come together.
func AccessCases() []AccessCase {
	return []AccessCase{
		{
			Name:    "a team member reads and deploys the team's services",
			Request: AccessFor(TeamPayments),
			Grants:  member, Status: http.StatusOK,
		},
		{
			Name:    "everyone reads in staging",
			Request: AccessFor("marketing").In(EnvStaging),
			Grants:  []GrantRef{stagingReader}, Status: http.StatusOK,
		},
		{
			// Both reader rules fire, with different reasons, and a
			// collecting kind keeps both.
			Name:    "a team member in staging is a reader for two reasons",
			Request: AccessFor(TeamPayments).In(EnvStaging),
			Grants:  []GrantRef{memberReader, stagingReader, memberDeployer}, Status: http.StatusOK,
		},
		{
			Name:    "the team's on-call SRE deploys for two hours",
			Request: AccessFor("payments-sre"),
			Grants:  []GrantRef{oncallDeployer}, Status: http.StatusOK,
		},
		{
			Name:    "a platform member manages releases for four hours",
			Request: AccessFor("platform"),
			Grants:  []GrantRef{platformRM}, Status: http.StatusOK,
		},
		{
			Name:    "admin clearance grants admin for an hour",
			Request: AccessFor().Cleared(RoleAdmin),
			Grants:  []GrantRef{clearanceAdmin}, Status: http.StatusOK,
		},
		{
			// The policy keeps admin and release_manager apart itself, so
			// the host's exclusive line never has to step in here.
			Name:    "a platform member with admin clearance is admin only",
			Request: AccessFor("platform").Cleared(RoleAdmin),
			Grants:  []GrantRef{clearanceAdmin}, Status: http.StatusOK,
		},
		{
			Name:    "break glass grants admin for fifteen minutes",
			Request: AccessFor("break-glass"),
			Grants:  []GrantRef{breakGlass}, Status: http.StatusOK,
		},
		{
			Name:    "a compliance member audits",
			Request: AccessFor("compliance"),
			Grants:  []GrantRef{{Role: RoleAuditor, Reason: "compliance_member"}}, Status: http.StatusOK,
		},
		{
			Name:    "an outsider gets nothing in production",
			Request: AccessFor("marketing"),
			Grants:  []GrantRef{}, Status: http.StatusForbidden,
		},
		{
			// The on-call rule matches the team's own SRE group only.
			Name:    "another team's SRE gets nothing",
			Request: AccessFor("checkout-sre"),
			Grants:  []GrantRef{}, Status: http.StatusForbidden,
		},
	}
}

// BadRequestCases returns bodies the deployments endpoint must refuse before
// evaluating anything.
func BadRequestCases() []BadRequestCase {
	return []BadRequestCase{
		{Name: "malformed JSON", Body: `{"release": {"soak": "6h"`},
		{Name: "a field at the wrong level", Body: OwnerRequest().JSONWithField("tier", "critical")},
		{Name: "roles sent by the client", Body: legacyActor("roles")},
		{Name: "teams sent by the client", Body: legacyActor("teams")},
		{Name: "a duration that doesn't parse", Body: OwnerRequest(Soak("six hours")).JSON()},
		{Name: "a number where a duration belongs", Body: `{"release": {"soak": 21600000000000}}`},
		{Name: "a lone minus sign as a duration", Body: OwnerRequest(Soak("-")).JSON()},
		{Name: "a day count longer than a duration holds", Body: OwnerRequest(Soak("9999999999d")).JSON()},
		{Name: "a negative soak", Body: OwnerRequest(Soak("-1h")).JSON()},
	}
}

// AccessBadRequestCases returns bodies the access endpoint must refuse
// before evaluating anything. A request without a team would evaluate the
// access policy for nobody's services, which no rule is written for.
func AccessBadRequestCases() []BadRequestCase {
	return []BadRequestCase{
		{Name: "malformed JSON", Body: `{"actor": {"name": "ada"`},
		{Name: "roles sent by the client", Body: `{"actor": {"name": "ada", "groups": [], "clearance": "", "roles": ["admin"]}, "team": "payments", "environment": "production"}`},
		{Name: "no team", Body: `{"actor": {"name": "ada", "groups": ["payments"], "clearance": ""}, "environment": "production"}`},
		{Name: "an empty team", Body: mustMarshal(AccessRequest{Actor: AccessActor{Name: Ada, Groups: []string{TeamPayments}}, Environment: EnvProduction})},
	}
}

// reviewCases are the deploys that end in a review: a team member who owns
// the service, as a deployer.
func reviewCases() []DecisionCase {
	return []DecisionCase{
		{
			Name: "a payments owner's PCI service needs both approver groups",
			Team: TeamPayments, Request: OwnerRequest(),
			Grants: member, Roles: []string{RoleDeployer},
			Status: http.StatusAccepted, Decision: DecisionReview, Reason: ReasonServiceOwner,
			Payload: bothApprovers,
		},
		{
			Name: "a non-PCI service needs only the payments leads",
			Team: TeamPayments, Request: OwnerRequest(NoLabel("compliance")),
			Grants: member, Roles: []string{RoleDeployer},
			Status: http.StatusAccepted, Decision: DecisionReview, Reason: ReasonServiceOwner,
			Payload: `{"approvers": ["payments-leads"]}`,
		},
		{
			Name: "a hotfix with a two hour soak skips the soak guardrail",
			Team: TeamPayments, Request: OwnerRequest(Soak("2h"), Hotfix()),
			Grants: member, Roles: []string{RoleDeployer},
			Status: http.StatusAccepted, Decision: DecisionReview, Reason: ReasonServiceOwner,
			Payload: bothApprovers,
		},
		{
			// Checkout keeps the platform's 24h minimum soak, so its owner
			// ships a release that soaked for a day.
			Name: "the checkout team reviews its own standard service",
			Team: TeamCheckout, Request: checkoutOwner(),
			Grants: member, Roles: []string{RoleDeployer},
			Status: http.StatusAccepted, Decision: DecisionReview, Reason: ReasonServiceOwner,
			Payload: `{"approvers": ["checkout-leads"]}`,
		},
	}
}

// approveCases are the deploys that end in an approval, one per reason.
func approveCases() []DecisionCase {
	return []DecisionCase{
		{
			// The platform group grants release_manager on top of the team
			// member's deployer, and a critical service is the release
			// manager's to approve.
			Name: "a platform member approves a critical service as release manager",
			Team: TeamPayments, Request: OwnerRequest(Tier("critical"), Groups(TeamPayments, "platform")),
			Grants: []GrantRef{memberReader, memberDeployer, platformRM}, Roles: []string{RoleDeployer, RoleReleaseManager},
			Status: http.StatusOK, Decision: DecisionApprove, Reason: RoleReleaseManager,
			Payload: `{"bake": "1h"}`,
		},
		{
			// admin is both roles to the deploy policy, so an admin needs no
			// team group to approve a critical service.
			Name: "an admin approves a critical service as release manager",
			Team: TeamPayments, Request: OwnerRequest(Tier("critical"), Groups(), Clearance(RoleAdmin)),
			Grants: []GrantRef{clearanceAdmin}, Roles: []string{RoleDeployer, RoleReleaseManager},
			Status: http.StatusOK, Decision: DecisionApprove, Reason: RoleReleaseManager,
			Payload: `{"bake": "1h"}`,
		},
		{
			Name: "an on-call payments SRE approves with the team's short bake",
			Team: TeamPayments, Request: OwnerRequest(Groups("payments-sre")),
			Grants: []GrantRef{oncallDeployer}, Roles: []string{RoleDeployer},
			Status: http.StatusOK, Decision: DecisionApprove, Reason: "payments_sre",
			Payload: `{"bake": "15m"}`,
		},
	}
}

// denyCases are the deploys that end in a deny, one per reason.
func denyCases() []DecisionCase {
	return []DecisionCase{
		{
			Name: "a two hour soak is shorter than the team's four hours",
			Team: TeamPayments, Request: OwnerRequest(Soak("2h")),
			Grants: member, Roles: []string{RoleDeployer},
			Status: http.StatusForbidden, Decision: DecisionDeny, Reason: "soak_too_short",
			Payload: noPayload,
		},
		{
			// No grants isn't an error: the deploy policy runs without
			// roles and its guardrail says why the deploy can't go ahead.
			Name: "an actor outside the team gets no role and is not eligible",
			Team: TeamPayments, Request: OwnerRequest(Groups("marketing")),
			Grants: []GrantRef{}, Roles: []string{},
			Status: http.StatusForbidden, Decision: DecisionDeny, Reason: "not_eligible",
			Payload: noPayload,
		},
		{
			// Both guardrail denies fire. The kind ranks not_eligible above
			// soak_too_short, so the host gets one deny, not a conflict.
			Name: "an outsider with a short soak is still just not eligible",
			Team: TeamPayments, Request: OwnerRequest(Groups("marketing"), Soak("2h")),
			Grants: []GrantRef{}, Roles: []string{},
			Status: http.StatusForbidden, Decision: DecisionDeny, Reason: "not_eligible",
			Payload: noPayload,
		},
		{
			// Checkout narrows the tiers to standard, so nothing matches an
			// internal service and the kind's default applies.
			Name: "checkout's owner of an internal service matches no rule",
			Team: TeamCheckout, Request: checkoutOwner(Tier("internal")),
			Grants: member, Roles: []string{RoleDeployer},
			Status: http.StatusForbidden, Decision: DecisionDeny, Reason: "no_rule_matched",
			Payload: noPayload,
		},
	}
}

// checkoutOwner is the checkout team's owner shipping a release that soaked
// for a day, with more mutators applied after.
func checkoutOwner(more ...Mutator) DeployRequest {
	return OwnerRequest(append([]Mutator{Owners(TeamCheckout), Groups(TeamCheckout), Soak("24h")}, more...)...)
}

// legacyActor renders the owner's request with an actor field from before
// the access layer. The service must refuse it: a client that still sends
// roles would otherwise believe it chose them.
func legacyActor(field string) string {
	r := OwnerRequest()
	actor := map[string]any{
		"name":      r.Actor.Name,
		"groups":    r.Actor.Groups,
		"clearance": r.Actor.Clearance,
		"regions":   r.Actor.Regions,
		field:       []string{RoleDeployer},
	}
	return mustMarshal(map[string]any{
		"release":     r.Release,
		"service":     r.Service,
		"actor":       actor,
		"environment": r.Environment,
	})
}
