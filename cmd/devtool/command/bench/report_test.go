package bench

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

func TestNaming(t *testing.T) {
	n := naming{module: "example.com/m", cpu: 4}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "module root", got: n.pkg("example.com/m"), want: "."},
		{name: "nested package", got: n.pkg("example.com/m/internal/eval"), want: "internal/eval"},
		{name: "other module", got: n.pkg("example.org/x"), want: "example.org/x"},
		{name: "go test name", got: n.bench("BenchmarkEval/rules=1-4"), want: "Eval/rules=1"},
		{name: "benchstat name", got: n.bench("Eval/rules=1-4"), want: "Eval/rules=1"},
		{name: "other cpu count", got: n.bench("Eval-2"), want: "Eval-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestFormatValue(t *testing.T) {
	tests := []struct {
		unit  string
		value float64
		want  string
	}{
		{unit: "sec/op", value: 332e-9, want: "332ns"},
		{unit: "sec/op", value: 1.18e-6, want: "1.18µs"},
		{unit: "sec/op", value: 58.1e-3, want: "58.1ms"},
		{unit: "sec/op", value: 2.5, want: "2.5s"},
		{unit: "ns/op", value: 1500, want: "1.5µs"},
		{unit: "B/op", value: 384, want: "384B"},
		{unit: "B/op", value: 22016, want: "21.5KiB"},
		{unit: "B/op", value: 3 << 20, want: "3MiB"},
		{unit: "B/op", value: 5 << 30, want: "5GiB"},
		{unit: "allocs/op", value: 36, want: "36"},
		{unit: "allocs/op", value: 1800, want: "1.8k"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatValue(tt.unit, tt.value); got != tt.want {
				t.Errorf("formatValue(%q, %v) = %q, want %q", tt.unit, tt.value, got, tt.want)
			}
		})
	}
}

func TestMedian(t *testing.T) {
	tests := []struct {
		values []float64
		want   float64
	}{
		{values: nil, want: 0},
		{values: []float64{3}, want: 3},
		{values: []float64{5, 1, 3}, want: 3},
		{values: []float64{4, 1, 3, 2}, want: 2.5},
	}
	for _, tt := range tests {
		if got := median(tt.values); got != tt.want {
			t.Errorf("median(%v) = %v, want %v", tt.values, got, tt.want)
		}
	}
}

func TestMeasureTable(t *testing.T) {
	samples, err := parseSamples("pkg: example.com/m/eval\n" +
		"BenchmarkEval/rules=64-2 10 58100 ns/op 22016 B/op 1800 allocs/op\n" +
		"BenchmarkEval/rules=1-2 10 1200 ns/op 384 B/op 36 allocs/op\n" +
		"BenchmarkEval/rules=1-2 10 1100 ns/op 384 B/op 36 allocs/op\n" +
		"BenchmarkEval/rules=1-2 10 1180 ns/op 384 B/op 36 allocs/op\n" +
		"pkg: example.com/m\n" +
		"BenchmarkRoot-2 10 90 ns/op 0 B/op 0 allocs/op\n")
	if err != nil {
		t.Fatal(err)
	}
	got := measureTable(pretty.New(&bytes.Buffer{}).Theme(), samples, naming{module: "example.com/m", cpu: 2})
	want := "" +
		"  PACKAGE  BENCHMARK      TIME/OP     B/OP  ALLOCS/OP\n" +
		"  .        Root              90ns       0B          0\n" +
		"  eval     Eval/rules=1    1.18µs     384B         36\n" +
		"  eval     Eval/rules=64   58.1µs  21.5KiB       1.8k\n"
	if got != want {
		t.Errorf("measureTable() =\n%s\nwant:\n%s", got, want)
	}
}

func TestChangesTable(t *testing.T) {
	changes := []change{
		{pkg: "example.com/m/fast", name: "Faster-2", unit: "sec/op", old: 100e-9, current: 80e-9, p: sigP, significant: true},
		{pkg: "example.com/m", name: "Noise-2", unit: "sec/op", old: 100e-9, current: 150e-9, p: "p=0.5 n=10"},
		{pkg: "example.com/m", name: "Slower-2", unit: "B/op", old: 100, current: 105, p: sigP, significant: true},
		{pkg: "example.com/m/eval", name: "Regressed-2", unit: "allocs/op", old: 0, current: 3, p: sigP, significant: true},
	}
	got := changesTable(pretty.New(&bytes.Buffer{}).Theme(), changes, naming{module: "example.com/m", cpu: 2})
	want := "" +
		"  PACKAGE  BENCHMARK  METRIC      BASE  HEAD        CHANGE\n" +
		"  eval     Regressed  allocs/op      0     3  0 to nonzero\n" +
		"  .        Slower     B/op        100B  105B        +5.00%\n" +
		"  fast     Faster     time/op    100ns  80ns       -20.00%\n"
	if got != want {
		t.Errorf("changesTable() =\n%s\nwant:\n%s", got, want)
	}
}

func TestSummarize(t *testing.T) {
	regressed := change{pkg: "example.com/m", name: "Eval-2", unit: "sec/op", old: 100e-9, current: 120e-9, p: sigP, significant: true}
	improved := change{pkg: "example.com/m", name: "Lex-2", unit: "B/op", old: 100, current: 80, p: sigP, significant: true}
	noise := change{pkg: "example.com/m", name: "Noise-2", unit: "sec/op", old: 100e-9, current: 101e-9, p: "p=0.5 n=10"}
	n := naming{module: "example.com/m", cpu: 2}

	tests := []struct {
		name     string
		changes  []change
		failures []string
		report   string
		added    []string
		removed  []string
		want     []string
		wantNot  []string
	}{
		{
			name:    "no changes",
			changes: []change{noise},
			report:  "report\n",
			want:    []string{"No confirmed regressions above 10%.", "<summary>benchstat report</summary>", "report\n```"},
			wantNot: []string{"## Significant changes"},
		},
		{
			name:     "regression and improvement",
			changes:  []change{improved, regressed},
			report:   "report\n",
			failures: []string{"example.com/m: Eval-2 sec/op +20.00% (p=0.000 n=10)"},
			want: []string{
				"- example.com/m: Eval-2 sec/op +20.00%",
				"| . | Eval | time/op | 100ns | 120ns | **+20.00%** |\n| . | Lex | B/op | 100B | 80B | -20.00% |",
			},
			wantNot: []string{"No confirmed regressions"},
		},
		{
			name:    "benchmarks only one revision measured",
			changes: []change{noise},
			report:  "report\n",
			added:   []string{"policy New", "policy Other"},
			removed: []string{"policy Old"},
			want: []string{
				"## Not compared",
				"- 2 new benchmarks, not compared: policy New, policy Other",
				"- 1 benchmark only on the base revision, not compared: policy Old",
			},
		},
		{
			name:    "nothing to compare",
			added:   []string{"policy New"},
			want:    []string{"No confirmed regressions above 10%.", "- 1 new benchmark, not compared: policy New"},
			wantNot: []string{"benchstat report", "## Significant changes"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarize(tt.changes, tt.failures, tt.report, n, tt.added, tt.removed)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("summary doesn't contain %q:\n%s", w, got)
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(got, w) {
					t.Errorf("summary contains %q:\n%s", w, got)
				}
			}
		})
	}
}
