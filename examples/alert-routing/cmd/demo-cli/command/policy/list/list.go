// Package list implements `demo-cli policy list`, which prints the team
// policy bundle alertrouter serves, with its source, fingerprint, load time
// and one root policy per team, from GET /api/v1/policies.
package list

import (
	"net/http"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/output"
)

// NewCommand returns the list command.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &cobra.Command{
		Use: "list", Short: "List the loaded team policies", Args: cobra.NoArgs,
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
