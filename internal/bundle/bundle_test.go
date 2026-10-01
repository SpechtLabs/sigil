package bundle_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/token"
)

const kindSrc = `kind K version 1

input name: string

decision deny {
  reason: no_rule_matched | banned
}

decision allow {
  reason: ok
}

collect one
precedence deny > allow
default deny(reason: no_rule_matched)
`

// library holds a module, a guardrail and two policies that invoke it,
// one of them under a `when`.
const library = `module lib: K@1

pub let banned = name == "mallory"

---

policy guard: K@1

use lib.{banned}

when banned {
  deny(reason: banned)
}

---

policy good: K@1

use guard

guard()

when name != "" {
  allow(reason: ok)
}

---

policy gated: K@1

use guard

when name == "x" {
  guard()
}
`

// broken is a document that fails its check.
const broken = `
---

policy broken: K@1

when nam == "x" {
  allow(reason: ok)
}
`

// TestCompileDoesNotLeak compiles a root that fails and then a good one
// from the same bundle: the second compile must not report the first's
// errors, which is what lets check and test compile every root in turn.
func TestCompileDoesNotLeak(t *testing.T) {
	k, errs := check.LoadKind("k.sigil", []byte(kindSrc))
	if errs != nil {
		t.Fatal(errs)
	}
	good := `policy good: K@1

when name != "" {
  allow(reason: ok)
}
`
	tests := []struct {
		name    string
		src     string
		root    string
		require []string
		wantErr string
	}{
		{name: "unknown root", src: good, root: "nope", wantErr: "bundle has no policy nope"},
		{name: "module root", src: library, root: "lib", wantErr: "lib is a module, not a policy"},
		{name: "gated requirement", src: library, root: "gated", require: []string{"guard"}, wantErr: "guard must be invoked unconditionally"},
		{name: "missing requirement", src: good, root: "good", require: []string{"guard"}, wantErr: "good doesn't invoke guard"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := bundle.New(k)
			b.Add("p.sigil", []byte(tt.src))
			_, errs := b.Compile(tt.root, bundle.Options{Static: true, Require: tt.require})
			if errs == nil || !strings.Contains(errs.Error(), tt.wantErr) {
				t.Fatalf("Compile(%s) = %v, want %q", tt.root, errs, tt.wantErr)
			}
			if _, errs := b.Compile("good", bundle.Options{Binding: gokind.Synthesize(k)}); errs != nil {
				t.Fatalf("Compile(good) after a failed compile = %v, want no error", errs)
			}
		})
	}
}

// TestDocuments lists the bundle's own documents in reading order and
// reports which checked cleanly.
func TestDocuments(t *testing.T) {
	k, errs := check.LoadKind("k.sigil", []byte(kindSrc))
	if errs != nil {
		t.Fatal(errs)
	}
	b := bundle.New(k)
	b.Add("p.sigil", []byte(library+broken))
	b.Check()

	docs := b.Documents()
	got := make([]string, 0, len(docs))
	for _, d := range docs {
		state := "clean"
		if !d.Clean() {
			state = "failed"
		}
		got = append(got, d.Name+":"+state)
	}
	want := "lib:clean guard:clean good:clean gated:clean broken:failed"
	if strings.Join(got, " ") != want {
		t.Errorf("Documents() = %s, want %s", strings.Join(got, " "), want)
	}

	unchecked := bundle.New(k)
	unchecked.Add("p.sigil", []byte(library))
	for _, d := range unchecked.Documents() {
		if d.Clean() {
			t.Errorf("%s is clean before Check", d.Name)
		}
	}
}

// TestIndex checks that documents parsed elsewhere index as Add's do,
// without the parse errors or the kind check Add makes, and that a name
// indexed twice is reported with Redefined's diagnostic.
func TestIndex(t *testing.T) {
	k, errs := check.LoadKind("k.sigil", []byte(kindSrc))
	if errs != nil {
		t.Fatal(errs)
	}
	stale := strings.Replace(kindSrc, "version 1", "version 2", 1)
	src := []byte(stale + "---\n" + library + "---\nmodule lib: K@1\n")
	f, perrs := parser.ParseFile("p.sigil", src)
	if perrs != nil {
		t.Fatal(perrs)
	}
	b := bundle.New(k)
	b.Index("p.sigil", src, f.Docs)
	if got, want := b.Policies(), []string{"guard", "good", "gated"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Policies() = %v, want %v", got, want)
	}
	if d := b.Document("K"); d == nil || !d.Kind {
		t.Errorf("Document(K) = %v, want the kind document, indexed", d)
	}
	errs = b.Errors()
	if len(errs) != 1 || errs[0].Msg != "module lib is defined twice" || !strings.Contains(errs[0].Help, "first defined at p.sigil:") {
		t.Fatalf("Errors() = %v, want only lib defined twice, not the stale kind", errs)
	}
	if string(b.SourceOf("p.sigil")) != string(src) {
		t.Error("Index didn't keep the source")
	}

	trusted := &bundle.Document{Node: f.Docs[1], Name: "lib", File: "t.sigil", Trusted: true}
	if e := bundle.Redefined("p.sigil", f.Docs[1], trusted); !strings.HasPrefix(e.Help, "the name belongs to the trusted source, defined at t.sigil:") {
		t.Errorf("Redefined() of a trusted name = %v", e)
	}
	if e := bundle.Redefined("q.sigil", f.Docs[2], trusted); e.Msg != "policy guard is defined twice" || e.File != "q.sigil" {
		t.Errorf("Redefined() of a policy = %v", e)
	}
	if e := bundle.Redefined("p.sigil", f.Docs[1], nil); e != nil {
		t.Errorf("Redefined() without a previous definition = %v, want nil", e)
	}
	if e := bundle.Redefined("p.sigil", f.Docs[0], trusted); e != nil {
		t.Errorf("Redefined() of a kind document = %v, want nil", e)
	}
}

// TestSeveralSources reads documents from two sources that may hold files
// of the same path, as a trusted bundle and the bundle it trusts, or two
// trusted sources, do. Every diagnostic quotes the line of its own
// source and names its own document, and a byte-for-byte copy of a
// document another source defines is left out rather than defined twice.
func TestSeveralSources(t *testing.T) {
	k, errs := check.LoadKind("k.sigil", []byte(kindSrc))
	if errs != nil {
		t.Fatal(errs)
	}
	file := func(src string) fstest.MapFS {
		return fstest.MapFS{"policies.sigil": &fstest.MapFile{Data: []byte(src)}}
	}
	const (
		vocabulary = "module vocabulary: K@1\n\npub let banned = nam == \"mallory\"\n"
		team       = "policy team: K@1\n\nwhen name == \"x\" {\n  allow(reason: nope)\n}\n"
		guard      = "policy guard: K@1\n\nwhen name == \"mallory\" {\n  deny(reason: banned)\n}\n"
	)
	tests := []struct {
		name     string
		trusted  []fstest.MapFS // loaded into the trusted bundle, in order
		bundle   fstest.MapFS   // loaded into the bundle that trusts them
		read     int            // policies and modules the trusted bundle was given
		want     []string       // substrings of the rendered diagnostics, in order
		wantNone bool
	}{
		{
			name:    "a trusted file and the bundle's own share a path",
			trusted: []fstest.MapFS{file(vocabulary)},
			bundle:  file(team),
			read:    1,
			want: []string{
				"policies.sigil:3:18 (vocabulary): error: unknown name `nam`", "3 | pub let banned = nam == \"mallory\"",
				"policies.sigil:4:17 (team): error: decision allow has no reason `nope`", "4 |   allow(reason: nope)",
			},
		},
		{
			name:    "two trusted sources share a path",
			trusted: []fstest.MapFS{file(guard), file(vocabulary)},
			bundle:  fstest.MapFS{},
			read:    2,
			want:    []string{"policies.sigil:3:18 (vocabulary): error: unknown name `nam`", "3 | pub let banned = nam == \"mallory\""},
		},
		{
			name:     "two trusted sources hold the same document",
			trusted:  []fstest.MapFS{file(guard), {"copy/guard.sigil": &fstest.MapFile{Data: []byte("// a copy\n" + guard)}}},
			bundle:   fstest.MapFS{},
			read:     2,
			wantNone: true,
		},
		{
			name:     "the bundle holds a copy of a trusted document",
			trusted:  []fstest.MapFS{file(guard)},
			bundle:   fstest.MapFS{"platform/policies.sigil": &fstest.MapFile{Data: []byte(guard)}},
			read:     1,
			wantNone: true,
		},
		{
			name:    "the bundle changes a trusted document",
			trusted: []fstest.MapFS{file(guard)},
			bundle:  fstest.MapFS{"platform/policies.sigil": &fstest.MapFile{Data: []byte(strings.Replace(guard, "mallory", "eve", 1))}},
			read:    1,
			want:    []string{"platform/policies.sigil:1:8: error: policy guard is defined twice", "the name belongs to the trusted source, defined at policies.sigil:1:1"},
		},
		{
			name:    "two trusted sources define one name differently",
			trusted: []fstest.MapFS{file(guard), {"other.sigil": &fstest.MapFile{Data: []byte("policy guard: K@1\n")}}},
			bundle:  fstest.MapFS{},
			read:    2,
			want:    []string{"other.sigil:1:8: error: policy guard is defined twice", "first defined at policies.sigil:1:1"},
		},
		{
			name:    "one source holds the same document twice",
			trusted: []fstest.MapFS{{"a.sigil": &fstest.MapFile{Data: []byte(guard)}, "b.sigil": &fstest.MapFile{Data: []byte(guard)}}},
			bundle:  fstest.MapFS{},
			read:    2,
			want:    []string{"b.sigil:1:8: error: policy guard is defined twice"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trusted := bundle.New(k)
			for _, src := range tt.trusted {
				if err := trusted.Load(src); err != nil {
					t.Fatal(err)
				}
			}
			if trusted.Read() != tt.read {
				t.Errorf("Read() = %d, want %d", trusted.Read(), tt.read)
			}
			b := bundle.New(k)
			b.Trust(trusted)
			if err := b.Load(tt.bundle); err != nil {
				t.Fatal(err)
			}
			b.Check()
			got := b.Render(b.Errors())
			if tt.wantNone {
				if got != "" {
					t.Fatalf("Render() = %s, want no diagnostics", got)
				}
				return
			}
			rest := got
			for _, w := range tt.want {
				i := strings.Index(rest, w)
				if i < 0 {
					t.Fatalf("Render() =\n%s\nwant %q, in order", got, w)
				}
				rest = rest[i+len(w):]
			}
		})
	}
}

// TestSourceFor checks that a diagnostic the bundle didn't report, such
// as the compiler's, quotes the document at its position, and one in no
// document the file's source.
func TestSourceFor(t *testing.T) {
	k, errs := check.LoadKind("k.sigil", []byte(kindSrc))
	if errs != nil {
		t.Fatal(errs)
	}
	src := []byte("// leading comment\n\npolicy good: K@1\n")
	b := bundle.New(k)
	b.Add("p.sigil", src)
	at := func(line, offset int) token.Pos { return token.Pos{Line: line, Column: 1, Offset: offset} }
	tests := []struct {
		name string
		e    *diag.Error
		doc  string
	}{
		{name: "in a document", e: &diag.Error{File: "p.sigil", Pos: at(3, 20)}, doc: "good"},
		{name: "before every document", e: &diag.Error{File: "p.sigil", Pos: at(1, 0)}},
		{name: "without a position", e: &diag.Error{File: "p.sigil"}},
		{name: "naming its document", e: &diag.Error{File: "p.sigil", Doc: "other", Pos: at(1, 0)}, doc: "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := b.SourceFor(tt.e); string(got) != string(src) {
				t.Errorf("SourceFor() = %q, want the file's source", got)
			}
			if got := b.DocumentOf(tt.e); got != tt.doc {
				t.Errorf("DocumentOf() = %q, want %q", got, tt.doc)
			}
		})
	}
	if got := b.SourceFor(&diag.Error{File: "nope.sigil"}); got != nil {
		t.Errorf("SourceFor() of an unknown file = %q, want nil", got)
	}
	if b.SourceFor(nil) != nil || b.DocumentOf(nil) != "" {
		t.Error("SourceFor(nil) or DocumentOf(nil) found something")
	}

	// A file only the trusted bundle holds, as a compiler diagnostic in it
	// names it: its document, or the file when no document is at the
	// position.
	trusted := bundle.New(k)
	tsrc := []byte("policy guard: K@1\n")
	trusted.Add("t.sigil", tsrc)
	team := bundle.New(k)
	team.Trust(trusted)
	team.Add("p.sigil", src)
	in := &diag.Error{File: "t.sigil", Pos: at(1, 8)}
	if got := team.SourceFor(in); string(got) != string(tsrc) {
		t.Errorf("SourceFor() in a trusted file = %q, want its source", got)
	}
	if got := team.SourceFor(&diag.Error{File: "t.sigil"}); string(got) != string(tsrc) {
		t.Errorf("SourceFor() without a position = %q, want the trusted file's source", got)
	}
	if got := team.Resolve(diag.ErrorList{in}); got[0].Doc != "guard" || got[0] == in {
		t.Errorf("Resolve() = %+v, want a copy naming guard", got[0])
	}
}
