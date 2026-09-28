package format

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/output"
)

const (
	messy = "policy a: K@1\nwhen x==1{deny( r )}\n"
	tidy  = "policy a: K@1\n\nwhen x == 1 { deny(r) }\n"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestRun runs fmt over a temporary tree in every mode.
func TestRun(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string // relative path to contents
		paths []string          // relative to the tree; nil formats the tree's root
		stdin string
		mode  mode
		out   string            // with every path relative to the tree
		after map[string]string // files expected after the run; nil means unchanged
		err   string
	}{
		{
			name:  "print a file",
			files: map[string]string{"a.sigil": messy},
			paths: []string{"a.sigil"},
			out:   tidy,
		},
		{
			name:  "print stdin",
			paths: []string{"-"},
			stdin: messy,
			out:   tidy,
		},
		{
			name: "directories recurse and skip dot entries and other files",
			files: map[string]string{
				"b.sigil":         tidy,
				"sub/a.sigil":     messy,
				".hidden/x.sigil": messy,
				".x.sigil":        messy,
				"notes.txt":       "not sigil",
			},
			paths: []string{"."},
			out:   tidy + tidy,
		},
		{
			name:  "no paths formats the current directory",
			files: map[string]string{"a.sigil": messy},
			out:   tidy,
		},
		{
			name:  "check lists unformatted files and fails",
			files: map[string]string{"a.sigil": messy, "b.sigil": tidy, "c/d.sigil": messy},
			paths: []string{"."},
			mode:  modeCheck,
			out:   "a.sigil\nc/d.sigil\n✗ 2 of 3 files are not formatted\n",
			err:   "2 files are not formatted",
		},
		{
			name:  "check passes on formatted files",
			files: map[string]string{"a.sigil": tidy},
			paths: []string{"."},
			mode:  modeCheck,
			out:   "✓ 1 file is formatted\n",
		},
		{
			name:  "check names stdin",
			paths: []string{"-"},
			stdin: messy,
			mode:  modeCheck,
			out:   "<stdin>\n✗ 1 of 1 file is not formatted\n",
			err:   "1 file is not formatted",
		},
		{
			name:  "write rewrites only changed files",
			files: map[string]string{"a.sigil": messy, "b.sigil": tidy},
			paths: []string{"."},
			mode:  modeWrite,
			out:   "✓ reformatted 1 file, 1 left unchanged\n  a.sigil\n",
			after: map[string]string{"a.sigil": tidy, "b.sigil": tidy},
		},
		{
			name:  "write reports files already formatted",
			files: map[string]string{"a.sigil": tidy},
			paths: []string{"."},
			mode:  modeWrite,
			out:   "✓ 1 file is already formatted\n",
		},
		{
			name:  "write refuses stdin",
			paths: []string{"-"},
			mode:  modeWrite,
			err:   "--write can't write back to stdin",
		},
		{
			name:  "a file that doesn't parse is reported and left alone",
			files: map[string]string{"a.sigil": "policy a: K@1\nlet = 1\n", "b.sigil": messy},
			paths: []string{"."},
			mode:  modeWrite,
			out: "a.sigil:2:5: error: expected a name after `let`, found `=`\n" +
				"  |\n" +
				"2 | let = 1\n" +
				"  |     ^\n" +
				"  = help: a let is written `let name = expression`\n\n" +
				"✗ 1 file has syntax errors\n",
			after: map[string]string{"a.sigil": "policy a: K@1\nlet = 1\n", "b.sigil": tidy},
			err:   "1 file has syntax errors",
		},
		{
			name:  "a missing path",
			paths: []string{"nope.sigil"},
			err:   "nope.sigil can't be read",
		},
		{
			name:  "no files warns",
			files: map[string]string{"notes.txt": "not sigil"},
			paths: []string{"."},
			mode:  modeCheck,
			out:   "! no .sigil files found, so nothing was formatted\n  name the files, or a directory that holds them\n",
		},
		{
			name:  "printing stops at a file that doesn't parse",
			files: map[string]string{"a.sigil": "policy a: K@1\nlet = 1\n", "b.sigil": messy},
			paths: []string{"."},
			out: tidy +
				"a.sigil:2:5: error: expected a name after `let`, found `=`\n" +
				"  |\n" +
				"2 | let = 1\n" +
				"  |     ^\n" +
				"  = help: a let is written `let name = expression`\n\n" +
				"✗ 1 file has syntax errors\n",
			err: "1 file has syntax errors",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range tt.files {
				p := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(dir)

			var out bytes.Buffer
			err := run(&out, strings.NewReader(tt.stdin), tt.paths, tt.mode, output.Text)
			switch {
			case tt.err == "" && err != nil:
				t.Fatalf("run: %v", err)
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Fatalf("run() error = %v, want %q", err, tt.err)
			}
			if out.String() != tt.out {
				t.Errorf("output:\n%s\nwant:\n%s", out.String(), tt.out)
			}
			after := tt.after
			if after == nil {
				after = tt.files
			}
			for name, want := range after {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Errorf("%s:\n%s\nwant:\n%s", name, got, want)
				}
			}
		})
	}
}

// TestRewriteKeepsMode checks that --write keeps a file's permissions.
func TestRewriteKeepsMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.sigil")
	if err := os.WriteFile(p, []byte(messy), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := run(&bytes.Buffer{}, strings.NewReader(""), []string{p}, modeWrite, output.Text); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("mode = %v, want 0640", got)
	}
}

// TestRunStructured runs fmt with JSON and YAML output over a tree with a
// formatted and an unformatted file, and a broken one where the case says,
// and compares what it prints with the golden files.
func TestRunStructured(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mode   mode
		format output.Format
		broken bool
		err    string
	}{
		{name: "print_json", mode: modePrint, format: output.JSON},
		{name: "print_yaml", mode: modePrint, format: output.YAML},
		{name: "check_json", mode: modeCheck, format: output.JSON, err: "1 file is not formatted"},
		{name: "check_yaml", mode: modeCheck, format: output.YAML, err: "1 file is not formatted"},
		{name: "write_json", mode: modeWrite, format: output.JSON},
		{name: "broken_json", mode: modeCheck, format: output.JSON, broken: true, err: "1 file has syntax errors"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := map[string]string{"a.sigil": messy, "b.sigil": tidy}
			if tt.broken {
				tree["c.sigil"] = "policy c: K@1\nlet = 1\n"
			}
			dir := t.TempDir()
			for name, src := range tree {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(dir)

			var out bytes.Buffer
			err := run(&out, strings.NewReader(""), nil, tt.mode, tt.format)
			switch {
			case tt.err == "" && err != nil:
				t.Fatalf("run: %v", err)
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Fatalf("run() error = %v, want %q", err, tt.err)
			}
			golden(t, filepath.Join(testdata, tt.name+".golden"), out.String())
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
