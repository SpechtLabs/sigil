// Package list implements `demo-cli policies list`, which prints every
// kind deploygate serves, with its version, source, load time and policies,
// from GET /api/v1/policies.
package list

import (
	"net/http"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/output"
)

// NewCommand returns the list command.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &cobra.Command{
		Use: "list", Short: "List the loaded policy bundles", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, data, herr := o.client.Do(cmd.Context(), http.MethodGet, "/api/v1/policies", nil)
			if herr != nil {
				return herr
			}
			return output.Print(cmd.OutOrStdout(), status, data, output.Policies, *o.json, false)
		},
	}
}
