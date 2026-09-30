// Package scenario implements `demo-cli scenarios`, which lists the
// built-in deploy and access scenarios with their team and what each shows.
package scenario

import (
	"bytes"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/deploy-gates/cmd/demo-cli/internal/output"
	"github.com/spechtlabs/sigil/examples/deploy-gates/cmd/demo-cli/internal/scenario"
)

// NewCommand lists the scenarios without contacting the service.
func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use: "scenarios", Short: "List the built-in deployment and access scenarios", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var out bytes.Buffer
			w := tabwriter.NewWriter(&out, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "COMMAND\tSCENARIO\tTEAM\tDESCRIPTION")
			for _, s := range scenario.List() {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Command, s.Name, s.Team, s.Description)
			}
			_ = w.Flush()
			return output.Write(cmd.OutOrStdout(), out.Bytes())
		},
	}
}
