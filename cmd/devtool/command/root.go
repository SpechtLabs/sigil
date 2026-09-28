// Package command implements the devtool root command: the repository's
// own benchmark, fuzzing and CI tooling. Every subcommand lives in its own
// sub-package and exposes a NewCommand constructor.
package command

import (
	"context"
	"io"
	"os"
	"strings"
	"syscall"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/devtool/command/bench"
	"github.com/spechtlabs/sigil/cmd/devtool/command/fuzz"
	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
)

// NewCommand returns the devtool root command with every subcommand attached.
func NewCommand() *cobra.Command {
	colorMode := output.ColorAuto

	cmd := &cobra.Command{
		Use:   "devtool",
		Short: "Benchmark, fuzz and CI tooling for developing sigil",
		Long: `devtool runs the checks go test alone doesn't: benchmark comparisons against
a base revision, and fuzz campaigns that run every target one at a time. The
mise tasks and the CI workflows call it. It isn't released; run it with
go run ./cmd/devtool from the repository.`,
		Example: `# Compare the benchmarks with main
go run ./cmd/devtool bench --baseline main

# Fuzz the parser's targets for a minute each
go run ./cmd/devtool fuzz run --fuzztime 1m ./internal/parser`,
		// No Args validator: cobra then reports an unknown subcommand and
		// suggests the closest one.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Execute reads --color before the parse; the flag is declared so it's
	// documented, completed and accepted.
	cmd.PersistentFlags().Var(&colorMode, "color", "When to color the output: "+strings.Join(output.Colors, ", "))
	// This only fails if the flag is missing or already has a completion
	// func, both of which are programming errors caught by the tests.
	_ = cmd.RegisterFlagCompletionFunc("color", cobra.FixedCompletions(output.Colors, cobra.ShellCompDirectiveNoFileComp))

	cmd.AddCommand(bench.NewCommand(), fuzz.NewCommand())
	return cmd
}

// Execute runs cmd on args, os.Args without the program name when nil,
// with sigil's styling for help, usage and errors, and returns the
// process's exit status: 1 when the command failed, after printing why,
// and 0 otherwise.
func Execute(ctx context.Context, cmd *cobra.Command, args []string) int {
	if args == nil {
		args = os.Args[1:]
	}
	cmd.SetArgs(args)
	output.ColorFromArgs(args).Apply()

	err := fang.Execute(ctx, cmd,
		fang.WithColorSchemeFunc(pretty.ColorScheme),
		fang.WithErrorHandler(func(w io.Writer, styles fang.Styles, err error) {
			pretty.ErrorHandler(w, styles, usage.Humanize(cmd, args, err))
		}),
		fang.WithoutVersion(),
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	)
	if err != nil {
		return 1
	}
	return 0
}
