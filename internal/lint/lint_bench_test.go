package lint_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/lint"
)

func BenchmarkLint(b *testing.B) {
	k, _ := benchtest.Kind(b, false)
	tree := bundle.New(k)
	tree.Add("bench.sigil", []byte(benchtest.Policy(64)))
	tree.Check()
	if errs := tree.Errors(); errs != nil {
		b.Fatal(errs)
	}
	b.ReportAllocs()
	for b.Loop() {
		lint.Run(tree, lint.Options{Kind: k})
	}
}
