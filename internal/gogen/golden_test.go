package gogen_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gogen"
)

// goldenDir holds the generated code of every testdata kind file as a Go
// package of its own, so `go build ./...` type-checks it and the round
// trip tests import it.
const goldenDir = "internal/golden"

var update = flag.Bool("update", false, "rewrite the golden packages and files")

// TestGolden generates every testdata/*.sigil kind file and compares the
// code with internal/golden/<package>/kind.go, the package the round trip
// tests build the kind from. A golden package that's stale against the
// generator fails here; -update rewrites it.
func TestGolden(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "*.sigil"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no testdata/*.sigil files")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src := read(t, path)
			code, k, errs := gogen.GenerateFile(filepath.Base(path), src, gogen.Options{})
			if errs != nil {
				t.Fatal(diag.RenderAll(errs, func(string) []byte { return src }, diag.Plain))
			}
			compare(t, filepath.Join(goldenDir, gogen.PackageName(k.Name), "kind.go"), string(code))
		})
	}
}

// TestGoldenErrors generates every testdata/errors/*.sigil kind file,
// which Go can't declare, and compares the rendered diagnostics with the
// .golden file beside it.
func TestGoldenErrors(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "errors", "*.sigil"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no testdata/errors/*.sigil files")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src := read(t, path)
			code, _, errs := gogen.GenerateFile(filepath.Base(path), src, gogen.Options{})
			if errs == nil || code != nil {
				t.Fatalf("GenerateFile() = %d bytes and no diagnostics, want diagnostics", len(code))
			}
			got := diag.RenderAll(errs, func(string) []byte { return src }, diag.Plain) + "\n"
			compare(t, strings.TrimSuffix(path, ".sigil")+".golden", got)
		})
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// compare checks got against the golden file at path, or rewrites the
// file with -update.
func compare(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
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
