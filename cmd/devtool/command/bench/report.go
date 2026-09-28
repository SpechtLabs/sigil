package bench

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// naming shortens what go test and benchstat print into what a person
// reads: packages relative to the module, and benchmarks without their
// Benchmark prefix and the -cpu suffix every one of them shares.
type naming struct {
	module string
	cpu    int
}

func (n naming) pkg(path string) string {
	if path == n.module {
		return "."
	}
	return strings.TrimPrefix(path, n.module+"/")
}

func (n naming) bench(name string) string {
	return strings.TrimSuffix(strings.TrimPrefix(name, "Benchmark"), "-"+strconv.Itoa(n.cpu))
}

// metric names a unit the way go test does; benchstat says sec/op for time.
func metric(unit string) string {
	if unit == unitSec || unit == unitNs {
		return "time/op"
	}
	return unit
}

// formatValue formats a median in its unit: 1.23µs, 21.5KiB, 1.8k.
func formatValue(unit string, v float64) string {
	switch unit {
	case unitSec:
		return formatTime(v * 1e9)
	case unitNs:
		return formatTime(v)
	case unitBytes:
		return formatBytes(v)
	default:
		return ui.Count(v)
	}
}

func formatTime(ns float64) string {
	switch {
	case ns >= 1e9:
		return ui.Count(ns/1e9) + "s"
	case ns >= 1e6:
		return ui.Count(ns/1e6) + "ms"
	case ns >= 1e3:
		return ui.Count(ns/1e3) + "µs"
	default:
		return ui.Count(ns) + "ns"
	}
}

func formatBytes(b float64) string {
	switch {
	case b >= 1<<30:
		return ui.Count(b/(1<<30)) + "GiB"
	case b >= 1<<20:
		return ui.Count(b/(1<<20)) + "MiB"
	case b >= 1<<10:
		return ui.Count(b/(1<<10)) + "KiB"
	default:
		return ui.Count(b) + "B"
	}
}

// The columns of the two tables bench run prints.
var (
	measureColumns = []ui.Column{
		{Heading: "PACKAGE"}, {Heading: "BENCHMARK"},
		{Heading: "TIME/OP", Right: true}, {Heading: "B/OP", Right: true}, {Heading: "ALLOCS/OP", Right: true},
	}
	changeColumns = []ui.Column{
		{Heading: "PACKAGE"}, {Heading: "BENCHMARK"}, {Heading: "METRIC"},
		{Heading: "BASE", Right: true}, {Heading: "HEAD", Right: true}, {Heading: "CHANGE", Right: true},
	}
)

// measureTable lays out each benchmark's median time, bytes and
// allocations, sorted by package and name.
func measureTable(th pretty.Theme, samples map[sampleKey][]float64, n naming) string {
	return ui.Table(th, measureColumns, measureRows(th, samples, n))
}

// measureSummary is the Markdown summary of a run without a base revision.
func measureSummary(title, detail string, samples map[sampleKey][]float64, n naming) string {
	return "# Go benchmarks\n\n" + title + ". " + detail + ".\n\n" +
		ui.Markdown(measureColumns, measureRows(pretty.Theme{}, samples, n))
}

func measureRows(th pretty.Theme, samples map[sampleKey][]float64, n naming) [][]ui.Cell {
	type bench struct{ pkg, name string }
	medians := map[bench]map[string]float64{}
	for k, values := range samples {
		b := bench{pkg: k.pkg, name: k.name}
		if medians[b] == nil {
			medians[b] = map[string]float64{}
		}
		medians[b][k.unit] = median(values)
	}
	benches := slices.SortedFunc(maps.Keys(medians), func(a, b bench) int {
		return strings.Compare(a.pkg+"\x00"+a.name, b.pkg+"\x00"+b.name)
	})

	rows := make([][]ui.Cell, 0, len(benches))
	for _, b := range benches {
		m := medians[b]
		rows = append(rows, []ui.Cell{
			{Text: n.pkg(b.pkg), Style: th.Muted},
			{Text: n.bench(b.name)},
			{Text: formatValue(unitNs, m[unitNs])},
			{Text: formatValue(unitBytes, m[unitBytes])},
			{Text: formatValue(unitAllocs, m[unitAllocs])},
		})
	}
	return rows
}

// changesTable lays out the significant changes with both medians:
// regressions first in the failure color, then other increases as
// warnings, then improvements.
func changesTable(th pretty.Theme, changes []change, n naming) string {
	return ui.Table(th, changeColumns, changeRows(th, changes, n, false))
}

// changeRows returns the significant changes' table rows. markdown marks
// the regressions in bold, since a Markdown table has no colors.
func changeRows(th pretty.Theme, changes []change, n naming, markdown bool) [][]ui.Cell {
	shown := significantChanges(changes)
	rows := make([][]ui.Cell, 0, len(shown))
	for _, c := range shown {
		delta := c.delta()
		if markdown && c.regressed() {
			delta = "**" + delta + "**"
		}
		rows = append(rows, []ui.Cell{
			{Text: n.pkg(c.pkg), Style: th.Muted},
			{Text: n.bench(c.name)},
			{Text: metric(c.unit), Style: th.Muted},
			{Text: formatValue(c.unit, c.old)},
			{Text: formatValue(c.unit, c.current)},
			{Text: delta, Style: []func(string) string{th.Fail, th.Warn, th.Ok}[c.rank()]},
		})
	}
	return rows
}

// summarize writes the Markdown summary of a comparison: the verdict, the
// significant changes as a table, and benchstat's report.
func summarize(changes []change, failures []string, report string, n naming) string {
	var b strings.Builder
	b.WriteString("# Go benchmark comparison\n\n")
	b.WriteString("Gate: increase above 10% and benchstat significance at alpha 0.01, for time, bytes or allocations per operation.\n\n")
	if len(failures) == 0 {
		b.WriteString("No confirmed regressions above 10%.\n")
	}
	for _, f := range failures {
		b.WriteString("- " + f + "\n")
	}
	if rows := changeRows(pretty.Theme{}, changes, n, true); len(rows) > 0 {
		b.WriteString("\n## Significant changes\n\n" + ui.Markdown(changeColumns, rows))
	}
	b.WriteString("\n<details><summary>benchstat report</summary>\n\n```text\n" + report + "```\n\n</details>\n")
	return b.String()
}

// significantChanges returns the significant changes, regressions first,
// then other increases, then improvements.
func significantChanges(changes []change) []change {
	shown := slices.DeleteFunc(slices.Clone(changes), func(c change) bool { return !c.significant })
	slices.SortStableFunc(shown, func(a, b change) int { return a.rank() - b.rank() })
	return shown
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	s := slices.Sorted(slices.Values(values))
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
