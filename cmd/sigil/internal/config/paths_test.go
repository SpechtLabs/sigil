package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// TestApply checks what the configuration adds to a command's sources:
// its kind files after the command line's, resolved against its
// directory, and the trusted paths of its requirements, apart from the
// files the paths already hold.
func TestApply(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"k.sigil", "platform/guard.sigil", "platform/common.sigil", "teams/a.sigil"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("kind K version 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	at := func(name string) string { return filepath.ToSlash(filepath.Join(dir, filepath.FromSlash(name))) }
	platform := "require:\n  - policy: guard\n    trusted: [platform]\n"
	tests := []struct {
		name        string
		src         string   // sigil.yaml; none when empty
		paths       []string // the command's paths
		wantKinds   []string
		wantTrusted []string
		wantErr     string
	}{
		{name: "kinds", src: "kinds: [k.sigil]\n", wantKinds: []string{"flag.sigil", filepath.Join(dir, "k.sigil")}},
		{name: "none", src: "lints: {}\n", wantKinds: []string{"flag.sigil"}},
		{name: "trusted outside the paths", src: platform, paths: []string{filepath.Join(dir, "teams")}, wantKinds: []string{"flag.sigil"}, wantTrusted: []string{at("platform/common.sigil"), at("platform/guard.sigil")}},
		{name: "trusted among the paths", src: platform, paths: []string{dir}, wantKinds: []string{"flag.sigil"}},
		{name: "trusted partly among the paths", src: platform, paths: []string{filepath.Join(dir, "platform", "guard.sigil")}, wantKinds: []string{"flag.sigil"}, wantTrusted: []string{at("platform/common.sigil")}},
		{name: "trusted shared by two entries", src: platform + "  - policy: common\n    trusted: platform\n", paths: []string{filepath.Join(dir, "teams")}, wantKinds: []string{"flag.sigil"}, wantTrusted: []string{at("platform/common.sigil"), at("platform/guard.sigil")}},
		{name: "trusted path missing", src: "require:\n  - policy: guard\n    trusted: [nope]\n", wantErr: "sigil.yaml:2:13: the trusted path " + filepath.Join(dir, "nope") + " of guard can't be read"},
		{name: "paths unreadable", src: platform, paths: []string{filepath.Join(dir, "nope")}, wantKinds: []string{"flag.sigil"}, wantTrusted: []string{filepath.Join(dir, "platform")}},
		{name: "missing kind file", src: "kinds: [k.sigil, nope.sigil]\n", wantErr: "sigil.yaml: the kind file " + filepath.Join(dir, "nope.sigil") + " can't be read"},
		{name: "unreadable configuration", wantErr: "couldn't be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name, config.FileName)
			if tt.src != "" {
				path = filepath.Join(dir, config.FileName)
				if err := os.WriteFile(path, []byte(tt.src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			src := project.Sources{Paths: tt.paths, Kinds: []string{"flag.sigil"}}
			err := config.Apply(path, ".", &src)
			switch {
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Apply() = %v, want %q", err, tt.wantErr)
				}
			case err != nil:
				t.Fatalf("Apply() = %v", err)
			case !slices.Equal(src.Kinds, tt.wantKinds) || !slices.Equal(src.Trusted, tt.wantTrusted):
				t.Errorf("Apply() gave kinds %v and trusted %v, want %v and %v", src.Kinds, src.Trusted, tt.wantKinds, tt.wantTrusted)
			}
		})
	}
}
