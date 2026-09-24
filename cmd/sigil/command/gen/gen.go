// Package gen implements the `sigil gen` command group.
package gen

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/command/gen/golang"
)

// NewCommand returns the gen command with every generator attached.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:     "gen",
		Aliases: []string{"generate"},
		Short:   "Generate code from a kind file",
		Long: `Generates code from a kind file, so other services can consume decisions
with typed values instead of loading the kind at run time. Each target language
is a subcommand.`,
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
		golang.NewCommand(),
	)

	return cmd
}
