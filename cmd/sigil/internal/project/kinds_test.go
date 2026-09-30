package project_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/diag"
)

// TestKindPrecedence checks which of two differing kind documents is
// reported: the later source, in the order kind files, paths, trusted
// paths, whatever order the files are read in.
func TestKindPrecedence(t *testing.T) {
	dir := t.TempDir()
	stale := strings.Replace(accessKind, "version 1", "version 2", 1)
	for name, src := range map[string]string{
		"access.sigil":            accessKind,
		"kinds/access.sigil":      accessKind,
		"platform/kind.sigil":     stale,
		"platform/base.sigil":     "policy platform.base: Access@1\n",
		"team/main.sigil":         "policy team.main: Access@1\n",
		"notakind.sigil":          "policy team.other: Access@1\n",
		"broken/notparsed.sigil":  "kind\n",
		"stalekinds/access.sigil": stale,
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	at := func(name string) string { return filepath.ToSlash(filepath.Join(dir, name)) }
	tests := []struct {
		name  string
		src   project.Sources
		wrong string // the file the diagnostic is in
		first string // the file it says the kind comes from
	}{
		{
			name:  "a stale trusted kind against the paths'",
			src:   project.Sources{Paths: []string{at("access.sigil"), at("team")}, Trusted: []string{at("platform")}},
			wrong: at("platform/kind.sigil"),
			first: at("access.sigil"),
		},
		{
			name:  "a stale path kind against a kind file",
			src:   project.Sources{Paths: []string{at("stalekinds"), at("team")}, Kinds: []string{at("kinds/access.sigil")}},
			wrong: at("stalekinds/access.sigil"),
			first: at("kinds/access.sigil"),
		},
		{
			name:  "a stale kind file against the paths'",
			src:   project.Sources{Paths: []string{at("access.sigil"), at("team")}, Kinds: []string{at("stalekinds/access.sigil")}},
			wrong: at("access.sigil"),
			first: at("stalekinds/access.sigil"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := project.Load(tt.src, nil)
			if err != nil {
				t.Fatal(err)
			}
			errs := p.Errors()
			if len(errs) != 1 || errs[0].File != tt.wrong || !strings.Contains(errs[0].Msg, "differs from the one at "+tt.first+":") {
				t.Fatalf("Errors() = %v, want the kind in %s to differ from %s", errs, tt.wrong, tt.first)
			}
			if !strings.Contains(errs[0].Help, "the first source wins") {
				t.Errorf("help = %q, want the precedence", errs[0].Help)
			}
		})
	}

	t.Run("a kind file without a kind", func(t *testing.T) {
		_, err := project.Load(project.Sources{Paths: []string{at("team")}, Kinds: []string{at("notakind.sigil")}}, nil)
		if err == nil || err.Error() != at("notakind.sigil")+" holds no kind document" {
			t.Fatalf("Load() error = %v, want the file to hold no kind document", err)
		}
	})
	t.Run("a kind file that doesn't parse", func(t *testing.T) {
		p, err := project.Load(project.Sources{Kinds: []string{at("broken/notparsed.sigil")}}, nil)
		if err != nil {
			t.Fatalf("Load() error = %v, want the parse error as a diagnostic", err)
		}
		if errs := p.Errors(); len(errs) == 0 || errs[0].File != at("broken/notparsed.sigil") {
			t.Errorf("Errors() = %v, want the parse error", errs)
		}
	})
}

// TestInKind checks which diagnostics a project places in a kind
// document: a kind file's own errors, but not a policy's, nor one without
// a position.
func TestInKind(t *testing.T) {
	src := strings.Replace(accessKind, "input user: string", "input user: nope", 1) + "---\npolicy p.main: Access@1\n\nlet x = nada\n"
	p, err := project.Load(project.Sources{Paths: []string{"-"}, Stdin: strings.NewReader(src)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Check()
	errs := p.Errors()
	if len(errs) != 1 || !p.InKind(errs[0]) {
		t.Fatalf("Errors() = %v, want the kind's unknown type, in the kind document", errs)
	}
	policy := *errs[0]
	policy.Pos.Offset = len(src) - 3
	if p.InKind(&policy) {
		t.Error("InKind() of a position in the policy = true")
	}
	if p.InKind(&diag.Error{File: "<stdin>", Msg: "nowhere"}) {
		t.Error("InKind() without a position = true")
	}
}
