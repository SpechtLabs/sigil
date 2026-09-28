package fuzz

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"
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
			name: "targets",
			want: "./a\n  FuzzA\n  FuzzB\n./b\n  FuzzC\n\n3 fuzz targets in 2 packages\n",
		},
		{
			name: "one package",
			args: []string{"./b"},
			want: "./b\n  FuzzC\n\n1 fuzz target in 1 package\n",
		},
		{
			name: "targets by name",
			args: []string{"--filter", "FuzzB|FuzzC"},
			want: "./a\n  FuzzB\n./b\n  FuzzC\n\n2 fuzz targets in 2 packages\n",
		},
		{
			name: "packages",
			args: []string{"--packages"},
			want: "./a\n./b\n\n3 fuzz targets in 2 packages\n",
		},
		{
			name: "packages as JSON",
			args: []string{"--packages", "-o", "json"},
			want: `["example.com/m/a","example.com/m/b"]` + "\n",
		},
		{
			name: "targets as JSON",
			args: []string{"-o", "json", "./b"},
			want: `[{"package":"example.com/m/b","name":"FuzzC"}]` + "\n",
		},
		{
			name: "packages as YAML",
			args: []string{"--packages", "-o", "yaml"},
			want: "---\n- example.com/m/a\n- example.com/m/b\n",
		},
		{name: "invalid format", args: []string{"-o", "xml"}, wantErr: "unsupported output type"},
		{name: "no targets", args: []string{"./c"}, wantErr: "no fuzz targets in ./c"},
		{name: "no matching targets", args: []string{"--filter", "Nope"}, wantErr: "no fuzz targets matching Nope in ./..."},
		{name: "invalid filter", args: []string{"--filter", "Fuzz("}, wantErr: "--filter Fuzz( isn't a valid regular expression"},
		{name: "filter from the environment", env: map[string]string{"FUZZ_FILTER": "C$"}, want: "./b\n  FuzzC\n\n1 fuzz target in 1 package\n"},
		{name: "unknown package", args: []string{"./missing"}, wantErr: "go list"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			cmd := NewCommand(WithRoot(fuzzModule(t, "")), WithGetenv(func(k string) string { return tt.env[k] }))
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
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		failing string
		chdir   bool
		timeout time.Duration

		wantOut    []string
		wantNot    []string
		wantErr    string
		wantAdvice []string
		wantFiles  []string
	}{
		{
			name: "every target",
			args: []string{"--time", "2x"},
			wantOut: []string{
				"Packages: ./a, ./b", "Runs:     3 fuzz targets for 2x each", "CPUs:     2",
				"✓ [1/3] FuzzA  ./a  ", "✓ [2/3] FuzzB  ./a  ", "✓ [3/3] FuzzC  ./b  ",
				"✓ Fuzzed 3 targets in", "new interesting inputs\n  Results in ",
			},
			wantFiles: []string{"fuzz.log", "summary.md", "metadata.json"},
		},
		{
			name:    "targets by name",
			args:    []string{"--time", "1x", "--filter", "B$"},
			wantOut: []string{"Filter:   B$", "✓ [1/1] FuzzB  ./a"},
			wantNot: []string{"FuzzA", "FuzzC"},
		},
		{
			name:    "an estimate for timed runs",
			args:    []string{"--time", "1s", "./a"},
			wantOut: []string{"Runs:     2 fuzz targets for 1s each", "Estimate: about 2s, plus building each package"},
		},
		{
			name:    "flags from the environment",
			args:    []string{"./b"},
			env:     map[string]string{"FUZZ_TIME": "3x", "FUZZ_CPU": "1", "FUZZ_TIMEOUT": "1m"},
			chdir:   true,
			wantOut: []string{"Runs:     1 fuzz target for 3x each", "CPUs:     1", "✓ [1/1] FuzzC  ./b", "Fuzzed 1 target in"},
		},
		{
			name:    "flags win over the environment",
			args:    []string{"--time", "1x", "./b"},
			env:     map[string]string{"FUZZ_TIME": "invalid"},
			wantOut: []string{"for 1x each"},
		},
		{
			name:    "go test's output with --verbose",
			args:    []string{"--time", "1x", "--verbose", "./b"},
			wantOut: []string{"  [1/1] FuzzC  ./b\n", "ok  \texample.com/m/b", "✓ [1/1] FuzzC  ./b"},
		},
		{
			name:       "a failing seed stops the run",
			args:       []string{"--time", "2x"},
			failing:    "a",
			wantOut:    []string{"✗ [1/3] FuzzA  ./a  failed", "--- FAIL: FuzzA"},
			wantNot:    []string{"FuzzB  ./a"},
			wantErr:    "fuzzing FuzzA in ./a failed",
			wantAdvice: []string{"go test's output above says why", "go test -run '^$' -fuzz '^FuzzA$' ./a"},
			wantFiles:  []string{"fuzz.log", "summary.md"},
		},
		{
			name:       "a finding says how to replay it",
			args:       []string{"--time", "60s", "./d"},
			failing:    "find",
			wantOut:    []string{"✗ [1/1] FuzzFind  ./d  found a failing input"},
			wantErr:    "FuzzFind found a failing input",
			wantAdvice: []string{"replay it with: go test -run=FuzzFind/", " ./d", "keep d/testdata/fuzz/FuzzFind/"},
		},
		{
			name:    "an interrupt stops fuzzing",
			args:    []string{"--time", "1h", "./b"},
			timeout: 5 * time.Second,
			wantOut: []string{"! Interrupted", "Finished 0 of 1 fuzz target before the interrupt"},
			wantErr: "interrupted",
		},
		{name: "invalid FUZZ_TIMEOUT", env: map[string]string{"FUZZ_TIMEOUT": "soon"}, wantErr: "invalid FUZZ_TIMEOUT value soon"},
		{name: "invalid filter", args: []string{"--filter", "("}, wantErr: "isn't a valid regular expression"},
		{name: "no targets", args: []string{"./c"}, wantErr: "no fuzz targets in ./c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Chdir can't run in parallel; every other case can.
			if !tt.chdir {
				t.Parallel()
			}
			root := fuzzModule(t, tt.failing)
			opts := []Option{WithGetenv(func(k string) string { return tt.env[k] })}
			if tt.chdir {
				t.Chdir(root)
			} else {
				opts = append(opts, WithRoot(root))
			}
			ctx := t.Context()
			if tt.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.timeout)
				defer cancel()
			}

			var out bytes.Buffer
			cmd := NewCommand(opts...)
			resultsDir := filepath.Join(t.TempDir(), "results")
			cmd.SetArgs(append(append([]string{"run"}, tt.args...), "--results", resultsDir))
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			err := cmd.ExecuteContext(ctx)
			checkErr(t, err, tt.wantErr)

			for _, want := range tt.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output doesn't contain %q:\n%s", want, out.String())
				}
			}
			for _, not := range tt.wantNot {
				if strings.Contains(out.String(), not) {
					t.Errorf("output contains %q:\n%s", not, out.String())
				}
			}
			for _, name := range tt.wantFiles {
				if _, serr := os.Stat(filepath.Join(resultsDir, name)); serr != nil {
					t.Errorf("missing %s: %v", name, serr)
				}
			}
			if len(tt.wantAdvice) > 0 {
				he, ok := errors.AsType[humane.Error](err)
				if !ok {
					t.Fatalf("error %v isn't a humane.Error", err)
				}
				advice := strings.Join(he.Advice(), "\n")
				for _, want := range tt.wantAdvice {
					if !strings.Contains(advice, want) {
						t.Errorf("advice doesn't contain %q:\n%s", want, advice)
					}
				}
			}
		})
	}
}

// fuzzModule creates a module with fuzz targets in packages a and b and
// none in c. failing names a package whose targets fail on their seed;
// "find" adds package d, whose target fails on inputs the fuzzer finds.
// The module is a committed git checkout, as devtool expects.
func fuzzModule(t *testing.T, failing string) string {
	t.Helper()
	target := func(pkg, name string) string {
		body := ""
		if pkg == failing {
			body = "\t\tt.Fatal(\"found\")\n"
		}
		return "func " + name + "(f *testing.F) {\n\tf.Add(1)\n\tf.Fuzz(func(t *testing.T, n int) {\n" + body + "\t})\n}\n"
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/m\n\ngo 1.27\n",
		"a/a.go":      "package a\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\n" + target("a", "FuzzA") + "\nfunc TestA(t *testing.T) {}\n",
		"a/x_test.go": "package a_test\n\nimport \"testing\"\n\n" + target("a", "FuzzB"),
		"b/b_test.go": "package b\n\nimport \"testing\"\n\n" + target("b", "FuzzC"),
		"c/c.go":      "package c\n",
	}
	if failing == "find" {
		files["d/d_test.go"] = "package d\n\nimport \"testing\"\n\n" +
			"func FuzzFind(f *testing.F) {\n\tf.Add(\"ok\")\n\tf.Fuzz(func(t *testing.T, s string) {\n" +
			"\t\tif len(s) > 2 && s[0] == 'x' {\n\t\t\tt.Fatalf(\"broken on %q\", s)\n\t\t}\n\t})\n}\n"
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
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
