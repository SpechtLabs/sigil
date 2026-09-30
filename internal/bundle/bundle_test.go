package bundle_test

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gokind"
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
