package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestList(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		want    string
		wantErr string
	}{
		{
			name: "benchmarks",
			want: "./a\n  BenchmarkSum\n  BenchmarkSumLong\n./b\n  BenchmarkB\n\n3 benchmarks in 2 packages\n",
		},
		{
			name: "one package",
			args: []string{"./b"},
			want: "./b\n  BenchmarkB\n\n1 benchmark in 1 package\n",
		},
		{
			name: "benchmarks by name",
			args: []string{"--filter", "Long|B$"},
			want: "./a\n  BenchmarkSumLong\n./b\n  BenchmarkB\n\n2 benchmarks in 2 packages\n",
		},
		{
			name: "filter from the environment",
			env:  map[string]string{"BENCH_FILTER": "Long"},
			want: "./a\n  BenchmarkSumLong\n\n1 benchmark in 1 package\n",
		},
		{
			name: "packages",
			args: []string{"--packages"},
			want: "./a\n./b\n\n3 benchmarks in 2 packages\n",
		},
		{
			name: "packages as JSON",
			args: []string{"--packages", "-o", "json"},
			want: `["example.com/m/a","example.com/m/b"]` + "\n",
		},
		{
			name: "benchmarks as JSON",
			args: []string{"-o", "json", "./b"},
			want: `[{"package":"example.com/m/b","name":"BenchmarkB"}]` + "\n",
		},
		{name: "no benchmarks", args: []string{"./internal/benchtest"}, wantErr: "no benchmarks in ./internal/benchtest"},
		{name: "no matching benchmarks", args: []string{"--filter", "Nope"}, wantErr: "no benchmarks matching Nope in ./..."},
		{name: "invalid filter", args: []string{"--filter", "Sum("}, wantErr: "--filter Sum( isn't a valid regular expression"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := testModule(t)
			writeFiles(t, root, map[string]string{"b/b_bench_test.go": "package b\n\nimport \"testing\"\n\nfunc BenchmarkB(b *testing.B) {}\n"})

			var out bytes.Buffer
			cmd := NewCommand(WithRoot(root), WithGetenv(func(k string) string { return tt.env[k] }))
			cmd.SetArgs(append([]string{"list"}, tt.args...))
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			checkErr(t, cmd.Execute(), tt.wantErr)
			if tt.wantErr == "" && out.String() != tt.want {
				t.Errorf("output = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	_, benchstatErr := exec.LookPath("benchstat")

	tests := []struct {
		name      string
		args      []string
		env       map[string]string
		edit      map[string]string
		fixtures  string
		chdir     bool
		cancel    bool
		benchstat bool

		wantErr   string
		wantFiles []string
		wantOut   []string
	}{
		{
			name:      "measure the checkout",
			args:      []string{"--no-baseline", "--count", "1", "--time", "1x"},
			env:       map[string]string{"BENCH_BASELINE": "HEAD"},
			chdir:     true,
			wantFiles: []string{"head.txt", "metadata.json", "summary.md"},
			wantOut: []string{
				"Runs:     1 sample of 1x each", "Packages: ./a", "CPUs:     2",
				"✓ Built 1 test binary in", "✓ [1/1] head",
				"  PACKAGE  BENCHMARK  TIME/OP  B/OP  ALLOCS/OP\n  a        Sum",
				"✓ Measured 2 benchmarks in 1 package\n  One sample each, so expect noise\n  Results in ",
			},
		},
		{
			name:    "medians of several samples",
			args:    []string{"--count", "3", "--time", "1x", "--filter", "Long", "./a"},
			wantOut: []string{"Filter:   Long", "✓ [3/3] head", "  a        SumLong", "Medians of 3 samples"},
		},
		{
			name:    "flags from the environment",
			env:     map[string]string{"BENCH_COUNT": "2", "BENCH_TIME": "1x", "BENCH_CPU": "1"},
			wantOut: []string{"Runs:     2 samples of 1x each", "CPUs:     1", "✓ [2/2] head"},
		},
		{
			name:    "flags win over the environment",
			args:    []string{"--count", "1", "--time", "2x"},
			env:     map[string]string{"BENCH_COUNT": "7", "BENCH_TIME": "nonsense"},
			wantOut: []string{"Runs:     1 sample of 2x each"},
		},
		{
			name:    "go test's output with --verbose",
			args:    []string{"--count", "1", "--time", "1x", "--verbose"},
			wantOut: []string{"  [1/1] head\n", "BenchmarkSum-2", "✓ [1/1] head"},
		},
		{
			name:      "compare with the base revision",
			args:      []string{"--time", "1x"},
			env:       map[string]string{"BENCH_BASELINE": "HEAD", "BENCH_COUNT": "10"},
			edit:      map[string]string{"a/a_bench_test.go": benchFile("BenchmarkRenamed")},
			benchstat: true,
			wantFiles: []string{"base.txt", "head.txt", "benchstat.txt", "benchstat.csv", "metadata.json", "summary.md"},
			wantOut:   []string{"Base:     ", "(HEAD)", "✓ [ 2/10] head → base", "Runs:     10 samples of 1x each per revision"},
		},
		{name: "package without benchmarks", args: []string{"./internal/benchtest"}, wantErr: "no benchmarks in ./internal/benchtest"},
		{name: "invalid filter", args: []string{"--filter", "Sum("}, wantErr: "--filter Sum( isn't a valid regular expression"},
		{name: "no matching benchmark", args: []string{"--filter", "Nope"}, wantErr: "no benchmarks matching Nope in ./..."},
		{
			name:    "sub-benchmark filter that matches nothing",
			args:    []string{"--count", "1", "--time", "1x", "--filter", "Sum/nope"},
			wantErr: "no benchmark measurements found",
		},
		{name: "interrupted", args: []string{"--count", "1"}, cancel: true, wantErr: "interrupted", wantOut: []string{"! Interrupted", "Finished 0 of 1 round"}},
		{name: "no samples", args: []string{"--count", "0"}, wantErr: "must be positive"},
		{name: "too few samples to compare", args: []string{"--baseline", "HEAD", "--count", "9"}, wantErr: "at least 10 samples"},
		{name: "invalid BENCH_COUNT", env: map[string]string{"BENCH_COUNT": "ten"}, wantErr: "invalid BENCH_COUNT value ten"},
		{name: "missing fixtures", args: []string{"--baseline", "HEAD"}, fixtures: "testdata/missing", wantErr: "can't copy testdata/missing"},
		{name: "unknown base revision", args: []string{"--baseline", "no-such-ref"}, wantErr: "git rev-parse"},
		{
			name:    "benchmark that fails to build",
			args:    []string{"--count", "1"},
			edit:    map[string]string{"a/a_bench_test.go": "package a\n\nfunc BenchmarkA(b *testing.B) {}\n"},
			wantErr: "go test -c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Chdir can't run in parallel; every other case can.
			if !tt.chdir {
				t.Parallel()
			}
			if tt.benchstat && benchstatErr != nil {
				t.Skip("benchstat isn't installed; the benchmark CI job runs this test")
			}
			root := testModule(t)
			writeFiles(t, root, tt.edit)
			output := filepath.Join(t.TempDir(), "results")

			opts := []Option{WithGetenv(func(k string) string { return tt.env[k] })}
			if tt.fixtures != "" {
				opts = append(opts, WithFixtures(tt.fixtures))
			}
			if tt.chdir {
				t.Chdir(filepath.Join(root, "a"))
			} else {
				opts = append(opts, WithRoot(root))
			}
			var out bytes.Buffer
			cmd := NewCommand(opts...)
			cmd.SetArgs(append(append([]string{"run"}, tt.args...), "--results", output))
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			ctx, cancel := context.WithCancel(t.Context())
			if tt.cancel {
				cancel()
			}
			defer cancel()
			checkErr(t, cmd.ExecuteContext(ctx), tt.wantErr)

			for _, name := range tt.wantFiles {
				if _, err := os.Stat(filepath.Join(output, name)); err != nil {
					t.Errorf("missing %s: %v", name, err)
				}
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output doesn't contain %q:\n%s", want, out.String())
				}
			}
		})
	}
}

func TestRunMetadata(t *testing.T) {
	root := testModule(t)
	writeFiles(t, root, map[string]string{"untracked.txt": ""})
	cmd := NewCommand(WithRoot(root), WithGetenv(func(string) string { return "" }))
	cmd.SetArgs([]string{"run", "--count", "1", "--time", "1x", "--cpu", "1", "--filter", "Sum$", "--results", "results"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(root, "results", "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m metadata
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Head) != 40 || m.Base != "" || m.CPU != 1 || m.Samples != 1 || m.Time != "1x" || m.Filter != "Sum$" ||
		!strings.HasPrefix(m.Go, "go1.") || strings.Join(m.Packages, ",") != "./a" || strings.Join(m.Workloads, ",") != "a/a_bench_test.go" {
		t.Errorf("metadata = %+v", m)
	}
	// untracked.txt makes the checkout dirty.
	if !m.Dirty {
		t.Error("metadata should record the dirty checkout")
	}

	summary, err := os.ReadFile(filepath.Join(root, "results", "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(summary), "# Go benchmarks\n\nMeasured 1 benchmark in 1 package. One sample each, so expect noise.\n\n| PACKAGE |") {
		t.Errorf("summary.md = %q", summary)
	}
}

// testModule creates a committed Go module with two benchmarks in package
// a and the shared fixtures directory, and returns its root.
func testModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":                        "module example.com/m\n\ngo 1.27\n",
		"a/a.go":                        "package a\n\n// Sum adds its arguments.\nfunc Sum(n ...int) (s int) {\n\tfor _, v := range n {\n\t\ts += v\n\t}\n\treturn s\n}\n",
		"a/a_bench_test.go":             benchFile("BenchmarkSum") + "\n" + strings.TrimPrefix(benchFile("BenchmarkSumLong"), "package a\n\nimport \"testing\"\n\n"),
		"internal/benchtest/fixture.go": "package benchtest\n",
	})
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=devtool", "-c", "user.email=devtool@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", args[0], err, out)
		}
	}
	return root
}

func benchFile(name string) string {
	return "package a\n\nimport \"testing\"\n\nfunc " + name + "(b *testing.B) {\n\tfor b.Loop() {\n\t\t_ = Sum(1, 2, 3)\n\t}\n}\n"
}
