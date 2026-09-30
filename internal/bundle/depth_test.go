package bundle_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gokind"
)

// chain returns n policies, p0 to p(n-1), each invoking the next, the
// last one with a rule, in the order given, as one file.
func chain(n int, reversed bool) string {
	docs := make([]string, n)
	for i := range n {
		src := fmt.Sprintf("policy p%d: K@1\n", i)
		if i+1 < n {
			src += fmt.Sprintf("\nuse p%d\n\np%d()\n", i+1, i+1)
		} else {
			src += "\nwhen name != \"\" {\n  allow(reason: ok)\n}\n"
		}
		docs[i] = src
	}
	if reversed {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			docs[i], docs[j] = docs[j], docs[i]
		}
	}
	return strings.Join(docs, "\n---\n\n")
}

// TestImportDepth checks chains of imports at, just below and just above
// MaxImportDepth, and far above it in both reading orders: up to the
// limit the root compiles, and past it the document where the chain
// grows too long reports it, once, at the import that continues it, and
// the root doesn't compile, however long the chain and whichever way it
// was read.
func TestImportDepth(t *testing.T) {
	k, errs := check.LoadKind("k.sigil", []byte(kindSrc))
	if errs != nil {
		t.Fatal(errs)
	}
	tests := []struct {
		name     string
		n        int
		reversed bool
	}{
		{name: "below the limit", n: bundle.MaxImportDepth - 1},
		{name: "at the limit", n: bundle.MaxImportDepth},
		{name: "past the limit", n: bundle.MaxImportDepth + 1},
		{name: "far past the limit", n: 20_000},
		{name: "far past the limit, read from the end", n: 20_000, reversed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := bundle.New(k)
			b.Add("chain.sigil", []byte(chain(tt.n, tt.reversed)))
			_, errs := b.Compile("p0", bundle.Options{Binding: gokind.Synthesize(k)})
			if tt.n <= bundle.MaxImportDepth {
				if errs != nil {
					t.Fatalf("Compile(p0) = %v, want a chain of %d to compile", errs, tt.n)
				}
				return
			}
			if len(errs) != 1 {
				t.Fatalf("Compile(p0) = %v, want one diagnostic", errs)
			}
			// The chain from p(n-65) to the end is 65 documents long.
			first := tt.n - bundle.MaxImportDepth - 1
			want := fmt.Sprintf("imports nest more than %d levels deep: p%d starts a chain of %d documents, each importing the next", bundle.MaxImportDepth, first, bundle.MaxImportDepth+1)
			if e := errs[0]; e.Msg != want || b.DocumentAt(e.File, e.Pos) != fmt.Sprintf("p%d", first) || !strings.Contains(e.Help, "import the documents deep in the chain directly") {
				t.Errorf("diagnostic = %q in %s, help %q; want %q in p%d", e.Msg, b.DocumentAt(e.File, e.Pos), e.Help, want, first)
			}
		})
	}
}

// TestImportCycleStillReported checks that a cycle is reported once, at
// the import that closes it, as before chains had a limit.
func TestImportCycleStillReported(t *testing.T) {
	k, errs := check.LoadKind("k.sigil", []byte(kindSrc))
	if errs != nil {
		t.Fatal(errs)
	}
	b := bundle.New(k)
	b.Add("cycle.sigil", []byte("policy a: K@1\n\nuse b\n\nb()\n\n---\n\npolicy b: K@1\n\nuse c\n\nc()\n\n---\n\npolicy c: K@1\n\nuse a\n\na()\n"))
	b.Check()
	if errs := b.Errors(); len(errs) != 1 || errs[0].Msg != "import cycle: a -> b -> c -> a" {
		t.Errorf("Errors() = %v, want the cycle a -> b -> c -> a", errs)
	}
}
