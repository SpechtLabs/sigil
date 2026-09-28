// Package bench implements `devtool bench`: running the Go benchmarks,
// alone or against a base revision with a regression gate, and listing
// them.
package bench

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/cmdflag"
)

const (
	// minSamples is the fewest samples per revision a comparison accepts;
	// fewer can't reach significance at alpha 0.01.
	minSamples = 10

	// The two sides of a comparison; each writes its samples to <side>.txt.
	sideBase = "base"
	sideHead = "head"

	// envPrefix starts the environment variable each flag can be set with.
	envPrefix = "BENCH"
)

// names describes the bench commands' flags.
var names = cmdflag.Names{
	Things: "benchmarks",
	Unit:   "sample",
	CPU:    "CPUs for every sample, as GOMAXPROCS",
}

// NewCommand returns the bench command with its subcommands.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Run and compare the Go benchmarks",
		Long: `Every Benchmark function in a *_bench_test.go file is a benchmark. bench run
measures them, alone or against a base revision, and bench list shows what it
would run.

Every flag can also be set with an environment variable: BENCH_ followed by
the flag's name in capitals, e.g. BENCH_TIME=1s for --time 1s.`,
	}
	cmd.AddCommand(newRunCommand(*o), newListCommand(*o))
	return cmd
}
