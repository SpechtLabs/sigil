// Package metric implements `demo-cli metrics`, which prints what
// deploygate serves on /metrics, in the Prometheus text format.
package metric

import (
	"net/http"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/deploy-gates/cmd/demo-cli/internal/output"
)

// NewCommand returns the metrics command. It refuses --json, since the
// metrics aren't JSON.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return &cobra.Command{
		Use: "metrics", Short: "Print Prometheus metrics", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *o.json {
				return humane.New("metrics are in Prometheus text format", "omit --json for this command")
			}
			status, data, herr := o.client.Do(cmd.Context(), http.MethodGet, "/metrics", nil)
			if herr != nil {
				return herr
			}
			return output.Print(cmd.OutOrStdout(), status, data, output.Metrics, *o.json, false)
		},
	}
}
