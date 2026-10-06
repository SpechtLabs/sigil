// Package access defines the AccessGrant kind: the roles a requestor holds in
// an environment for one team's services. It is a collecting kind, so an
// evaluation grants every role whose rule fires, and deploygate turns the
// granted roles into the deploy policy's actor.roles: deployer and
// release_manager carry over, admin counts as both, and reader and auditor
// grant no deploy role.
//
// The package shows the two ways a collecting kind says no. The host
// declares [Admin] and [ReleaseManager] exclusive with [policy.WithExclusive],
// so an evaluation that grants both fails with a [policy.ConflictError]. The
// platform's access.guardrails, which every access policy has to invoke,
// asserts on the outcome that no one is both auditor and deployer, and an
// evaluation that breaks it fails with a [policy.AssertionError]. A host
// ranges over the outcome and switches on each entry's [policy.Entry.Value],
// which is the grant's payload as its role's own struct.
//
// Like the deploy kind, the Go types are the source of truth and `sigilc
// export AccessGrant` writes policies/access_grant.sigil from them.
package access

import (
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// Input is what an access policy can read: who asks, for which team's
// services, and in which environment.
type Input struct {
	Actor Actor `policy:"actor" json:"actor"`
	// Team is the team whose services the actor wants access to.
	Team string `policy:"team" json:"team"`
	// Environment is where, such as production or staging.
	Environment string `policy:"environment" json:"environment"`
}

// Actor is the requestor as the identity provider describes them: the groups
// they belong to and the clearance level they hold.
type Actor struct {
	// Name identifies the actor. The platform's guardrails assert that it
	// isn't empty, since every grant is recorded against it.
	Name string `policy:"name" json:"name"`
	// Groups are the identity provider's groups, such as a team's name, its
	// on-call group, platform, break-glass or compliance.
	Groups []string `policy:"groups" json:"groups"`
	// Clearance is the actor's clearance level. access.main grants the admin
	// role to an actor cleared as admin.
	Clearance string `policy:"clearance" json:"clearance"`
}

// GrantData is the payload of a role that expires: how long the grant lasts.
type GrantData struct {
	// TTL is eight hours when the granting rule doesn't set it.
	TTL time.Duration `policy:"ttl,default=8h" json:"ttl"`
}

// The payloads of the two roles that last as long as a [GrantData]. Each role
// has its own type, so a type switch on a grant tells them apart.
type (
	// DeployerData is the deployer role's payload.
	DeployerData GrantData
	// ReleaseManagerData is the release manager role's payload.
	ReleaseManagerData GrantData
)

// AdminData is the payload of the admin role, which expires sooner.
type AdminData struct {
	// TTL is one hour when the granting rule doesn't set it.
	TTL time.Duration `policy:"ttl,default=1h" json:"ttl"`
}

// The payloads of the roles that carry only a reason.
type (
	// ReaderData is the reader role's payload.
	ReaderData struct{}
	// AuditorData is the auditor role's payload.
	AuditorData struct{}
)

// The roles, declared with their reasons. Reader and Auditor carry only a
// reason; the others carry a time to live.
var (
	Reader         = policy.NewDecision[ReaderData]("reader", "team_member", "everyone_in_staging")
	Deployer       = policy.NewDecision[DeployerData]("deployer", "team_member", "oncall")
	ReleaseManager = policy.NewDecision[ReleaseManagerData]("release_manager", "platform_member")
	Admin          = policy.NewDecision[AdminData]("admin", "clearance", "break_glass")
	Auditor        = policy.NewDecision[AuditorData]("auditor", "compliance_member")
)

// Kind is the AccessGrant contract, version 1. WithCollect makes every fired
// role part of the outcome, in this declaration order. Admin already implies
// release management, so a policy that grants both contradicts itself: the
// host declares the pair exclusive, and such an evaluation fails with a
// conflict instead of handing out two grants.
//
// The kind has no host functions yet, and recovers their panics anyway, so
// the first one added fails closed like the deploy kind's do. See
// [policy.WithRecoverHostPanics].
var Kind = policy.NewKind[Input]("AccessGrant",
	policy.WithVersion(1),
	policy.WithCollect(Reader, Deployer, ReleaseManager, Admin, Auditor),
	policy.WithExclusive(Admin, ReleaseManager),
	policy.WithRecoverHostPanics(),
)
