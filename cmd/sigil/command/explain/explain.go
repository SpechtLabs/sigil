// Package explain implements the `sigil explain` command.
package explain

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/placeholder"
)

// NewCommand returns the explain command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "explain PATH...",
		SuggestFor: []string{"flatten", "expand", "show"},
		Short:      "Flatten a policy into the guarded decisions it can produce",
		Long: `Flattens a policy into one list of guarded decisions. Every policy invocation
is inlined, the when blocks around it are pushed down into each of the invoked
rules' conditions, and params show as the values they are bound to. The result
answers what a policy actually does without reading every document it invokes.

Every PATH is a file, a directory, or "-" for stdin, and a file may hold several
documents. All documents from all paths form one bundle, indexed by the names in
their headers. A directory contributes the .sigil files directly inside it, or
every one below it with --recursive. --policy names the policy to explain;
without it, explain explains every policy in the bundle, one after another.

With --input, explain also evaluates the policy and marks which rules fired and
which candidate won. Like eval, that needs an implementation of every function
the kind declares, and a single root policy; without --input, explain needs only
the kind file.`,
		Example: `# List every decision a team policy can produce, and under which conditions
sigil explain --kind deploy_approval.sigil --policy payments.production deploy/ payments/

# Review every policy in a ConfigMap's bundle at once
sigil explain --kind deploy_approval.sigil policies.sigil

# Mark the rules that fire for one input, and the winner
sigil explain --kind deploy_approval.sigil --input release.json --policy payments.production policies.sigil`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file the policy is written against (required)")
	cmd.Flags().StringP("input", "i", "", `Input document (JSON) to mark firing rules for, or "-" for stdin`)
	cmd.Flags().StringP("policy", "p", "", "Name of the policy to explain; every policy in the bundle when omitted")
	cmd.Flags().BoolP("recursive", "R", false, "Read .sigil files in subdirectories of directory arguments too")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.MarkFlagFilename("input", "json")
	_ = cmd.RegisterFlagCompletionFunc("policy", cobra.NoFileCompletions)

	return cmd
}
