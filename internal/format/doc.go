package format

import (
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/token"
)

// doc prints a document from its header to its last statement. A blank
// line follows the header, and top-level items are grouped by kind: a
// blank line comes between two groups, and around every rule,
// invocation, type, decision, default and conflict outcome, which stand
// alone.
func (p *printer) doc(d ast.Doc) {
	var items []ast.Node
	switch d := d.(type) {
	case *ast.PolicyDoc:
		p.header("policy", d.Name, d.Kind, d.Pin)
		for _, u := range d.Uses {
			items = append(items, u)
		}
		for _, s := range d.Stmts {
			items = append(items, s)
		}
	case *ast.ModuleDoc:
		p.header("module", d.Name, d.Kind, d.Pin)
		for _, u := range d.Uses {
			items = append(items, u)
		}
		for _, l := range d.Lets {
			items = append(items, l)
		}
	case *ast.KindDoc:
		p.write("kind " + d.Name.Name + " version " + d.Version.Text)
		p.last = d.Version.End().Line
		if d.Accepts != nil {
			p.write(", accepts: " + d.Accepts.Text)
			p.last = d.Accepts.End().Line
		}
		for _, decl := range d.Decls {
			items = append(items, decl)
		}
	}
	prev := groupHeader
	for _, n := range items {
		g := group(n)
		if g != prev || g == groupAlone {
			p.section(n.Pos(), 0)
		} else {
			p.open(n.Pos(), 0, 0, true, true)
		}
		prev = g
		switch n := n.(type) {
		case *ast.UseStmt:
			p.use(n)
		case ast.Stmt:
			p.stmt(n, 0)
		case ast.Decl:
			p.decl(n)
		}
	}
}

// The groups top-level items fall into.
const (
	groupHeader = iota
	groupAlone  // a rule, invocation, type, decision, default or conflict: never grouped
	groupUse
	groupParam
	groupLet
	groupAssert
	groupInput
	groupFn
	groupResolution // collect, precedence and exclusive
)

// group returns the group of a top-level item.
func group(n ast.Node) int {
	switch n.(type) {
	case *ast.UseStmt:
		return groupUse
	case *ast.ParamStmt:
		return groupParam
	case *ast.LetStmt:
		return groupLet
	case *ast.AssertStmt:
		return groupAssert
	case *ast.InputDecl:
		return groupInput
	case *ast.FnDecl:
		return groupFn
	case *ast.CollectDecl, *ast.PrecedenceDecl, *ast.ExclusiveDecl:
		return groupResolution
	}
	return groupAlone
}

// header prints `policy name: Kind@N` or its module counterpart.
func (p *printer) header(keyword string, name *ast.PolicyName, kind *ast.Ident, pin *ast.IntLit) {
	if name == nil {
		return
	}
	if kind == nil {
		return
	}
	p.write(keyword + " " + name.String() + ": " + kind.Name)
	p.last = kind.End().Line
	if pin != nil {
		p.write("@" + pin.Text)
		p.last = pin.End().Line
	}
}

// use prints an import in whichever of its three forms it was written.
func (p *printer) use(u *ast.UseStmt) {
	if u == nil {
		return
	}
	p.write("use " + u.Path.String())
	switch {
	case u.Alias != nil:
		p.write(" as " + u.Alias.Name)
	case u.Items != nil:
		items := make([]string, len(u.Items))
		for i, it := range u.Items {
			items[i] = it.Name.Name
			if it.Alias != nil {
				items[i] += " as " + it.Alias.Name
			}
		}
		p.write(".{" + strings.Join(items, ", ") + "}")
	}
	p.last = u.End().Line
}

// stmt prints a statement at indentation level indent, which its first
// line already has. Lines an expression in it breaks onto are indented
// one level deeper.
func (p *printer) stmt(s ast.Stmt, indent int) {
	switch s := s.(type) {
	case *ast.ParamStmt:
		p.write("param " + s.Name.Name + ": ")
		p.typ(s.Type)
		if s.Default != nil {
			p.write(" = ")
			p.expr(s.Default, indent+1)
		}
		// Bounds print in one order, min before max, whichever the author
		// wrote first.
		if s.Min != nil {
			p.write(", min: ")
			p.expr(s.Min, indent+1)
		}
		if s.Max != nil {
			p.write(", max: ")
			p.expr(s.Max, indent+1)
		}
	case *ast.LetStmt:
		if s.Pub {
			p.write("pub ")
		}
		p.write("let " + s.Name.Name + " =")
		p.last = s.Name.End().Line
		if s.Value.Pos().Line > s.Name.Pos().Line {
			p.open(s.Value.Pos(), indent+1, indent+1, false, false)
		} else {
			p.write(" ")
		}
		p.expr(s.Value, indent+1)
	case *ast.WhenStmt:
		p.when(s, indent)
	case *ast.AssertStmt:
		p.write("assert(" + s.Reason.Text + ",")
		p.last = s.Reason.End().Line
		if s.Cond.Pos().Line > s.Reason.Pos().Line {
			p.open(s.Cond.Pos(), indent+1, indent+1, false, false)
		} else {
			p.write(" ")
		}
		p.expr(s.Cond, indent+1)
		p.write(")")
	case *ast.CallStmt:
		p.call(s, indent)
	}
	p.last = s.End().Line
}

// when prints a rule. A rule written on one line with a single decision,
// invocation or assert in its body stays on one line, like a one-line
// function in gofmt; every other body gets a line per statement.
func (p *printer) when(s *ast.WhenStmt, indent int) {
	if s == nil {
		return
	}
	p.write("when ")
	p.expr(s.Cond, indent+1)
	closing := before(s.End())
	if p.oneLine(s) {
		p.write(" { ")
		p.stmt(s.Body[0], indent)
		p.write(" }")
		return
	}
	p.write(" {")
	if len(s.Body) == 0 && !p.commentBefore(closing) {
		p.write("}")
		return
	}
	for i, b := range s.Body {
		p.open(b.Pos(), indent+1, indent+1, i > 0, true)
		p.stmt(b, indent+1)
	}
	p.close(closing, indent, indent+1, len(s.Body) > 0)
	p.write("}")
}

// oneLine reports whether a rule keeps its one-line form.
func (p *printer) oneLine(s *ast.WhenStmt) bool {
	if s == nil {
		return false
	}
	if s.Pos().Line != s.End().Line || len(s.Body) != 1 || p.commentBefore(s.End()) {
		return false
	}
	switch s.Body[0].(type) {
	case *ast.CallStmt, *ast.AssertStmt:
		return true
	}
	return false
}

// commentBefore reports whether a comment not printed yet starts before
// pos.
func (p *printer) commentBefore(pos token.Pos) bool {
	return p.next < len(p.comments) && p.comments[p.next].Pos.Offset < pos.Offset
}

// call prints a decision constructor or a policy invocation. Its
// arguments go one per line, with a trailing comma, when the first of
// them started a new line in the source.
func (p *printer) call(s *ast.CallStmt, indent int) {
	if s == nil {
		return
	}
	p.write(s.Name.Name)
	n := len(s.Args)
	if s.Positional != nil {
		n++
	}
	arg := func(i int) (ast.Node, func(int)) {
		if s.Positional != nil {
			if i == 0 {
				return s.Positional, func(ind int) { p.expr(s.Positional, ind) }
			}
			i--
		}
		a := s.Args[i]
		return a.Name, func(ind int) {
			p.write(a.Name.Name + ": ")
			p.expr(a.Value, ind)
		}
	}
	p.list("(", ")", s.Name.End().Line, before(s.End()), n, arg, indent)
}

// decl prints a kind declaration at the top level of its document.
func (p *printer) decl(d ast.Decl) {
	switch d := d.(type) {
	case *ast.TypeDecl:
		p.write("type " + d.Name.Name + " {")
		p.last = d.Name.End().Line
		if len(d.Fields) == 0 && !p.commentBefore(d.End()) {
			p.write("}")
			break
		}
		for i, f := range d.Fields {
			p.open(f.Name.Pos(), 1, 1, i > 0, true)
			p.field(f)
		}
		p.close(before(d.End()), 0, 1, len(d.Fields) > 0)
		p.write("}")
	case *ast.InputDecl:
		p.write("input " + d.Name.Name + ": ")
		p.typ(d.Type)
	case *ast.FnDecl:
		p.write("fn " + d.Name.Name + "(")
		for i, t := range d.Params {
			if i > 0 {
				p.write(", ")
			}
			p.typ(t)
		}
		p.write(") -> ")
		p.typ(d.Result)
	case *ast.DecisionDecl:
		p.decision(d)
	case *ast.PrecedenceDecl:
		p.write("precedence ")
		if d.Scope != nil {
			p.write(d.Scope.Name + ": ")
		}
		names := make([]string, len(d.Names))
		for i, n := range d.Names {
			names[i] = n.Name
		}
		p.write(strings.Join(names, " > "))
	case *ast.ExclusiveDecl:
		outcomes := make([]string, len(d.Outcomes))
		for i, o := range d.Outcomes {
			outcomes[i] = o.String()
		}
		p.write("exclusive " + strings.Join(outcomes, ", "))
	case *ast.CollectDecl:
		if d.All {
			p.write("collect all")
		} else {
			p.write("collect one")
		}
	case *ast.DefaultDecl:
		p.write("default ")
		p.call(d.Call, 0)
	case *ast.ConflictDecl:
		p.write("conflict ")
		p.call(d.Call, 0)
	}
	p.last = d.End().Line
}

// decision prints a decision: its payload fields, laid out like call
// arguments, then its reasons, one per line.
func (p *printer) decision(d *ast.DecisionDecl) {
	if d == nil {
		return
	}
	p.write("decision " + d.Name.Name)
	p.last = d.Name.End().Line
	if len(d.Fields) > 0 {
		field := func(i int) (ast.Node, func(int)) {
			return d.Fields[i].Name, func(int) { p.field(d.Fields[i]) }
		}
		p.list("(", ")", d.Name.End().Line, d.Reasons[0].Pos(), len(d.Fields), field, 1)
	}
	p.write(" {")
	for i, r := range d.Reasons {
		p.open(r.Pos(), 1, 1, i > 0, true)
		p.write(r.Name)
		p.last = r.End().Line
	}
	p.close(before(d.End()), 0, 1, true)
	p.write("}")
}

// field prints `name: type`, with a payload field's default.
func (p *printer) field(f *ast.Field) {
	p.write(f.Name.Name + ": ")
	p.typ(f.Type)
	if f.Default != nil {
		p.write(" = ")
		p.expr(f.Default, 2)
	}
	p.last = f.Type.End().Line
	if f.Default != nil {
		p.last = f.Default.End().Line
	}
}

// typ prints a type expression.
func (p *printer) typ(t ast.Type) {
	switch t := t.(type) {
	case *ast.NamedType:
		p.write(t.Name.Name)
	case *ast.OptionalType:
		p.write("?")
		p.typ(t.Elem)
	case *ast.ListType:
		p.write("list<")
		p.typ(t.Elem)
		p.write(">")
	case *ast.MapType:
		p.write("map<")
		p.typ(t.Key)
		p.write(", ")
		p.typ(t.Value)
		p.write(">")
	}
	p.last = t.End().Line
}

// before returns the position of the one-byte token that ends just
// before end, such as a closing bracket.
func before(end token.Pos) token.Pos {
	return token.Pos{Offset: end.Offset - 1, Line: end.Line, Column: end.Column - 1}
}
