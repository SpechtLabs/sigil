package format

import (
	"strconv"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/token"
)

// expr prints an expression. indent is the level of the lines it breaks
// onto: where an `and`, `or` or `xor` broke in the source, and inside
// parentheses one level deeper.
func (p *printer) expr(x ast.Expr, indent int) {
	switch x := x.(type) {
	case *ast.Ident:
		p.write(x.Name)
	case *ast.IntLit:
		p.write(x.Text)
	case *ast.FloatLit:
		p.write(x.Text)
	case *ast.DurationLit:
		p.write(x.Text)
	case *ast.StringLit:
		p.write(x.Text)
	case *ast.BoolLit:
		p.write(strconv.FormatBool(x.Value))
	case *ast.Outcome:
		p.write("outcome")
	case *ast.ListLit:
		item := func(i int) (ast.Node, func(int)) {
			return x.Elems[i], func(ind int) { p.expr(x.Elems[i], ind) }
		}
		p.list("[", "]", x.From.Line, before(x.End()), len(x.Elems), item, indent)
	case *ast.MapLit:
		item := func(i int) (ast.Node, func(int)) {
			e := x.Entries[i]
			return e.Key, func(ind int) {
				p.expr(e.Key, ind)
				p.write(": ")
				p.expr(e.Value, ind)
			}
		}
		p.list("{", "}", x.From.Line, before(x.End()), len(x.Entries), item, indent)
	case *ast.ParenExpr:
		p.write("(")
		p.expr(x.X, indent+1)
		p.write(")")
	case *ast.UnaryExpr:
		p.unary(x, indent)
	case *ast.BinaryExpr:
		p.binary(x, indent)
	case *ast.SelectorExpr:
		_, integer := x.X.(*ast.IntLit)
		if integer && !x.Optional {
			// Preserve the token boundary in source such as `0 .field`.
			// The checker will reject the field access after formatting.
			p.write("(")
			p.expr(x.X, indent)
			p.write(")")
		} else {
			p.expr(x.X, indent)
		}
		if x.Optional {
			p.write("?.")
		} else {
			p.write(".")
		}
		p.write(x.Sel.Name)
	case *ast.IndexExpr:
		p.expr(x.X, indent)
		p.write("[")
		p.expr(x.Index, indent+1)
		p.write("]")
	case *ast.CallExpr:
		p.expr(x.Fun, indent)
		item := func(i int) (ast.Node, func(int)) {
			return x.Args[i], func(ind int) { p.expr(x.Args[i], ind) }
		}
		p.list("(", ")", x.Fun.End().Line, before(x.End()), len(x.Args), item, indent)
	case *ast.QuantExpr:
		p.quant(x, indent)
	}
	p.last = x.End().Line
}

// unary prints a prefix operator. A word operator is followed by a space;
// `-` isn't, unless its operand starts with another `-`, so that two
// minus signs never touch and three never read as a separator.
func (p *printer) unary(x *ast.UnaryExpr, indent int) {
	if x == nil {
		return
	}
	switch x.Op {
	case ast.OpNeg:
		p.write("-")
		if u, ok := x.X.(*ast.UnaryExpr); ok && u.Op == ast.OpNeg {
			p.write(" ")
		}
	default:
		p.write(x.Op.String() + " ")
	}
	p.expr(x.X, indent)
}

// binary prints an infix operator with a space on each side. A chain of
// `and`, `or` or `xor` breaks before an operator wherever the source had
// a line break next to it, and nowhere else.
func (p *printer) binary(x *ast.BinaryExpr, indent int) {
	if x == nil {
		return
	}
	if !breaks(x.Op) {
		p.expr(x.X, indent)
		p.write(" " + x.Op.String() + " ")
		p.expr(x.Y, indent)
		return
	}
	chain := []*ast.BinaryExpr{x}
	for {
		left, ok := chain[0].X.(*ast.BinaryExpr)
		if !ok || left.Op != x.Op {
			break
		}
		chain = append([]*ast.BinaryExpr{left}, chain...)
	}
	p.expr(chain[0].X, indent)
	for _, link := range chain {
		if link.OpPos.Line > link.X.End().Line || link.Y.Pos().Line > link.OpPos.Line {
			p.open(link.OpPos, indent, indent, false, false)
			p.write(link.Op.String() + " ")
		} else {
			p.write(" " + link.Op.String() + " ")
		}
		p.expr(link.Y, indent)
	}
}

// quant prints a quantifier. A body with a top-level `and`, `or` or `xor`
// gets parentheses, since the body extends as far right as it can and
// the parentheses make that visible.
func (p *printer) quant(x *ast.QuantExpr, indent int) {
	if x == nil {
		return
	}
	p.write(x.Op.String() + " " + x.Var.Name + " in ")
	p.expr(x.Range, indent)
	p.write(": ")
	if b, ok := x.Body.(*ast.BinaryExpr); ok && breaks(b.Op) {
		p.write("(")
		p.expr(b, indent+1)
		p.write(")")
		return
	}
	p.expr(x.Body, indent)
}

// list prints bracketed items: on one line, separated by `, `, or one per
// line with a trailing comma when the first item started a line after
// the one the opening bracket is on. The items of a broken list are
// indented one level deeper than the line the list starts on, and the
// closing bracket lines up with that line.
func (p *printer) list(open, close string, openLine int, closing token.Pos, n int, item func(int) (ast.Node, func(int)), indent int) {
	p.write(open)
	if n == 0 {
		p.write(close)
		return
	}
	if first, _ := item(0); first.Pos().Line <= openLine {
		for i := range n {
			if i > 0 {
				p.write(", ")
			}
			_, print := item(i)
			print(indent)
		}
		p.write(close)
		return
	}
	base := p.indentOf()
	for i := range n {
		node, print := item(i)
		p.open(node.Pos(), base+1, base+1, false, false)
		print(base + 2)
		p.write(",")
	}
	p.close(closing, base, base+1, false)
	p.write(close)
}

// breaks reports whether a chain of op can be broken across lines.
func breaks(op ast.Op) bool {
	return op == ast.OpAnd || op == ast.OpOr || op == ast.OpXor
}
