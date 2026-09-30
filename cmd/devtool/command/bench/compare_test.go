package bench

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/resultdir"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

const sigP = "p=0.000 n=10"

func TestRegressions(t *testing.T) {
	tests := []struct {
		name    string
		table   string
		want    int
		wantErr string
	}{
		{name: "significant slowdown", table: table("100", "120", "+20.00%", "sec/op", sigP), want: 1},
		{name: "increase at the threshold", table: table("100", "110", "+10.00%", "sec/op", sigP)},
		{name: "small increase", table: table("100", "105", "+5.00%", "sec/op", sigP)},
		{name: "insignificant increase", table: table("100", "150", "~", "sec/op", "p=0.512 n=10")},
		{name: "improvement", table: table("100", "80", "-20.00%", "sec/op", sigP)},
		{name: "unrounded value above the threshold", table: table("100", "110.004", "+10.00%", "sec/op", sigP), want: 1},
		{name: "bytes regression", table: table("10", "12", "+20.00%", "B/op", sigP), want: 1},
		{name: "allocations regression", table: table("10", "12", "+20.00%", "allocs/op", sigP), want: 1},
		{name: "bytes from zero", table: table("0", "1", "+∞%", "B/op", sigP), want: 1},
		{name: "allocations from zero", table: table("0", "1", "+∞%", "allocs/op", sigP), want: 1},
		{name: "NaN median", table: table("100", "nan", "+20.00%", "sec/op", sigP), wantErr: "non-finite"},
		{name: "infinite median", table: table("100", "inf", "+20.00%", "sec/op", sigP), wantErr: "non-finite"},
		{name: "negative median", table: table("100", "-1", "+20.00%", "sec/op", sigP), wantErr: "non-finite"},
		{name: "unparseable median", table: table("100", "fast", "+20.00%", "sec/op", sigP), wantErr: "non-finite"},
		{name: "empty", table: "", wantErr: "no comparisons"},
		{name: "package only", table: "pkg: example/policy\n", wantErr: "no comparisons"},
		{
			name:    "changed header",
			table:   strings.Replace(table("100", "120", "+20.00%", "sec/op", sigP), ",CI,vs base,P", ",CI,changed,P", 1),
			wantErr: "unexpected benchstat CSV header",
		},
		{name: "row before a header", table: "pkg: example/policy\nEval-2,1,0%,1,0%,~,p=1\n", wantErr: "unexpected benchstat CSV row"},
		{name: "malformed CSV", table: "\"unterminated\n", wantErr: "can't read benchstat's CSV"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes, err := parseChanges(tt.table)
			checkErr(t, err, tt.wantErr)
			got := regressions(changes)
			if len(got) != tt.want {
				t.Errorf("regressions() = %q, want %d", got, tt.want)
			}
		})
	}
}

func TestParseSamples(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantErr string
	}{
		{name: "valid", text: raw(100, 64, 2, 10, "Eval")},
		{name: "throughput is ignored", text: strings.ReplaceAll(raw(100, 64, 2, 1, "Eval"), " ns/op", " ns/op 123.45 MB/s")},
		{name: "empty", text: "", wantErr: "no benchmark measurements"},
		{name: "NaN", text: strings.Replace(raw(100, 64, 2, 1, "Eval"), "100 ns/op", "nan ns/op", 1), wantErr: "invalid measurement"},
		{name: "infinite", text: strings.Replace(raw(100, 64, 2, 1, "Eval"), "100 ns/op", "inf ns/op", 1), wantErr: "invalid measurement"},
		{name: "negative", text: strings.Replace(raw(100, 64, 2, 1, "Eval"), "100 ns/op", "-1 ns/op", 1), wantErr: "invalid measurement"},
		{name: "missing allocations", text: strings.ReplaceAll(raw(100, 64, 2, 1, "Eval"), " 2 allocs/op", ""), wantErr: "must report time, bytes and allocations"},
		{name: "zero iterations", text: strings.Replace(raw(100, 64, 2, 1, "Eval"), " 1000 ", " 0 ", 1), wantErr: "malformed"},
		{name: "no package", text: "BenchmarkEval-2 1000 100 ns/op 64 B/op 2 allocs/op\n", wantErr: "malformed"},
		{name: "too short", text: "pkg: p\nBenchmarkEval-2 1000\n", wantErr: "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSamples(tt.text)
			checkErr(t, err, tt.wantErr)
		})
	}
}

func TestValidateSamples(t *testing.T) {
	tests := []struct {
		name          string
		before, after string
		wantErr       string
	}{
		{name: "matching", before: raw(100, 64, 2, 10, "Eval"), after: raw(110, 64, 2, 10, "Eval")},
		{name: "both empty", wantErr: "no benchmark measurements"},
		{name: "base measured nothing", after: raw(100, 64, 2, 10, "New")},
		{name: "head empty", before: raw(100, 64, 2, 10, "Eval"), wantErr: "no benchmark measurements"},
		{name: "different benchmarks on each side", before: raw(100, 64, 2, 10, "Eval"), after: raw(100, 64, 2, 10, "Renamed")},
		{name: "missing sample", before: raw(100, 64, 2, 10, "Eval"), after: raw(100, 64, 2, 9, "Eval"), wantErr: "head: example/policy BenchmarkEval-2"},
		{name: "extra sample", before: raw(100, 64, 2, 11, "Eval"), after: raw(100, 64, 2, 10, "Eval"), wantErr: "has 11 samples"},
		{
			name:    "missing metric",
			before:  raw(100, 64, 2, 10, "Eval"),
			after:   strings.ReplaceAll(raw(100, 64, 2, 10, "Eval"), " 2 allocs/op", ""),
			wantErr: "must report time, bytes and allocations",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkErr(t, validateSamples(tt.before, tt.after, 10), tt.wantErr)
		})
	}
}

func TestCompare(t *testing.T) {
	benchstat, err := exec.LookPath("benchstat")
	if err != nil {
		t.Skip("benchstat isn't installed; the benchmark CI job runs this test")
	}

	tests := []struct {
		name    string
		base    string
		head    string
		wantErr string
		wantOut string
		// wantCompared says whether the comparison wrote the samples of
		// the benchmarks both revisions measured apart from the raw ones.
		wantCompared bool
	}{
		{name: "unchanged", head: raw(100, 64, 2, 10, "Eval"), wantOut: "No confirmed regressions above 10%\n  0 of 3 comparisons changed significantly\n  Results in "},
		{name: "small slowdown", head: raw(105, 64, 2, 10, "Eval")},
		{name: "speedup", head: raw(80, 64, 2, 10, "Eval")},
		{name: "slowdown", head: raw(120, 64, 2, 10, "Eval"), wantErr: "1 benchmark regression above 10%", wantOut: "  PACKAGE  BENCHMARK  METRIC"},
		{name: "more bytes", head: raw(100, 128, 2, 10, "Eval"), wantErr: "1 benchmark regression above 10%"},
		{name: "more allocations", head: raw(100, 64, 3, 10, "Eval"), wantErr: "1 benchmark regression above 10%"},
		{name: "mismatched samples", head: raw(100, 64, 2, 9, "Eval"), wantErr: "expected 10"},
		{
			name:         "a new benchmark isn't compared",
			head:         raw(100, 64, 2, 10, "Eval") + raw(900, 900, 9, 10, "New"),
			wantOut:      "0 of 3 comparisons changed significantly\n  1 new benchmark, not compared: policy New\n",
			wantCompared: true,
		},
		{
			name:         "a benchmark only the base measured isn't compared",
			base:         raw(100, 64, 2, 10, "Eval") + raw(100, 64, 2, 10, "Old"),
			head:         raw(100, 64, 2, 10, "Eval"),
			wantOut:      "1 benchmark only on the base revision, not compared: policy Old",
			wantCompared: true,
		},
		{
			name:         "nothing in common",
			head:         raw(100, 64, 2, 10, "New"),
			wantOut:      "0 of 0 comparisons changed significantly\n  1 new benchmark, not compared: policy New\n  1 benchmark only on the base revision, not compared: policy Eval",
			wantCompared: false,
		},
		{name: "a regression next to a new benchmark still fails", head: raw(120, 64, 2, 10, "Eval") + raw(100, 64, 2, 10, "New"), wantErr: "1 benchmark regression above 10%", wantCompared: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			base := tt.base
			if base == "" {
				base = raw(100, 64, 2, 10, "Eval")
			}
			writeFiles(t, dir, map[string]string{"base.txt": base, "head.txt": tt.head})
			var out bytes.Buffer
			err := compare(t.Context(), pretty.New(&out), resultsDir(t, dir), benchstat, naming{module: "example", cpu: 2}, 10)
			checkErr(t, err, tt.wantErr)
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output %q doesn't contain %q", out.String(), tt.wantOut)
			}
			if _, err := os.Stat(filepath.Join(dir, comparedHead)); (err == nil) != tt.wantCompared {
				t.Errorf("%s exists: %v, want %v", comparedHead, err == nil, tt.wantCompared)
			}
			if tt.wantErr == "" || strings.Contains(tt.wantErr, "regressions") {
				summary, err := os.ReadFile(filepath.Join(dir, "summary.md"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(summary), "# Go benchmark comparison") {
					t.Errorf("summary.md = %q", summary)
				}
			}
		})
	}
}

func TestCompareErrors(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		count   int
		wantErr string
	}{
		{name: "too few samples", count: 9, wantErr: "at least 10 samples"},
		{name: "no base samples", count: 10, wantErr: "can't read the base samples"},
		{name: "no head samples", files: map[string]string{"base.txt": ""}, count: 10, wantErr: "can't read the head samples"},
		{
			name:    "missing benchstat",
			files:   map[string]string{"base.txt": raw(100, 64, 2, 10, "Eval"), "head.txt": raw(100, 64, 2, 10, "Eval")},
			count:   10,
			wantErr: "failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, tt.files)
			err := compare(t.Context(), pretty.New(io.Discard), resultsDir(t, dir), filepath.Join(dir, "no-benchstat"), naming{}, tt.count)
			checkErr(t, err, tt.wantErr)
		})
	}
}

func TestDelta(t *testing.T) {
	tests := []struct {
		old, current float64
		want         string
	}{
		{old: 100, current: 112.5, want: "+12.50%"},
		{old: 100, current: 80, want: "-20.00%"},
		{old: 0, current: 0, want: "+0.00%"},
		{old: 0, current: 1, want: "0 to nonzero"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := (change{old: tt.old, current: tt.current}).delta(); got != tt.want {
				t.Errorf("delta() = %q, want %q", got, tt.want)
			}
		})
	}
}

// raw returns count lines of go test -bench output for one benchmark.
func raw(ns, size, allocs, count int, name string) string {
	var b strings.Builder
	b.WriteString("pkg: example/policy\n")
	for range count {
		fmt.Fprintf(&b, "Benchmark%s-2 1000 %d ns/op %d B/op %d allocs/op\n", name, ns, size, allocs)
	}
	return b.String()
}

// table returns benchstat CSV comparing one benchmark's medians.
func table(old, current, delta, unit, p string) string {
	return "pkg: example/policy\n,base.txt,,head.txt,,,\n" +
		fmt.Sprintf(",%s,CI,%s,CI,vs base,P\n", unit, unit) +
		fmt.Sprintf("Eval-2,%s,0%%,%s,0%%,%s,%s\n", old, current, delta, p) +
		fmt.Sprintf("geomean,%s,,%s,,%s,\n", old, current, delta)
}

func checkErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Fatalf("unexpected error: %v", err)
	case want != "" && err == nil:
		t.Fatalf("expected an error containing %q", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Fatalf("error %q doesn't contain %q", err, want)
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// resultsDir returns dir as a results directory, keeping what's in it.
func resultsDir(t *testing.T, dir string) resultdir.Dir {
	t.Helper()
	res, err := resultdir.Reset(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
