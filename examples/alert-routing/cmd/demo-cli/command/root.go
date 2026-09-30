// Package command implements the demo-cli root command. Each subcommand
// lives in its own package and receives dependencies through With* options,
// the same layout the sigil CLI uses.
//
// [NewCommand] builds the tree and owns the global flags: --url and --timeout
// configure the one API client every subcommand shares, and --json is a flag
// the subcommands read through a pointer once cobra has parsed it.
// [Execute] runs the tree and turns the outcome into the exit status.
package command

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/command/metric"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/command/policy"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/command/route"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/command/scenario"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/command/status"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/command/team"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/command/version"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/command/webhook"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/client"
)

// NewCommand returns the demo CLI with every subcommand attached. The
// version is dev unless [WithVersion] sets it.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{version: "dev"}
	for _, opt := range opts {
		opt(o)
	}
	api := client.New()
	jsonOutput := false
	cmd := &cobra.Command{
		Use: "demo-cli", Short: "Route alerts through the example alerting platform",
		Long:         "Ask alertrouter how to route an alert or a whole Alertmanager batch, inspect the teams and policies, and reload them.\nNamed scenarios send the sample requests the test suites and the load test check alertrouter against.",
		SilenceUsage: true, SilenceErrors: true,
		Example: "  demo-cli route checkout-critical\n  demo-cli route checkout-sustained --explain\n  demo-cli webhook webhook-mixed\n  demo-cli policy reload",
	}
	cmd.PersistentFlags().StringVar(&api.URL, "url", api.URL, "alertrouter URL (env ALERTROUTER_URL)")
	cmd.PersistentFlags().DurationVar(&api.Timeout, "timeout", api.Timeout, "HTTP request timeout")
	cmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "print the full JSON response")
	cmd.AddCommand(
		route.NewCommand(route.WithClient(api), route.WithJSON(&jsonOutput)),
		webhook.NewCommand(webhook.WithClient(api), webhook.WithJSON(&jsonOutput)),
		team.NewCommand(team.WithClient(api), team.WithJSON(&jsonOutput)),
		policy.NewCommand(policy.WithClient(api), policy.WithJSON(&jsonOutput)),
		status.NewCommand(status.WithClient(api), status.WithJSON(&jsonOutput)),
		metric.NewCommand(metric.WithClient(api), metric.WithJSON(&jsonOutput)),
		scenario.NewCommand(),
		version.NewCommand(version.WithVersion(o.version)),
	)
	return cmd
}
