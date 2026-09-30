// Package scenario implements `demo-cli scenario`, whose list subcommand,
// also what it runs on its own, lists the built-in route and webhook
// scenarios with their team and what each shows.
package scenario

import (
	"bytes"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/output"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/scenario"
)

// NewCommand lists the scenarios without contacting the service, on its
// own and as `scenario list`.
func NewCommand() *cobra.Command {
	cmd := newList()
	cmd.Use = "scenario"
	cmd.Aliases = []string{"scenarios"}
	cmd.AddCommand(newList())
	return cmd
}

func newList() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List the built-in route and webhook scenarios", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			scenarios, herr := scenario.List()
			if herr != nil {
				return herr
			}
			var out bytes.Buffer
			w := tabwriter.NewWriter(&out, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "COMMAND\tSCENARIO\tTEAM\tDESCRIPTION")
			for _, s := range scenarios {
				team := s.Team
				if team == "" {
					team = "-"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Kind, s.Name, team, s.Description)
			}
			_ = w.Flush()
			return output.Write(cmd.OutOrStdout(), out.Bytes())
		},
	}
}
