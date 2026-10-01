package build

import (
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/types"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// newlines turns every line ending into `\n`.
var newlines = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// stmt is a statement of a document built in Go.
type stmt interface {
	// render prints the statement on the current line, which is at
	// indentation level indent.
	render(r *renderer, indent int)
	site() Site
}

// letStmt is `let name = x` or `pub let name = x`.
type letStmt struct {
	x     node
	scope *body
	name  string
	s     Site
	pub   bool
}

// paramStmt is `param name: type`, with a default and bounds.
type paramStmt struct {
	def, min, max node
	t             reflect.Type
	doc           *document
	name          string
	s             Site
}

// whenStmt is `when cond { … }`.
type whenStmt struct {
	cond  node
	inner *body
	s     Site
}

// assertStmt is `assert("reason", cond)`.
type assertStmt struct {
	cond   node
	reason string
	s      Site
}

// callStmt is a decision constructor, or an invocation of target.
type callStmt struct {
	target  Invocable // nil for a decision
	outcome policy.Outcome
	args    []Argument
	s       Site
}

// commentStmt is a comment before the next statement.
type commentStmt struct {
	text string
}

// letRef reads a let.
type letRef struct {
	let *letStmt
}

// paramRef reads a param.
type paramRef struct {
	param *paramStmt
	s     Site
}

// renderer prints a document's statements, lowering their expressions as
// it goes.
type renderer struct {
	l *lowerer
	printer
}

// namedArg is one `name: value` of a call statement.
type namedArg struct {
	x    ast.Expr
	name string
}

func (s *letStmt) site() Site     { return s.s }
func (s *paramStmt) site() Site   { return s.s }
func (s *whenStmt) site() Site    { return s.s }
func (s *assertStmt) site() Site  { return s.s }
func (s *callStmt) site() Site    { return s.s }
func (s *commentStmt) site() Site { return Site{} }

func (s *letStmt) render(r *renderer, indent int) {
	kw := "let "
	if s.pub {
		kw = "pub let "
	}
	r.write(kw + s.name + " =")
	r.value(r.l.expr(s.x, s.s), indent)
}

func (s *paramStmt) render(r *renderer, indent int) {
	t, ok := r.l.binding.TypeOf(s.t)
	typ := "<error>"
	switch _, optional := t.(*types.Optional); {
	case !ok:
		r.l.errorf(s.s, "Go type %v has no Sigil type in kind %s", s.t, r.l.model.Name)
	case optional:
		r.l.errorf(s.s, "param %s can't be optional (Go type %v); a param always has a value", s.name, s.t)
	default:
		typ = t.String()
	}
	r.write("param " + s.name + ": " + typ)
	for _, b := range []struct {
		x  node
		kw string
	}{{s.def, " = "}, {s.min, ", min: "}, {s.max, ", max: "}} {
		if b.x != nil {
			r.write(b.kw)
			r.expr(r.l.expr(b.x, s.s), top, indent+1)
		}
	}
}

func (s *whenStmt) render(r *renderer, indent int) {
	r.write("when ")
	r.expr(r.l.expr(s.cond, s.s), top, indent+1)
	r.write(" {")
	r.stmts(s.inner, indent+1)
	r.newline(indent)
	r.write("}")
}

func (s *assertStmt) render(r *renderer, indent int) {
	r.write("assert(" + strconv.Quote(s.reason) + ",")
	r.value(r.l.expr(s.cond, s.s), indent)
	r.write(")")
}

func (s *callStmt) render(r *renderer, indent int) {
	var name string
	var args []namedArg
	if s.target == nil {
		name = s.outcome.Decision()
		args = append(args, namedArg{name: "reason", x: &ast.Ident{Name: s.outcome.Name()}})
	} else {
		name = r.l.invocation(s.target, s.s)
	}
	for _, a := range s.args {
		args = append(args, namedArg{name: a.name, x: r.l.expr(a.x, a.s)})
	}
	r.write(name)
	r.args(args)
}

func (s *commentStmt) render(r *renderer, indent int) {
	for i, line := range lines(s.text) {
		if i > 0 {
			r.newline(indent)
		}
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			r.write("//")
			continue
		}
		r.write("// " + line)
	}
}

// lower reads the let, which must be in scope: in this document, and in
// the body being lowered or one around it.
func (n *letRef) lower(l *lowerer) ast.Expr {
	let := n.let
	switch {
	case let.scope.doc != l.doc:
		l.errorf(let.s, "let %s belongs to %s, not %s; read a pub let of another document with build.Ref", let.name, let.scope.doc.name, l.doc.name)
		return &ast.BadExpr{}
	case !l.visible(let.scope):
		l.errorf(let.s, "let %s is read outside the `when` body it's declared in; a scoped let is visible in its body and the bodies nested in it", let.name)
		return &ast.BadExpr{}
	}
	return &ast.Ident{Name: let.name}
}

// lower reads the param, which must be one of this document's.
func (n *paramRef) lower(l *lowerer) ast.Expr {
	if n.param.doc != l.doc {
		l.errorf(n.s, "param %s belongs to %s, not %s; pass its value to the other policy as an invocation argument", n.param.name, n.param.doc.name, l.doc.name)
		return &ast.BadExpr{}
	}
	return &ast.Ident{Name: n.param.name}
}

// stmts prints the statements of b, each on a line of its own at
// indent. A blank line comes before a comment, which starts a group of
// statements, and, in a `when` body, sets a nested rule apart, as
// `sigil fmt` does at the top level. A comment stays with the statement
// after it.
func (r *renderer) stmts(b *body, indent int) {
	afterWhen := false
	for i, s := range b.stmts {
		_, when := leading(b.stmts[i:]).(*whenStmt)
		switch {
		case i == 0 || isComment(b.stmts[i-1]):
		case isComment(s), b.parent != nil && (when || afterWhen):
			r.blank()
		}
		r.newline(indent)
		r.l.scope = b
		s.render(r, indent)
		if !isComment(s) {
			afterWhen = when
		}
	}
	r.l.scope = b
}

// value prints x after a let's `=` or an assert's reason: on the same
// line when its first line fits there and it isn't an `and`, `or` or
// `xor` chain that breaks, otherwise on a line of its own one level
// deeper. A list that breaks opens on the same line.
func (r *renderer) value(x ast.Expr, indent int) {
	same := r.trial(func(q *printer) {
		q.write(" ")
		q.expr(x, top, indent+1)
	})
	first, _, broken := strings.Cut(same, "\n")
	b, ok := x.(*ast.BinaryExpr)
	chain := ok && breaks(b.Op) && broken
	if !chain && r.fits(first) {
		r.write(" ")
	} else {
		r.newline(indent + 1)
	}
	r.expr(x, top, indent+1)
}

// args prints the named arguments of a call statement.
func (r *renderer) args(args []namedArg) {
	q := &printer{flat: true}
	q.argList(args, 0)
	r.argList(args, utf8.RuneCountInString(q.b.String()))
}

// argList prints named arguments as a bracketed list width characters
// wide on one line.
func (p *printer) argList(args []namedArg, width int) {
	p.list("(", ")", func() int { return width }, len(args), func(i, indent int) {
		p.write(args[i].name + ": ")
		p.expr(args[i].x, top, indent)
	})
}

// add appends s to the body.
func (b *body) add(s stmt) {
	b.stmts = append(b.stmts, s)
}

// when adds a rule whose body f builds.
func (b *body) when(s Site, cond node, f func(*Block)) {
	inner := &body{doc: b.doc, parent: b}
	b.add(&whenStmt{cond: cond, inner: inner, s: s})
	if f != nil {
		f(&Block{b: inner})
	}
}

// assert adds an assert.
func (b *body) assert(s Site, reason string, cond node) {
	b.add(&assertStmt{reason: reason, cond: cond, s: s})
}

// decide adds a decision constructor.
func (b *body) decide(s Site, o policy.Outcome, args []Argument) {
	if o.Decision() == "" {
		b.doc.errs = append(b.doc.errs, s.errorf("the outcome is the zero policy.Outcome; take one from a decision's Reason, like deploy.Deny.Reason(\"change_freeze\")"))
		return
	}
	b.checkArgs(args, true)
	b.add(&callStmt{outcome: o, args: args, s: s})
}

// invoke adds an invocation.
func (b *body) invoke(s Site, target Invocable, args []Argument) {
	if isNil(target) {
		b.doc.errs = append(b.doc.errs, s.errorf("the policy to invoke is nil"))
		return
	}
	b.checkArgs(args, false)
	b.add(&callStmt{target: target, args: args, s: s})
}

// comment adds a comment.
func (b *body) comment(text string) {
	b.add(&commentStmt{text: text})
}

// checkArgs reports argument names that can't be written, and a reason
// passed to a decision as an argument.
func (b *body) checkArgs(args []Argument, decision bool) {
	given := map[string]bool{}
	for _, a := range args {
		switch {
		case !isIdent(a.name):
			b.doc.errs = append(b.doc.errs, a.s.errorf("argument name %q isn't an identifier", a.name))
		case decision && a.name == "reason":
			b.doc.errs = append(b.doc.errs, a.s.errorf("the reason comes from the policy.Outcome; drop the reason argument"))
		case given[a.name]:
			b.doc.errs = append(b.doc.errs, a.s.errorf("argument %s is given twice", a.name))
		}
		given[a.name] = true
	}
}

// leading returns the statement the first of ss stands for: itself, or,
// for a comment, the statement the comment comes before.
func leading(ss []stmt) stmt {
	for _, s := range ss {
		if !isComment(s) {
			return s
		}
	}
	return nil
}

// isComment reports whether s is a comment.
func isComment(s stmt) bool {
	_, ok := s.(*commentStmt)
	return ok
}

// isNil reports whether v is nil or a nil pointer in an interface.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// lines splits the text of a comment into its lines, at every line
// ending the lexer ends a comment at: `\r\n`, `\r` and `\n`. Text after a
// lone `\r` would otherwise become code.
func lines(text string) []string {
	return strings.Split(newlines.Replace(text), "\n")
}
