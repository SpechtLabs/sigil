package project_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// TestSpellings checks that however the paths and the trusted paths spell
// a file, relatively, absolutely, with `./` or `..`, or through a link, it
// is read once, and a trusted file stays out of the regular ones.
func TestSpellings(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"access.sigil":        accessKind,
		"platform/base.sigil": "policy platform.base: Access@1\n",
		"team/main.sigil":     "policy team.main: Access@1\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("platform", filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	abs := func(name string) string { return filepath.Join(dir, name) }
	tests := []struct {
		name    string
		paths   []string
		trusted []string
		kinds   []string
	}{
		{name: "relative", paths: []string{"."}, trusted: []string{"platform"}},
		{name: "absolute", paths: []string{dir}, trusted: []string{abs("platform")}},
		{name: "absolute paths, relative trusted", paths: []string{dir}, trusted: []string{"platform"}},
		{name: "relative paths, absolute trusted", paths: []string{"."}, trusted: []string{abs("platform")}},
		{name: "./ and ..", paths: []string{"./", "team/../team/main.sigil"}, trusted: []string{"./platform/"}},
		{name: "trusted through a link", paths: []string{"access.sigil", "team", "platform"}, trusted: []string{"linked"}},
		{name: "a kind file named every way", paths: []string{".", abs("access.sigil")}, trusted: []string{"platform"}, kinds: []string{"./access.sigil", abs("access.sigil")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := project.Load(project.Sources{Paths: tt.paths, Trusted: tt.trusted, Kinds: tt.kinds}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if errs := p.Errors(); errs != nil {
				t.Fatalf("Errors() = %v, want every file read once", errs)
			}
			if p.Files() != 2 {
				t.Errorf("Files() = %d, want access.sigil and team/main.sigil", p.Files())
			}
			if g := p.Group("platform.base"); g == nil || !g.Bundle.Document("platform.base").Trusted {
				t.Error("platform.base isn't read as trusted")
			}
		})
	}
}
