// Package gen implements the `sigil gen` command group, which generates
// code from a kind file. Each target language is a subcommand in its own
// package; the only one is `sigil gen go`, in package golang. Run on its
// own, gen prints its own help.
package gen

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/command/gen/golang"
)

// NewCommand returns the gen command with every generator attached.
func NewCommand(opts ...Option) *cobra.Command {
	text := output.Text
	o := &options{output: &text}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:     "gen",
		Aliases: []string{"generate"},
		Short:   "Generate code from a kind file",
		Long: `Generates code from a kind file, so other services can consume decisions with
typed values without importing the host that defines the kind. Each target
language is a subcommand.`,
		Example: `# Generate typed Go code for the deploy approval kind
sigil gen go --package approval deploy_approval.sigil`,
		// cobra only reports unknown subcommands for the root, so reject stray
		// arguments here and print help when gen is run on its own.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		golang.NewCommand(golang.WithOutput(o.output)),
	)

	return cmd
}
