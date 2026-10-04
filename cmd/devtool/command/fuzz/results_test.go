package fuzz

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummaryMarkdown(t *testing.T) {
	found := targetResult{
		Target: "FuzzPatch", Dir: "./internal/stamp", Result: resultFound, Execs: 15_600_000, NewInputs: 106,
		Input: "internal/stamp/testdata/fuzz/FuzzPatch/5a2f", Replay: "go test -run=FuzzPatch/5a2f ./internal/stamp",
	}
	crashed := targetResult{Target: "FuzzB", Dir: "./b", Result: resultFailed, Replay: "go test -run '^$' -fuzz '^FuzzB$' ./b"}
	passed := targetResult{Target: "FuzzA", Dir: "./a", Result: resultPassed, Execs: 2_000_000, NewInputs: 12}
	notRun := targetResult{Target: "FuzzC", Dir: "./c", Result: resultNotRun}

	tests := []struct {
		name string
		runs []runResults
		want string
	}{
		{
			name: "every target passed",
			runs: []runResults{{Time: "60m", Restored: 120, Targets: []targetResult{passed}}},
			want: "# Go fuzzing\n\n**1 of 1 target passed** · 60m per target · 2M execs · 12 new inputs · started from 120 corpus inputs\n\n" +
				"| FUZZ TARGET | PACKAGE | EXECS | NEW INPUTS | RESULT |\n| --- | --- | ---: | ---: | --- |\n" +
				"| FuzzA | ./a | 2M | 12 | ✓ passed |\n",
		},
		{
			name: "jobs merge, failures come first and say how to replay them",
			runs: []runResults{
				{Time: "60m", Restored: 3, Targets: []targetResult{passed}},
				{Time: "60m", Restored: 4, Targets: []targetResult{found}},
				{Time: "60m", Targets: []targetResult{crashed, notRun}},
			},
			want: "# Go fuzzing\n\n**1 of 4 targets passed** · 60m per target · 17.6M execs · 118 new inputs · started from 7 corpus inputs\n\n" +
				"## Failures\n\n" +
				"**FuzzPatch** in `./internal/stamp` found a failing input, saved as `internal/stamp/testdata/fuzz/FuzzPatch/5a2f`. Replay it with:\n\n" +
				"```sh\ngo test -run=FuzzPatch/5a2f ./internal/stamp\n```\n\n" +
				"**FuzzB** in `./b` failed. Rerun it with:\n\n```sh\ngo test -run '^$' -fuzz '^FuzzB$' ./b\n```\n\n" +
				"## Targets\n\n" +
				"| FUZZ TARGET | PACKAGE | EXECS | NEW INPUTS | RESULT |\n| --- | --- | ---: | ---: | --- |\n" +
				"| FuzzPatch | ./internal/stamp | 15.6M | 106 | ✗ found a failing input |\n" +
				"| FuzzB | ./b | 0 | 0 | ✗ failed |\n" +
				"| FuzzA | ./a | 2M | 12 | ✓ passed |\n" +
				"| FuzzC | ./c | 0 | 0 | – not run |\n",
		},
		{
			name: "runs of different lengths leave the time out",
			runs: []runResults{{Time: "60m", Targets: []targetResult{passed}}, {Time: "1m", Targets: []targetResult{passed}}},
			want: "# Go fuzzing\n\n**2 of 2 targets passed** · 4M execs · 24 new inputs\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summaryMarkdown(tt.runs); !strings.HasPrefix(got, tt.want) {
				t.Errorf("summaryMarkdown() =\n%s\nwant it to start with\n%s", got, tt.want)
			}
		})
	}
}

func TestSummaryCommand(t *testing.T) {
	dir := t.TempDir()
	for job, r := range map[string]runResults{
		"fuzz-0/fuzz-results": {Time: "60m", Targets: []targetResult{{Target: "FuzzA", Dir: "./a", Result: resultPassed}}},
		"fuzz-1/fuzz-results": {Time: "60m", Targets: []targetResult{{Target: "FuzzB", Dir: "./a", Result: resultPassed}}},
	} {
		b, _ := json.Marshal(r)
		if err := os.MkdirAll(filepath.Join(dir, job), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, job, resultsFile), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, resultsFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{name: "every job's results", args: []string{dir}, want: "**2 of 2 targets passed** · 60m per target"},
		{name: "no results", args: []string{t.TempDir()}, wantErr: "no results.json under"},
		{name: "unreadable results", args: []string{broken}, wantErr: "can't read the fuzz results under"},
		{name: "no directory", wantErr: "requires at least 1 arg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			cmd := NewCommand()
			cmd.SetArgs(append([]string{"summary"}, tt.args...))
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			checkErr(t, cmd.Execute(), tt.wantErr)
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("output = %q, want it to contain %q", out.String(), tt.want)
			}
		})
	}
}

// A run that stops at a failure records it with its replay command, and
// the targets after it as not run.
func TestRunRecordsResults(t *testing.T) {
	t.Parallel()
	root := fuzzModule(t, "a")
	results := t.TempDir()
	cmd := NewCommand(WithRoot(root), withFuzzCache(t.TempDir()))
	cmd.SetArgs([]string{"run", "--time", "2x", "--no-restore", "--results", results})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Fatal("fuzz run of a failing target succeeded")
	}

	b, err := os.ReadFile(filepath.Join(results, resultsFile))
	if err != nil {
		t.Fatal(err)
	}
	var run runResults
	if err = json.Unmarshal(b, &run); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(run.Targets))
	for _, r := range run.Targets {
		got = append(got, r.Target+" "+r.Result)
	}
	if want := "FuzzA failed, FuzzB not run, FuzzC not run"; strings.Join(got, ", ") != want {
		t.Errorf("results = %q, want %q", strings.Join(got, ", "), want)
	}
	if run.Time != "2x" || run.Head == "" || run.Targets[0].Replay != "go test -run '^$' -fuzz '^FuzzA$' ./a" {
		t.Errorf("results.json = %+v", run)
	}
	summary, err := os.ReadFile(filepath.Join(results, "summary.md"))
	if err != nil || !strings.Contains(string(summary), "**0 of 3 targets passed**") || !strings.Contains(string(summary), "## Failures") {
		t.Errorf("summary.md = %q, %v", summary, err)
	}
}
