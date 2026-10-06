package lsp

import (
	"fmt"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// target is the name at a position and what it refers to: what hover
// shows, and where definition goes.
type target struct {
	hover string     // markdown
	defs  []location // where the name is declared
	from  int        // the name's span in the file
	to    int
}

// location is a span of a file the project read.
type location struct {
	file string
	from int
	to   int
}

// resolver finds the target at a position in one checked document.
type resolver struct {
	v      *view
	doc    *bundle.Document
	info   *check.Info
	kind   *kind.Kind
	offset int
}

// targetAt returns the name at offset and what it refers to, or nil
// where there's no name, or the document wasn't checked.
func (v *view) targetAt(offset int) *target {
	d := v.docAt(offset)
	if d == nil || d.Info == nil {
		return nil
	}
	r := &resolver{v: v, doc: d, info: d.Info, kind: v.kindOf(d), offset: offset}
	if r.kind == nil {
		return nil
	}
	switch n := d.Node.(type) {
	case *ast.PolicyDoc:
		if t := r.header(n.Kind); t != nil {
			return t
		}
		if t := r.uses(n.Uses); t != nil {
			return t
		}
		return r.stmts(n.Stmts)
	case *ast.ModuleDoc:
		if t := r.header(n.Kind); t != nil {
			return t
		}
		if t := r.uses(n.Uses); t != nil {
			return t
		}
		stmts := make([]ast.Stmt, len(n.Lets))
		for i, l := range n.Lets {
			stmts[i] = l
		}
		return r.stmts(stmts)
	}
	return nil
}

// at reports whether the position is on n.
func (r *resolver) at(n ast.Node) bool { return within(n, r.offset) }

// header is the kind a header names: the whole contract.
func (r *resolver) header(k *ast.Ident) *target {
	if k == nil || !r.at(k) {
		return nil
	}
	t := r.on(k)
	t.hover = code(strings.TrimSuffix(r.kind.Source(), "\n"))
	if doc, file := r.v.proj.KindSource(k.Name); doc != nil {
		t.defs = []location{{file: file, from: doc.Name.Pos().Offset, to: doc.Name.End().Offset}}
	}
	return t
}

// on starts the target of the name n.
func (r *resolver) on(n ast.Node) *target {
	from, to := span(n)
	return &target{from: from, to: to}
}

// uses finds the target in the imports: a path, an alias or an imported
// name.
func (r *resolver) uses(uses []*ast.UseStmt) *target {
	for _, u := range uses {
		if !r.at(u) {
			continue
		}
		doc := r.v.document(u.Path.String())
		switch {
		case r.at(u.Path):
			return r.document(r.on(u.Path), doc)
		case u.Alias != nil && r.at(u.Alias):
			return r.document(r.on(u.Alias), doc)
		}
		for _, it := range u.Items {
			switch {
			case r.at(it.Name):
				return r.importedLet(r.on(it.Name), doc, it.Name.Name)
			case it.Alias != nil && r.at(it.Alias):
				return r.importedLet(r.on(it.Alias), doc, it.Name.Name)
			}
		}
	}
	return nil
}

// stmts finds the target in a list of statements.
func (r *resolver) stmts(stmts []ast.Stmt) *target {
	for _, s := range stmts {
		if !r.at(s) {
			continue
		}
		switch s := s.(type) {
		case *ast.ParamStmt:
			return r.param(s)
		case *ast.LetStmt:
			if r.at(s.Name) {
				return r.let(r.on(s.Name), s)
			}
			return r.expr(s.Value)
		case *ast.WhenStmt:
			if r.at(s.Cond) {
				return r.expr(s.Cond)
			}
			return r.stmts(s.Body)
		case *ast.AssertStmt:
			return r.expr(s.Cond)
		case *ast.CallStmt:
			return r.call(s)
		}
	}
	return nil
}

// param finds the target in a param: its name, its type, or a name in
// its default or bounds.
func (r *resolver) param(s *ast.ParamStmt) *target {
	if r.at(s.Name) {
		t := r.on(s.Name)
		t.hover = code("param " + paramText(s, r.info.Params[s]))
		t.defs = r.here(s.Name)
		return t
	}
	if r.at(s.Type) {
		return r.typeName(s.Type)
	}
	for _, x := range []ast.Expr{s.Default, s.Min, s.Max} {
		if x != nil && r.at(x) {
			return r.expr(x)
		}
	}
	return nil
}

// paramText renders a param declaration without its keyword.
func paramText(s *ast.ParamStmt, t types.Type) string {
	return paramSource(&check.ExportedParam{Name: s.Name.Name, Type: t, Decl: s})
}

// typeName finds the struct type or enum a type expression names.
func (r *resolver) typeName(t ast.Type) *target {
	switch t := t.(type) {
	case *ast.NamedType:
		if s := r.kind.Type(t.Name.Name); s != nil {
			out := r.on(t)
			find := func(d ast.Decl) *ast.Ident { return typeDecl(d, s.Name) }
			out.hover = joinLines(code(structSource(s)), r.comment(find))
			out.defs = r.inKind(find)
			return out
		}
		if e := r.kind.Enum(t.Name.Name); e != nil {
			return r.enum(r.on(t), e)
		}
	case *ast.OptionalType:
		return r.typeName(t.Elem)
	case *ast.ListType:
		if r.at(t.Elem) {
			return r.typeName(t.Elem)
		}
	case *ast.MapType:
		if r.at(t.Key) {
			return r.typeName(t.Key)
		}
		if r.at(t.Value) {
			return r.typeName(t.Value)
		}
	}
	return nil
}

// let is a let of the document: its declaration, and its type.
func (r *resolver) let(t *target, s *ast.LetStmt) *target {
	kw := "let "
	if s.Pub {
		kw = "pub let "
	}
	b, _ := r.lookup(s.Name)
	t.hover = joinLines(code(kw+typed(s.Name.Name, b.Type)), unchecked(b.Type), code(declsOf(b.Type, r.kind)))
	t.defs = r.here(s.Name)
	return t
}

// call finds the target in a constructor or an invocation: its name, an
// argument's name, a reason, or a name in an argument.
func (r *resolver) call(s *ast.CallStmt) *target {
	d := r.info.Constructors[s]
	var doc *check.Exported
	if b, ok := r.lookup(s.Name); ok && b.Entity == check.Invocable {
		doc = b.Doc
	}
	if r.at(s.Name) {
		switch {
		case d != nil:
			return r.decision(r.on(s.Name), d)
		case doc != nil:
			return r.document(r.on(s.Name), r.v.document(doc.Name))
		}
		return nil
	}
	if s.Positional != nil && r.at(s.Positional) {
		return r.expr(s.Positional)
	}
	for _, a := range s.Args {
		switch {
		case r.at(a.Name) && d != nil:
			return r.payload(r.on(a.Name), d, a.Name.Name)
		case r.at(a.Name) && doc != nil:
			return r.invocationArg(r.on(a.Name), doc, a.Name.Name)
		case !r.at(a.Value):
		case d != nil && a.Name.Name == reasonArg:
			if id, ok := a.Value.(*ast.Ident); ok {
				return r.reason(r.on(id), d, id.Name)
			}
			return nil
		default:
			return r.expr(a.Value)
		}
	}
	return nil
}

// payload is an argument of a constructor: the reason, or a payload
// field.
func (r *resolver) payload(t *target, d *kind.Decision, name string) *target {
	if name == reasonArg {
		t.hover = joinLines(code(reasonArg+": "+strings.Join(d.Reasons, " | ")), fmt.Sprintf("The reason of `%s`.", d.Name))
		t.defs = r.inKind(func(decl ast.Decl) *ast.Ident { return reasonField(decl, d.Name) })
		return t
	}
	f := d.Field(name)
	if f == nil {
		return nil
	}
	find := func(decl ast.Decl) *ast.Ident { return decisionField(decl, d.Name, name) }
	t.hover = joinLines(code(fieldSource(f)), fmt.Sprintf("A payload field of `%s`.", d.Name), r.comment(find), code(declsOf(f.Type, r.kind)))
	t.defs = r.inKind(find)
	return t
}

// invocationArg is an argument of an invocation: a param of the invoked
// policy.
func (r *resolver) invocationArg(t *target, doc *check.Exported, name string) *target {
	p := doc.Param(name)
	if p == nil {
		return nil
	}
	t.hover = joinLines(code("param "+paramSource(p)), fmt.Sprintf("A param of `%s`.", doc.Name))
	if target := r.v.document(doc.Name); target != nil && p.Decl != nil {
		t.defs = []location{{file: target.File, from: p.Decl.Name.Pos().Offset, to: p.Decl.Name.End().Offset}}
	}
	return t
}

// reason is one of a decision's reasons.
func (r *resolver) reason(t *target, d *kind.Decision, name string) *target {
	if !d.HasReason(name) {
		return nil
	}
	t.hover = joinLines(fmt.Sprintf("`%s`, a reason of `%s`.", name, d.Name), code(strings.TrimSuffix(d.Source(), "\n")))
	t.defs = r.inKind(func(decl ast.Decl) *ast.Ident { return decisionReason(decl, d.Name, name) })
	return t
}

// decision is a decision: its whole declaration.
func (r *resolver) decision(t *target, d *kind.Decision) *target {
	find := func(decl ast.Decl) *ast.Ident { return decisionDecl(decl, d.Name) }
	t.hover = joinLines(code(strings.TrimSuffix(d.Source(), "\n")), r.comment(find))
	t.defs = r.inKind(find)
	return t
}

// enum is an enum: its values.
func (r *resolver) enum(t *target, e *types.Enum) *target {
	find := func(decl ast.Decl) *ast.Ident { return enumDecl(decl, e.Name) }
	t.hover = joinLines(code(enumSource(e)), r.comment(find))
	t.defs = r.inKind(find)
	return t
}

// enumValue is one value of an enum.
func (r *resolver) enumValue(t *target, e *types.Enum, value string) *target {
	t.hover = joinLines(fmt.Sprintf("`%s`, a value of `%s`.", value, e.Name), code(enumSource(e)))
	t.defs = r.inKind(func(decl ast.Decl) *ast.Ident { return enumValue(decl, e.Name, value) })
	return t
}

// document is a policy or module a `use` or an invocation names: its
// header, its params and its pub lets.
func (r *resolver) document(t *target, d *bundle.Document) *target {
	if d == nil {
		return nil
	}
	var params, lets string
	if d.Exported != nil {
		params, lets = paramsOf(d.Exported), letsOf(d.Exported)
	}
	t.hover = code(joinLines(describeDoc(d.Node), params, lets))
	if name := docName(d.Node); name != nil {
		t.defs = []location{{file: d.File, from: name.Pos().Offset, to: name.End().Offset}}
	}
	return t
}

// importedLet is a pub let of another document, d.
func (r *resolver) importedLet(t *target, d *bundle.Document, name string) *target {
	if d == nil || d.Exported == nil {
		return nil
	}
	typ, ok := d.Exported.Lets[name]
	if !ok {
		return nil
	}
	t.hover = joinLines(code("pub let "+typed(name, typ)), fmt.Sprintf("From `%s`.", d.Name), r.v.letComment(d.Name, name), code(declsOf(typ, r.kind)))
	if l := letIn(d.Node, name); l != nil {
		t.defs = []location{{file: d.File, from: l.Name.Pos().Offset, to: l.Name.End().Offset}}
	}
	return t
}

// expr finds the target in an expression: the innermost name the
// position is on.
func (r *resolver) expr(x ast.Expr) *target {
	var path []ast.Expr
	ast.Inspect(x, func(n ast.Expr) bool {
		if !r.at(n) {
			return false
		}
		path = append(path, n)
		return true
	})
	if len(path) == 0 {
		return nil
	}
	id, ok := path[len(path)-1].(*ast.Ident)
	if !ok {
		return nil
	}
	if len(path) > 1 {
		switch p := path[len(path)-2].(type) {
		case *ast.SelectorExpr:
			if p.Sel == id {
				return r.selector(p)
			}
		case *ast.QuantExpr:
			if p.Var == id {
				return r.variable(r.on(id), id, r.info.TypeOf(id))
			}
		case *ast.FilterExpr:
			if p.Var == id {
				return r.variable(r.on(id), id, r.info.TypeOf(id))
			}
		}
	}
	return r.name(id)
}

// lookup resolves a name where it's written.
func (r *resolver) lookup(id *ast.Ident) (check.Binding, bool) {
	env := r.info.ScopeAt(id.Pos().Offset)
	if env == nil {
		return check.Binding{}, false
	}
	return env.Lookup(id.Name)
}

// name is a name read in an expression.
func (r *resolver) name(id *ast.Ident) *target {
	b, ok := r.lookup(id)
	if !ok {
		return nil
	}
	t := r.on(id)
	switch b.Entity {
	case check.Input:
		find := func(d ast.Decl) *ast.Ident { return inputDecl(d, id.Name) }
		t.hover = joinLines(code("input "+typed(id.Name, b.Type)), r.comment(find), code(declsOf(b.Type, r.kind)))
		t.defs = r.inKind(find)
	case check.Function:
		find := func(d ast.Decl) *ast.Ident { return fnDecl(d, id.Name) }
		t.hover = joinLines(code(b.Func.Signature()), r.comment(find))
		t.defs = r.inKind(find)
	case check.DecisionName:
		return r.decision(t, r.kind.Decision(id.Name))
	case check.EnumType:
		return r.enum(t, b.Type.(*types.Enum))
	case check.EnumValue:
		e, _ := b.Type.(*types.Enum)
		if e == nil {
			e, _ = r.info.TypeOf(id).(*types.Enum)
		}
		if e == nil {
			return nil
		}
		return r.enumValue(t, e, id.Name)
	case check.Param:
		if p := paramNamed(r.doc.Node, b.Decl); p != nil {
			t.hover = code("param " + paramText(p, b.Type))
		}
		t.defs = r.here(b.Decl)
	case check.Let:
		if b.Doc != nil {
			return r.importedLet(t, r.v.document(b.Doc.Name), b.Let)
		}
		if l := letIn(r.doc.Node, id.Name); l != nil {
			return r.let(t, l)
		}
	case check.QuantVar, check.FilterVar:
		return r.variable(t, b.Decl, b.Type)
	case check.Module, check.Invocable:
		return r.document(t, r.v.document(b.Doc.Name))
	}
	return t
}

// variable is a quantifier's or a filter's variable, declared at decl.
func (r *resolver) variable(t *target, decl *ast.Ident, typ types.Type) *target {
	t.hover = joinLines(code(typed(decl.Name, typ)), unchecked(typ), code(declsOf(typ, r.kind)))
	t.defs = r.here(decl)
	return t
}

// selector is the name after `.` or `?.`: a field, a module's pub let, an
// enum's value, a decision's reason, or the decision after `outcome`.
func (r *resolver) selector(sel *ast.SelectorExpr) *target {
	t := r.on(sel.Sel)
	name := sel.Sel.Name
	if x, ok := sel.X.(*ast.Ident); ok {
		b, _ := r.lookup(x)
		switch b.Entity {
		case check.Module, check.Invocable:
			return r.importedLet(t, r.v.document(b.Doc.Name), name)
		case check.EnumType:
			return r.enumValue(t, b.Type.(*types.Enum), name)
		case check.DecisionName:
			return r.reason(t, r.kind.Decision(x.Name), name)
		}
	}
	if _, ok := sel.X.(*ast.Outcome); ok {
		if d := r.kind.Decision(name); d != nil {
			return r.decision(t, d)
		}
		return nil
	}
	base := r.info.TypeOf(sel.X)
	if opt, ok := base.(*types.Optional); ok {
		base = opt.Elem
	}
	switch b := base.(type) {
	case *types.Struct:
		s := r.kind.Type(b.Name)
		if s == nil || s.Field(name) == nil {
			return nil
		}
		f := s.Field(name)
		find := func(d ast.Decl) *ast.Ident { return typeField(d, s.Name, name) }
		t.hover = joinLines(code(name+": "+f.Type.String()), fmt.Sprintf("A field of `%s`.", s.Name), r.comment(find), code(declsOf(f.Type, r.kind)))
		t.defs = r.inKind(find)
		return t
	case *types.Candidate:
		return r.payload(t, r.kind.Decision(b.Decision), name)
	case *types.List:
		if cand, ok := b.Elem.(*types.Candidate); ok {
			return r.reason(t, r.kind.Decision(cand.Decision), name)
		}
	}
	return nil
}

// here is the location of a name in the document's own file.
func (r *resolver) here(n *ast.Ident) []location {
	if n == nil {
		return nil
	}
	return []location{{file: r.doc.File, from: n.Pos().Offset, to: n.End().Offset}}
}

// inKind is the location of the declaration in the kind's document that
// find picks, or nil for a kind linked into the binary, which has none.
func (r *resolver) inKind(find func(ast.Decl) *ast.Ident) []location {
	doc, file := r.v.proj.KindSource(r.kind.Name)
	if doc == nil {
		return nil
	}
	for _, d := range doc.Decls {
		if id := find(d); id != nil {
			return []location{{file: file, from: id.Pos().Offset, to: id.End().Offset}}
		}
	}
	return nil
}

// docName returns a policy's or module's name.
func docName(d ast.Doc) *ast.PolicyName {
	switch d := d.(type) {
	case *ast.PolicyDoc:
		return d.Name
	case *ast.ModuleDoc:
		return d.Name
	}
	return nil
}

// letIn returns the top-level let called name in a document, or one in a
// `when` body of a policy, or nil.
func letIn(d ast.Doc, name string) *ast.LetStmt {
	switch d := d.(type) {
	case *ast.PolicyDoc:
		return letInStmts(d.Stmts, name)
	case *ast.ModuleDoc:
		for _, l := range d.Lets {
			if l.Name.Name == name {
				return l
			}
		}
	}
	return nil
}

// letInStmts returns the let called name among stmts, `when` bodies
// included, or nil.
func letInStmts(stmts []ast.Stmt, name string) *ast.LetStmt {
	for _, s := range stmts {
		switch s := s.(type) {
		case *ast.LetStmt:
			if s.Name.Name == name {
				return s
			}
		case *ast.WhenStmt:
			if l := letInStmts(s.Body, name); l != nil {
				return l
			}
		}
	}
	return nil
}

// paramNamed returns the param whose name is decl, or nil.
func paramNamed(d ast.Doc, decl *ast.Ident) *ast.ParamStmt {
	p, ok := d.(*ast.PolicyDoc)
	if !ok {
		return nil
	}
	for _, s := range p.Stmts {
		if param, ok := s.(*ast.ParamStmt); ok && param.Name == decl {
			return param
		}
	}
	return nil
}

// comment is the doc comment of the declaration in the kind's document
// that find picks, or "" for none.
func (r *resolver) comment(find func(ast.Decl) *ast.Ident) string {
	return r.v.kindComment(r.kind.Name, find)
}
