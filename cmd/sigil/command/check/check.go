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
		Use:        "check POLICY_FILE...",
		SuggestFor: []string{"validate", "verify", "lint"},
		Short:      "Check policies against a kind and report their cost",
		Long: `Parses and type-checks policies against a kind, resolves use statements,
detects let and use cycles, and reports the estimated worst-case cost of each
policy.

check needs only the kind file, not implementations of the host functions it
declares, so it is the command a policy repository runs in CI.`,
		Example: `# Type-check one policy
sigil check --kind deploy_approval.sigil deploy/production.sigil

# Check every policy in a directory, as CI would
sigil check --kind deploy_approval.sigil deploy/`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file to check the policies against (required)")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagFilename("kind", "sigil")

	return cmd
}
