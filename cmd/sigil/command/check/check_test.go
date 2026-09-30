package check

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestCheck runs check over the testdata bundles and compares what it
// prints, and the error it fails with, with the golden files.
func TestCheck(t *testing.T) {
	tests := []struct {
		name     string
		config   string // under testdata/config; defaults.yaml when empty
		kind     string // the kind file; testdata/deploy_approval.sigil when empty
		format   output.Format
		paths    []string
		trusted  []string
		patterns []string
		requires []string
		noKind   bool // no --kind: the kinds come from the paths
	}{
		{name: "lints", paths: []string{"testdata/lints"}},
		{name: "lints_strict", config: "strict.yaml", paths: []string{"testdata/lints"}},
		{name: "lints_json", format: output.JSON, paths: []string{"testdata/lints"}},
		{name: "errors", paths: []string{"testdata/errors"}},
		{name: "errors_yaml", format: output.YAML, paths: []string{"testdata/errors"}},
		{name: "compile", paths: []string{"testdata/compile"}},
		{name: "stale_kind", paths: []string{"testdata/stale"}},
		{name: "legacy_kind", kind: "testdata/kinds/legacy.sigil", paths: []string{"testdata/compile"}},
		{name: "legacy_kind_json", kind: "testdata/kinds/legacy.sigil", format: output.JSON, paths: []string{"testdata/compile"}},
		{name: "invalid_kind", kind: "testdata/kinds/invalid.sigil", paths: []string{"testdata/compile"}},
		{name: "config_typo", config: "typo.yaml", paths: []string{"testdata/lints"}},
		{name: "require_ok", paths: []string{"testdata/require"}, trusted: []string{"testdata/lints/deploy"}, patterns: []string{"teams.*"}, requires: []string{"deploy.guardrails"}},
		{name: "require_gated", paths: []string{"testdata/lints/teams"}, trusted: []string{"testdata/lints/deploy"}, patterns: []string{"teams.payments"}, requires: []string{"deploy.guardrails"}},
		{name: "require_roots", paths: []string{"testdata/lints"}, requires: []string{"deploy.guardrails"}},
		{name: "require_no_match", paths: []string{"testdata/lints"}, patterns: []string{"nope.*"}, requires: []string{"deploy.guardrails"}},
		{name: "trusted_in_paths", paths: []string{"testdata/lints"}, trusted: []string{"testdata/lints/deploy"}},
		{name: "trusted_collision", paths: []string{"testdata/collision"}, trusted: []string{"testdata/lints/deploy"}},
		{name: "kinds", paths: []string{"testdata/multikind"}, noKind: true},
		{name: "require_other_kind", paths: []string{"testdata/multikind"}, requires: []string{"roles.main"}, noKind: true},
		{name: "require_missing", paths: []string{"testdata/multikind"}, requires: []string{"nope.guard"}, noKind: true},
		{name: "unknown_kind", paths: []string{"testdata/unknown_kind"}, noKind: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := tt.format
			if format == "" {
				format = output.Text
			}
			config := tt.config
			if config == "" {
				config = "defaults.yaml"
			}
			kinds := []string{filepath.Join("testdata", "deploy_approval.sigil")}
			switch {
			case tt.noKind:
				kinds = nil
			case tt.kind != "":
				kinds = []string{tt.kind}
			}
			var out bytes.Buffer
			src := project.Sources{Paths: tt.paths, Trusted: tt.trusted, Kinds: kinds, Stdin: strings.NewReader("")}
			err := run(&out, &options{output: &format}, filepath.Join("testdata", "config", config), src, tt.patterns, tt.requires)
			golden(t, tt.name, render(out.String(), err))
		})
	}
}

// TestConfigDiscovery checks that without --config, check reads the
// nearest sigil.yaml at or above the working directory, and that without
// paths it reads the working directory.
func TestConfigDiscovery(t *testing.T) {
	dir := t.TempDir()
	kind, err := os.ReadFile(filepath.Join("testdata", "deploy_approval.sigil"))
	if err != nil {
		t.Fatal(err)
	}
	policy := "policy p: DeployApproval@1\n\nlet unused = 1\n"
	files := map[string]string{
		"deploy_approval.sigil": string(kind),
		"sigil.yaml":            "lints:\n  unused-let: error\n",
		"sub/p.sigil":           policy,
	}
	for name, src := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(filepath.Join(dir, "sub"))
	format := output.Text
	var out bytes.Buffer
	herr := run(&out, &options{output: &format}, "", project.Sources{Kinds: []string{filepath.Join("..", "deploy_approval.sigil")}}, nil, nil)
	if herr == nil || !strings.Contains(out.String(), "error: let unused is never read [unused-let]") {
		t.Fatalf("run() = %v with output %q, want the unused-let lint as an error", herr, out.String())
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

// TestNoFiles checks that a directory without .sigil files is a warning,
// not a pass.
func TestNoFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	format := output.Text
	var out bytes.Buffer
	err := run(&out, &options{output: &format}, filepath.Join("testdata", "config", "defaults.yaml"), project.Sources{Paths: []string{dir}, Kinds: []string{filepath.Join("testdata", "deploy_approval.sigil")}}, nil, nil)
	if err != nil {
		t.Fatalf("run() = %v", err)
	}
	if want := "! no .sigil files found, so nothing was checked\n"; !strings.HasPrefix(out.String(), want) {
		t.Errorf("output = %q, want it to start with %q", out.String(), want)
	}
}
