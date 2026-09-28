package gokind_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/gokind"
)

func BenchmarkGoKindBuild(b *testing.B) {
	opts := benchtest.WithOptions(false)
	b.ReportAllocs()
	for b.Loop() {
		if _, _, errs := gokind.Build(opts); errs != nil {
			b.Fatal(errs)
		}
	}
}

func BenchmarkSynthesize(b *testing.B) {
	k, _ := benchtest.Kind(b, false)
	b.ReportAllocs()
	for b.Loop() {
		gokind.Synthesize(k)
	}
}

func BenchmarkDecodeInput(b *testing.B) {
	k, binding := benchtest.Kind(b, false)
	raw := map[string]any{"actor": "ada", "roles": []any{"reader", "deployer"}, "scores": []any{int64(1), int64(2)}, "labels": map[string]any{"team": "payments"}, "enabled": true}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := binding.DecodeInput(k, raw); err != nil {
			b.Fatal(err)
		}
	}
}
