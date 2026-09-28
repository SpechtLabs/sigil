package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
)

func TestHeader(t *testing.T) {
	checkout := gotool.Checkout{Head: "3868261f1630ddbfa10a4f65d2cd3541e2ba2b09", Go: "go1.27.1 darwin/arm64"}
	tests := []struct {
		name string
		plan Plan
		want []string
	}{
		{
			name: "the rows every run has",
			plan: Plan{Checkout: checkout, Packages: []string{"./a", "./b"}, Runs: "3 fuzz targets for 10s each", CPU: 2, Results: "fuzz-results"},
			want: []string{
				"Head=3868261", "Packages=./a, ./b", "Runs=3 fuzz targets for 10s each",
				"CPUs=2", "Go=go1.27.1 darwin/arm64", "Results=fuzz-results",
			},
		},
		{
			name: "every optional row, in order",
			plan: Plan{
				Checkout: gotool.Checkout{Head: checkout.Head, Dirty: true, Go: checkout.Go},
				Base:     "c4695b6 (main)",
				Packages: []string{"./a", "./b", "./c", "./d"},
				Filter:   "Lexer",
				Runs:     "10 samples of 200ms each per revision",
				CPU:      4,
				Estimate: "about 4m",
				Results:  "benchmark-results",
			},
			want: []string{
				"Head=3868261 with uncommitted changes", "Base=c4695b6 (main)", "Packages=4 packages", "Filter=Lexer",
				"Runs=10 samples of 200ms each per revision", "CPUs=4", "Estimate=about 4m",
				"Go=go1.27.1 darwin/arm64", "Results=benchmark-results",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := Header(printer(&out, false), tt.plan); err != nil {
				t.Fatal(err)
			}
			var got []string
			for line := range strings.Lines(out.String()) {
				key, value, _ := strings.Cut(line, ":")
				got = append(got, key+"="+strings.TrimSpace(value))
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestHeaderInABox(t *testing.T) {
	var out bytes.Buffer
	if err := Header(printer(&out, true), Plan{Title: "Fuzzing", Checkout: gotool.Checkout{Head: "abc"}}); err != nil {
		t.Fatal(err)
	}
	got := sgr.ReplaceAllString(out.String(), "")
	if !strings.HasPrefix(got, "╭") || !strings.Contains(got, "Fuzzing") || !strings.Contains(got, "Head:") {
		t.Errorf("header =\n%s", got)
	}
}

func TestInterrupted(t *testing.T) {
	tests := []struct {
		done, total int
		want        string
	}{
		{done: 2, total: 5, want: "Finished 2 of 5 rounds before the interrupt; the results are incomplete."},
		{done: 0, total: 1, want: "Finished 0 of 1 round before the interrupt; the results are incomplete."},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			var out bytes.Buffer
			err := Interrupted(printer(&out, false), tt.done, tt.total, "round", "rounds")
			if err == nil || err.Error() != "interrupted" {
				t.Errorf("error = %v, want interrupted", err)
			}
			if !strings.Contains(out.String(), "Interrupted") || !strings.Contains(out.String(), tt.want) {
				t.Errorf("output = %q, want the warning and %q", out.String(), tt.want)
			}
		})
	}
}

func TestPlural(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{n: 0, want: "rounds"},
		{n: 1, want: "round"},
		{n: 2, want: "rounds"},
	}
	for _, tt := range tests {
		if got := Plural(tt.n, "round", "rounds"); got != tt.want {
			t.Errorf("Plural(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
