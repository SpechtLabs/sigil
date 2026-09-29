// Package status implements `demo-cli status`, which asks deploygate's
// /readyz whether both policy bundles are loaded.
package status

import (
	"net/http"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/output"
)

// NewCommand returns the status command. A service that isn't ready
// answers 503, and the command exits 1.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &cobra.Command{
		Use: "status", Short: "Check whether deploygate is ready", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, data, herr := o.client.Do(cmd.Context(), http.MethodGet, "/readyz", nil)
			if herr != nil {
				return herr
			}
			return output.Print(cmd.OutOrStdout(), status, data, output.Status, *o.json, false)
		},
	}
}
