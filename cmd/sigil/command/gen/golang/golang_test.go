package golang

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/output"
)

const (
	gateKind = "kind Gate version 1\n\ninput name: string\n\ndecision deny {\n  reason: no_rule_matched\n}\n\ncollect one\nprecedence deny\n\ndefault deny(reason: no_rule_matched)\n"
	// orphanKind declares a struct type nothing uses, which Go can't
	// declare.
	orphanKind = "kind Gate version 1\n\ntype Orphan {\n  x: int\n}\n\ninput name: string\n\ndecision deny {\n  reason: no_rule_matched\n}\n\ncollect one\nprecedence deny\n\ndefault deny(reason: no_rule_matched)\n"
	brokenKind = "kind Gate version 1\n\ninput name: strin\n"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// failingReader is stdin that can't be read.
type failingReader struct{}

// TestGen covers printing, writing and checking the generated code, and
// every way a run fails.
func TestGen(t *testing.T) {
	code := generate(t, gateKind, "")
	tests := []struct {
		name    string
		kind    string // the kind file's content; "-" for none
		stdin   string // read for KIND_FILE -
		path    string // KIND_FILE, relative to the test's directory
		pkg     string
		out     string // --out, relative to the test's directory; "" for none
		file    string // existing content of the --out file; "-" for none
		check   bool
		want    string // printed, or the --out file's content afterwards
		status  string // the status line printed with --out
		wantErr string
	}{
		{name: "print", kind: gateKind, path: "gate.sigil", want: code},
		{name: "print from stdin", kind: "-", stdin: gateKind, path: "-", want: code},
		{name: "print another package", kind: gateKind, path: "gate.sigil", pkg: "approval", want: strings.Replace(code, "package gate\n", "package approval\n", 1)},
		{name: "write a new file and its directory", kind: gateKind, path: "gate.sigil", out: "gate/kind.go", file: "-", want: code, status: "✓ wrote gate/kind.go"},
		{name: "rewrite a stale file", kind: gateKind, path: "gate.sigil", out: "kind.go", file: "package gate\n", want: code, status: "✓ wrote kind.go"},
		{name: "leave a current file", kind: gateKind, path: "gate.sigil", out: "kind.go", file: code, want: code, status: "✓ kind.go is up to date"},
		{name: "check a current file", kind: gateKind, path: "gate.sigil", out: "kind.go", file: code, check: true, want: code, status: "✓ kind.go is up to date"},
		{name: "check a stale file", kind: gateKind, path: "gate.sigil", out: "kind.go", file: "stale\n", check: true, want: "stale\n", status: "✗ kind.go is stale", wantErr: "kind.go is stale: it doesn't match the code gate.sigil generates"},
		{name: "check a missing file", kind: gateKind, path: "gate.sigil", out: "kind.go", file: "-", check: true, status: "✗ kind.go is stale", wantErr: "is stale"},
		{name: "check without a file", kind: gateKind, path: "gate.sigil", check: true, wantErr: "--check needs the file to compare"},
		{name: "a package that isn't an identifier", kind: gateKind, path: "gate.sigil", pkg: "my-pkg", wantErr: `--package: package name "my-pkg" isn't a Go identifier`},
		{name: "a package that is a keyword", kind: gateKind, path: "gate.sigil", pkg: "func", wantErr: `--package: package name "func" is a Go keyword`},
		{name: "out is a directory", kind: gateKind, path: "gate.sigil", out: ".", wantErr: "--out . is a directory"},
		{name: "a missing kind file", kind: "-", path: "nope.sigil", wantErr: "nope.sigil can't be read"},
		{name: "a kind file that doesn't load", kind: brokenKind, path: "gate.sigil", wantErr: "the kind file doesn't load, so no code was generated"},
		{name: "a kind Go can't declare", kind: orphanKind, path: "gate.sigil", wantErr: "Go can't declare the kind as the file writes it"},
		{name: "a directory that can't be created", kind: gateKind, path: "gate.sigil", out: "gate.sigil/kind.go", wantErr: "the directory of gate.sigil/kind.go couldn't be created"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if tt.kind != "-" {
				writeFile(t, tt.path, tt.kind)
			}
			if tt.out != "" && tt.file != "-" && tt.file != "" {
				writeFile(t, tt.out, tt.file)
			}
			var stdout bytes.Buffer
			r := request{path: tt.path, pkg: tt.pkg, out: tt.out, check: tt.check}
			err := run(&stdout, strings.NewReader(tt.stdin), r, output.Text)
			switch {
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("run() = %v, want %q", err, tt.wantErr)
			case tt.wantErr == "" && err != nil:
				t.Fatalf("run() = %v", err)
			}
			got := stdout.String()
			if tt.out != "" && tt.status != "" {
				if !strings.HasPrefix(got, tt.status) {
					t.Errorf("printed %q with --out, want a status line starting with %q", got, tt.status)
				}
				src, _ := os.ReadFile(tt.out)
				got = string(src)
			}
			if got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestGenUnwritable covers an --out file in a directory gen go can't
// write to.
func TestGenUnwritable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, "gate.sigil", gateKind)
	if err := os.Mkdir("ro", 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "ro"), 0o700) })
	err := run(&bytes.Buffer{}, strings.NewReader(""), request{path: "gate.sigil", out: "ro/kind.go"}, output.Text)
	if err == nil || !strings.Contains(err.Error(), "ro/kind.go couldn't be written") {
		t.Fatalf("run() = %v, want the file not to be written", err)
	}
}

// TestGenStdinFails covers a kind file that can't be read from stdin.
func TestGenStdinFails(t *testing.T) {
	err := run(&bytes.Buffer{}, failingReader{}, request{path: "-"}, output.Text)
	if err == nil || !strings.Contains(err.Error(), "the kind file couldn't be read from stdin") {
		t.Fatalf("run() = %v, want stdin not to be read", err)
	}
}

// TestGenStructured covers JSON and YAML output: the code alone when
// printing, the --out file's status when writing or checking, and the
// diagnostics of a kind file Go can't declare.
func TestGenStructured(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	code := generate(t, gateKind, "")
	tests := []struct {
		name    string
		format  output.Format
		kind    string
		out     bool
		file    string // existing content of the --out file; "-" for none
		check   bool
		wantErr string
	}{
		{name: "print_json", format: output.JSON, kind: gateKind},
		{name: "print_yaml", format: output.YAML, kind: gateKind},
		{name: "written_json", format: output.JSON, kind: gateKind, out: true, file: "-"},
		{name: "current_yaml", format: output.YAML, kind: gateKind, out: true, file: code, check: true},
		{name: "stale_json", format: output.JSON, kind: gateKind, out: true, file: "stale\n", check: true, wantErr: "kind.go is stale"},
		{name: "diagnostics_json", format: output.JSON, kind: orphanKind, wantErr: "Go can't declare the kind"},
		{name: "diagnostics_yaml", format: output.YAML, kind: brokenKind, wantErr: "the kind file doesn't load"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeFile(t, "gate.sigil", tt.kind)
			r := request{path: "gate.sigil", check: tt.check}
			if tt.out {
				r.out = "kind.go"
				if tt.file != "-" {
					writeFile(t, r.out, tt.file)
				}
			}
			var stdout bytes.Buffer
			err := run(&stdout, strings.NewReader(""), r, tt.format)
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

// Read implements io.Reader. It always fails.
func (failingReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }

// generate returns what gen go prints for the kind file src.
func generate(t *testing.T, src, pkg string) string {
	t.Helper()
	t.Chdir(t.TempDir())
	writeFile(t, "gate.sigil", src)
	var stdout bytes.Buffer
	if err := run(&stdout, strings.NewReader(""), request{path: "gate.sigil", pkg: pkg}, output.Text); err != nil {
		t.Fatal(err)
	}
	return stdout.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
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
