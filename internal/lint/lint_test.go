package lint_test

import (
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lint"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestGolden lints every testdata directory as one bundle and compares
// the rendered findings with the matching .golden file. A directory may
// hold its own kind.sigil, used instead of testdata/kind.sigil, and an
// options file with `level <lint> <off|warn|error>` and `require
// <policy>` lines. Run with -update to accept changes.
func TestGolden(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join("testdata", e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			got := golden(t, dir)
			goldenPath := dir + ".golden"
			if *update {
				if werr := os.WriteFile(goldenPath, []byte(got), 0o644); werr != nil {
					t.Fatal(werr)
				}
				return
			}
			want, rerr := os.ReadFile(goldenPath)
			if rerr != nil {
				t.Fatalf("%v (run with -update to create it)", rerr)
			}
			if got != string(want) {
				t.Errorf("output differs from %s (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", goldenPath, got, want)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in   string
		want lint.Level
		ok   bool
	}{
		{in: "off", want: lint.Off, ok: true},
		{in: "warn", want: lint.Warn, ok: true},
		{in: "error", want: lint.Error, ok: true},
		{in: "warning", want: lint.Off},
		{in: "", want: lint.Off},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := lint.ParseLevel(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Errorf("ParseLevel(%q) = %v, %v, want %v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
			if ok && got.String() != tt.in {
				t.Errorf("String() = %q, want %q", got.String(), tt.in)
			}
		})
	}
}

func TestNames(t *testing.T) {
	names := lint.Names()
	if len(names) != len(lint.All) || names[0] != lint.UnusedImport || names[len(names)-1] != lint.PathMatchesName {
		t.Errorf("Names() = %v", names)
	}
	for _, l := range lint.All {
		want := lint.Warn
		if l.Name == lint.QualifiedImports || l.Name == lint.PathMatchesName {
			want = lint.Off
		}
		if l.Default != want {
			t.Errorf("%s defaults to %v, want %v", l.Name, l.Default, want)
		}
	}
}

// golden lints one testdata directory and renders the findings, after
// the checker's errors if the bundle has any.
func golden(t *testing.T, dir string) string {
	t.Helper()
	kindFile := filepath.Join(dir, "kind.sigil")
	if _, err := os.Stat(kindFile); err != nil {
		kindFile = filepath.Join("testdata", "kind.sigil")
	}
	src, err := os.ReadFile(kindFile)
	if err != nil {
		t.Fatal(err)
	}
	k, errs := check.LoadKind(kindFile, src)
	if errs != nil {
		t.Fatalf("LoadKind: %v", errs)
	}
	b := bundle.New(k)
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sigil") || d.Name() == "kind.sigil" {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, path)
		b.Add(filepath.ToSlash(rel), data)
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	b.Check()
	o := options(t, dir)
	o.Kind = k

	var out strings.Builder
	if errs := b.Errors(); errs != nil {
		out.WriteString("== check errors ==\n")
		for _, e := range errs {
			out.WriteString(diag.Render(e, b.Sources[e.File]))
		}
		out.WriteString("\n")
	}
	findings := lint.Run(b, o)
	if len(findings) == 0 {
		out.WriteString("no findings\n")
	}
	for i, f := range findings {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(f.Level.String() + " " + f.Lint + " (" + f.Doc + ")\n")
		out.WriteString(diag.Render(f.Error, b.Sources[f.File]))
	}
	return out.String()
}

// options reads a directory's options file, if it has one.
func options(t *testing.T, dir string) lint.Options {
	t.Helper()
	o := lint.Options{Levels: map[string]lint.Level{}}
	data, err := os.ReadFile(filepath.Join(dir, "options"))
	if err != nil {
		return o
	}
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 3 && fields[0] == "level":
			lv, ok := lint.ParseLevel(fields[2])
			if !ok {
				t.Fatalf("options: bad level in %q", line)
			}
			o.Levels[fields[1]] = lv
		case len(fields) == 2 && fields[0] == "require":
			o.Required = append(o.Required, fields[1])
		case len(fields) > 0:
			t.Fatalf("options: can't read %q", line)
		}
	}
	return o
}
