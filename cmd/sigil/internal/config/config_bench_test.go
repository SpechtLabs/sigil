package config_test

import (
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
)

func BenchmarkConfigParse(b *testing.B) {
	src := []byte("lints:\n  unused-let: error\n")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := config.Parse("sigil.yaml", src); err != nil {
			b.Fatal(err)
		}
	}
}
