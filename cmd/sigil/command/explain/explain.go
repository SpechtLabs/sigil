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
		Use:        "explain POLICY_FILE",
		SuggestFor: []string{"flatten", "expand", "show"},
		Short:      "Flatten a policy into the guarded decisions it can produce",
		Long: `Flattens a policy into one list of guarded decisions. Every policy invocation
is inlined, the when blocks around it are pushed down into each of the invoked
rules' conditions, and params show as the values they are bound to. The result
answers what a policy actually does without reading every file it invokes.

With --input, explain also evaluates the policy and marks which rules fired and
which candidate won. Like eval, that needs an implementation of every function
the kind declares; without --input, explain needs only the kind file.`,
		Example: `# List every decision a team policy can produce, and under which conditions
sigil explain --kind deploy_approval.sigil payments/production.sigil

# Mark the rules that fire for one input, and the winner
sigil explain --kind deploy_approval.sigil --input release.json payments/production.sigil`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: complete.SigilFilesUpTo(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file the policy is written against (required)")
	cmd.Flags().StringP("input", "i", "", `Input document (JSON) to mark firing rules for, or "-" for stdin`)
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.MarkFlagFilename("input", "json")

	return cmd
}
