// Package deploy defines the DeployApproval kind: the contract every deploy
// policy is checked against. It is the running example of the documentation,
// so the policies under examples/policies read exactly like the docs.
//
// The Go types here are the source of truth. `sigilc export` writes them out
// as policies/deploy_approval.sigil for the tooling that runs without this
// code, and TestKindFileIsCurrent fails when that copy is stale.
package deploy

import (
	"strings"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// Input is everything a deploy policy can read: the release being shipped,
// the service it belongs to, who asks, and where.
type Input struct {
	Release     Release `policy:"release" json:"release"`
	Service     Service `policy:"service" json:"service"`
	Actor       Actor   `policy:"actor" json:"actor"`
	Environment string  `policy:"environment" json:"environment"`
}

// Release describes the artifact that is about to ship.
type Release struct {
	Soak   time.Duration `policy:"soak" json:"soak"`
	Hotfix bool          `policy:"hotfix" json:"hotfix"`
}

// Service describes the workload the release belongs to.
type Service struct {
	Name   string            `policy:"name" json:"name"`
	Tier   string            `policy:"tier" json:"tier"`
	Owners []string          `policy:"owners" json:"owners"`
	Labels map[string]string `policy:"labels" json:"labels"`
}

// Actor is the person, or automation, that requests the deploy.
type Actor struct {
	Name    string   `policy:"name" json:"name"`
	Teams   []string `policy:"teams" json:"teams"`
	Roles   []string `policy:"roles" json:"roles"`
	Regions []string `policy:"regions" json:"regions"`
}

// ReviewData is the payload of a review: who has to sign off.
type ReviewData struct {
	Approvers []string `policy:"approvers" json:"approvers"`
}

// ApproveData is the payload of an approval: how long the rollout bakes.
type ApproveData struct {
	Bake time.Duration `policy:"bake,default=1h" json:"bake"`
}

// The decisions, declared with their reasons. Deny carries only a reason.
var (
	Deny    = policy.NewDecision[policy.None]("deny", "not_eligible", "soak_too_short", "no_rule_matched")
	Review  = policy.NewDecision[ReviewData]("review", "service_owner")
	Approve = policy.NewDecision[ApproveData]("approve", "release_manager", "payments_sre")
)

// Kind is the DeployApproval contract, version 1. Decisions are listed in
// precedence order: a deny beats a review beats an approval, so a guardrail
// always wins over a team's approval. The reasons of deny and approve are
// ranked too, so two rules of the same decision never conflict: a deploy that
// is both ineligible and too fresh is denied as not_eligible.
var Kind = policy.NewKind[Input]("DeployApproval",
	policy.WithVersion(1),
	policy.WithDecisions(Deny, Review, Approve),
	policy.WithReasonPrecedence(Deny, "not_eligible", "soak_too_short", "no_rule_matched"),
	policy.WithReasonPrecedence(Approve, "release_manager", "payments_sre"),
	policy.WithDefault(Deny, "no_rule_matched"),
	policy.WithFunc("split", strings.Split),
)
