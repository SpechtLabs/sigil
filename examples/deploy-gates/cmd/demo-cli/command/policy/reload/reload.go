// Package reload implements `demo-cli policies reload`, which makes
// deploygate reload both policy bundles now through POST
// /api/v1/policies/reload and prints what serves afterwards, or why a
// bundle didn't load.
package reload

import (
	"net/http"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/deploy-gates/cmd/demo-cli/internal/output"
)

// NewCommand returns the reload command.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &cobra.Command{
		Use: "reload", Short: "Reload both policy bundles", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, data, herr := o.client.Do(cmd.Context(), http.MethodPost, "/api/v1/policies/reload", nil)
			if herr != nil {
				return herr
			}
			return output.Print(cmd.OutOrStdout(), status, data, output.Policies, *o.json, false)
		},
	}
}
