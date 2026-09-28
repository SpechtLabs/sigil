package parser_test

import (
	"fmt"
	"testing"

	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/parser"
)

func BenchmarkParseFile(b *testing.B) {
	for _, n := range []int{1, 64} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			src := []byte(benchtest.Policy(n))
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for b.Loop() {
				if _, errs := parser.ParseFile("bench.sigil", src); errs != nil {
					b.Fatal(errs)
				}
			}
		})
	}
}

func BenchmarkParseExpression(b *testing.B) {
	src := []byte(benchtest.Expression)
	b.ReportAllocs()
	for b.Loop() {
		if _, errs := parser.ParseExpr("bench.sigil", src); errs != nil {
			b.Fatal(errs)
		}
	}
}
