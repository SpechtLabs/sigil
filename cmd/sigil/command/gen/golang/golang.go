// Package golang implements the `sigil gen go` command. It isn't named go
// because that is a keyword.
package golang

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/placeholder"
)

// NewCommand returns the gen go command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:     "go KIND_FILE",
		Aliases: []string{"golang"},
		Short:   "Generate typed Go code from a kind file",
		Long: `Generates typed Go code from a kind file: a struct for every input type and
decision payload, so a Go service can consume decisions with typed values
instead of loading the kind dynamically.

The code is printed unless --out names a file to write it to.`,
		Example: `# Print the generated code
sigil gen go deploy_approval.sigil

# Write it into the approval package
sigil gen go --package approval --out approval/kind.go deploy_approval.sigil`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: complete.SigilFilesUpTo(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}

	cmd.Flags().StringP("package", "p", "", "Go package name of the generated code (defaults to the kind's name)")
	cmd.Flags().String("out", "", "File to write the generated code to (defaults to stdout)")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.RegisterFlagCompletionFunc("package", cobra.NoFileCompletions)
	_ = cmd.MarkFlagFilename("out", "go")

	return cmd
}
