// Package eval implements the `sigil eval` command.
package eval

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/placeholder"
)

// NewCommand returns the eval command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "eval POLICY_FILE",
		Aliases:    []string{"evaluate"},
		SuggestFor: []string{"run", "exec"},
		Short:      "Evaluate a policy against a JSON input and show the trace",
		Long: `Evaluates a policy against a JSON input and prints the decision with its full
trace: every candidate, the winner, and which of the winner's conditions held.

Evaluation calls host functions, so eval needs an implementation of every
function the kind declares. A host can build its own sigil binary with its
functions linked in.`,
		Example: `# Evaluate a policy and print the decision with its trace
sigil eval --kind deploy_approval.sigil --input release.json payments/production.sigil

# Read the input from stdin
cat release.json | sigil eval --kind deploy_approval.sigil --input - payments/production.sigil`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: complete.SigilFilesUpTo(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file the policy is written against (required)")
	cmd.Flags().StringP("input", "i", "", `Input document (JSON) to evaluate the policy against, or "-" for stdin (required)`)
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("input")
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.MarkFlagFilename("input", "json")

	return cmd
}
