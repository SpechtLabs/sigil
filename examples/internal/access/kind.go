// Package access defines the AccessGrant kind: the roles a requestor holds in
// an environment for one team's services. It is a collecting kind, so an
// evaluation grants every role whose rule fires, and deploygate feeds the
// granted roles into the deploy policy as the actor's roles.
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
	Actor       Actor  `policy:"actor" json:"actor"`
	Team        string `policy:"team" json:"team"`
	Environment string `policy:"environment" json:"environment"`
}

// Actor is the requestor as the identity provider describes them: the groups
// they belong to and the clearance level they hold.
type Actor struct {
	Name      string   `policy:"name" json:"name"`
	Groups    []string `policy:"groups" json:"groups"`
	Clearance string   `policy:"clearance" json:"clearance"`
}

// GrantData is the payload of a role that expires: how long the grant lasts.
type GrantData struct {
	TTL time.Duration `policy:"ttl,default=8h" json:"ttl"`
}

// AdminData is the payload of the admin role, which expires sooner.
type AdminData struct {
	TTL time.Duration `policy:"ttl,default=1h" json:"ttl"`
}

// The roles, declared with their reasons. Reader and Auditor carry only a
// reason; the others carry a time to live.
var (
	Reader         = policy.NewDecision[policy.None]("reader", "team_member", "everyone_in_staging")
	Deployer       = policy.NewDecision[GrantData]("deployer", "team_member", "oncall")
	ReleaseManager = policy.NewDecision[GrantData]("release_manager", "platform_member")
	Admin          = policy.NewDecision[AdminData]("admin", "clearance", "break_glass")
	Auditor        = policy.NewDecision[policy.None]("auditor", "compliance_member")
)

// Kind is the AccessGrant contract, version 1. WithCollect makes every fired
// role part of the outcome, in this declaration order. Admin already implies
// release management, so a policy that grants both contradicts itself: the
// host declares the pair exclusive, and such an evaluation fails with a
// conflict instead of handing out two grants.
var Kind = policy.NewKind[Input]("AccessGrant",
	policy.WithVersion(1),
	policy.WithCollect(Reader, Deployer, ReleaseManager, Admin, Auditor),
	policy.WithExclusive(Admin, ReleaseManager),
)
