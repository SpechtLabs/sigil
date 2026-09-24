// Package test implements the `sigil test` command.
package test

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/placeholder"
)

// NewCommand returns the test command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:   "test [PATH...]",
		Short: "Run policy test cases",
		Long: `Runs test cases, each an input JSON document plus the expected decision and
reason. Asserting on the reason as well as the decision catches a deploy that is
denied for the wrong reason, a common way policy regressions hide.

With no paths, test runs every test case under the current directory. Like
eval, it needs an implementation of every function the kind declares.`,
		Example: `# Run every test case under the current directory
sigil test --kind deploy_approval.sigil

# Run only the test cases for the production policies
sigil test --kind deploy_approval.sigil deploy/

# Run the test cases whose name matches a regular expression
sigil test --kind deploy_approval.sigil --run 'freeze'`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file the policies are written against (required)")
	cmd.Flags().String("run", "", "Only run test cases whose name matches this regular expression")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.RegisterFlagCompletionFunc("run", cobra.NoFileCompletions)

	return cmd
}
