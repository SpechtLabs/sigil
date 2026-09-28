package ast_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/parser"
)

func BenchmarkASTPrint(b *testing.B) {
	x, errs := parser.ParseExpr("bench.sigil", []byte(benchtest.Expression))
	if errs != nil {
		b.Fatal(errs)
	}
	b.ReportAllocs()
	for b.Loop() {
		ast.Sprint(x)
	}
}
