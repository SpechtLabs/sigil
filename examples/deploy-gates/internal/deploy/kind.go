// Package deploy defines the DeployApproval kind: the contract every deploy
// policy is checked against. It is the running example of the documentation,
// so the policies under examples/deploy-gates/policies read exactly like the
// docs.
//
// The package shows how a host declares a kind in Go. [Input] and its nested
// structs carry `policy:` tags that name what a policy reads, so the Sigil
// field release.soak is [Release.Soak], a duration. [Tier] is a named string
// type the kind registers as an enum, so service.tier is one of critical,
// standard and internal, written bare in a policy. [Deny], [Review] and
// [Approve] are the decision handles, each with its reasons and payload type,
// and [Kind] ties them together with the Tier enum, the precedence, the
// default decision and the host function split. The host reads a result
// through the same handles: [policy.Decision.Match] hands back a
// [ReviewData] or an [ApproveData], not a map.
//
// The Go types here are the source of truth. `sigilc export` writes them out
// as policies/deploy_approval.sigil for the tooling that runs without this
// code, and TestKindFileIsCurrent fails when that copy is stale. The
// package's tests also run every team's policy tests through
// [github.com/spechtlabs/sigil/pkg/policytest.Run], with the guardrails
// required from the platform's documents the way the service loads them.
package deploy

import (
	"slices"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// Tier is a service's criticality. [Kind] declares it as the enum Tier, so
// a policy writes service.tier == critical, and a misspelled tier is a
// compile error instead of a comparison that never matches.
type Tier string

// The tiers, in the order the kind declares them.
const (
	TierCritical Tier = "critical"
	TierStandard Tier = "standard"
	TierInternal Tier = "internal"
)

// Tiers lists every tier [Kind] declares, in declaration order.
var Tiers = []Tier{TierCritical, TierStandard, TierInternal}

// Input is everything a deploy policy can read: the release being shipped,
// the service it belongs to, who asks, and where.
type Input struct {
	Release Release `policy:"release" json:"release"`
	Service Service `policy:"service" json:"service"`
	Actor   Actor   `policy:"actor" json:"actor"`
	// Environment is where the release goes, such as production.
	Environment string `policy:"environment" json:"environment"`
	// Freeze is the change freeze in force when the policy runs. It is a
	// fact the host resolves, not something a client asserts: deploygate
	// fills it from its freeze source after decoding a request.
	Freeze Freeze `policy:"freeze" json:"freeze"`
}

// Release describes the artifact that is about to ship.
type Release struct {
	// Soak is how long the release has soaked before this deploy. The
	// platform's guardrails deny a release below the team's minimum soak.
	Soak time.Duration `policy:"soak" json:"soak"`
	// Hotfix exempts the release from the minimum soak.
	Hotfix bool `policy:"hotfix" json:"hotfix"`
}

// Service describes the workload the release belongs to.
type Service struct {
	Name string `policy:"name" json:"name"`
	// Tier is the service's criticality. A tier outside [Tiers] fails the
	// evaluation when a rule reads it, so check it with [Tier.Valid] first.
	Tier Tier `policy:"tier" json:"tier"`
	// Owners are the teams that own the service.
	Owners []string `policy:"owners" json:"owners"`
	// Labels are the workload's labels. The policies read compliance and
	// regions, and require the deployment platform's managed-by and
	// lifecycle labels.
	Labels map[string]string `policy:"labels" json:"labels"`
}

// Actor is the person, or automation, that requests the deploy.
type Actor struct {
	Name string `policy:"name" json:"name"`
	// Teams are the teams the actor belongs to. deploygate fills them from
	// the identity provider's groups.
	Teams []string `policy:"teams" json:"teams"`
	// Roles are the deploy roles, deployer and release_manager. deploygate
	// derives them from the access policy's grants, so a client can't claim
	// one.
	Roles []string `policy:"roles" json:"roles"`
	// Regions are the regions the actor is cleared for. The actor is cleared
	// for a service when they include every region its regions label lists.
	Regions []string `policy:"regions" json:"regions"`
}

// Freeze is the change freeze as the host knows it when it evaluates. The
// platform's deploy.freeze module turns it into is_frozen, and
// deploy.guardrails denies a frozen deploy with change_freeze.
type Freeze struct {
	// Environments are the environments frozen right now. deploygate sends
	// an empty list rather than null, so a logged input replays as it was.
	Environments []string `policy:"environments" json:"environments"`
	// Unknown is true when the host can't tell which environments are
	// frozen, because its freeze source has been unreachable for longer than
	// it may be stale. The policy then treats every environment as frozen:
	// it fails closed.
	Unknown bool `policy:"unknown" json:"unknown"`
}

// ReviewData is the payload of a review: who has to sign off.
type ReviewData struct {
	// Approvers are the groups whose sign-off the deploy waits for.
	Approvers []string `policy:"approvers" json:"approvers"`
}

// ApproveData is the payload of an approval: how long the rollout bakes.
type ApproveData struct {
	// Bake is one hour when the approving rule doesn't set it.
	Bake time.Duration `policy:"bake,default=1h" json:"bake"`
}

// The decisions, declared with their reasons. Deny carries only a reason.
var (
	Deny    = policy.NewDecision[policy.None]("deny", "not_eligible", "change_freeze", "soak_too_short", "no_rule_matched")
	Review  = policy.NewDecision[ReviewData]("review", "service_owner")
	Approve = policy.NewDecision[ApproveData]("approve", "release_manager", "payments_sre")
)

// The reasons Go code names, as typed handles: the kind's options rank them
// and pick the default, and the server answers with that default when the
// deploy policy never ran. A misspelled reason panics here at start-up, with a
// did-you-mean hint, instead of compiling into a comparison that never matches.
var (
	NotEligible   = Deny.Reason("not_eligible")
	ChangeFreeze  = Deny.Reason("change_freeze")
	SoakTooShort  = Deny.Reason("soak_too_short")
	NoRuleMatched = Deny.Reason("no_rule_matched")

	ReleaseManager = Approve.Reason("release_manager")
	PaymentsSRE    = Approve.Reason("payments_sre")
)

// Kind is the DeployApproval contract, version 2. Decisions are listed in
// precedence order: a deny beats a review beats an approval, so a guardrail
// always wins over a team's approval. The reasons of deny and approve are
// ranked too, so two rules of the same decision never conflict: a deploy that
// is both ineligible and too fresh is denied as not_eligible, and one that is
// frozen and too fresh as change_freeze.
//
// Version 2 added the freeze input and the change_freeze reason. Both are
// additions, so the kind accepts every version still, and a policy pinned
// to @1 loads unchanged; see docs/guides/evolve-a-kind.md.
//
// A host function that panics fails the evaluation closed, with the default
// and a [policy.RuntimeError], instead of unwinding into gin's recovery,
// which would answer an empty 500 that skips the request metrics and the
// evaluation error count. See [policy.WithRecoverHostPanics].
var Kind = policy.NewKind[Input]("DeployApproval",
	policy.WithVersion(2),
	policy.WithEnum(TierCritical, TierStandard, TierInternal),
	policy.WithDecisions(Deny, Review, Approve),
	policy.WithReasonPrecedence(NotEligible, ChangeFreeze, SoakTooShort, NoRuleMatched),
	policy.WithReasonPrecedence(ReleaseManager, PaymentsSRE),
	policy.WithDefault(NoRuleMatched),
	policy.WithFunc("split", strings.Split),
	policy.WithRecoverHostPanics(),
)

// Valid reports whether t is one of [Tiers]. A policy that reads a tier
// outside them fails with a runtime error, so deploygate refuses one with
// 400 before any policy runs.
func (t Tier) Valid() bool {
	return slices.Contains(Tiers, t)
}
