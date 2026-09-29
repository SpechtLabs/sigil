// Package lsp implements the `sigil lsp` command, which will run the Sigil
// language server over stdin and stdout. It isn't implemented yet.
// [NewCommand] registers the command with its help and flags, and running
// it reports a not-implemented error.
package lsp

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/placeholder"
)

// NewCommand returns the lsp command.
func NewCommand(opts ...Option) *cobra.Command {
	text := output.Text
	o := &options{output: &text}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "lsp",
		SuggestFor: []string{"language-server", "server"},
		Short:      "Run the Sigil language server (planned)",
		Long: `Planned: this command is not implemented yet, and exits with an error.

Runs the Sigil language server, which editors start in the background. It
reads the kind file and offers completion for inputs, fields, functions and
decision payload keys, hover with a decision's full signature, and
go-to-definition for let bindings and use targets.

The server talks to the editor over stdin and stdout.`,
		Example: `# Start the language server the way an editor would
sigil lsp --stdio`,
		Args:              usage.None(),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd, *o.output)
		},
	}

	// Editors pass --stdio by convention; stdio is the only transport.
	cmd.Flags().Bool("stdio", true, "Talk to the editor over stdin and stdout")

	return cmd
}
