package bench

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
		Short: "List the benchmarks bench run would run",
		Example: `# Every benchmark, grouped by package
devtool bench list

# The packages with benchmarks as a JSON array
devtool bench list --packages -o json`,
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
	filter, err := gotool.CompileFilter(lo.Filter)
	if err != nil {
		return err
	}
	root, err := moduleRoot(ctx, o.root)
	if err != nil {
		return err
	}
	all, err := gotool.Discover(ctx, root, patterns, "Benchmark")
	if err != nil {
		return err
	}
	benchmarks, err := gotool.Select(all, filter, patterns, names.Things)
	if err != nil {
		return err
	}
	return ui.List(p, lo.Format, benchmarks, lo.Packages, "benchmark", "benchmarks")
}

func moduleRoot(ctx context.Context, root string) (string, humane.Error) {
	if root != "" {
		return root, nil
	}
	return gotool.ModuleRoot(ctx, ".")
}
