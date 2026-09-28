package kind_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/benchtest"
)

func BenchmarkKindSource(b *testing.B) {
	k, _ := benchtest.Kind(b, false)
	b.ReportAllocs()
	for b.Loop() {
		k.Source()
	}
}
