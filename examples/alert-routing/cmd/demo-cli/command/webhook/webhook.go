// Package webhook implements `demo-cli webhook`, which delivers a batch of
// alerts the way Alertmanager does. It posts a built-in scenario's webhook,
// or the JSON read with --file, to /api/v1/alerts and prints what became of
// every alert.
package webhook

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/output"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/scenario"
)

// defaultScenario is what `demo-cli webhook` sends without an argument.
const defaultScenario = "webhook-mixed"

// NewCommand returns the webhook command. Without an argument it runs the
// webhook-mixed scenario.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	var file string
	names := scenario.Names(scenario.Webhook)
	cmd := &cobra.Command{
		Use: "webhook [scenario]", Short: "Deliver a batch of alerts as Alertmanager does",
		Long:              fmt.Sprintf("Run a built-in webhook scenario, or submit your own Alertmanager webhook with --file.\nScenarios: %s. Default: %s.", strings.Join(names, ", "), defaultScenario),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: cobra.FixedCompletions(names, cobra.ShellCompDirectiveNoFileComp),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, _, herr := scenario.Read(cmd.InOrStdin(), scenario.Webhook, defaultScenario, args, file, "")
			if herr != nil {
				return herr
			}
			status, data, herr := o.client.Do(cmd.Context(), http.MethodPost, "/api/v1/alerts", body)
			if herr != nil {
				return herr
			}
			return output.Print(cmd.OutOrStdout(), status, data, output.Webhook, *o.json, false)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "read a webhook from a JSON file, or - for stdin")
	_ = cmd.MarkFlagFilename("file", "json")
	return cmd
}
