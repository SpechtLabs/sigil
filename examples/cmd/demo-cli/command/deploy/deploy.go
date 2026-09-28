// Package deploy implements the demo-cli deploy command.
package deploy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/output"
	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/scenario"
)

// NewCommand returns the deploy command.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	var file, team string
	var explain bool
	names := scenario.Names(scenario.Deploy)
	cmd := &cobra.Command{
		Use: "deploy [scenario]", Short: "Request a deployment decision",
		Long:              fmt.Sprintf("Run a built-in deploy scenario, or submit your own JSON with --file.\nScenarios: %s. Default: owner.", strings.Join(names, ", ")),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: cobra.FixedCompletions(names, cobra.ShellCompDirectiveNoFileComp),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, targetTeam, herr := scenario.Read(cmd.InOrStdin(), scenario.Deploy, "owner", args, file, team)
			if herr != nil {
				return herr
			}
			path := "/api/v1/teams/" + url.PathEscape(targetTeam) + "/deployments"
			status, data, herr := o.client.Do(cmd.Context(), http.MethodPost, path, body)
			if herr != nil {
				return herr
			}
			return output.Print(cmd.OutOrStdout(), status, data, output.Deploy, *o.json, explain)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "read a request from a JSON file, or - for stdin")
	cmd.Flags().BoolVar(&explain, "explain", false, "include candidates, conditions and source locations")
	cmd.Flags().StringVar(&team, "team", "", "target team (required with --file; otherwise the scenario's team)")
	_ = cmd.MarkFlagFilename("file", "json")
	return cmd
}
