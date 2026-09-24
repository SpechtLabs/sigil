// Package lsp implements the `sigil lsp` command.
package lsp

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/placeholder"
)

// NewCommand returns the lsp command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "lsp",
		SuggestFor: []string{"language-server", "server"},
		Short:      "Run the Sigil language server",
		Long: `Runs the Sigil language server, which editors start in the background. It
reads the kind file and offers completion for inputs, fields, functions and
decision payload keys, hover with a decision's full signature, and
go-to-definition for let bindings and use targets.

The server talks to the editor over stdin and stdout.`,
		Example: `# Start the language server the way an editor would
sigil lsp --stdio`,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}

	// Editors pass --stdio by convention; stdio is the only transport.
	cmd.Flags().Bool("stdio", true, "Talk to the editor over stdin and stdout")

	return cmd
}
