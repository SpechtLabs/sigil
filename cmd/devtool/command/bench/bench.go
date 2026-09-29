// Package bench implements `devtool bench`: running the Go benchmarks,
// alone or against a base revision with a regression gate, and listing
// them.
//
// `bench run` builds every package's test binary on each revision before
// any sample runs, then samples the benchmarks in rounds, alternating
// which revision goes first. With --baseline, the base revision is
// extracted with git archive and the checkout's *_bench_test.go files and
// shared fixtures are copied onto it, so both revisions run identical
// workloads. benchstat then compares the samples, and a significant
// increase above 10% in time, bytes or allocations per operation fails the
// run. `bench list` shows what `bench run` would measure.
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
