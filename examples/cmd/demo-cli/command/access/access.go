// Package access implements the demo-cli access command.
package access

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/output"
	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/scenario"
)

// NewCommand returns the access command.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	var file string
	var explain bool
	names := scenario.Names(scenario.Access)
	cmd := &cobra.Command{
		Use: "access [scenario]", Short: "Check the roles an actor may hold",
		Long:              fmt.Sprintf("Run a built-in access scenario, or submit your own JSON with --file.\nScenarios: %s. Default: member.", strings.Join(names, ", ")),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: cobra.FixedCompletions(names, cobra.ShellCompDirectiveNoFileComp),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, _, herr := scenario.Read(cmd.InOrStdin(), scenario.Access, "member", args, file, "")
			if herr != nil {
				return herr
			}
			path := "/api/v1/access/grants"
			status, data, herr := o.client.Do(cmd.Context(), http.MethodPost, path, body)
			if herr != nil {
				return herr
			}
			return output.Print(cmd.OutOrStdout(), status, data, output.Access, *o.json, explain)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "read a request from a JSON file, or - for stdin")
	cmd.Flags().BoolVar(&explain, "explain", false, "include candidates, conditions and source locations")

	_ = cmd.MarkFlagFilename("file", "json")
	return cmd
}
