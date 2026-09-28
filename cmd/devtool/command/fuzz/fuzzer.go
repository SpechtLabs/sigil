package fuzz

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/cmdflag"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/resultdir"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// stopGrace is how long go test gets to save its corpus and exit after an
// interrupt before it's killed.
const stopGrace = 10 * time.Second

// summaryColumns are the columns of the summary's table.
var summaryColumns = []ui.Column{
	{Heading: "FUZZ TARGET"}, {Heading: "PACKAGE"},
	{Heading: "EXECS", Right: true}, {Heading: "NEW INPUTS", Right: true}, {Heading: "RESULT"},
}

// fuzzer runs the targets one at a time, a step each.
type fuzzer struct {
	p       *pretty.Printer
	steps   *ui.Steps
	stdout  io.Writer
	root    string
	ro      cmdflag.Run
	targets []gotool.Target
	res     resultdir.Dir
	// fuzzFor is --time as a duration, for each target's progress; zero
	// when it's an iteration count.
	fuzzFor time.Duration
	// rows are the summary's rows, one per target that ran.
	rows [][]ui.Cell
}

func newFuzzer(p *pretty.Printer, stdout io.Writer, root string, ro cmdflag.Run, targets []gotool.Target, res resultdir.Dir) *fuzzer {
	f := &fuzzer{
		p: p, steps: ui.NewSteps(p, len(targets), ro.Verbose), stdout: stdout,
		root: root, ro: ro, targets: targets, res: res,
	}
	f.fuzzFor, _ = time.ParseDuration(ro.Time)
	for _, t := range targets {
		f.steps.Align(t.Name, t.Dir)
	}
	return f
}

// all fuzzes every target and writes the summary, whether they all pass
// or one fails.
func (f *fuzzer) all(ctx context.Context) humane.Error {
	log, err := f.res.Open(logFile)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	var execs, found int64
	for _, t := range f.targets {
		s, err := f.one(ctx, t, log)
		execs += s.execs
		found += s.found
		if err != nil {
			_ = f.summarize("Fuzzing stopped at "+t.Name, err.Error())
			return err
		}
	}

	n := len(f.targets)
	title := fmt.Sprintf("Fuzzed %d %s in %s", n, ui.Plural(n, "target", "targets"), ui.Duration(f.steps.Elapsed()))
	detail := fmt.Sprintf("%s execs, %d new interesting %s", ui.Count(float64(execs)), found, ui.Plural(int(found), "input", "inputs"))
	if err := f.summarize(title, detail); err != nil {
		return err
	}
	return f.p.Ok(title, detail, "Results in "+f.res.Display())
}

// one fuzzes a target, a step of the run.
func (f *fuzzer) one(ctx context.Context, t gotool.Target, log io.Writer) (stats, humane.Error) {
	if err := f.steps.Next(t.Name, t.Dir); err != nil {
		return stats{}, err
	}
	if _, err := fmt.Fprintf(log, "=== %s %s\n", t.Name, t.Dir); err != nil {
		return stats{}, humane.Wrap(err, "can't write "+f.res.Display(logFile), "check that --results is writable")
	}

	pr := &progress{stream: log}
	if f.ro.Verbose {
		pr.stream = io.MultiWriter(log, f.stdout)
	}
	pr.update = func(s stats) { _ = f.steps.Progress(s.String(), f.progress(s)) }

	cmd := exec.CommandContext(ctx, "go", "test", t.Package, "-run", "^$", "-fuzz", "^"+t.Name+"$", //nolint:gosec // go test on a discovered target is the point
		"-fuzztime", f.ro.Time, "-parallel", strconv.Itoa(f.ro.CPU), "-timeout", f.ro.Timeout.String())
	cmd.Dir = f.root
	cmd.Stdout = pr
	cmd.Stderr = pr
	// An interrupt lets go test stop fuzzing cleanly and keep its corpus.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = stopGrace
	err := cmd.Run()

	s := pr.stats
	switch {
	case err == nil:
		f.rows = append(f.rows, summaryRow(t, s, "passed"))
		return s, f.steps.Done(s.summary())
	case ctx.Err() != nil:
		return s, humane.Wrap(err, "interrupted", "run the fuzz target again")
	default:
		return s, f.failed(t, pr, err)
	}
}

// failed finishes a failed target's step, prints what go test said when
// the status line hid it, and returns the error to show under it.
func (f *fuzzer) failed(t gotool.Target, pr *progress, err error) humane.Error {
	result := "failed"
	if pr.input != "" {
		result = "found a failing input"
	}
	f.rows = append(f.rows, summaryRow(t, pr.stats, result))
	if serr := f.steps.Failed(result); serr != nil {
		return serr
	}
	if !f.ro.Verbose {
		if perr := f.p.Print("\n" + withoutProgress(pr.output()) + "\n"); perr != nil {
			return perr
		}
	}

	if pr.input == "" {
		return humane.New(fmt.Sprintf("fuzzing %s in %s failed: %v", t.Name, t.Dir, err),
			"go test's output above says why",
			"rerun it alone with: go test -run '^$' -fuzz '^"+t.Name+"$' "+t.Dir)
	}
	rerun := pr.rerun
	if rerun == "" {
		rerun = "go test -run=" + t.Name + "/" + path.Base(pr.input)
	}
	return humane.New(t.Name+" found a failing input",
		"replay it with: "+rerun+" "+t.Dir,
		"keep "+path.Join(t.Dir, pr.input)+" with the fix, so every go test run replays it")
}

// progress returns how far into --time the target is, or zero when --time
// is an iteration count.
func (f *fuzzer) progress(s stats) float64 {
	elapsed, err := time.ParseDuration(s.elapsed)
	if f.fuzzFor <= 0 || err != nil {
		return 0
	}
	return float64(elapsed) / float64(f.fuzzFor)
}

// summarize writes the Markdown summary CI adds to the job page.
func (f *fuzzer) summarize(title, detail string) humane.Error {
	return f.res.Write(resultdir.Summary, "# Go fuzzing\n\n"+title+". "+detail+".\n\n"+ui.Markdown(summaryColumns, f.rows))
}

func summaryRow(t gotool.Target, s stats, result string) []ui.Cell {
	return []ui.Cell{
		{Text: t.Name}, {Text: t.Dir},
		{Text: ui.Count(float64(s.execs))}, {Text: strconv.FormatInt(s.found, 10)}, {Text: result},
	}
}

// summary describes a finished run: how much it tried and found.
func (s stats) summary() string {
	out := ui.Count(float64(s.execs)) + " execs"
	if d, err := time.ParseDuration(s.elapsed); err == nil && d > 0 {
		out += " (" + ui.Count(float64(s.execs)/d.Seconds()) + "/s)"
	}
	return out + fmt.Sprintf(" · %d new %s", s.found, ui.Plural(int(s.found), "input", "inputs"))
}
