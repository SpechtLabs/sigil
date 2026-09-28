package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/internal/lint"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		want   map[string]lint.Level
		err    string
		advice string
	}{
		{
			name: "levels",
			src:  "lints:\n  gated-deny: error\n  qualified-imports: warn\n  unused-let: off\n",
			want: map[string]lint.Level{lint.GatedDeny: lint.Error, lint.QualifiedImports: lint.Warn, lint.UnusedLet: lint.Off},
		},
		{name: "empty file", src: "", want: map[string]lint.Level{}},
		{name: "only a comment", src: "# nothing configured yet\n", want: map[string]lint.Level{}},
		{name: "unknown lint", src: "lints:\n  gated-denies: error\n", err: `unknown lint "gated-denies"`, advice: `did you mean "gated-deny"?`},
		{name: "unknown lint without a near name", src: "lints:\n  frobnicate: warn\n", err: `unknown lint "frobnicate"`, advice: "lints: unused-import, shadowed-kind-name"},
		{name: "bad level", src: "lints:\n  gated-deny: fatal\n", err: `lint gated-deny has unknown level "fatal"`, advice: "off, warn or error"},
		{name: "unknown top-level key", src: "lint:\n  gated-deny: error\n", err: "isn't a valid configuration", advice: "sigil.yaml holds `lints:`"},
		{name: "not a map", src: "lints: [gated-deny]\n", err: "isn't a valid configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := config.Parse("sigil.yaml", []byte(tt.src))
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Parse() error = %v, want %q", err, tt.err)
				}
				if advice := strings.Join(err.Advice(), "; "); !strings.Contains(advice, tt.advice) {
					t.Errorf("advice = %q, want %q", advice, tt.advice)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if c.File != "sigil.yaml" || !reflect.DeepEqual(c.Lints, tt.want) {
				t.Errorf("Parse() = %+v, want lints %v", c, tt.want)
			}
		})
	}
}

// TestLoad finds the nearest sigil.yaml at or above a directory.
func TestLoad(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	nested := filepath.Join(repo, "teams", "payments")
	inner := filepath.Join(repo, "teams", "platform")
	for _, d := range []string{nested, inner} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(repo, config.FileName), "lints:\n  gated-deny: error\n")
	write(t, filepath.Join(inner, config.FileName), "lints:\n  unused-let: off\n")
	explicit := filepath.Join(root, "other.yaml")
	write(t, explicit, "lints:\n  path-matches-name: warn\n")
	// A directory named sigil.yaml isn't a configuration.
	if err := os.MkdirAll(filepath.Join(nested, config.FileName), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		dir  string
		file string // where the configuration came from; empty for the defaults
		want map[string]lint.Level
		err  string
	}{
		{name: "in the directory", dir: repo, file: filepath.Join(repo, config.FileName), want: map[string]lint.Level{lint.GatedDeny: lint.Error}},
		{name: "in a parent", dir: nested, file: filepath.Join(repo, config.FileName), want: map[string]lint.Level{lint.GatedDeny: lint.Error}},
		{name: "the nearest wins", dir: inner, file: filepath.Join(inner, config.FileName), want: map[string]lint.Level{lint.UnusedLet: lint.Off}},
		{name: "explicit path", path: explicit, dir: nested, file: explicit, want: map[string]lint.Level{lint.PathMatchesName: lint.Warn}},
		{name: "none found", dir: root},
		{name: "explicit path missing", path: filepath.Join(root, "nope.yaml"), dir: repo, err: "couldn't be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := config.Load(tt.path, tt.dir)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Load() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if c.File != tt.file || len(c.Lints) != len(tt.want) || (tt.want != nil && !reflect.DeepEqual(c.Lints, tt.want)) {
				t.Errorf("Load() = %+v, want file %q with %v", c, tt.file, tt.want)
			}
		})
	}
}

func write(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}
