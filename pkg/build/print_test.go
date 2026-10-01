package build

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/parser"
)

// TestPrintRoundTrip prints generated trees and parses them back: the
// parser must build the same tree, and every pair of parentheses the
// printer wrote must be needed, apart from those `sigil fmt` adds itself.
func TestPrintRoundTrip(t *testing.T) {
	for seed := range uint64(3000) {
		roundTrip(t, seed)
	}
}

// FuzzPrint is TestPrintRoundTrip over any seed.
func FuzzPrint(f *testing.F) {
	for seed := range uint64(16) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed uint64) {
		roundTrip(t, seed)
	})
}

// TestPrint pins the output for the cases the precedence table calls out.
func TestPrint(t *testing.T) {
	a, b, c := id("a"), id("b"), id("c")
	tests := []struct {
		name string
		x    ast.Expr
		want string
	}{
		{"and binds tighter than or", bin(ast.OpOr, a, bin(ast.OpAnd, b, c)), "a or b and c"},
		{"or under and", bin(ast.OpAnd, bin(ast.OpOr, a, b), c), "(a or b) and c"},
		{"left associative", bin(ast.OpSub, bin(ast.OpSub, a, b), c), "a - b - c"},
		{"right operand of a left associative operator", bin(ast.OpSub, a, bin(ast.OpSub, b, c)), "a - (b - c)"},
		{"comparisons don't chain", bin(ast.OpEq, bin(ast.OpEq, a, b), c), "(a == b) == c"},
		{"comparison on the right", bin(ast.OpEq, a, bin(ast.OpLt, b, c)), "a == (b < c)"},
		{"xor doesn't chain", bin(ast.OpXor, bin(ast.OpXor, a, b), c), "(a xor b) xor c"},
		{"xor under or", bin(ast.OpOr, bin(ast.OpXor, a, b), c), "(a xor b) or c"},
		{"or under xor", bin(ast.OpXor, a, bin(ast.OpOr, b, c)), "a xor (b or c)"},
		{"coalesce is right associative", bin(ast.OpCoalesce, a, bin(ast.OpCoalesce, b, c)), "a ?? b ?? c"},
		{"coalesce on the left", bin(ast.OpCoalesce, bin(ast.OpCoalesce, a, b), c), "(a ?? b) ?? c"},
		{"coalesce under a comparison", bin(ast.OpEq, bin(ast.OpCoalesce, a, b), c), "a ?? b == c"},
		{"sum under coalesce", bin(ast.OpCoalesce, a, bin(ast.OpAdd, b, c)), "a ?? b + c"},
		{"not over a comparison", un(ast.OpNot, bin(ast.OpEq, a, b)), "not a == b"},
		{"not over and", un(ast.OpNot, bin(ast.OpAnd, a, b)), "not (a and b)"},
		{"not as a comparison operand", bin(ast.OpEq, un(ast.OpNot, a), b), "(not a) == b"},
		{"not before and", bin(ast.OpAnd, un(ast.OpNot, a), b), "not a and b"},
		{"minus before minus", un(ast.OpNeg, un(ast.OpNeg, a)), "- -a"},
		{"minus over a sum", un(ast.OpNeg, bin(ast.OpAdd, a, b)), "-(a + b)"},
		{"present over a field", un(ast.OpPresent, sel(a, "b", true)), "present a?.b"},
		{"field of a sum", sel(bin(ast.OpAdd, a, b), "c", false), "(a + b).c"},
		{"field of an integer", sel(&ast.IntLit{Text: "1"}, "c", false), "(1).c"},
		{"quantifier last", bin(ast.OpAnd, a, quant(b, c)), "a and any v in b: c"},
		{"quantifier first", bin(ast.OpAnd, quant(b, c), a), "(any v in b: c) and a"},
		{"quantifier under not", un(ast.OpNot, quant(b, c)), "not any v in b: c"},
		{"quantifier under not, then and", bin(ast.OpAnd, un(ast.OpNot, quant(b, c)), a), "not (any v in b: c) and a"},
		{"quantifier as a comparison operand", bin(ast.OpEq, a, quant(b, c)), "a == (any v in b: c)"},
		{"body with and", quant(a, bin(ast.OpAnd, b, c)), "any v in a: (b and c)"},
		{"filter range", &ast.FilterExpr{Var: id("v"), Range: bin(ast.OpCoalesce, a, b), Body: c}, "filter v in a ?? b: c"},
		{"filter range with a comparison", &ast.FilterExpr{Var: id("v"), Range: bin(ast.OpEq, a, b), Body: c}, "filter v in (a == b): c"},
		{"quantifier in a list", &ast.ListLit{Elems: []ast.Expr{quant(a, b), c}}, "[any v in a: b, c]"},
		{"map key", &ast.MapLit{Entries: []ast.MapEntry{{Key: bin(ast.OpEq, a, b), Value: bin(ast.OpOr, a, c)}}}, "{(a == b): a or c}"},
		{"three operands break", bin(ast.OpAnd, bin(ast.OpAnd, a, b), c), "a\nand b\nand c"},
		{"call and index", &ast.IndexExpr{X: &ast.CallExpr{Fun: id("f"), Args: []ast.Expr{a, b}}, Index: c}, "f(a, b)[c]"},
		{"parentheses as written", &ast.ParenExpr{X: bin(ast.OpOr, a, b)}, "(a or b)"},
		{"bad expression", &ast.BadExpr{}, "<error>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &printer{}
			p.expr(tt.x, top, 0)
			if got := p.b.String(); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestPrintBreaks checks where long expressions break: an `and` chain
// that runs past the line, and a list that does.
func TestPrintBreaks(t *testing.T) {
	long := id(strings.Repeat("x", 50))
	tests := []struct {
		name string
		x    ast.Expr
		want string
	}{
		{
			name: "two long operands",
			x:    bin(ast.OpOr, long, long),
			want: long.Name + "\n  or " + long.Name,
		},
		{
			name: "long list",
			x:    &ast.ListLit{Elems: []ast.Expr{long, long}},
			want: "[\n  " + long.Name + ",\n  " + long.Name + ",\n]",
		},
		{
			name: "long call",
			x:    &ast.CallExpr{Fun: id("f"), Args: []ast.Expr{long, long}},
			want: "f(\n  " + long.Name + ",\n  " + long.Name + ",\n)",
		},
		{
			name: "short list",
			x:    &ast.ListLit{Elems: []ast.Expr{id("a")}},
			want: "[a]",
		},
		{
			name: "empty list",
			x:    &ast.ListLit{},
			want: "[]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &printer{}
			p.expr(tt.x, top, 1)
			if got := p.b.String(); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// roundTrip generates the tree of seed, prints it and checks it parses
// back, with no parentheses to spare.
func roundTrip(t *testing.T, seed uint64) {
	t.Helper()
	g := &gen{r: rand.New(rand.NewPCG(seed, seed^0x5eed))}
	x := g.expr(4)
	want := ast.Sprint(x)
	p := &printer{}
	p.expr(x, top, 0)
	src := p.b.String()
	parsed, errs := parser.ParseExpr("", []byte(src))
	if errs != nil {
		t.Fatalf("seed %d: %s doesn't parse: %v\nwant %s", seed, src, errs, want)
	}
	if got := ast.Sprint(strip(parsed)); got != want {
		t.Fatalf("seed %d: %s parses as\n%s\nwant\n%s", seed, src, got, want)
	}
	for _, span := range spare(parsed, nil) {
		without := src[:span.From.Offset] + " " + src[span.From.Offset+1:span.To.Offset-1] + " " + src[span.To.Offset:]
		again, errs := parser.ParseExpr("", []byte(without))
		if errs == nil && ast.Sprint(strip(again)) == want {
			t.Fatalf("seed %d: the parentheses at %d in %s aren't needed", seed, span.From.Offset, src)
		}
	}
}

// gen generates expression trees from a random source.
type gen struct {
	r *rand.Rand
}

var genOps = []ast.Op{
	ast.OpOr, ast.OpXor, ast.OpAnd, ast.OpEq, ast.OpNotEq, ast.OpLt, ast.OpLtEq, ast.OpGt, ast.OpGtEq,
	ast.OpIn, ast.OpNotIn, ast.OpAllIn, ast.OpAnyIn, ast.OpOneIn, ast.OpExclusiveIn, ast.OpHas,
	ast.OpLike, ast.OpMatches, ast.OpCoalesce, ast.OpAdd, ast.OpSub,
}

// expr returns a tree at most depth levels deep.
func (g *gen) expr(depth int) ast.Expr {
	if depth == 0 || g.r.IntN(5) == 0 {
		return g.leaf()
	}
	d := depth - 1
	switch g.r.IntN(10) {
	case 0, 1, 2, 3:
		return bin(genOps[g.r.IntN(len(genOps))], g.expr(d), g.expr(d))
	case 4:
		return un([]ast.Op{ast.OpNot, ast.OpNeg, ast.OpPresent}[g.r.IntN(3)], g.expr(d))
	case 5:
		if g.r.IntN(3) == 0 {
			return &ast.FilterExpr{Var: id("v"), Range: g.expr(d), Body: g.expr(d)}
		}
		q := quant(g.expr(d), g.expr(d))
		if g.r.IntN(2) == 0 {
			q.Op = ast.OpAll
		}
		return q
	case 6:
		return sel(g.expr(d), "f", g.r.IntN(2) == 0)
	case 7:
		return &ast.IndexExpr{X: g.expr(d), Index: g.expr(d)}
	case 8:
		return &ast.CallExpr{Fun: id("f"), Args: []ast.Expr{g.expr(d), g.expr(d)}}
	}
	if g.r.IntN(2) == 0 {
		return &ast.ListLit{Elems: []ast.Expr{g.expr(d), g.expr(d)}}
	}
	return &ast.MapLit{Entries: []ast.MapEntry{{Key: g.expr(d), Value: g.expr(d)}}}
}

// leaf returns an operand without children.
func (g *gen) leaf() ast.Expr {
	switch g.r.IntN(5) {
	case 0:
		return &ast.IntLit{Text: "1"}
	case 1:
		return &ast.StringLit{Text: `"s"`}
	case 2:
		return &ast.DurationLit{Text: "1h"}
	case 3:
		return &ast.BoolLit{Value: true}
	}
	return id([]string{"a", "b", "c"}[g.r.IntN(3)])
}

// strip removes the parentheses from a tree, so trees printed with them
// compare equal to the trees they were printed from.
func strip(x ast.Expr) ast.Expr {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return strip(x.X)
	case *ast.UnaryExpr:
		return un(x.Op, strip(x.X))
	case *ast.BinaryExpr:
		return bin(x.Op, strip(x.X), strip(x.Y))
	case *ast.SelectorExpr:
		return sel(strip(x.X), x.Sel.Name, x.Optional)
	case *ast.IndexExpr:
		return &ast.IndexExpr{X: strip(x.X), Index: strip(x.Index)}
	case *ast.CallExpr:
		c := &ast.CallExpr{Fun: strip(x.Fun)}
		for _, a := range x.Args {
			c.Args = append(c.Args, strip(a))
		}
		return c
	case *ast.QuantExpr:
		return &ast.QuantExpr{Op: x.Op, Var: x.Var, Range: strip(x.Range), Body: strip(x.Body)}
	case *ast.FilterExpr:
		return &ast.FilterExpr{Var: x.Var, Range: strip(x.Range), Body: strip(x.Body)}
	case *ast.ListLit:
		l := &ast.ListLit{}
		for _, e := range x.Elems {
			l.Elems = append(l.Elems, strip(e))
		}
		return l
	case *ast.MapLit:
		m := &ast.MapLit{}
		for _, e := range x.Entries {
			m.Entries = append(m.Entries, ast.MapEntry{Key: strip(e.Key), Value: strip(e.Value)})
		}
		return m
	}
	return x
}

// spare returns the spans of the parentheses in x that the printer
// chose, leaving out those it writes whatever the precedence: around a
// quantifier body with a top-level `and`, `or` or `xor`, as `sigil fmt`
// does, and around an integer before `.`. parent is the node above x.
func spare(x, parent ast.Expr) []ast.Span {
	var out []ast.Span
	visit := func(children ...ast.Expr) {
		for _, c := range children {
			out = append(out, spare(c, x)...)
		}
	}
	switch x := x.(type) {
	case *ast.ParenExpr:
		if !forced(x, parent) {
			out = append(out, x.Span)
		}
		visit(x.X)
	case *ast.UnaryExpr:
		visit(x.X)
	case *ast.BinaryExpr:
		visit(x.X, x.Y)
	case *ast.SelectorExpr:
		visit(x.X)
	case *ast.IndexExpr:
		visit(x.X, x.Index)
	case *ast.CallExpr:
		visit(x.Args...)
	case *ast.QuantExpr:
		visit(x.Range, x.Body)
	case *ast.FilterExpr:
		visit(x.Range, x.Body)
	case *ast.ListLit:
		visit(x.Elems...)
	case *ast.MapLit:
		for _, e := range x.Entries {
			visit(e.Key, e.Value)
		}
	}
	return out
}

// forced reports whether the printer writes the parentheses p in parent
// whatever the precedence.
func forced(p *ast.ParenExpr, parent ast.Expr) bool {
	switch parent := parent.(type) {
	case *ast.QuantExpr, *ast.FilterExpr:
		b, ok := p.X.(*ast.BinaryExpr)
		var body ast.Expr
		if q, isQuant := parent.(*ast.QuantExpr); isQuant {
			body = q.Body
		} else {
			body = parent.(*ast.FilterExpr).Body
		}
		return ok && breaks(b.Op) && body == ast.Expr(p)
	case *ast.SelectorExpr:
		_, integer := p.X.(*ast.IntLit)
		return integer && !parent.Optional
	}
	return false
}

func id(name string) *ast.Ident { return &ast.Ident{Name: name} }

func bin(op ast.Op, x, y ast.Expr) *ast.BinaryExpr { return &ast.BinaryExpr{Op: op, X: x, Y: y} }

func un(op ast.Op, x ast.Expr) *ast.UnaryExpr { return &ast.UnaryExpr{Op: op, X: x} }

func sel(x ast.Expr, name string, optional bool) *ast.SelectorExpr {
	return &ast.SelectorExpr{X: x, Sel: id(name), Optional: optional}
}

func quant(rng, body ast.Expr) *ast.QuantExpr {
	return &ast.QuantExpr{Op: ast.OpAny, Var: id("v"), Range: rng, Body: body}
}
