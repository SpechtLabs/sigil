// Package reload implements `demo-cli policy reload`, which makes
// alertrouter reload the team policy bundle now through POST
// /api/v1/policies/reload and prints what serves afterwards, or why the
// bundle didn't load.
package reload

import (
	"net/http"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/output"
)

// NewCommand returns the reload command.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &cobra.Command{
		Use: "reload", Short: "Reload the team policy bundle", Args: cobra.NoArgs,
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
