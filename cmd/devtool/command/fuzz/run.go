package fuzz

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/cmdflag"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/resultdir"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// logFile is where fuzz run writes go test's output, besides the summary
// and metadata.
const logFile = "fuzz.log"

// metadata records what a run fuzzed, next to its results.
type metadata struct {
	Head     string   `json:"head"`
	Dirty    bool     `json:"dirty"`
	Go       string   `json:"go"`
	Filter   string   `json:"filter,omitempty"`
	Time     string   `json:"time"`
	CPU      int      `json:"cpu"`
	Packages []string `json:"packages"`
	Targets  []string `json:"targets"`
}

// restoreOptions are fuzz run's own flags, besides the ones every run
// command takes.
type restoreOptions struct {
	skip   bool   // --no-restore
	remote string // --remote
}

func newRunCommand(o options) *cobra.Command {
	ro := defaultRunOptions()
	rs := restoreOptions{remote: defaultRemote}

	cmd := &cobra.Command{
		Use:   "run [PACKAGE...]",
		Short: "Fuzz every target, one at a time",
		Long: `Fuzzes each fuzz target in the packages (every one by default, or those the
PACKAGE patterns select) with go test -fuzz for --time, one after the other,
and stops at the first failure.

Before it starts, it copies the targets' inputs from the remote's fuzz-corpus
branch into go test's cache, so fuzzing picks up where earlier runs left off;
--no-restore skips that. fuzz corpus push shares what the run found.

On a terminal, each target gets a status line with go test's progress and the
time left, and a line once it's done; --verbose prints go test's output
instead. When a target finds a failing input, go test saves it under the
package's testdata/fuzz directory, and the error says how to replay it. go
test's output, a Markdown summary and the run's metadata go to --results.`,
		Example: `# Every fuzz target for ten seconds each
devtool fuzz run

# The parser's fuzz targets for a minute each
devtool fuzz run --time 1m ./internal/parser

# One fuzz target, until you stop it with Ctrl-C
devtool fuzz run --filter FuzzParseExpr --time 24h ./internal/parser`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), pretty.New(cmd.OutOrStdout()), cmd.OutOrStdout(), o, ro, rs, args)
		},
	}
	ro.Register(cmd, names)
	cmd.Flags().BoolVar(&rs.skip, "no-restore", false, "Don't restore the inputs on the fuzz-corpus branch before fuzzing")
	cmd.Flags().StringVar(&rs.remote, "remote", rs.remote, "Remote whose fuzz-corpus branch to restore the inputs from")
	gotool.BindEnv(cmd, envPrefix, o.getenv)
	return cmd
}

func run(ctx context.Context, p *pretty.Printer, stdout io.Writer, o options, ro cmdflag.Run, rs restoreOptions, patterns []string) humane.Error {
	root, targets, err := discover(ctx, o, patterns, ro.Filter)
	if err != nil {
		return err
	}
	res, err := resultdir.Reset(root, ro.Results, logFile)
	if err != nil {
		return err
	}
	checkout, err := gotool.Describe(ctx, root)
	if err != nil {
		return err
	}
	meta := metadata{
		Head: checkout.Head, Dirty: checkout.Dirty, Go: checkout.Go, Filter: ro.Filter,
		Time: ro.Time, CPU: ro.CPU, Packages: gotool.Packages(targets),
	}
	for _, t := range targets {
		meta.Targets = append(meta.Targets, t.Dir+" "+t.Name)
	}
	if err = res.WriteJSON(resultdir.Metadata, meta); err != nil {
		return err
	}
	pl := plan(ro, meta, targets, checkout, res)
	pl.Corpus = "not restored, --no-restore"
	if !rs.skip {
		pl.Corpus = restore(ctx, p, o, root, rs.remote, targets)
	}
	if err = ui.Header(p, pl); err != nil {
		return err
	}

	f := newFuzzer(p, stdout, root, ro, targets, res)
	err = f.all(ctx)
	_ = f.steps.Clear()
	if err != nil && ctx.Err() != nil {
		// go test stopped on the interrupt; that's the story.
		return ui.Interrupted(p, f.steps.Finished(), len(targets), "fuzz target", "fuzz targets")
	}
	return err
}

// plan is what the header shows before the run starts.
func plan(ro cmdflag.Run, meta metadata, targets []gotool.Target, checkout gotool.Checkout, res resultdir.Dir) ui.Plan {
	pl := ui.Plan{
		Title:    "Fuzzing",
		Checkout: checkout,
		Packages: meta.Packages,
		Filter:   ro.Filter,
		Runs:     fmt.Sprintf("%d %s for %s each", len(targets), ui.Plural(len(targets), "fuzz target", "fuzz targets"), ro.Time),
		CPU:      ro.CPU,
		Results:  res.Display(),
	}
	if d, err := time.ParseDuration(ro.Time); err == nil && len(targets) > 1 {
		pl.Estimate = "about " + ui.Duration(time.Duration(len(targets))*d) + ", plus building each package"
	}
	return pl
}
