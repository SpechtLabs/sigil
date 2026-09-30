package eval

import (
	"bytes"
	"context"
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

// TestEval evaluates the testdata bundles against the testdata inputs
// and compares what eval prints, and the error it fails with, with the
// golden files.
func TestEval(t *testing.T) {
	// broken is an Access policy access.main doesn't use, with a type
	// error, and brokenKind a kind document that doesn't check: only the
	// second stops access.main.
	const (
		broken     = "policy access.other: Access@1\n\nwhen user.nmae == \"x\" {\n  allow(reason: admin)\n}\n"
		brokenKind = "kind Other version 1\n\ninput x: nope\n\ndecision allow {\n  reason: yes\n}\n\ncollect one\n\ndefault allow(reason: yes)\n"
	)
	const (
		access  = "access"
		grants  = "grants"
		reviews = "reviews"
		tiers   = "tiers"
	)
	tests := []struct {
		name     string
		kind     string // access, grants, reviews or tiers
		kindFile string // the kind file under testdata, without .sigil; the kind's own when empty
		input    string // under testdata/inputs, "-" for stdin, or empty for no --input
		stdin    string
		policy   string
		format   output.Format
		paths    []string // instead of testdata/<kind>, with no --kind
		stubs    string   // under testdata/stubs, for --stubs
		stub     []string // --stub flags
	}{
		{name: "winner", kind: access, input: "admin.json"},
		{name: "winner_json", kind: access, input: "admin.json", format: output.JSON},
		{name: "winner_yaml", kind: access, input: "admin.json", format: output.YAML},
		{name: "default", kind: access, input: "nobody.json"},
		{name: "assert", kind: access, input: "unnamed.json"},
		{name: "assert_json", kind: access, input: "unnamed.json", format: output.JSON},
		{name: "conflict", kind: access, input: "conflict.json"},
		{name: "conflict_outcome", kind: access, kindFile: "access_conflict", input: "conflict.json"},
		{name: "conflict_outcome_json", kind: access, kindFile: "access_conflict", input: "conflict.json", format: output.JSON},
		{name: "conflict_outcome_unused", kind: access, kindFile: "access_conflict", input: "nobody.json"},
		{name: "unbound_function", kind: access, input: "vault.json"},
		{name: "unknown_field", kind: access, input: "typo.json"},
		{name: "duration_as_number", kind: access, input: "number_age.json"},
		{name: "invalid_json", kind: access, input: "broken.json"},
		{name: "stdin", kind: access, input: "-", stdin: `{"user": {"name": "cy", "teams": ["platform"]}}`},
		{name: "stdin_implicit", kind: access, stdin: `{"user": {"name": "cy", "teams": ["platform"]}}`},
		{name: "stdin_yaml", kind: access, stdin: "user: {name: cy, teams: [platform]}\n"},
		{name: "yaml_file", kind: access, input: "admin.yaml"},
		{name: "neither_json_nor_yaml", kind: access, input: "garbage.txt"},
		{name: "stdin_unknown_field", kind: access, stdin: `{"user": {"admn": true}}`},
		{name: "named_policy", kind: access, input: "admin.json", policy: "access.main"},
		{name: "unknown_policy", kind: access, input: "admin.json", policy: "access.nope"},
		{name: "collect", kind: grants, input: "grants.json"},
		{name: "collect_json", kind: grants, input: "grants.json", format: output.JSON},
		{name: "collect_empty", kind: grants, input: "nogrants.json"},
		{name: "collect_empty_json", kind: grants, input: "nogrants.json", format: output.JSON},
		{name: "outcome_assert", kind: grants, input: "writeonly.json"},
		{name: "outcome_assert_json", kind: grants, input: "writeonly.json", format: output.JSON},
		{name: "outcome_assert_yaml", kind: grants, input: "writeonly.json", format: output.YAML},
		{name: "candidate_assert", kind: reviews, input: "selfreview.json"},
		{name: "enum", kind: tiers, input: "tier_standard.json"},
		{name: "enum_json", kind: tiers, input: "tier_standard.json", format: output.JSON},
		{name: "enum_yaml", kind: tiers, input: "tier_standard.json", format: output.YAML},
		{name: "enum_outside_the_set", kind: tiers, input: "tier_typo.json"},
		{name: "kind_among_paths", input: "admin.json", paths: []string{"testdata/access.sigil", "testdata/access"}},
		{name: "two_kinds", input: "grants.json", policy: "grants", paths: []string{"testdata/access.sigil", "testdata/access", "testdata/grants.sigil", "testdata/grants"}},
		{name: "stdin_bundle", input: "admin.json", paths: []string{"-"}, stdin: bundleOf(t, "testdata/access.sigil", "testdata/access/main.sigil")},
		{name: "unrelated_error", input: "admin.json", policy: "access.main", paths: []string{"testdata/access.sigil", "testdata/access", "-"}, stdin: broken},
		{name: "root_error", input: "admin.json", policy: "access.other", paths: []string{"testdata/access.sigil", "testdata/access", "-"}, stdin: broken},
		{name: "kind_error", input: "admin.json", policy: "access.main", paths: []string{"testdata/access.sigil", "testdata/access", "-"}, stdin: brokenKind},
		{name: "stub_flag", kind: access, input: "vault.json", stub: []string{"owner=ada"}},
		{name: "stubs_file", kind: access, input: "vault.json", stubs: "owner.yaml"},
		{name: "stub_flag_after_file", kind: access, input: "vault.json", stubs: "owner.yaml", stub: []string{"owner=ada", "owner=bob"}},
		{name: "stub_error", kind: access, input: "vault.json", stubs: "down.yaml"},
		{name: "stub_unmatched", kind: access, input: "vault.json", stubs: "only_calls.json"},
		{name: "stubs_invalid", kind: access, input: "vault.json", stubs: "invalid.yaml", stub: []string{"owner"}},
		{name: "stubs_unknown", kind: access, input: "vault.json", stubs: "unknown.yaml", stub: []string{"owner=[1]"}},
		{name: "stubs_line_order", kind: access, input: "vault.json", stubs: "order.yaml"},
		{name: "stubs_missing", kind: access, input: "vault.json", stubs: "nope.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := tt.format
			if format == "" {
				format = output.Text
			}
			input := tt.input
			if input != "-" && input != "" {
				input = filepath.Join("testdata", "inputs", input)
			}
			kindFile := tt.kindFile
			if kindFile == "" {
				kindFile = tt.kind
			}
			src := project.Sources{Paths: []string{filepath.Join("testdata", tt.kind)}, Kinds: []string{filepath.Join("testdata", kindFile+".sigil")}, Stdin: strings.NewReader(tt.stdin)}
			if tt.paths != nil {
				src.Paths, src.Kinds = tt.paths, nil
			}
			stubs := tt.stubs
			if stubs != "" {
				stubs = filepath.Join("testdata", "stubs", stubs)
			}
			var out bytes.Buffer
			err := run(context.Background(), &out, &options{output: &format}, request{src: src, input: input, policy: tt.policy, stubs: stubs, stub: tt.stub})
			golden(t, tt.name, render(out.String(), err))
		})
	}
}

// TestEvalErrors covers the failures that happen before anything is
// evaluated.
func TestEvalErrors(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		input    string
		paths    []string
		stdin    string
		terminal bool
		wantErr  string
	}{
		{name: "input and bundle from stdin", kind: "testdata/access.sigil", input: "-", paths: []string{"-"}, wantErr: "can't both come from stdin"},
		{name: "several policies", kind: "testdata/access.sigil", input: "testdata/inputs/admin.json", paths: []string{"testdata/access", "testdata/multi.sigil"}, wantErr: "the bundle holds several policies"},
		{name: "no policies", kind: "testdata/grants.sigil", input: "testdata/inputs/grants.json", paths: []string{"testdata/grants.sigil"}, wantErr: "the bundle holds no policies"},
		{name: "missing input", kind: "testdata/access.sigil", input: "testdata/inputs/nope.json", paths: []string{"testdata/access"}, wantErr: "the input couldn't be read"},
		{name: "missing kind", kind: "testdata/nope.sigil", input: "testdata/inputs/admin.json", paths: []string{"testdata/access"}, wantErr: "the kind file couldn't be read"},
		{name: "wrong kind", kind: "testdata/grants.sigil", input: "testdata/inputs/admin.json", paths: []string{"testdata/access"}, wantErr: "the bundle doesn't check"},
		{name: "no input on a terminal", kind: "testdata/access.sigil", paths: []string{"testdata/access"}, terminal: true, wantErr: "--input is required when stdin is a terminal"},
		{name: "no input with the bundle on stdin", kind: "testdata/access.sigil", paths: []string{"-"}, wantErr: "--input is required when the bundle comes from stdin"},
		{name: "empty stdin", kind: "testdata/access.sigil", paths: []string{"testdata/access"}, stdin: " \n", wantErr: "the input from stdin is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := output.Text
			src := project.Sources{Paths: tt.paths, Kinds: []string{tt.kind}, Stdin: strings.NewReader(tt.stdin)}
			err := run(context.Background(), &bytes.Buffer{}, &options{output: &format}, request{src: src, input: tt.input, terminal: tt.terminal})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("run() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestCurrentDirectory checks that without paths, eval reads the working
// directory, every level of it.
func TestCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"access.sigil", "access/main.sigil", "inputs/admin.json"} {
		src, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	format := output.Text
	var out bytes.Buffer
	if err := run(context.Background(), &out, &options{output: &format}, request{input: "inputs/admin.json"}); err != nil {
		t.Fatalf("run() = %v", err)
	}
	if !strings.HasPrefix(out.String(), "access.main: allow(reason: admin)") {
		t.Errorf("output = %q, want access.main's decision", out.String())
	}
}

// bundleOf joins files into one self-contained bundle, the way a
// ConfigMap key holds the kind and its policies.
func bundleOf(t *testing.T, files ...string) string {
	t.Helper()
	docs := make([]string, len(files))
	for i, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		docs[i] = string(src)
	}
	return strings.Join(docs, "\n---\n")
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

// TestIsTerminal checks that neither a reader nor a regular file counts
// as a terminal, so eval reads them as the input.
func TestIsTerminal(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "inputs", "admin.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if isTerminal(strings.NewReader("")) || isTerminal(f) {
		t.Error("isTerminal() = true for a reader or a regular file")
	}
}

// TestConfigKinds checks that eval loads the kind files the nearest
// sigil.yaml, or the one --config names, lists under kinds:, relative to
// that file.
func TestConfigKinds(t *testing.T) {
	tests := []struct {
		name    string
		config  string // sigil.yaml, one level above the policies
		flag    string // --config, relative to the policies
		wantErr string
	}{
		{name: "nearest", config: "kinds: [vendor/access.sigil]\n"},
		{name: "named", config: "kinds: vendor/access.sigil\n", flag: "../sigil.yaml"},
		{name: "no kind", config: "lints: {}\n", wantErr: "no kind Access was found"},
		{name: "missing kind file", config: "kinds: [vendor/nope.sigil]\n", wantErr: "../sigil.yaml: the kind file ../vendor/nope.sigil can't be read"},
		{name: "invalid", config: "kinds: [vendor/access.sigil\n", wantErr: "isn't valid YAML"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{
				"sigil.yaml":              tt.config,
				"vendor/access.sigil":     read(t, "testdata/access.sigil"),
				"policies/main.sigil":     read(t, "testdata/access/main.sigil"),
				"policies/inputs/in.json": read(t, "testdata/inputs/admin.json"),
			})
			t.Chdir(filepath.Join(dir, "policies"))
			format := output.Text
			var out bytes.Buffer
			err := run(context.Background(), &out, &options{output: &format}, request{input: "inputs/in.json", config: tt.flag})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("run() = %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("run() = %v, want %q", err, tt.wantErr)
			case tt.wantErr == "" && !strings.HasPrefix(out.String(), "access.main: allow(reason: admin)"):
				t.Errorf("output = %q, want access.main's decision", out.String())
			}
		})
	}
}

// read returns a testdata file's contents.
func read(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

// writeFiles creates files under dir, by slash-separated names.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
