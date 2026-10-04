package build

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spechtlabs/sigil/internal/ast"
)

// maxWidth is the column a rendered line should stay within. A chain or a
// bracketed list that would run past it breaks across lines.
const maxWidth = 88

// The levels of the precedence table at
// https://sigil.specht-labs.de/reference/expressions/, lowest first, as
// the parser's binding powers number them. A quantifier and a filter sit
// on the level of `not`: they may stand where `not` may.
const (
	levelOr       = 1 // or, xor
	levelAnd      = 2
	levelNot      = 3 // not, quantifiers, filters
	levelCmp      = 4
	levelCoalesce = 5
	levelAdd      = 6
	levelUnary    = 7 // unary minus, present
	levelPostfix  = 8 // field access, index, call, and every operand
)

// slot is where an operand sits: the lowest level that may stand there
// without parentheses, whether anything of the enclosing expression
// follows it, and the operator it's an operand of.
type slot struct {
	min  int
	tail bool   // nothing follows the operand before its expression ends
	of   ast.Op // the enclosing operator, or OpInvalid
}

// top is the slot of a whole expression: a condition, a let's value, an
// argument or anything inside brackets.
var top = slot{min: levelOr, tail: true}

// printer writes Sigil source with as few parentheses as the parser
// needs to build the same tree, breaking a long `and`, `or` or `xor`
// chain and a long bracketed list across lines the way `sigil fmt`
// keeps them. It tracks the column so it knows when a line runs long.
type printer struct {
	b      strings.Builder
	col    int // characters on the current line
	indent int // indentation level of the current line
	flat   bool
}

// write appends s, which holds no line break, to the current line.
func (p *printer) write(s string) {
	p.b.WriteString(s)
	p.col += utf8.RuneCountInString(s)
}

// newline starts a line at indentation level indent.
func (p *printer) newline(indent int) {
	p.b.WriteByte('\n')
	p.b.WriteString(strings.Repeat("  ", indent))
	p.col = 2 * indent
	p.indent = indent
}

// blank ends the current line and leaves an empty one after it.
func (p *printer) blank() {
	p.b.WriteByte('\n')
}

// trial prints with f into a printer that starts where p is, without
// touching p, and returns what it printed.
func (p *printer) trial(f func(q *printer)) string {
	q := &printer{col: p.col, indent: p.indent, flat: p.flat}
	f(q)
	return q.b.String()
}

// fits reports whether text, printed from the current column, stays on
// one line within maxWidth.
func (p *printer) fits(text string) bool {
	return !strings.Contains(text, "\n") && p.col+utf8.RuneCountInString(text) <= maxWidth
}

// expr prints x in slot s. indent is the level of the lines a chain or a
// list in it breaks onto.
func (p *printer) expr(x ast.Expr, s slot, indent int) {
	if parenthesize(x, s) {
		p.write("(")
		p.expr(x, top, indent+1)
		p.write(")")
		return
	}
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
		p.list("[", "]", func() int { return flatWidth(x, s) }, len(x.Elems), func(i, ind int) { p.expr(x.Elems[i], top, ind) })
	case *ast.MapLit:
		p.list("{", "}", func() int { return flatWidth(x, s) }, len(x.Entries), func(i, ind int) {
			p.expr(x.Entries[i].Key, slot{min: levelCoalesce}, ind)
			p.write(": ")
			p.expr(x.Entries[i].Value, top, ind)
		})
	case *ast.ParenExpr:
		p.write("(")
		p.expr(x.X, top, indent+1)
		p.write(")")
	case *ast.UnaryExpr:
		p.unary(x, s, indent)
	case *ast.BinaryExpr:
		p.binary(x, s, indent)
	case *ast.SelectorExpr:
		if _, integer := x.X.(*ast.IntLit); integer && !x.Optional {
			// A dot touching an integer would start a float.
			p.write("(")
			p.expr(x.X, top, indent+1)
			p.write(")")
		} else {
			p.expr(x.X, slot{min: levelPostfix}, indent)
		}
		if x.Optional {
			p.write("?.")
		} else {
			p.write(".")
		}
		p.write(x.Sel.Name)
	case *ast.IndexExpr:
		p.expr(x.X, slot{min: levelPostfix}, indent)
		p.write("[")
		p.expr(x.Index, top, indent+1)
		p.write("]")
	case *ast.CallExpr:
		p.expr(x.Fun, slot{min: levelPostfix}, indent)
		p.list("(", ")", func() int { return flatWidth(&ast.ListLit{Elems: x.Args}, top) }, len(x.Args), func(i, ind int) { p.expr(x.Args[i], top, ind) })
	case *ast.QuantExpr:
		p.binder(x.Op.String(), x.Var.Name, x.Range, x.Body, indent)
	case *ast.FilterExpr:
		p.binder("filter", x.Var.Name, x.Range, x.Body, indent)
	default:
		p.write("<error>")
	}
}

// unary prints a prefix operator. `-` takes a space before an operand
// that starts with another `-`, so the two never touch.
func (p *printer) unary(x *ast.UnaryExpr, s slot, indent int) {
	switch x.Op {
	case ast.OpNot:
		p.write("not ")
		p.expr(x.X, slot{min: levelNot, tail: s.tail, of: x.Op}, indent)
		return
	case ast.OpNeg:
		p.write("-")
		if u, ok := x.X.(*ast.UnaryExpr); ok && u.Op == ast.OpNeg {
			p.write(" ")
		}
	default:
		p.write(x.Op.String() + " ")
	}
	p.expr(x.X, slot{min: levelUnary, tail: s.tail, of: x.Op}, indent)
}

// binary prints an infix operator. A chain of `and`, `or` or `xor`
// breaks before every operator when it has three operands or more, or
// when it runs past maxWidth on one line.
func (p *printer) binary(x *ast.BinaryExpr, s slot, indent int) {
	left, right := operands(x.Op, s.tail)
	if !breaks(x.Op) {
		p.expr(x.X, left, indent)
		p.write(" " + x.Op.String() + " ")
		p.expr(x.Y, right, indent)
		return
	}
	chain := []*ast.BinaryExpr{x}
	for {
		l, ok := chain[0].X.(*ast.BinaryExpr)
		if !ok || l.Op != x.Op || parenthesize(l, left) {
			break
		}
		chain = append([]*ast.BinaryExpr{l}, chain...)
	}
	broken := !p.flat && (len(chain) >= 2 || !p.fits(flat(x, s)))
	p.expr(chain[0].X, left, indent)
	for i, link := range chain {
		if broken {
			p.newline(indent)
			p.write(x.Op.String() + " ")
		} else {
			p.write(" " + x.Op.String() + " ")
		}
		r := right
		r.tail = s.tail && i == len(chain)-1
		p.expr(link.Y, r, indent)
	}
}

// binder prints a quantifier or a filter, spelled kw. A body whose top
// level is `and`, `or` or `xor` gets parentheses, as `sigil fmt` gives
// it, since the body extends as far right as it can.
func (p *printer) binder(kw, name string, rng, body ast.Expr, indent int) {
	p.write(kw + " " + name + " in ")
	p.expr(rng, slot{min: levelCoalesce}, indent)
	p.write(": ")
	if b, ok := body.(*ast.BinaryExpr); ok && breaks(b.Op) {
		p.write("(")
		p.expr(b, top, indent+1)
		p.write(")")
		return
	}
	p.expr(body, top, indent)
}

// list prints n bracketed items: on one line when the whole list, width()
// characters wide, fits there, otherwise one per line with a trailing
// comma, indented one level deeper than the line the list starts on.
func (p *printer) list(open, closing string, width func() int, n int, item func(i, indent int)) {
	p.write(open)
	if n == 0 {
		p.write(closing)
		return
	}
	if p.flat || p.col-1+width() <= maxWidth {
		for i := range n {
			if i > 0 {
				p.write(", ")
			}
			item(i, p.indent+1)
		}
		p.write(closing)
		return
	}
	base := p.indent
	for i := range n {
		p.newline(base + 1)
		item(i, base+2)
		p.write(",")
	}
	p.newline(base)
	p.write(closing)
}

// flat prints x in slot s on one line.
func flat(x ast.Expr, s slot) string {
	p := &printer{flat: true}
	p.expr(x, s, 0)
	return p.b.String()
}

// flatWidth is how many characters x takes on one line.
func flatWidth(x ast.Expr, s slot) int {
	return utf8.RuneCountInString(flat(x, s))
}

// parenthesize reports whether x needs parentheses in slot s for the
// parser to read it back as the same tree.
func parenthesize(x ast.Expr, s slot) bool {
	switch x := x.(type) {
	case *ast.QuantExpr, *ast.FilterExpr:
		// The body extends as far right as it can, so anything after it
		// would join the body.
		return levelNot < s.min || !s.tail
	case *ast.BinaryExpr:
		if s.of == ast.OpOr && x.Op == ast.OpXor {
			return true // `or` and `xor` don't mix without parentheses
		}
	}
	return level(x) < s.min
}

// operands returns the slots of an infix operator's two operands. A
// left-associative operator takes its own level on the left, a
// right-associative one on the right, and a non-associative one on
// neither side.
func operands(op ast.Op, tail bool) (left, right slot) {
	l := binaryLevel(op)
	switch {
	case op == ast.OpCoalesce:
		return slot{min: l + 1, of: op}, slot{min: l, tail: tail, of: op}
	case op.IsComparison(), op == ast.OpXor:
		return slot{min: l + 1, of: op}, slot{min: l + 1, tail: tail, of: op}
	}
	return slot{min: l, of: op}, slot{min: l + 1, tail: tail, of: op}
}

// level returns the precedence level of x's outermost operator. A
// quantifier or filter is parenthesize's, since where it may stand
// depends on more than its level.
func level(x ast.Expr) int {
	switch x := x.(type) {
	case *ast.BinaryExpr:
		return binaryLevel(x.Op)
	case *ast.UnaryExpr:
		if x.Op == ast.OpNot {
			return levelNot
		}
		return levelUnary
	}
	return levelPostfix
}

// binaryLevel returns the precedence level of an infix operator.
func binaryLevel(op ast.Op) int {
	switch {
	case op == ast.OpOr, op == ast.OpXor:
		return levelOr
	case op == ast.OpAnd:
		return levelAnd
	case op.IsComparison():
		return levelCmp
	case op == ast.OpCoalesce:
		return levelCoalesce
	}
	return levelAdd
}

// breaks reports whether a chain of op can break across lines.
func breaks(op ast.Op) bool {
	return op == ast.OpAnd || op == ast.OpOr || op == ast.OpXor
}
