package bundle_test

import (
	"fmt"
	"testing"

	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/bundle"
)

func BenchmarkBundleCompile(b *testing.B) {
	k, binding := benchtest.Kind(b, false)
	for _, n := range []int{1, 64} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			src := []byte(benchtest.Policy(n))
			b.ReportAllocs()
			for b.Loop() {
				tree := bundle.New(k)
				tree.Add("bench.sigil", src)
				if _, errs := tree.Compile("main", bundle.Options{Binding: binding}); errs != nil {
					b.Fatal(errs)
				}
			}
		})
	}
}
