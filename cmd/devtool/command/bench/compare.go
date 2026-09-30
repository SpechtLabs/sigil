package bench

import (
	"context"
	"encoding/csv"
	"fmt"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/resultdir"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

const (
	// threshold is the relative increase above which a significant change
	// fails the comparison.
	threshold = 0.10

	// The units benchmarks report in. go test says ns/op for time, and
	// benchstat converts it to sec/op.
	unitNs     = "ns/op"
	unitSec    = "sec/op"
	unitBytes  = "B/op"
	unitAllocs = "allocs/op"

	// The samples of the benchmarks both revisions measured, which benchstat
	// compares, when one revision measured benchmarks the other didn't.
	comparedBase = "base.compared.txt"
	comparedHead = "head.compared.txt"
)

// units are the measurements every benchmark must report, as go test names
// them; benchstat reports ns/op as sec/op.
var (
	units    = []string{unitNs, unitBytes, unitAllocs}
	csvUnits = []string{unitSec, unitBytes, unitAllocs}
)

// sampleKey identifies one metric of one benchmark.
type sampleKey struct {
	pkg, name, unit string
}

// change is benchstat's comparison of one metric of one benchmark.
type change struct {
	pkg, name, unit string
	old, current    float64
	// p is benchstat's significance column, e.g. "p=0.000 n=10".
	p           string
	significant bool
}

// compare runs benchstat on the base and head samples, writes its reports
// and the Markdown summary to the results, prints the significant changes
// and the verdict, and fails on a confirmed regression.
//
// A benchmark only one revision measured, such as one the checkout adds,
// has nothing to compare with. It's listed as not compared and kept away
// from benchstat, so it can't fail the gate.
func compare(ctx context.Context, p *pretty.Printer, res resultdir.Dir, benchstat string, n naming, count int) humane.Error {
	before, after, err := loadSamples(res, count)
	if err != nil {
		return err
	}
	both, added, removed := match(before, after, n)
	changes, report, err := compareBoth(ctx, res, benchstat, before, after, both, len(added)+len(removed) > 0)
	if err != nil {
		return err
	}
	failures := regressions(changes)
	if err := res.Write(resultdir.Summary, summarize(changes, failures, report, n, added, removed)); err != nil {
		return err
	}
	return verdict(p, changes, len(failures), res, n, added, removed)
}

// match returns the benchmarks both revisions measured, and names the
// ones only the checkout measured, added, and only the base, removed.
func match(before, after string, n naming) (both map[[2]string]bool, added, removed []string) {
	old, current := benchmarks(before), benchmarks(after)
	both = map[[2]string]bool{}
	for _, b := range sortedBenchmarks(current) {
		if old[b] {
			both[b] = true
		} else {
			added = append(added, n.pkg(b[0])+" "+n.bench(b[1]))
		}
	}
	for _, b := range sortedBenchmarks(old) {
		if !current[b] {
			removed = append(removed, n.pkg(b[0])+" "+n.bench(b[1]))
		}
	}
	return both, added, removed
}

// compareBoth runs benchstat on the benchmarks both revisions measured,
// from the raw samples, or from copies with only those benchmarks when
// uneven says the revisions measured different ones, and returns the
// changes and benchstat's report. With nothing in common, there's nothing
// to run.
func compareBoth(ctx context.Context, res resultdir.Dir, benchstat, before, after string, both map[[2]string]bool, uneven bool) ([]change, string, humane.Error) {
	if len(both) == 0 {
		return nil, "", nil
	}
	baseFile, headFile := sideBase+".txt", sideHead+".txt"
	if uneven {
		baseFile, headFile = comparedBase, comparedHead
		if err := res.Write(baseFile, onlyBenchmarks(before, both)); err != nil {
			return nil, "", err
		}
		if err := res.Write(headFile, onlyBenchmarks(after, both)); err != nil {
			return nil, "", err
		}
	}
	report, table, err := runBenchstat(ctx, res, benchstat, baseFile, headFile)
	if err != nil {
		return nil, "", err
	}
	changes, err := parseChanges(table)
	return changes, report, err
}

// loadSamples reads both revisions' samples and checks that they're
// complete.
func loadSamples(res resultdir.Dir, count int) (before, after string, err humane.Error) {
	if count < minSamples {
		return "", "", humane.New(fmt.Sprintf("a comparison needs at least %d samples per revision", minSamples), "raise --count")
	}
	b, rerr := os.ReadFile(res.Path(sideBase + ".txt"))
	if rerr != nil {
		return "", "", humane.Wrap(rerr, "can't read the base samples", "run the comparison again")
	}
	a, rerr := os.ReadFile(res.Path(sideHead + ".txt"))
	if rerr != nil {
		return "", "", humane.Wrap(rerr, "can't read the head samples", "run the comparison again")
	}
	if err := validateSamples(string(b), string(a), count); err != nil {
		return "", "", err
	}
	return string(b), string(a), nil
}

// runBenchstat compares baseFile with headFile, labeled base and head,
// writes benchstat's text report and CSV next to the samples and returns
// both.
func runBenchstat(ctx context.Context, res resultdir.Dir, benchstat, baseFile, headFile string) (report, table string, err humane.Error) {
	dir := res.Path("")
	flags := []string{"-alpha", "0.01", "-filter", ".unit:(ns/op OR B/op OR allocs/op)"}
	files := []string{sideBase + "=" + baseFile, sideHead + "=" + headFile}
	if report, err = gotool.Output(ctx, dir, nil, benchstat, append(flags, files...)...); err != nil {
		return "", "", err
	}
	if table, err = gotool.Output(ctx, dir, nil, benchstat, append(append(flags, "-format", "csv"), files...)...); err != nil {
		return "", "", err
	}
	if err = res.Write("benchstat.txt", report); err != nil {
		return "", "", err
	}
	if err = res.Write("benchstat.csv", table); err != nil {
		return "", "", err
	}
	return report, table, nil
}

// verdict prints the significant changes and, when none is a regression,
// that the gate passed. A regression fails the command instead, with the
// table above showing it first. Benchmarks only one revision measured are
// named as not compared.
func verdict(p *pretty.Printer, changes []change, failures int, res resultdir.Dir, n naming, added, removed []string) humane.Error {
	significant := len(significantChanges(changes))
	if significant > 0 {
		th := p.Theme()
		if err := p.Print("\n" + changesTable(th, changes, n) + "\n"); err != nil {
			return err
		}
	}

	changed := fmt.Sprintf("%d of %d %s changed significantly", significant, len(changes), ui.Plural(len(changes), "comparison", "comparisons"))
	if failures == 0 {
		details := append([]string{changed}, notCompared(added, removed)...)
		return p.Ok("No confirmed regressions above 10%", append(details, "Results in "+res.Display())...)
	}
	return humane.New(fmt.Sprintf("%d benchmark %s above 10%%", failures, ui.Plural(failures, "regression", "regressions")),
		"the table above lists them first; "+changed,
		"timing can mislead on a busy machine; rerun a surprising failure on an idle one",
		"benchstat's full report is in "+res.Display("benchstat.txt"))
}

// parseSamples reads go test -bench output into its measurements. Every
// benchmark must report time, bytes and allocations; other metrics such as
// MB/s are ignored.
func parseSamples(text string) (map[sampleKey][]float64, humane.Error) {
	result := map[sampleKey][]float64{}
	var pkg string
	for line := range strings.Lines(text) {
		line = strings.TrimRight(line, "\n")
		if p, ok := strings.CutPrefix(line, "pkg: "); ok {
			pkg = p
		}
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		fields := strings.Fields(line)
		if pkg == "" || len(fields) < 4 || !positiveInt(fields[1]) {
			return nil, humane.New("malformed benchmark result: "+line, "check that the benchmark ran with -benchmem")
		}

		seen := map[string]bool{}
		for i := 2; i+1 < len(fields); i += 2 {
			value, err := strconv.ParseFloat(fields[i], 64)
			if err != nil || math.IsInf(value, 0) || math.IsNaN(value) || value < 0 {
				return nil, humane.New("invalid measurement: "+line, "check the benchmark's reported metrics")
			}
			if unit := fields[i+1]; slices.Contains(units, unit) {
				seen[unit] = true
				key := sampleKey{pkg: pkg, name: fields[0], unit: unit}
				result[key] = append(result[key], value)
			}
		}
		if len(seen) != len(units) {
			return nil, humane.New("benchmark must report time, bytes and allocations: "+line, "run it with -benchmem")
		}
	}
	if len(result) == 0 {
		return nil, humane.New("no benchmark measurements found", "check that --bench matches at least one benchmark")
	}
	return result, nil
}

// validateSamples checks that both revisions' samples parse, with count
// samples of every benchmark and metric. The revisions may measure
// different benchmarks; see compare.
func validateSamples(before, after string, count int) humane.Error {
	// The base revision measures nothing when every benchmark is new.
	old := map[sampleKey][]float64{}
	if strings.Contains(before, "\nBenchmark") || strings.HasPrefix(before, "Benchmark") {
		var err humane.Error
		if old, err = parseSamples(before); err != nil {
			return err
		}
	}
	current, err := parseSamples(after)
	if err != nil {
		return err
	}
	for side, data := range map[string]map[sampleKey][]float64{"base": old, "head": current} {
		for key, values := range data {
			if len(values) != count {
				return humane.New(fmt.Sprintf("%s: %s %s %s has %d samples, expected %d",
					side, key.pkg, key.name, key.unit, len(values), count), "run the comparison again")
			}
		}
	}
	return nil
}

// parseChanges reads benchstat's CSV into one change per benchmark and
// metric. benchstat marks insignificant changes with "~".
func parseChanges(table string) ([]change, humane.Error) {
	r := csv.NewReader(strings.NewReader(table))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, humane.Wrap(err, "can't read benchstat's CSV", "check the benchstat version pinned in .mise.toml")
	}

	var changes []change
	var pkg, unit string
	for _, row := range rows {
		if len(row) == 1 {
			if p, ok := strings.CutPrefix(row[0], "pkg: "); ok {
				pkg = p
			}
			continue
		}
		if len(row) == 7 && row[0] == "" && slices.Contains(csvUnits, row[1]) {
			if !slices.Equal(row[2:], []string{"CI", row[1], "CI", "vs base", "P"}) {
				return nil, unexpectedCSV("header", row)
			}
			unit = row[1]
			continue
		}
		if len(row) == 0 || row[0] == "" || row[0] == "geomean" {
			continue
		}
		if unit == "" || pkg == "" || len(row) != 7 {
			return nil, unexpectedCSV("row", row)
		}

		old, errOld := strconv.ParseFloat(row[1], 64)
		current, errNew := strconv.ParseFloat(row[3], 64)
		if errOld != nil || errNew != nil || !finite(old) || !finite(current) {
			return nil, humane.New("benchstat reported a non-finite or negative median: "+strings.Join(row, ","),
				"check the raw samples in base.txt and head.txt")
		}
		changes = append(changes, change{
			pkg: pkg, name: row[0], unit: unit, old: old, current: current, p: row[6],
			significant: row[5] != "~" && strings.HasPrefix(row[6], "p="),
		})
	}
	if len(changes) == 0 {
		return nil, humane.New("benchstat produced no comparisons", "check that base.txt and head.txt hold samples")
	}
	return changes, nil
}

// regressions returns every confirmed regression, one line each.
func regressions(changes []change) []string {
	var findings []string
	for _, c := range changes {
		if c.regressed() {
			findings = append(findings, fmt.Sprintf("%s: %s %s %s (%s)", c.pkg, c.name, c.unit, c.delta(), c.p))
		}
	}
	return findings
}

// regressed reports whether the change is a significant increase above the
// threshold. It's computed from the unrounded medians, not from the rounded
// percentage benchstat prints.
func (c change) regressed() bool {
	return c.significant && c.current > c.old*(1+threshold)
}

// rank orders changes for display: 0 for a regression, 1 for another
// increase and 2 for a decrease.
func (c change) rank() int {
	switch {
	case c.regressed():
		return 0
	case c.current > c.old:
		return 1
	default:
		return 2
	}
}

// delta is the relative change of the median, e.g. "+12.50%".
func (c change) delta() string {
	switch {
	case c.old != 0:
		return fmt.Sprintf("%+.2f%%", (c.current/c.old-1)*100)
	case c.current == 0:
		return "+0.00%"
	default:
		return "0 to nonzero"
	}
}

func unexpectedCSV(what string, row []string) humane.Error {
	return humane.New("unexpected benchstat CSV "+what+": "+strings.Join(row, ","),
		"check the benchstat version pinned in .mise.toml")
}

func finite(v float64) bool {
	return !math.IsInf(v, 0) && !math.IsNaN(v) && v >= 0
}

func positiveInt(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0
}

// benchmarks returns the benchmarks samples measured, as package and name.
// The samples have been validated.
func benchmarks(samples string) map[[2]string]bool {
	parsed, _ := parseSamples(samples)
	out := map[[2]string]bool{}
	for k := range parsed {
		out[[2]string{k.pkg, k.name}] = true
	}
	return out
}

// sortedBenchmarks returns the benchmarks of set in package and name order.
func sortedBenchmarks(set map[[2]string]bool) [][2]string {
	return slices.SortedFunc(maps.Keys(set), func(a, b [2]string) int {
		return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1])
	})
}

// onlyBenchmarks returns samples without the results of the benchmarks
// keep doesn't list, and with everything else as it was.
func onlyBenchmarks(samples string, keep map[[2]string]bool) string {
	var b strings.Builder
	var pkg string
	for line := range strings.Lines(samples) {
		if p, ok := strings.CutPrefix(strings.TrimRight(line, "\n"), "pkg: "); ok {
			pkg = p
		}
		if fields := strings.Fields(line); strings.HasPrefix(line, "Benchmark") && len(fields) > 0 && !keep[[2]string{pkg, fields[0]}] {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// notCompared describes the benchmarks only one revision measured, one
// line per side that has any.
func notCompared(added, removed []string) []string {
	var lines []string
	if len(added) > 0 {
		lines = append(lines, fmt.Sprintf("%d new %s, not compared: %s", len(added), ui.Plural(len(added), "benchmark", "benchmarks"), strings.Join(added, ", ")))
	}
	if len(removed) > 0 {
		lines = append(lines, fmt.Sprintf("%d %s only on the base revision, not compared: %s", len(removed), ui.Plural(len(removed), "benchmark", "benchmarks"), strings.Join(removed, ", ")))
	}
	return lines
}
