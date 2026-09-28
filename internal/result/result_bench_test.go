package result_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/result"
)

func BenchmarkResult(b *testing.B) {
	p := benchtest.Compile(b, benchtest.Composed, true)
	for _, name := range []string{"success", "assertion"} {
		b.Run(name, func(b *testing.B) {
			in := benchtest.Value()
			if name == "assertion" {
				in.Actor = ""
			}
			b.ReportAllocs()
			for b.Loop() {
				res := result.Evaluate(p, &in)
				if (res.Failure != nil) != (name == "assertion") {
					b.Fatal("unexpected evaluation failure")
				}
			}
		})
	}
}
