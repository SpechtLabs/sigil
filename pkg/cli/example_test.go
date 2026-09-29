package cli_test

import (
	"github.com/spechtlabs/sigil/pkg/cli"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// A host binary with two kinds linked in. It is the main function of a
// command such as cmd/sigil in the host repository.
func ExampleMain() {
	type DeployInput struct {
		Service string `policy:"service"`
	}
	type AccessInput struct {
		User string `policy:"user"`
	}

	deny := policy.NewDecision[policy.None]("deny", "no_rule_matched")
	approve := policy.NewDecision[policy.None]("approve", "owner")
	deploy := policy.NewKind[DeployInput]("DeployApproval",
		policy.WithVersion(1),
		policy.WithDecisions(deny, approve),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)

	read := policy.NewDecision[policy.None]("read", "member")
	access := policy.NewKind[AccessInput]("AccessGrant",
		policy.WithVersion(1),
		policy.WithCollect(read),
	)

	// With two kinds, commands pick one with --kind deploy_approval.sigil,
	// and `sigil export AccessGrant` writes one kind file.
	cli.Main(
		cli.WithKind(deploy),
		cli.WithKind(access),
		cli.WithVersion("v1.2.3"),
	)
}
