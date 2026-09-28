package export

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/pkg/policy"
)

type (
	input struct {
		Name string `policy:"name"`
	}
	other struct {
		Team string `policy:"team"`
	}
)

var (
	deny  = policy.NewDecision[policy.None]("deny", "no_rule_matched")
	gate  = policy.NewKind[input]("Gate", policy.WithVersion(1), policy.WithDecisions(deny), policy.WithDefault(deny, "no_rule_matched"))
	teams = policy.NewKind[other]("Teams", policy.WithVersion(3), policy.WithDecisions(deny), policy.WithDefault(deny, "no_rule_matched"))
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestExport covers printing, writing and checking the kind file.
func TestExport(t *testing.T) {
	one := []project.Linked{link(gate)}
	both := []project.Linked{link(gate), link(teams)}
	tests := []struct {
		name    string
		kinds   []project.Linked
		kind    string
		file    string // existing content of the --out file; "-" for no --out
		check   bool
		want    string // printed, or the file's content afterwards
		wantErr string
	}{
		{name: "print", kinds: one, file: "-", want: gate.Schema()},
		{name: "print by name", kinds: both, kind: "Teams", file: "-", want: teams.Schema()},
		{name: "write a new file", kinds: one, want: gate.Schema()},
		{name: "rewrite a stale file", kinds: one, file: "kind Gate version 0\n", want: gate.Schema()},
		{name: "check a current file", kinds: one, file: gate.Schema(), check: true, want: gate.Schema()},
		{name: "check a stale file", kinds: one, file: "stale\n", check: true, want: "stale\n", wantErr: "is stale"},
		{name: "check without a file", kinds: one, file: "-", check: true, wantErr: "--check needs the file to compare"},
		{name: "no kind linked", file: "-", wantErr: "no kind is linked into this binary"},
		{name: "several kinds need a name", kinds: both, file: "-", wantErr: "this binary links several kinds"},
		{name: "unknown kind", kinds: both, kind: "Nope", file: "-", wantErr: "no kind Nope is linked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			out := ""
			if tt.file != "-" {
				out = filepath.Join(t.TempDir(), "kind.sigil")
				if tt.file != "" {
					if err := os.WriteFile(out, []byte(tt.file), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := run(&stdout, tt.kinds, tt.kind, out, tt.check, output.Text)
			switch {
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("run() = %v, want %q", err, tt.wantErr)
			case tt.wantErr == "" && err != nil:
				t.Fatalf("run() = %v", err)
			}
			got := stdout.String()
			if out != "" {
				// With --out, stdout carries one status line and the
				// kind goes to the file.
				mark := "✓ "
				if tt.wantErr != "" {
					mark = "✗ "
				}
				if !strings.HasPrefix(got, mark) {
					t.Errorf("printed %q with --out, want a status line starting with %q", got, mark)
				}
				src, _ := os.ReadFile(out)
				got = string(src)
			}
			if got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func link[In any](k *policy.Kind[In]) project.Linked {
	c := k.Contract()
	return project.Linked{Model: c.Model, Binding: c.Binding}
}

// TestExportStructured covers JSON and YAML output: the kind alone when
// printing, and the --out file's status too when writing or checking.
func TestExportStructured(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	one := []project.Linked{link(gate)}
	tests := []struct {
		name    string
		format  output.Format
		file    string // existing content of the --out file; "-" for no --out
		check   bool
		wantErr string
	}{
		{name: "print_json", format: output.JSON, file: "-"},
		{name: "print_yaml", format: output.YAML, file: "-"},
		{name: "written_json", format: output.JSON},
		{name: "current_json", format: output.JSON, file: gate.Schema(), check: true},
		{name: "stale_json", format: output.JSON, file: "stale\n", check: true, wantErr: "kind.sigil is stale"},
		{name: "stale_yaml", format: output.YAML, file: "stale\n", check: true, wantErr: "kind.sigil is stale"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			out := ""
			if tt.file != "-" {
				out = "kind.sigil"
				if tt.file != "" {
					if err := os.WriteFile(out, []byte(tt.file), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			var stdout bytes.Buffer
			err := run(&stdout, one, "", out, tt.check, tt.format)
			switch {
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("run() = %v, want %q", err, tt.wantErr)
			case tt.wantErr == "" && err != nil:
				t.Fatalf("run() = %v", err)
			}
			golden(t, filepath.Join(testdata, tt.name+".golden"), stdout.String())
		})
	}
}

func golden(t *testing.T, path, got string) {
	t.Helper()
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
