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

// BenchmarkEvalPolicy measures how evaluation grows with the rules: rules=N
// is a collecting kind where every rule matches, one-matching evaluates as
// many rules but only one of them matches, and ranked is the same on a
// `collect one` kind with a precedence.
func BenchmarkEvalPolicy(b *testing.B) {
	for _, n := range []int{1, 8, 16, 32, 64, 128, 256, 512} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			benchmarkEvalPolicy(b, benchtest.Policy(n), true, n)
		})
	}
	for _, n := range []int{1, 8, 16, 32, 64, 128, 256, 512} {
		b.Run(fmt.Sprintf("one-matching/rules=%d", n), func(b *testing.B) {
			benchmarkEvalPolicy(b, benchtest.OneMatching(n), true, 1)
		})
	}
	for _, n := range []int{1, 8, 16, 32, 64, 128, 256, 512} {
		b.Run(fmt.Sprintf("ranked/rules=%d", n), func(b *testing.B) {
			benchmarkEvalPolicy(b, benchtest.OneMatching(n), false, 1)
		})
	}
	b.Run("composed", func(b *testing.B) {
		benchmarkEvalPolicy(b, benchtest.Composed, true, 2)
	})
}

// benchmarkEvalPolicy evaluates source compiled for a collecting or a
// ranked kind, checking every evaluation yields top candidates.
func benchmarkEvalPolicy(b *testing.B, source string, collect bool, top int) {
	b.Helper()
	p := benchtest.Compile(b, source, collect)
	in := benchtest.Value()
	b.ReportAllocs()
	for b.Loop() {
		out, err := p.Eval(&in)
		if err != nil || out.Conflict != nil || len(out.Top) != top {
			b.Fatalf("evaluation: %v, %v", out, err)
		}
	}
}

// BenchmarkEvalEnum measures reading host enum values, which checks each
// against its enum: a plain value, and every element of a list.
func BenchmarkEvalEnum(b *testing.B) {
	for _, bc := range []struct{ name, rule string }{
		{name: "value", rule: "when account.tier == critical { move(reason: ok, to: critical) }"},
		{name: "list", rule: "when standard in account.tiers { move(reason: ok, to: critical) }"},
	} {
		b.Run(bc.name, func(b *testing.B) {
			p := compileEnums(b, bc.rule)
			in := account()
			b.ReportAllocs()
			for b.Loop() {
				if _, err := p.Eval(&in); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
