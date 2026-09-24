// Package format implements the `sigil fmt` command. It isn't named fmt so it
// doesn't shadow the standard library package.
package format

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/placeholder"
)

// NewCommand returns the fmt command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:     "fmt [PATH...]",
		Aliases: []string{"format"},
		Short:   "Rewrite policy and kind files in the canonical style",
		Long: `Rewrites Sigil policy and kind files into the one canonical style, like gofmt.
It needs nothing but the files themselves.

Sigil's grammar is whitespace-insensitive, so styles drift between teams unless
one tool owns the layout. Directories are formatted recursively; with no paths,
fmt formats the current directory. The result is printed unless --write updates
the files in place. In CI, --check fails when any file isn't formatted.`,
		Example: `# Print the formatted version of a policy
sigil fmt deploy/production.sigil

# Format every .sigil file in the repository in place
sigil fmt --write .

# Fail when a file isn't formatted, e.g. in CI
sigil fmt --check .`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}

	cmd.Flags().BoolP("write", "w", false, "Write the result back to the files instead of printing it")
	cmd.Flags().Bool("check", false, "Only report files that aren't formatted, and fail if there are any")
	cmd.MarkFlagsMutuallyExclusive("write", "check")

	return cmd
}
