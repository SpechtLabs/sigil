package breaking

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// onMain is the old kind file every case compares with, from the directory
// of the case, which holds the new one.
const onMain = "../main/deploy_approval.sigil"

// TestBreaking runs breaking over the kind file pairs under testdata,
// each the kind on main and a change to it, and compares what it prints,
// and the error it fails with, with the golden files.
func TestBreaking(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	mainSrc, err := os.ReadFile(filepath.Join(testdata, "main", "deploy_approval.sigil"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		dir    string // the case's directory under testdata; the name when empty
		format output.Format
		old    string // onMain when empty
		next   string // deploy_approval.sigil when empty
		stdin  string
		ok     bool
	}{
		{name: "compatible", ok: true},
		{name: "compatible_json", dir: "compatible", format: output.JSON, ok: true},
		{name: "breaking"},
		{name: "breaking_json", dir: "breaking", format: output.JSON},
		{name: "breaking_yaml", dir: "breaking", format: output.YAML},
		{name: "covered", ok: true},
		{name: "covered_json", dir: "covered", format: output.JSON, ok: true},
		{name: "unbumped"},
		{name: "backwards"},
		{name: "version_only", ok: true},
		{name: "renamed", ok: true},
		{name: "unchanged", dir: "main", old: "deploy_approval.sigil", ok: true},
		{name: "invalid"},
		{name: "invalid_json", dir: "invalid", format: output.JSON},
		{name: "invalid_old", dir: "compatible", old: "../invalid/deploy_approval.sigil"},
		{name: "stdin_old_json", dir: "breaking", format: output.JSON, old: "-", stdin: string(mainSrc)},
		{name: "stdin_new", dir: "main", old: "../covered/deploy_approval.sigil", next: "-", stdin: string(mainSrc)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, format, old, next := tt.dir, tt.format, tt.old, tt.next
			if dir == "" {
				dir = tt.name
			}
			if format == "" {
				format = output.Text
			}
			if old == "" {
				old = onMain
			}
			if next == "" {
				next = "deploy_approval.sigil"
			}
			t.Chdir(filepath.Join(testdata, dir))
			var out bytes.Buffer
			err := run(strings.NewReader(tt.stdin), &out, format, old, next)
			if (err == nil) != tt.ok {
				t.Fatalf("run() = %v, want ok %v", err, tt.ok)
			}
			got := out.String()
			if err != nil {
				// A failure the command reported is only its summary; one it
				// didn't, a kind file that doesn't load, is what the user sees.
				if !pretty.Reported(err) {
					got += "--- error ---\n" + err.Error() + "\n"
				} else if format != output.Text && out.Len() == 0 {
					t.Error("reported a failure as JSON or YAML, but printed nothing")
				}
			}
			golden(t, filepath.Join(testdata, tt.name+".golden"), got)
		})
	}
}

// TestBreakingErrors covers the failures before anything is compared.
func TestBreakingErrors(t *testing.T) {
	tests := []struct {
		name      string
		old, next string
		stdin     *failingReader
		want      string
		advice    string
	}{
		{name: "both from stdin", old: "-", next: "-", want: "OLD_KIND_FILE and NEW_KIND_FILE can't both come from stdin", advice: "git show"},
		{name: "a missing file", old: "nope.sigil", next: filepath.Join("testdata", "main", "deploy_approval.sigil"), want: "nope.sigil couldn't be read", advice: "path"},
		{name: "a missing new file", old: filepath.Join("testdata", "main", "deploy_approval.sigil"), next: "nope.sigil", want: "nope.sigil couldn't be read", advice: "path"},
		{name: "unreadable stdin", old: "-", next: filepath.Join("testdata", "main", "deploy_approval.sigil"), stdin: &failingReader{}, want: "stdin couldn't be read", advice: "pipe a kind file in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdin io.Reader = strings.NewReader("")
			if tt.stdin != nil {
				stdin = tt.stdin
			}
			var out bytes.Buffer
			err := run(stdin, &out, output.Text, tt.old, tt.next)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("run() = %v, want %q", err, tt.want)
			}
			if !strings.Contains(strings.Join(err.Advice(), "\n"), tt.advice) {
				t.Errorf("advice %q doesn't mention %q", err.Advice(), tt.advice)
			}
			if out.Len() != 0 {
				t.Errorf("printed %q, want nothing", out.String())
			}
		})
	}
}

// TestNewCommand checks that the command passes its arguments and the
// --output format through to run.
func TestNewCommand(t *testing.T) {
	format := output.JSON
	cmd := NewCommand(WithOutput(&format), WithOutput(nil))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(""))
	kindFile := filepath.Join("testdata", "main", "deploy_approval.sigil")
	cmd.SetArgs([]string{kindFile, kindFile})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if !strings.HasPrefix(out.String(), "{\n  \"old\": {") {
		t.Errorf("printed %q, want a JSON record", out.String())
	}
}

type failingReader struct{}

func (*failingReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }

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
