package project_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
)

// TestScope checks what a scope covers: the diagnostics it keeps, the
// policies in it, and that its bundle compiles a policy an error in an
// unrelated document would stop.
func TestScope(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"access.sigil":   accessKind,
		"other.sigil":    "kind Other version 1\n\ninput x: nope\n",
		"lib.sigil":      "module lib.util: Access@1\n\npub let yes = true\n",
		"trusted.sigil":  "module trusted.base: Access@1\n\npub let base = true\n",
		"main.sigil":     "policy access.main: Access@1\n\nuse lib.util.{yes}\nuse trusted.base.{base}\n\nwhen yes and base {\n  deny(reason: no_rule_matched)\n}\n",
		"broken.sigil":   "policy access.broken: Access@1\n\nwhen nope {\n  deny(reason: no_rule_matched)\n}\n",
		"dangling.sigil": "policy access.dangling: Access@1\n\nuse nope.missing\n",
		"mixed.sigil":    accessKind + "\n---\n\npolicy access.mixed: Access@1\n\nwhen {\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := func(name string) string { return filepath.Join(dir, name) }
	p, err := project.Load(project.Sources{
		Paths:   []string{path("access.sigil"), path("other.sigil"), path("lib.sigil"), path("main.sigil"), path("broken.sigil"), path("dangling.sigil"), path("mixed.sigil")},
		Trusted: []string{path("trusted.sigil")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Check()
	tests := []struct {
		name     string
		selected []string
		policies []string
		keep     []string // the files of the kept diagnostics, by base name, each once
		compiles bool     // whether access.main compiles in the scope's bundle
	}{
		{name: "every document", policies: []string{"access.broken", "access.dangling", "access.main", "access.mixed"}, keep: []string{"broken.sigil", "dangling.sigil", "mixed.sigil", "other.sigil"}},
		{name: "a policy and what it uses", selected: []string{"access.main", "access.main"}, policies: []string{"access.main"}, keep: []string{"other.sigil"}, compiles: true},
		{name: "a use of a name nobody defines", selected: []string{"access.dangling"}, policies: []string{"access.dangling"}, keep: []string{"dangling.sigil", "other.sigil"}},
		{name: "an unrelated policy", selected: []string{"access.broken"}, policies: []string{"access.broken"}, keep: []string{"broken.sigil", "other.sigil"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := p.ScopeOf(tt.selected)
			if !reflect.DeepEqual(s.Selected(), tt.selected) {
				t.Errorf("Selected() = %v, want %v", s.Selected(), tt.selected)
			}
			if !reflect.DeepEqual(s.Policies(), tt.policies) {
				t.Errorf("Policies() = %v, want %v", s.Policies(), tt.policies)
			}
			kept := s.Keep(append(p.Errors(), &diag.Error{Msg: "nowhere"}))
			var got []string
			for _, e := range kept {
				if e.File != "" {
					got = append(got, filepath.Base(e.File))
				}
			}
			got = slices.Compact(got)
			if !reflect.DeepEqual(got, tt.keep) {
				t.Errorf("Keep() in %v, want %v:\n%s", got, tt.keep, p.Render(kept))
			}
			if s.Keep(nil) != nil {
				t.Error("Keep(nil) != nil")
			}
			g := p.Group("access.main")
			b := s.Bundle(g)
			if b != s.Bundle(g) {
				t.Error("Bundle() built the bundle twice")
			}
			if tt.selected == nil && b != g.Bundle {
				t.Error("Bundle() of every document isn't the group's own")
			}
			_, errs := b.Compile("access.main", bundle.Options{Static: true})
			if (errs == nil) != tt.compiles {
				t.Errorf("Compile(access.main) = %v, want it to compile: %v", errs, tt.compiles)
			}
		})
	}
}
