package project_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// failingReader is stdin that can't be read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("stdin is closed") }

// TestLoadReadErrors checks that Load fails on a path, trusted or not,
// that can't be read, naming it, before it parses anything.
func TestLoadReadErrors(t *testing.T) {
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked.sigil")
	if err := os.WriteFile(locked, []byte("policy a.b: Access@1\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(locked); err == nil {
		_ = f.Close()
		t.Skip("the file is readable despite its mode, as it is for root")
	}
	missing := filepath.Join(dir, "missing")
	tests := []struct {
		name string
		src  project.Sources
		want string
	}{
		{name: "missing path", src: project.Sources{Paths: []string{missing}}, want: missing + " can't be read"},
		{name: "missing trusted path", src: project.Sources{Paths: []string{dir}, Trusted: []string{missing}}, want: missing + " can't be read"},
		{name: "unreadable path", src: project.Sources{Paths: []string{locked}}, want: filepath.ToSlash(locked) + " couldn't be read"},
		{name: "unreadable trusted path", src: project.Sources{Trusted: []string{locked}}, want: filepath.ToSlash(locked) + " couldn't be read"},
		{name: "unreadable stdin", src: project.Sources{Paths: []string{"-"}, Stdin: failingReader{}}, want: "stdin couldn't be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := project.Load(tt.src, nil)
			if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Errorf("Load() = %v, %v, want %q", p, err, tt.want)
			}
		})
	}
}
