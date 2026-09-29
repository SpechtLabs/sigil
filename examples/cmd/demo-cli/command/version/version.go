// Package version implements `demo-cli version`, which prints the version
// the binary was built with, dev for a local build.
package version

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/output"
)

// NewCommand returns the version command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{version: "dev"}
	for _, opt := range opts {
		opt(o)
	}
	return &cobra.Command{
		Use: "version", Short: "Print the CLI version", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return output.Write(cmd.OutOrStdout(), []byte("demo-cli "+o.version+"\n"))
		},
	}
}
