// Package command implements the demo-cli root command. Each subcommand
// lives in its own package and receives dependencies through With* options.
package command

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/command/access"
	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/command/deploy"
	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/command/metric"
	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/command/policy"
	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/command/scenario"
	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/command/status"
	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/command/version"
	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/client"
)

// NewCommand returns the demo CLI with every subcommand attached.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{version: "dev"}
	for _, opt := range opts {
		opt(o)
	}
	api := client.New()
	jsonOutput := false
	cmd := &cobra.Command{
		Use: "demo-cli", Short: "Work with the example deployment platform",
		Long:         "Ask deploygate for deployment decisions and access grants, inspect policies, and reload them.\nNamed scenarios supply the identity and release metadata a platform would normally look up.",
		SilenceUsage: true, SilenceErrors: true,
		Example: "  demo-cli deploy owner\n  demo-cli deploy sre --explain\n  demo-cli access member\n  demo-cli policies reload",
	}
	cmd.PersistentFlags().StringVar(&api.URL, "url", api.URL, "deploygate URL (env DEPLOYGATE_URL)")
	cmd.PersistentFlags().DurationVar(&api.Timeout, "timeout", api.Timeout, "HTTP request timeout")
	cmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "print the full JSON response")
	cmd.AddCommand(
		deploy.NewCommand(deploy.WithClient(api), deploy.WithJSON(&jsonOutput)),
		access.NewCommand(access.WithClient(api), access.WithJSON(&jsonOutput)),
		policy.NewCommand(policy.WithClient(api), policy.WithJSON(&jsonOutput)),
		status.NewCommand(status.WithClient(api), status.WithJSON(&jsonOutput)),
		metric.NewCommand(metric.WithClient(api), metric.WithJSON(&jsonOutput)),
		scenario.NewCommand(),
		version.NewCommand(version.WithVersion(o.version)),
	)
	return cmd
}
