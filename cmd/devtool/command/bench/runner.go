package bench

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/resultdir"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// maxBuilds is how many test binaries build at once. go test -c already
// compiles a package's dependencies in parallel; a few builds side by side
// keep the machine busy through the links, which run one at a time.
const maxBuilds = 4

// tree is one revision's source tree and the packages built on it: every
// package for the checkout, and those the base revision has for the base.
type tree struct {
	side     string
	dir      string
	packages []string
}

// buildJob is one test binary to build.
type buildJob struct {
	t      tree
	pkg    string
	binary string
}

// builds collects the results of the builds running side by side.
type builds struct {
	mu     sync.Mutex
	r      *runner
	cancel context.CancelFunc
	total  int
	done   int
	// err is the first failure to build the checkout; it stops the others.
	err humane.Error
	// base holds the base revision's packages that failed to build, and
	// why. They get another chance with the base revision's own fixtures.
	base map[string]humane.Error
}

// runner builds the packages on every tree and samples them, a round of
// samples per step.
type runner struct {
	o        options
	ro       runOptions
	p        *pretty.Printer
	line     *ui.Line
	steps    *ui.Steps
	env      []string
	tmp      string
	root     string
	res      resultdir.Dir
	names    naming
	meta     metadata
	base     base
	trees    []tree
	packages []string
	binaries map[[2]string]string
	// dirs holds the directory a package's benchmarks run in on a side, by
	// side and package, where it isn't the package in the side's tree.
	dirs map[[2]string]string
}

func newRunner(p *pretty.Printer, o options, ro runOptions, tmp string, res resultdir.Dir) *runner {
	return &runner{
		o:     o,
		ro:    ro,
		p:     p,
		line:  ui.NewLine(p, 0),
		steps: ui.NewSteps(p, ro.count, ro.Verbose),
		tmp:   tmp,
		res:   res,
		env:   append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOMAXPROCS="+strconv.Itoa(ro.CPU)),
	}
}

func (r *runner) run(ctx context.Context, root string, patterns []string) humane.Error {
	if err := r.prepare(ctx, root, patterns); err != nil {
		return err
	}
	if err := r.build(ctx); err != nil {
		return err
	}
	if err := r.measure(ctx); err != nil {
		return err
	}
	return r.finish(ctx)
}

// prepare finds the workloads, exports the base revision when there is one,
// records the run's metadata and prints its plan.
func (r *runner) prepare(ctx context.Context, root string, patterns []string) humane.Error {
	files, packages, err := workloads(ctx, root, patterns, r.ro.Filter)
	if err != nil {
		return err
	}
	checkout, err := gotool.Describe(ctx, root)
	if err != nil {
		return err
	}
	module, err := gotool.Output(ctx, root, nil, "go", "list", "-m")
	if err != nil {
		return err
	}
	r.names = naming{module: strings.TrimSpace(module), cpu: r.ro.CPU}
	r.root = root
	r.packages = packages
	r.trees = []tree{{side: sideHead, dir: root, packages: packages}}
	meta := metadata{
		Head: checkout.Head, Dirty: checkout.Dirty, Go: checkout.Go, Filter: r.ro.Filter,
		Time: r.ro.Time, CPU: r.ro.CPU, Samples: r.ro.count, Packages: packages, Workloads: files,
	}

	if r.ro.baseline != "" {
		b, err := prepareBase(ctx, root, r.tmp, r.ro.baseline, r.o.fixtures, files)
		if err != nil {
			return err
		}
		meta.Base = b.sha
		r.base = b
		var onBase []string
		for _, pkg := range packages {
			if b.has(pkg) {
				onBase = append(onBase, pkg)
			}
		}
		r.trees = []tree{{side: sideBase, dir: b.dir, packages: onBase}, r.trees[0]}
	}
	r.meta = meta
	if err := r.res.WriteJSON(resultdir.Metadata, meta); err != nil {
		return err
	}
	return ui.Header(r.p, plan(r.ro, meta, r.res))
}

// build compiles every package's test binary on every tree before any
// sample runs, so compiler processes never compete with measurements.
func (r *runner) build(ctx context.Context) humane.Error {
	jobs := make([]buildJob, 0, len(r.trees)*len(r.packages))
	for _, t := range r.trees {
		for _, pkg := range t.packages {
			jobs = append(jobs, buildJob{t: t, pkg: pkg, binary: r.binary(t.side, pkg)})
		}
	}

	start := time.Now()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.binaries = map[[2]string]string{}
	r.dirs = map[[2]string]string{}
	b := &builds{r: r, cancel: cancel, total: len(jobs), base: map[string]humane.Error{}}
	slots := make(chan struct{}, min(maxBuilds, runtime.NumCPU()))
	buildStatus(r, 0, len(jobs), "")

	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			if ctx.Err() == nil {
				_, err := gotool.Output(ctx, j.t.dir, r.env, "go", "test", "-c", "-buildvcs=false", "-o", j.binary, j.pkg)
				b.finish(j, err)
			}
		})
	}
	wg.Wait()
	_ = r.line.Clear()
	if b.err != nil {
		return b.err
	}
	built := len(jobs) - len(b.base)
	if err := r.p.Ok(fmt.Sprintf("Built %d test %s in %s", built, ui.Plural(built, "binary", "binaries"), ui.Duration(time.Since(start)))); err != nil {
		return err
	}
	return r.checkBase(ctx, b.base)
}

// checkBase runs every base binary once, with a single iteration of each
// benchmark, and gives each package that failed to build or run with the
// checkout's fixtures a second try with the base revision's own. That's
// what a change to the fixtures the base revision can't build or parse
// needs, such as fixtures in a syntax it doesn't know yet. The comparison
// then runs the same workloads, each written for its revision.
func (r *runner) checkBase(ctx context.Context, failed map[string]humane.Error) humane.Error {
	if r.ro.baseline == "" {
		return nil
	}
	for _, pkg := range r.trees[0].packages {
		if _, ok := failed[pkg]; !ok {
			if err := r.smoke(ctx, pkg); err != nil {
				failed[pkg] = err
			}
		}
	}
	if len(failed) == 0 {
		return nil
	}
	retry := slices.Sorted(maps.Keys(failed))
	if r.o.fixtures == "" {
		return failed[retry[0]]
	}

	own := filepath.Join(r.tmp, sideBase+"-own")
	if err := r.base.withOwnFixtures(ctx, r.root, own); err != nil {
		return err
	}
	for _, pkg := range retry {
		binary := r.binary(sideBase+"-own", pkg)
		if _, err := gotool.Output(ctx, own, r.env, "go", "test", "-c", "-buildvcs=false", "-o", binary, pkg); err != nil {
			return failedTwice(pkg, r.o.fixtures, failed[pkg], err)
		}
		key := [2]string{sideBase, pkg}
		r.binaries[key], r.dirs[key] = binary, filepath.Join(own, pkg)
		if err := r.smoke(ctx, pkg); err != nil {
			return failedTwice(pkg, r.o.fixtures, failed[pkg], err)
		}
	}
	r.meta.OwnFixtures = retry
	if err := r.res.WriteJSON(resultdir.Metadata, r.meta); err != nil {
		return err
	}
	return r.p.Warning("The base revision runs "+strings.Join(retry, ", ")+" with its own "+r.o.fixtures,
		"the checkout's fixtures don't build or run on it, so each revision measures its own version of the workloads")
}

// smoke runs the base revision's benchmarks of pkg once, one iteration
// each.
func (r *runner) smoke(ctx context.Context, pkg string) humane.Error {
	_, err := gotool.Output(ctx, r.dir(sideBase, pkg), r.env, r.binaries[[2]string{sideBase, pkg}], r.benchArgs("1x")...)
	return err
}

// binary is where the test binary of pkg on side goes.
func (r *runner) binary(side, pkg string) string {
	return filepath.Join(r.tmp, fmt.Sprintf("%s-%d.test", side, slices.Index(r.packages, pkg)))
}

// dir is the directory the benchmarks of pkg run in on side.
func (r *runner) dir(side, pkg string) string {
	if d, ok := r.dirs[[2]string{side, pkg}]; ok {
		return d
	}
	for _, t := range r.trees {
		if t.side == side {
			return filepath.Join(t.dir, pkg)
		}
	}
	return pkg
}

// benchArgs are the test binary's flags for one sample of benchtime.
func (r *runner) benchArgs(benchtime string) []string {
	return []string{
		"-test.run=^$", "-test.bench=" + benchFilter(r.ro.Filter), "-test.benchmem", "-test.count=1",
		"-test.cpu=" + strconv.Itoa(r.ro.CPU), "-test.benchtime=" + benchtime, "-test.timeout=" + r.ro.Timeout.String(),
	}
}

// measure runs the rounds of samples, one step each.
func (r *runner) measure(ctx context.Context) humane.Error {
	for i := range r.ro.count {
		if err := r.round(ctx, i); err != nil {
			return err
		}
	}
	return nil
}

// round samples every package once on each tree, base first in even
// rounds and head first in odd ones.
func (r *runner) round(ctx context.Context, i int) humane.Error {
	order := slices.Clone(r.trees)
	if i%2 == 1 {
		slices.Reverse(order)
	}
	sides := make([]string, len(order))
	for j, t := range order {
		sides[j] = t.side
	}
	if err := r.steps.Next(strings.Join(sides, " → "), ""); err != nil {
		return err
	}

	start := time.Now()
	units, done := 0, 0
	for _, t := range order {
		units += len(t.packages)
	}
	for _, pkg := range r.packages {
		for _, t := range order {
			if !slices.Contains(t.packages, pkg) {
				continue // a package the base revision doesn't have
			}
			_ = r.steps.Progress(pkg, float64(done)/float64(units))
			if err := r.sample(ctx, t, pkg); err != nil {
				return err
			}
			done++
		}
	}
	return r.steps.Done(ui.Duration(time.Since(start)))
}

// sample runs a package's benchmarks once on one tree and appends the
// results to the tree's samples.
func (r *runner) sample(ctx context.Context, t tree, pkg string) humane.Error {
	out, err := gotool.Output(ctx, r.dir(t.side, pkg), r.env, r.binaries[[2]string{t.side, pkg}], r.benchArgs(r.ro.Time)...)
	if err != nil {
		return err
	}
	if r.ro.Verbose {
		if err := r.p.Print(out); err != nil {
			return err
		}
	}
	return r.res.Append(t.side+".txt", out)
}

// finish checks the head samples and either prints their medians or
// compares them with the base revision's.
func (r *runner) finish(ctx context.Context) humane.Error {
	head, rerr := os.ReadFile(r.res.Path(sideHead + ".txt"))
	if rerr != nil {
		return humane.Wrap(rerr, "can't read the samples just written", "check that --results is writable")
	}
	// A mistyped --filter must not turn the check green with no work done.
	samples, err := parseSamples(string(head))
	if err != nil {
		return err
	}
	if r.ro.baseline != "" {
		return compare(ctx, r.p, r.res, r.ro.benchstat, r.names, r.ro.count)
	}

	n := countBenchmarks(samples)
	title := fmt.Sprintf("Measured %d %s in %d %s", n, ui.Plural(n, "benchmark", "benchmarks"),
		len(r.packages), ui.Plural(len(r.packages), "package", "packages"))
	of := "Medians of " + strconv.Itoa(r.ro.count) + " samples"
	if r.ro.count == 1 {
		of = "One sample each, so expect noise"
	}
	if err := r.res.Write(resultdir.Summary, measureSummary(title, of, samples, r.names)); err != nil {
		return err
	}
	if err := r.p.Print("\n" + measureTable(r.p.Theme(), samples, r.names) + "\n"); err != nil {
		return err
	}
	return r.p.Ok(title, of, "Results in "+r.res.Display())
}

// finishedRounds returns how many rounds of samples are done.
func (r *runner) finishedRounds() int {
	return r.steps.Finished()
}

// clear erases any status line, e.g. before an error is printed.
func (r *runner) clear() humane.Error {
	if err := r.line.Clear(); err != nil {
		return err
	}
	return r.steps.Clear()
}

// countBenchmarks returns how many distinct benchmarks samples measured.
func countBenchmarks(samples map[sampleKey][]float64) int {
	seen := map[[2]string]bool{}
	for k := range samples {
		seen[[2]string{k.pkg, k.name}] = true
	}
	return len(seen)
}

// finish records a build's result and updates the status line.
func (b *builds) finish(j buildJob, err humane.Error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case err != nil && j.t.side == sideBase:
		b.base[j.pkg] = err
		return
	case err != nil:
		if b.err == nil {
			b.err = err
			b.cancel()
		}
		return
	}
	b.r.binaries[[2]string{j.t.side, j.pkg}] = j.binary
	b.done++
	buildStatus(b.r, b.done, b.total, j.pkg+" ("+j.t.side+")")
}

// buildStatus redraws the build's status line with done of total binaries
// built, last among them.
func buildStatus(r *runner, done, total int, last string) {
	if r.ro.Verbose {
		return
	}
	th := r.p.Theme()
	_ = r.line.Set(ui.Marker(th) + " " + th.Bold(fmt.Sprintf("Building %d/%d test binaries", done, total)) + "  " + th.Muted(last))
}

// failedTwice is the error for a base package that neither the checkout's
// fixtures nor the base revision's own let build and run.
func failedTwice(pkg, fixtures string, first, second humane.Error) humane.Error {
	return humane.Wrap(second, "the base revision can't run the benchmarks of "+pkg,
		"with the checkout's "+fixtures+": "+first.Display(),
		"make the workloads build on both revisions, or move what depends on the checkout's API into a *"+setupSuffix+" file")
}
