package project_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// TestExpandLinks checks how a directory walk treats symbolic links: one
// that leads nowhere is skipped unless it would be read, and one that
// leads back up the tree doesn't send the walk round in circles.
func TestExpandLinks(t *testing.T) {
	tests := []struct {
		name  string
		files []string          // plain files to create
		links map[string]string // link name to target, relative to the link
		want  []string
		err   string
	}{
		{
			name:  "a dangling link Expand wouldn't read",
			files: []string{"a.sigil"},
			links: map[string]string{"docs/broken.md": "nowhere"},
			want:  []string{"a.sigil"},
		},
		{
			name:  "a link to itself Expand wouldn't read",
			files: []string{"a.sigil"},
			links: map[string]string{"self": "self"},
			want:  []string{"a.sigil"},
		},
		{
			name:  "a dangling link Expand would read",
			files: []string{"a.sigil"},
			links: map[string]string{"b.sigil": "nowhere.sigil"},
			err:   "b.sigil is a symbolic link to nothing that can be read",
		},
		{
			name:  "a link back up the tree",
			files: []string{"a.sigil", "sub/b.sigil"},
			links: map[string]string{"sub/up": ".."},
			want:  []string{"a.sigil", "sub/b.sigil"},
		},
		{
			name:  "two names for one directory",
			files: []string{"real/a.sigil"},
			links: map[string]string{"alias": "real"},
			want:  []string{"alias/a.sigil"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				p := filepath.Join(dir, f)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for link, target := range tt.links {
				p := filepath.Join(dir, link)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(dir)
			got, err := project.Expand([]string{"."}, project.IsSigil)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Expand() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Expand() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}
