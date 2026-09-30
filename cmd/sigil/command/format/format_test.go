package format

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

const (
	messy = "policy a: K@1\nwhen x==1{deny( r )}\n"
	tidy  = "policy a: K@1\n\nwhen x == 1 { deny(reason: r) }\n"
	// hunk is the diff's hunk from messy to tidy.
	hunk = "@@ -1,2 +1,3 @@\n policy a: K@1\n-when x==1{deny( r )}\n+\n+when x == 1 { deny(reason: r) }\n"
	// broken is a file that doesn't parse, and brokenReport what fmt
	// prints for it as a.sigil.
	broken       = "policy a: K@1\nlet = 1\n"
	brokenReport = "a.sigil:2:5: error: expected a name after `let`, found `=`\n" +
		"  |\n" +
		"2 | let = 1\n" +
		"  |     ^\n" +
		"  = help: a let is written `let name = expression`\n\n" +
		"✗ 1 file has syntax errors\n"
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
			name:  "diff a file on request",
			files: map[string]string{"a.sigil": messy},
			paths: []string{"a.sigil"},
			mode:  modeDiff,
			out:   "--- a.sigil.orig\n+++ a.sigil\n" + hunk + "ℹ 1 of 1 file would be reformatted\n  run `sigil fmt -w` on the same paths to rewrite it\n",
		},
		{
			name:  "diff stdin on request",
			paths: []string{"-"},
			stdin: messy,
			mode:  modeDiff,
			out:   "--- <stdin>.orig\n+++ <stdin>\n" + hunk + "ℹ 1 of 1 file would be reformatted\n  run `sigil fmt -w` on the same paths to rewrite it\n",
		},
		{
			name:  "a directory prints a diff of the files that would change",
			files: map[string]string{"a.sigil": messy, "b.sigil": tidy, "c/d.sigil": messy},
			paths: []string{"."},
			out: "--- a.sigil.orig\n+++ a.sigil\n" + hunk +
				"--- c/d.sigil.orig\n+++ c/d.sigil\n" + hunk +
				"ℹ 2 of 3 files would be reformatted\n  run `sigil fmt -w` on the same paths to rewrite them\n",
		},
		{
			name:  "several files print a diff",
			files: map[string]string{"a.sigil": messy, "b.sigil": tidy},
			paths: []string{"a.sigil", "b.sigil"},
			out:   "--- a.sigil.orig\n+++ a.sigil\n" + hunk + "ℹ 1 of 2 files would be reformatted\n  run `sigil fmt -w` on the same paths to rewrite it\n",
		},
		{
			name:  "a file and stdin print a diff",
			files: map[string]string{"a.sigil": tidy},
			paths: []string{"a.sigil", "-"},
			stdin: messy,
			out:   "--- <stdin>.orig\n+++ <stdin>\n" + hunk + "ℹ 1 of 2 files would be reformatted\n  run `sigil fmt -w` on the same paths to rewrite it\n",
		},
		{
			name:  "a directory of formatted files diffs nothing",
			files: map[string]string{"a.sigil": tidy, "b.sigil": tidy},
			paths: []string{"."},
			out:   "✓ 2 files are already formatted\n",
		},
		{
			name:  "a diff stops at a file that doesn't parse",
			files: map[string]string{"a.sigil": broken, "b.sigil": messy},
			paths: []string{"."},
			out:   "--- b.sigil.orig\n+++ b.sigil\n" + hunk + brokenReport,
			err:   "1 file has syntax errors",
		},
		{
			name:  "no files in a directory warns",
			files: map[string]string{"notes.txt": "not sigil"},
			out:   "! no .sigil files found, so nothing was formatted\n  name the files, or a directory that holds them\n",
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
			mode:  modePrint,
			out:   tidy + tidy,
		},
		{
			name:  "no paths diffs the current directory",
			files: map[string]string{"a.sigil": messy},
			out:   "--- a.sigil.orig\n+++ a.sigil\n" + hunk + "ℹ 1 of 1 file would be reformatted\n  run `sigil fmt -w` on the same paths to rewrite it\n",
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
			name:  "a file named twice is read once",
			files: map[string]string{"a.sigil": messy},
			paths: []string{"a.sigil", "./a.sigil", "."},
			mode:  modeCheck,
			out:   "a.sigil\n✗ 1 of 1 file is not formatted\n",
			err:   "1 file is not formatted",
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
			files: map[string]string{"a.sigil": broken, "b.sigil": messy},
			paths: []string{"."},
			mode:  modeWrite,
			out:   brokenReport,
			after: map[string]string{"a.sigil": broken, "b.sigil": tidy},
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
			files: map[string]string{"a.sigil": broken, "b.sigil": messy},
			paths: []string{"."},
			mode:  modePrint,
			out:   tidy + brokenReport,
			err:   "1 file has syntax errors",
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

// TestPaint colors a diff for a terminal line by line, and leaves it as
// it is off one. A removed line that starts with "-- " is a removal, not
// a header.
func TestPaint(t *testing.T) {
	d := "--- a.sigil.orig\n+++ a.sigil\n@@ -1,2 +1,2 @@\n policy a: K@1\n--- old\n+-- new\n\\ No newline at end of file"
	tty := pretty.New(&bytes.Buffer{}, pretty.WithEnviron([]string{"CLICOLOR_FORCE=1", "TERM=xterm-256color"}), pretty.WithDarkBackground(true)).Theme()
	want := strings.Join([]string{
		tty.Bold("--- a.sigil.orig"),
		tty.Bold("+++ a.sigil"),
		tty.Info("@@ -1,2 +1,2 @@"),
		" policy a: K@1",
		tty.Fail("--- old"),
		tty.Ok("+-- new"),
		`\ No newline at end of file`,
	}, "\n")
	if got := paint(tty, d); got != want {
		t.Errorf("paint() on a terminal = %q, want %q", got, want)
	}
	if tty.Fail("--- old") == "--- old" {
		t.Fatal("the terminal theme doesn't style, so the test proves nothing")
	}
	plain := pretty.New(&bytes.Buffer{}, pretty.WithEnviron(nil)).Theme()
	if got := paint(plain, d); got != d {
		t.Errorf("paint() off a terminal = %q, want the diff unchanged", got)
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

// grant is an unformatted policy whose changes are far enough apart to
// make two hunks in a diff.
const grant = `policy access.main: AccessGrant@1

use access.guardrails
guardrails()

when team_member {
  reader(reason: team_member)
  deployer(reason: team_member)
}

when environment == "staging" {
  reader(reason: everyone_in_staging)
}

when on_call {
  deployer(reason: oncall, ttl: 2h)
}

when admin_cleared {admin( reason: clearance )}
`

// TestRunStructured runs fmt over a tree with a formatted and an
// unformatted file, and a broken one or a longer policy where the case
// says, and compares what it prints with the golden files: JSON and YAML
// in every mode, and the text of the diff mode, which is the default for
// a directory.
func TestRunStructured(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mode   mode
		format output.Format
		paths  []string // nil formats the tree's root
		broken bool
		long   bool // add grant as d.sigil
		err    string
	}{
		{name: "diff_text", format: output.Text, long: true},
		{name: "diff_json", format: output.JSON},
		{name: "diff_yaml", format: output.YAML},
		{name: "auto_file_json", format: output.JSON, paths: []string{"a.sigil"}},
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
			if tt.long {
				tree["d.sigil"] = grant
			}
			dir := t.TempDir()
			for name, src := range tree {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(dir)

			var out bytes.Buffer
			err := run(&out, strings.NewReader(""), tt.paths, tt.mode, tt.format)
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

// TestFlags checks that each flag picks its mode, that no flag leaves the
// choice to the paths, and that the mode flags exclude each other.
func TestFlags(t *testing.T) {
	const diffA = "--- a.sigil.orig\n+++ a.sigil\n" + hunk
	tests := []struct {
		args []string
		out  string // the start of the output
		err  string
	}{
		{args: []string{"a.sigil"}, out: tidy},
		{args: []string{}, out: diffA},
		{args: []string{"-d", "a.sigil"}, out: diffA},
		{args: []string{"--diff", "-o", "json", "a.sigil"}, out: "[\n  {\n    \"file\": \"a.sigil\",\n    \"formatted\": false,\n    \"diff\": "},
		{args: []string{"--check", "a.sigil"}, out: "a.sigil\n", err: "1 file is not formatted"},
		{args: []string{"-w", "--diff"}, err: "none of the others can be"},
		{args: []string{"--check", "-d"}, err: "none of the others can be"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "a.sigil"), []byte(messy), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)

			format := output.Text
			cmd := NewCommand(WithOutput(&format), WithOutput(nil))
			cmd.Flags().VarP(&format, "output", "o", "")
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			switch {
			case tt.err == "" && err != nil:
				t.Fatalf("Execute() error = %v", err)
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Fatalf("Execute() error = %v, want %q", err, tt.err)
			}
			if !strings.HasPrefix(out.String(), tt.out) {
				t.Errorf("output = %q, want it to start with %q", out.String(), tt.out)
			}
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
