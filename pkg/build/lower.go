package build

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/token"
)

// node is an expression as the builder records it. It turns into a
// syntax tree only when its document renders, since what a name means,
// a field's path or a let's scope, depends on the document.
type node interface {
	lower(l *lowerer) ast.Expr
}

// lowerer turns one document's expressions into syntax trees, collecting
// what goes wrong.
type lowerer struct {
	doc     *document
	model   *kind.Kind
	binding *gokind.Binding
	scope   *body   // the body whose lets are the innermost in scope
	binders []*root // the quantifier and filter variables in scope, innermost last
	uses    *imports
	errs    Errors
}

// binaryNode is an infix operator.
type binaryNode struct {
	x, y node
	s    Site
	op   ast.Op
}

// unaryNode is a prefix operator.
type unaryNode struct {
	x  node
	s  Site
	op ast.Op
}

// patternNode is `like` or `matches` with its literal pattern.
type patternNode struct {
	x       node
	pattern string
	s       Site
	op      ast.Op
}

// indexNode is `x[i]`.
type indexNode struct {
	x, i node
	s    Site
}

// callNode is a host function call.
type callNode struct {
	name string
	args []node
	s    Site
}

// listNode is a list literal of expressions.
type listNode struct {
	xs []node
	s  Site
}

// errNode is an expression whose builder call failed. It reports the
// error when its document renders, since the call had no document to
// report it to.
type errNode struct {
	err *Error
}

// nilNode stands for the zero Expr where no operator's call can report
// it.
type nilNode struct {
	s Site
}

func (n *binaryNode) lower(l *lowerer) ast.Expr {
	return &ast.BinaryExpr{Op: n.op, X: l.expr(n.x, n.s), Y: l.expr(n.y, n.s)}
}

func (n *unaryNode) lower(l *lowerer) ast.Expr {
	return &ast.UnaryExpr{Op: n.op, X: l.expr(n.x, n.s)}
}

// lower renders the pattern as a raw string when it holds a backslash,
// so a regular expression reads as written.
func (n *patternNode) lower(l *lowerer) ast.Expr {
	x := l.expr(n.x, n.s)
	if n.op == ast.OpMatches {
		if _, err := regexp.Compile(n.pattern); err != nil {
			l.errorf(n.s, "invalid regular expression: %v", err)
		}
	}
	lit := &ast.StringLit{Text: strconv.Quote(n.pattern), Value: n.pattern}
	if strings.Contains(n.pattern, `\`) && !strings.Contains(n.pattern, "`") {
		lit.Text, lit.Raw = "`"+n.pattern+"`", true
	}
	return &ast.BinaryExpr{Op: n.op, X: x, Y: lit}
}

func (n *indexNode) lower(l *lowerer) ast.Expr {
	return &ast.IndexExpr{X: l.expr(n.x, n.s), Index: l.expr(n.i, n.s)}
}

func (n *callNode) lower(l *lowerer) ast.Expr {
	c := &ast.CallExpr{Fun: &ast.Ident{Name: n.name}, Args: make([]ast.Expr, len(n.args))}
	for i, a := range n.args {
		c.Args[i] = l.expr(a, n.s)
	}
	return c
}

func (n *listNode) lower(l *lowerer) ast.Expr {
	lit := &ast.ListLit{Elems: make([]ast.Expr, len(n.xs))}
	for i, x := range n.xs {
		lit.Elems[i] = l.expr(x, n.s)
	}
	return lit
}

func (n *errNode) lower(l *lowerer) ast.Expr {
	l.errs = append(l.errs, n.err)
	return &ast.BadExpr{}
}

func (n *nilNode) lower(l *lowerer) ast.Expr {
	return l.expr(nil, n.s)
}

// expr lowers n, an operand of the builder call at s, which is where a
// zero Expr is reported.
func (l *lowerer) expr(n node, s Site) ast.Expr { //nolint:returninterface // an expression is any node of the tree
	if n == nil {
		l.errorf(s, "an operand is the zero build.Expr; build one with build.Field, build.Lit or another constructor")
		return &ast.BadExpr{}
	}
	return n.lower(l)
}

// errorf records an error at s.
func (l *lowerer) errorf(s Site, format string, args ...any) {
	l.errs = append(l.errs, s.errorf(format, args...))
}

// visible reports whether the lets of b are in scope where the lowerer
// is: in the body being lowered or one enclosing it.
func (l *lowerer) visible(b *body) bool {
	for s := l.scope; s != nil; s = s.parent {
		if s == b {
			return true
		}
	}
	return false
}

// identError says why name can't be a Sigil identifier, or returns ""
// when it can.
func identError(name string) string {
	if !isIdent(name) {
		return strconv.Quote(name) + " isn't an identifier: start with a letter or `_`, then letters, digits and `_`"
	}
	if token.Lookup(name) != token.Ident {
		return strconv.Quote(name) + " is a keyword"
	}
	return ""
}

// isIdent reports whether s is spelled like an identifier.
func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		letter := r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
		if !letter && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}
