package build_test

import (
	"fmt"

	"github.com/spechtlabs/sigil/pkg/build"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// Mistakes in the Go code come back from Source, every one of them, with
// the file and line of the builder call.
func ExampleErrors() {
	type Input struct {
		Environment string `policy:"environment"`
		Region      string // no policy tag
	}
	deny := policy.NewDecision[policy.None]("deny", "no_rule_matched")
	kind := policy.NewKind[Input]("DeployApproval",
		policy.WithVersion(1),
		policy.WithDecisions(deny),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)
	m := build.Module("deploy.region", kind, func(m *build.ModuleDoc[Input], in *Input) {
		build.Pub(m, "eu", build.Field(&in.Region).Like("eu-*"))
		build.Pub(m, "eu", build.Field(&in.Environment).Eq(build.Lit("eu")))
	})
	_, err := m.Source()
	fmt.Println(err)
	// Output:
	// example_errors_test.go:24: build.Field: field Input.Region has no `policy` tag, so policies can't read it; tag it, or read a tagged field
	// example_errors_test.go:25: build.Pub: eu is already declared at example_errors_test.go:24; every name in a document means one thing
}
