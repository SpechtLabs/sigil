// Package team implements `demo-cli teams`, which prints alertrouter's team
// directory, every team with its on-call target and channel, from GET
// /api/v1/teams.
package team

import (
	"net/http"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/output"
)

// NewCommand returns the teams command.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &cobra.Command{
		Use: "teams", Short: "List the team directory", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			status, data, herr := o.client.Do(cmd.Context(), http.MethodGet, "/api/v1/teams", nil)
			if herr != nil {
				return herr
			}
			return output.Print(cmd.OutOrStdout(), status, data, output.Teams, *o.json, false)
		},
	}
}
