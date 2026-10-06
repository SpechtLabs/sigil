package workspace

import (
	"slices"
	"testing"
)

// lookupKind is a kind small enough to read in a test.
const lookupKind = "kind K version 1\n\ninput n: int\n\ndecision deny {\n  reason: no\n}\n\ncollect all\n"

// TestProjectLookups checks what a project says about what it read: its
// kinds and where they're declared, and the names of the files.
func TestProjectLookups(t *testing.T) {
	files := []File{
		{Name: "/r/k.sigil", Source: []byte(lookupKind)},
		{Name: "/r/a.sigil", Source: []byte("module a.m: K@1\n\npub let x = n > 1\n---\npolicy a.p: K@1\n\nuse a.m.{x}\n\nwhen x {\n  deny(reason: no)\n}\n")},
		{Name: "/r/b.sigil", Source: []byte("policy b.p: Missing@1\n---\npolicy a.p: K@1\n")},
		{Name: "/r/bad.sigil", Source: []byte("kind Bad version 1\n\ninput x: nope\n")},
	}
	trusted := []File{{Name: "/t/g.sigil", Source: []byte("policy g.p: K@1\n")}}
	p := NewLoader(nil).Load(files, trusted)
	p.Check()

	kinds := make([]string, 0, len(p.Kinds()))
	for _, k := range p.Kinds() {
		kinds = append(kinds, k.Model.Name)
	}
	if !slices.Equal(kinds, []string{"K"}) {
		t.Errorf("Kinds() = %v, want [K]", kinds)
	}
	if doc, file := p.KindSource("K"); doc == nil || doc.Name.Name != "K" || file != "/r/k.sigil" {
		t.Errorf("KindSource(K) = %v, %q", doc, file)
	}
	if doc, file := p.KindSource("Nope"); doc != nil || file != "" {
		t.Errorf("KindSource(Nope) = %v, %q, want nothing", doc, file)
	}

	want := []string{"/r/a.sigil", "/r/b.sigil", "/r/bad.sigil", "/r/k.sigil", "/t/g.sigil"}
	if got := p.SourceNames(); !slices.Equal(got, want) {
		t.Errorf("SourceNames() = %v, want %v", got, want)
	}
	if (&Project{}).Kinds() != nil {
		t.Error("a project that loaded nothing has kinds")
	}
	if doc, _ := (&Project{}).KindSource("K"); doc != nil {
		t.Error("a project that loaded nothing has a kind document")
	}
}
