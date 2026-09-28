package fuzz

import (
	"context"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/cmdflag"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

func newListCommand(o options) *cobra.Command {
	var lo cmdflag.List

	cmd := &cobra.Command{
		Use:   "list [PACKAGE...]",
		Short: "List the fuzz targets fuzz run would run",
		Example: `# Every fuzz target, grouped by package
devtool fuzz list

# The packages with fuzz targets as a JSON array, e.g. for a CI matrix
devtool fuzz list --packages -o json`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return list(cmd.Context(), pretty.New(cmd.OutOrStdout()), o, lo, args)
		},
	}
	lo.Register(cmd, names)
	gotool.BindEnv(cmd, envPrefix, o.getenv)
	return cmd
}

func list(ctx context.Context, p *pretty.Printer, o options, lo cmdflag.List, patterns []string) humane.Error {
	_, targets, err := discover(ctx, o, patterns, lo.Filter)
	if err != nil {
		return err
	}
	return ui.List(p, lo.Format, targets, lo.Packages, "fuzz target", "fuzz targets")
}

// discover returns the module root and the fuzz targets filter selects in
// the packages patterns select.
func discover(ctx context.Context, o options, patterns []string, filter string) (string, []gotool.Target, humane.Error) {
	re, err := gotool.CompileFilter(filter)
	if err != nil {
		return "", nil, err
	}
	root := o.root
	if root == "" {
		if root, err = gotool.ModuleRoot(ctx, "."); err != nil {
			return "", nil, err
		}
	}
	all, err := gotool.Discover(ctx, root, patterns, "Fuzz")
	if err != nil {
		return "", nil, err
	}
	targets, err := gotool.Select(all, re, patterns, names.Things)
	return root, targets, err
}
