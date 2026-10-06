package lexer_test

import (
	"fmt"
	"testing"

	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/lexer"
	"github.com/spechtlabs/sigil/internal/token"
)

func BenchmarkLexer(b *testing.B) {
	for _, n := range []int{1, 8, 16, 32, 64, 128, 256, 512} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			src := []byte(benchtest.Policy(n))
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for b.Loop() {
				l := lexer.New(src)
				for l.Next().Kind != token.EOF {
				}
			}
		})
	}
}
