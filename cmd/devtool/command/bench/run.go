package bench

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/resultdir"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// outputs are the files bench run writes to --results besides the summary
// and metadata.
var outputs = []string{"base.txt", "head.txt", "benchstat.txt", "benchstat.csv"}

// metadata records what a run measured, next to its results.
type metadata struct {
	Head      string   `json:"head"`
	Base      string   `json:"base,omitempty"`
	Dirty     bool     `json:"dirty"`
	Go        string   `json:"go"`
	Filter    string   `json:"filter,omitempty"`
	Time      string   `json:"time"`
	CPU       int      `json:"cpu"`
	Samples   int      `json:"samples"`
	Packages  []string `json:"packages"`
	Workloads []string `json:"workloads"`
}

func newRunCommand(o options) *cobra.Command {
	ro := defaultRunOptions()

	cmd := &cobra.Command{
		Use:   "run [PACKAGE...]",
		Short: "Measure the benchmarks, or compare them with a base revision",
		Long: `Builds the packages with benchmarks (every one by default, or those the
PACKAGE patterns select) and samples each benchmark --count times.

Without --baseline, it prints each benchmark's median time, bytes and
allocations per operation. With --baseline, the base revision is exported to a
temporary directory and the current *_bench_test.go files and shared fixtures
are copied onto it, so both revisions run identical workloads. Samples
alternate between base-first and head-first. It then lists the significant
changes and fails when one is an increase above 10% in time, bytes or
allocations per operation.

On a terminal, each round of samples gets a status line with the time left,
and a line once it's done; --verbose prints go test's output instead. The raw
samples, benchstat's report, a Markdown summary and the run's metadata go to
--results.`,
		Example: `# The medians of every benchmark
devtool bench run

# Did an uncommitted change make the evaluator slower or allocate more?
devtool bench run --baseline HEAD ./internal/eval

# Compare the branch with the commit it forked from main
devtool bench run --baseline "$(git merge-base HEAD main)"

# A few benchmarks, sampled for longer
devtool bench run --filter PolicyEval --time 1s ./pkg/policy`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), pretty.New(cmd.OutOrStdout()), o, ro, args)
		},
	}

	ro.Register(cmd, names)
	f := cmd.Flags()
	f.StringVarP(&ro.baseline, "baseline", "b", "", "Revision to compare with, e.g. main or HEAD")
	f.BoolVar(&ro.noBaseline, "no-baseline", false, "Only measure the checkout, even when BENCH_BASELINE is set")
	f.IntVar(&ro.count, "count", ro.count, "Samples of every benchmark, per revision")
	f.StringVar(&ro.benchstat, "benchstat", ro.benchstat, "Path to the benchstat binary the comparison runs")
	gotool.BindEnv(cmd, envPrefix, o.getenv)
	return cmd
}

func run(ctx context.Context, p *pretty.Printer, o options, ro runOptions, patterns []string) humane.Error {
	if ro.noBaseline {
		ro.baseline = ""
	}
	if err := validate(ro); err != nil {
		return err
	}
	root, err := moduleRoot(ctx, o.root)
	if err != nil {
		return err
	}
	res, err := resultdir.Reset(root, ro.Results, outputs...)
	if err != nil {
		return err
	}
	tmp, terr := os.MkdirTemp("", "sigil-bench-")
	if terr != nil {
		return humane.Wrap(terr, "can't create a temporary directory", "check that TMPDIR is writable")
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	r := newRunner(p, o, ro, tmp, res)
	err = r.run(ctx, root, patterns)
	_ = r.clear()
	if err != nil && ctx.Err() != nil {
		// Whatever failed was killed by the interrupt; that's the story.
		return ui.Interrupted(p, r.finishedRounds(), ro.count, "round", "rounds")
	}
	return err
}

func validate(ro runOptions) humane.Error {
	if ro.count < 1 || ro.CPU < 1 {
		return humane.New("--count and --cpu must be positive", "pass at least one sample and one CPU")
	}
	if ro.baseline != "" && ro.count < minSamples {
		return humane.New(fmt.Sprintf("a comparison needs at least %d samples per revision", minSamples),
			"raise --count, or pass --no-baseline to only measure the checkout")
	}
	// go test would only reject it after every package is built.
	_, err := gotool.CompileFilter(ro.Filter)
	return err
}

// plan is what the header shows before the run starts.
func plan(ro runOptions, meta metadata, res resultdir.Dir) ui.Plan {
	pl := ui.Plan{
		Title:    "Benchmarks",
		Checkout: gotool.Checkout{Head: meta.Head, Dirty: meta.Dirty, Go: meta.Go},
		Packages: meta.Packages,
		Filter:   ro.Filter,
		Runs:     fmt.Sprintf("%d %s of %s each", meta.Samples, ui.Plural(meta.Samples, "sample", "samples"), meta.Time),
		CPU:      meta.CPU,
		Results:  res.Display(),
	}
	if meta.Base != "" {
		pl.Title = "Benchmark comparison"
		pl.Base = gotool.ShortSHA(meta.Base) + " (" + ro.baseline + ")"
		pl.Runs += " per revision"
	}
	return pl
}

// benchFilter is what go test's -test.bench gets: the filter, or every
// benchmark when there's none.
func benchFilter(filter string) string {
	if strings.TrimSpace(filter) == "" {
		return "."
	}
	return filter
}
