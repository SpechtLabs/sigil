package eval_test

import (
	"fmt"
	"testing"

	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/parser"
)

func BenchmarkCompileExpression(b *testing.B) {
	k, binding := benchtest.Kind(b, false)
	x, errs := parser.ParseExpr("bench.sigil", []byte(benchtest.Expression))
	if errs != nil {
		b.Fatal(errs)
	}
	c := check.New("bench.sigil")
	c.Expr(x, check.NewEnv(k))
	if c.Errors() != nil {
		b.Fatal(c.Errors())
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := eval.Compile(x, c.Info(), eval.NewScope(binding)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvalPolicy(b *testing.B) {
	for _, n := range []int{1, 64} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			p := benchtest.Compile(b, benchtest.Policy(n), true)
			in := benchtest.Value()
			b.ReportAllocs()
			for b.Loop() {
				out, err := p.Eval(&in)
				if err != nil || len(out.Top) != n {
					b.Fatalf("evaluation: %v, %v", out, err)
				}
			}
		})
	}
	b.Run("composed", func(b *testing.B) {
		p := benchtest.Compile(b, benchtest.Composed, true)
		in := benchtest.Value()
		b.ReportAllocs()
		for b.Loop() {
			out, err := p.Eval(&in)
			if err != nil || len(out.Top) != 2 {
				b.Fatalf("evaluation: %v, %v", out, err)
			}
		}
	})
}
