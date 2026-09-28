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

	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestEval evaluates the testdata bundles against the testdata inputs
// and compares what eval prints, and the error it fails with, with the
// golden files.
func TestEval(t *testing.T) {
	const (
		access = "access"
		grants = "grants"
	)
	tests := []struct {
		name   string
		kind   string // access or grants
		input  string // under testdata/inputs, or "-" for stdin
		stdin  string
		policy string
		format output.Format
	}{
		{name: "winner", kind: access, input: "admin.json"},
		{name: "winner_json", kind: access, input: "admin.json", format: output.JSON},
		{name: "winner_yaml", kind: access, input: "admin.json", format: output.YAML},
		{name: "default", kind: access, input: "nobody.json"},
		{name: "assert", kind: access, input: "unnamed.json"},
		{name: "assert_json", kind: access, input: "unnamed.json", format: output.JSON},
		{name: "conflict", kind: access, input: "conflict.json"},
		{name: "unbound_function", kind: access, input: "vault.json"},
		{name: "unknown_field", kind: access, input: "typo.json"},
		{name: "duration_as_number", kind: access, input: "number_age.json"},
		{name: "invalid_json", kind: access, input: "broken.json"},
		{name: "stdin", kind: access, input: "-", stdin: `{"user": {"name": "cy", "teams": ["platform"]}}`},
		{name: "named_policy", kind: access, input: "admin.json", policy: "access.main"},
		{name: "unknown_policy", kind: access, input: "admin.json", policy: "access.nope"},
		{name: "collect", kind: grants, input: "grants.json"},
		{name: "collect_json", kind: grants, input: "grants.json", format: output.JSON},
		{name: "collect_empty", kind: grants, input: "nogrants.json"},
		{name: "collect_empty_json", kind: grants, input: "nogrants.json", format: output.JSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := tt.format
			if format == "" {
				format = output.Text
			}
			input := tt.input
			if input != "-" {
				input = filepath.Join("testdata", "inputs", input)
			}
			var out bytes.Buffer
			src := project.Sources{Paths: []string{filepath.Join("testdata", tt.kind)}, Stdin: strings.NewReader(tt.stdin)}
			err := run(context.Background(), &out, &options{output: &format}, filepath.Join("testdata", tt.kind+".sigil"), input, tt.policy, src)
			golden(t, tt.name, render(out.String(), err))
		})
	}
}

// TestEvalErrors covers the failures that happen before anything is
// evaluated.
func TestEvalErrors(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		input   string
		paths   []string
		wantErr string
	}{
		{name: "input and bundle from stdin", kind: "testdata/access.sigil", input: "-", paths: []string{"-"}, wantErr: "can't both come from stdin"},
		{name: "several policies", kind: "testdata/access.sigil", input: "testdata/inputs/admin.json", paths: []string{"testdata/access", "testdata/multi.sigil"}, wantErr: "the bundle holds several policies"},
		{name: "no policies", kind: "testdata/grants.sigil", input: "testdata/inputs/grants.json", paths: []string{"testdata/grants.sigil"}, wantErr: "the bundle holds no policies"},
		{name: "missing input", kind: "testdata/access.sigil", input: "testdata/inputs/nope.json", paths: []string{"testdata/access"}, wantErr: "the input couldn't be read"},
		{name: "missing kind", kind: "testdata/nope.sigil", input: "testdata/inputs/admin.json", paths: []string{"testdata/access"}, wantErr: "the kind file couldn't be read"},
		{name: "wrong kind", kind: "testdata/grants.sigil", input: "testdata/inputs/admin.json", paths: []string{"testdata/access"}, wantErr: "document is for kind Access, not Grants"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := output.Text
			src := project.Sources{Paths: tt.paths, Stdin: strings.NewReader("")}
			err := run(context.Background(), &bytes.Buffer{}, &options{output: &format}, tt.kind, tt.input, "", src)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("run() = %v, want %q", err, tt.wantErr)
			}
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
