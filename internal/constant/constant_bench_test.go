package constant_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
)

func BenchmarkConstantEval(b *testing.B) {
	x, errs := parser.ParseExpr("bench.sigil", []byte(`{"low": 1 + 2, "high": 100 - 3, "zero": 0}`))
	if errs != nil {
		b.Fatal(errs)
	}
	typ := &types.Map{Key: types.String, Value: types.Int}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := constant.Eval(x, typ); err != nil {
			b.Fatal(err)
		}
	}
}
