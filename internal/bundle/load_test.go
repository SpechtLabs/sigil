package bundle_test

import (
	"testing"
	"testing/fstest"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
)

// TestLoads checks that Loads says of every path what Load does with a
// file there.
func TestLoads(t *testing.T) {
	k, errs := check.LoadKind("k.sigil", []byte(kindSrc))
	if errs != nil {
		t.Fatal(errs)
	}
	tests := []struct {
		path  string
		loads bool
	}{
		{"a.sigil", true},
		{"deploy/freeze.sigil", true},
		{"a..b/c.sigil", true},
		{".a.sigil", false},
		{".platform/deploy/freeze.sigil", false},
		{"platform/..data/freeze.sigil", false},
		{"platform/.git/x.sigil", false},
		{"a.txt", false},
		{"a.sigil.bak", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := bundle.Loads(tt.path); got != tt.loads {
				t.Errorf("Loads(%q) = %v, want %v", tt.path, got, tt.loads)
			}
			b := bundle.New(k)
			if err := b.Load(fstest.MapFS{tt.path: {Data: []byte("module m: K@1\n")}}); err != nil {
				t.Fatal(err)
			}
			if _, read := b.Sources[tt.path]; read != tt.loads {
				t.Errorf("Load read %q: %v, want %v", tt.path, read, tt.loads)
			}
		})
	}
}
