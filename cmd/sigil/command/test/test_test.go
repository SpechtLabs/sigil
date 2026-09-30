package test

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/internal/output"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestTest runs the testdata test files and compares what test prints,
// and the error it fails with, with the golden files.
func TestTest(t *testing.T) {
	const policies = "testdata/access/main.sigil"
	tests := []struct {
		name    string
		run     string
		format  output.Format
		paths   []string
		verbose bool
		noKind  bool // no --kind: the kind comes from the paths
	}{
		{name: "pass", paths: []string{"testdata/access"}},
		{name: "pass_verbose", paths: []string{"testdata/access"}, verbose: true},
		{name: "failures", paths: []string{policies, "testdata/failing"}},
		{name: "failures_json", paths: []string{policies, "testdata/failing"}, format: output.JSON},
		{name: "failures_yaml", paths: []string{policies, "testdata/failing"}, format: output.YAML},
		{name: "run_filter", paths: []string{policies, "testdata/failing"}, run: "^wrong", verbose: true},
		{name: "run_filter_none", paths: []string{"testdata/access"}, run: "matches nothing"},
		{name: "one_file", paths: []string{policies, "testdata/access/main_test.yaml"}},
		{name: "invalid", paths: []string{policies, "testdata/invalid"}},
		{name: "bad_yaml", paths: []string{policies, "testdata/badyaml"}},
		{name: "no_policy", paths: []string{policies, "testdata/nopolicy"}},
		{name: "broken_bundle", paths: []string{"testdata/broken"}},
		{name: "no_test_files", paths: []string{policies}},
		{name: "invalid_run", paths: []string{"testdata/access"}, run: "("},
		{name: "kind_among_paths", paths: []string{"testdata/access.sigil", "testdata/access"}, noKind: true},
		{name: "missing_path", paths: []string{"testdata/nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := tt.format
			if format == "" {
				format = output.Text
			}
			kinds := []string{filepath.Join("testdata", "access.sigil")}
			if tt.noKind {
				kinds = nil
			}
			var out bytes.Buffer
			err := runTests(context.Background(), &out, &options{output: &format}, kinds, tt.run, tt.verbose, tt.paths)
			golden(t, tt.name, render(out.String(), err))
		})
	}
}

// render joins what the command printed and the error it returned.
func render(out string, err humane.Error) string {
	if err == nil {
		return out + "--- ok ---\n"
	}
	s := out + "--- error ---\n" + err.Error() + "\n"
	for _, a := range err.Advice() {
		s += "advice: " + a + "\n"
	}
	return s
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}
