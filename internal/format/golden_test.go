package format

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/diag"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestGolden formats every testdata/*.sigil file and compares the result
// with the matching .golden file. Run with -update to accept changes;
// review the diff, since the golden files are the canonical style.
func TestGolden(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "*.sigil"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no testdata/*.sigil files")
	}

	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".sigil")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := golden(filepath.Base(path), src)

			goldenPath := strings.TrimSuffix(path, ".sigil") + ".golden"
			if *update {
				if werr := os.WriteFile(goldenPath, []byte(got), 0o644); werr != nil {
					t.Fatal(werr)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("output differs from %s (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", goldenPath, got, want)
			}
		})
	}
}

// golden renders what a golden file holds: the formatted source, or the
// diagnostics of a file that doesn't parse.
func golden(name string, src []byte) string {
	out, errs := Source(name, src)
	if errs == nil {
		return string(out)
	}
	var b strings.Builder
	b.WriteString("== diagnostics ==\n")
	for _, e := range errs {
		b.WriteString("\n")
		b.WriteString(diag.Render(e, src))
	}
	return b.String()
}
