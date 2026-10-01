package bundle_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/parser"
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

// TestLoadSeveralSources loads two sources into one bundle, as a trusted
// bundle does: distinct paths add up, and a path both hold is an error,
// since a diagnostic names a file by its path alone.
func TestLoadSeveralSources(t *testing.T) {
	k, errs := check.LoadKind("k.sigil", []byte(kindSrc))
	if errs != nil {
		t.Fatal(errs)
	}
	platform := fstest.MapFS{"deploy/lib.sigil": &fstest.MapFile{Data: []byte(library)}}
	tests := []struct {
		name    string
		second  fstest.MapFS
		wantErr string
	}{
		{name: "distinct paths", second: fstest.MapFS{"vocabulary/lib.sigil": &fstest.MapFile{Data: []byte("module vocabulary: K@1\n")}}},
		{name: "one path in both", second: fstest.MapFS{"deploy/lib.sigil": &fstest.MapFile{Data: []byte("module other: K@1\n")}}, wantErr: "policy file deploy/lib.sigil is in two of the sources read into one bundle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := bundle.New(k)
			if err := b.Load(platform); err != nil {
				t.Fatal(err)
			}
			err := b.Load(tt.second)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Load() = %v, want no error", err)
			case tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr):
				t.Fatalf("Load() = %v, want %q", err, tt.wantErr)
			}
			if string(b.SourceOf("deploy/lib.sigil")) != library {
				t.Error("the second source replaced the first's file")
			}
		})
	}
}
