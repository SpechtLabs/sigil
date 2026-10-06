package project_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// TestOverlay reads a directory with an overlay, as the language server
// does with an editor's buffers: an overlay source replaces its file, a
// new `.sigil` file only the overlay holds is read with the files around
// it, and the overlay's files outside the paths, under a hidden directory
// or of another type are left out. A kind file named outside the paths is
// replaced too.
func TestOverlay(t *testing.T) {
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	write := func(name, src string) string {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	kindFile := write("kinds/k.sigil", "on disk")
	edited := write("policies/a.sigil", "on disk")
	untouched := write("policies/b.sigil", "on disk")
	at := func(name string) string { return filepath.Join(dir, "policies", filepath.FromSlash(name)) }
	overlay := map[string][]byte{
		edited:                              []byte("edited"),
		at("new.sigil"):                     []byte("new"),
		at("sub/new.sigil"):                 []byte("new below"),
		at(".hidden/x.sigil"):               []byte("hidden"),
		at("notes.txt"):                     []byte("not sigil"),
		filepath.Join(dir, "outside.sigil"): []byte("outside"),
		kindFile:                            []byte("edited kind"),
	}
	files, err := project.Read(project.Sources{Paths: []string{filepath.Join(dir, "policies")}, Kinds: []string{kindFile}, Overlay: overlay})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	names := make([]string, 0, len(files.Paths))
	for _, f := range files.Paths {
		got[f.Name] = string(f.Source)
		names = append(names, f.Name)
	}
	slash := filepath.ToSlash
	want := []string{slash(edited), slash(untouched), slash(at("new.sigil")), slash(at("sub/new.sigil"))}
	if !slices.Equal(names, want) {
		t.Errorf("read %v, want %v", names, want)
	}
	for name, src := range map[string]string{slash(edited): "edited", slash(untouched): "on disk", slash(at("new.sigil")): "new"} {
		if got[name] != src {
			t.Errorf("%s holds %q, want %q", name, got[name], src)
		}
	}
	if len(files.Kinds) != 1 || string(files.Kinds[0].Source) != "edited kind" {
		t.Errorf("the kind file holds %q, want the overlay's", files.Kinds[0].Source)
	}
}

// TestOverlayTrusted checks that a new file only the overlay holds, below
// a trusted path, is read as trusted, not as one of the paths.
func TestOverlayTrusted(t *testing.T) {
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	platform := filepath.Join(dir, "platform")
	if err := os.MkdirAll(platform, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(platform, "a.sigil"), []byte("on disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(platform, "new.sigil")
	files, err := project.Read(project.Sources{Paths: []string{dir}, Trusted: []string{platform}, Overlay: map[string][]byte{fresh: []byte("new")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Paths) != 0 {
		t.Errorf("paths = %v, want none", files.Paths)
	}
	if len(files.Trusted) != 2 || files.Trusted[1].Name != filepath.ToSlash(fresh) {
		t.Errorf("trusted = %v, want a.sigil and new.sigil", files.Trusted)
	}
}
