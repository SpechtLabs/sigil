// Package policy implements `demo-cli policies`, the parent of the list and
// reload subcommands. Run on its own, it lists the policies like `policies
// list`.
package policy

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/deploy-gates/cmd/demo-cli/command/policy/list"
	"github.com/spechtlabs/sigil/examples/deploy-gates/cmd/demo-cli/command/policy/reload"
)

// NewCommand lists policies by default and exposes list and reload subcommands.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	cmd := list.NewCommand(list.WithClient(o.client), list.WithJSON(o.json))
	cmd.Use = "policies"
	cmd.AddCommand(
		list.NewCommand(list.WithClient(o.client), list.WithJSON(o.json)),
		reload.NewCommand(reload.WithClient(o.client), reload.WithJSON(o.json)),
	)
	return cmd
}
