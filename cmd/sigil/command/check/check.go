// Package check implements the `sigil check` command.
package check

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/placeholder"
)

// NewCommand returns the check command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "check PATH...",
		SuggestFor: []string{"validate", "verify", "lint"},
		Short:      "Check policies against a kind and report their cost",
		Long: `Parses and type-checks policies and modules against a kind, resolves imports
and policy invocations, detects let, import and invocation cycles, and reports
the estimated worst-case cost of each policy.

Every PATH is a file, a directory, or "-" for stdin, and a file may hold several
documents. All documents from all paths are checked together as one bundle,
indexed by the names in their headers, so a name defined twice is an error. A
directory contributes the .sigil files directly inside it, or every one below
it with --recursive.

--require names a policy that every root policy must invoke unconditionally,
the same check a host makes with policy.Require. Repeat it to require several.
The roots are the policies named with --policy, or, without it, every policy in
the bundle that no other policy invokes.

check needs only the kind file, not implementations of the host functions it
declares, so it is the command a policy repository runs in CI.`,
		Example: `# Type-check one policy
sigil check --kind deploy_approval.sigil deploy/production.sigil

# Check every document in a policy repository, as CI would
sigil check --kind deploy_approval.sigil --recursive .

# Check the bundle a kustomize ConfigMap is built from
sigil check --kind deploy_approval.sigil deploy/*.sigil payments/*.sigil

# Check that every team policy invokes the guardrails unconditionally
sigil check --kind deploy_approval.sigil --require deploy.guardrails deploy/ payments/`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file to check the policies against (required)")
	cmd.Flags().BoolP("recursive", "R", false, "Read .sigil files in subdirectories of directory arguments too")
	cmd.Flags().StringSliceP("policy", "p", nil, "Root policy for --require checks (repeatable); every policy nothing invokes when omitted")
	cmd.Flags().StringSlice("require", nil, "Policy that every checked policy must invoke unconditionally (repeatable)")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.RegisterFlagCompletionFunc("require", cobra.NoFileCompletions)
	_ = cmd.RegisterFlagCompletionFunc("policy", cobra.NoFileCompletions)

	return cmd
}
