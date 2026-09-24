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
		Use:        "eval PATH...",
		Aliases:    []string{"evaluate"},
		SuggestFor: []string{"run", "exec"},
		Short:      "Evaluate a policy against a JSON input and show the trace",
		Long: `Evaluates a policy against a JSON input and prints the decision with its full
trace: every candidate, the winner, and which of the winner's conditions held.

Every PATH is a file, a directory, or "-" for stdin, and a file may hold several
documents. All documents from all paths form one bundle, indexed by the names in
their headers. A directory contributes the .sigil files directly inside it, or
every one below it with --recursive. If the bundle holds exactly one policy,
that policy is evaluated; otherwise --policy names the one to evaluate.

Evaluation calls host functions, so eval needs an implementation of every
function the kind declares. A host can build its own sigil binary with its
functions linked in.`,
		Example: `# Evaluate the only policy in a file and print the decision with its trace
sigil eval --kind deploy_approval.sigil --input release.json gate.sigil

# Pick the root policy from the documents in two directories
sigil eval --kind deploy_approval.sigil --input release.json --policy payments.production deploy/ payments/

# Read the bundle from stdin, for example a rendered ConfigMap key
kustomize build . | yq '.data["policies.sigil"]' | sigil eval --kind deploy_approval.sigil --input release.json --policy payments.production -`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file the policy is written against (required)")
	cmd.Flags().StringP("input", "i", "", `Input document (JSON) to evaluate the policy against, or "-" for stdin (required)`)
	cmd.Flags().StringP("policy", "p", "", "Name of the policy to evaluate; required when the bundle holds more than one")
	cmd.Flags().BoolP("recursive", "R", false, "Read .sigil files in subdirectories of directory arguments too")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("input")
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.MarkFlagFilename("input", "json")
	_ = cmd.RegisterFlagCompletionFunc("policy", cobra.NoFileCompletions)

	return cmd
}
