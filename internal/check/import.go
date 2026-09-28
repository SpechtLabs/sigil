package check

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/types"
)

// Exported is what a checked document offers to documents that import
// or invoke it: its pub lets with their types, and, for a policy, its
// params. The bundle builds one per document and hands them to later
// documents through a Resolver.
type Exported struct {
	Lets    map[string]types.Type // pub lets
	Name    string
	Private []string // lets that aren't pub, for the hint when one is imported
	Params  []*ExportedParam
	Module  bool
	Kind    bool // the name belongs to a kind document, which can't be imported
	// Failed marks a document that couldn't be checked, such as one in an
	// import cycle. Importing from it is accepted without a word, so its
	// own error isn't repeated at every use.
	Failed bool
}

// ExportedParam is one param of an invocable policy.
type ExportedParam struct {
	Type     types.Type
	Decl     *ast.ParamStmt
	Name     string
	Required bool
}

// Param returns the param called name, or nil.
func (e *Exported) Param(name string) *ExportedParam {
	for _, p := range e.Params {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// ParamNames lists the params in declaration order.
func (e *Exported) ParamNames() []string {
	names := make([]string, len(e.Params))
	for i, p := range e.Params {
		names[i] = p.Name
	}
	return names
}

// LetNames lists the pub lets, sorted.
func (e *Exported) LetNames() []string {
	names := make([]string, 0, len(e.Lets))
	for n := range e.Lets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Resolver finds the document a `use` names, by the name in its header.
// It returns false for a name the bundle doesn't define.
type Resolver func(name string) (*Exported, bool)

// Read is an imported let an expression reads: the document it comes
// from and the let's name there.
type Read struct {
	Doc string
	Let string
}

// uses binds every import. A whole import binds the document under its
// last path segment or alias; a selective import binds each listed pub
// let. Nothing in the document may take an imported name.
func (c *Checker) uses(uses []*ast.UseStmt, env *Env) {
	for _, u := range uses {
		doc, ok := c.resolveUse(u)
		if !ok {
			continue
		}
		if len(u.Items) == 0 {
			name := u.Path.Parts[len(u.Path.Parts)-1]
			if u.Alias != nil {
				name = u.Alias
			}
			entity := Invocable
			if doc.Module {
				entity = Module
			}
			c.declare(name, env, Binding{Entity: entity, Doc: doc})
			continue
		}
		for _, item := range u.Items {
			t, ok := c.importedLet(doc, item.Name)
			if !ok && !doc.Failed {
				continue
			}
			if !ok {
				t = types.Invalid
			}
			name := item.Name
			if item.Alias != nil {
				name = item.Alias
			}
			c.declare(name, env, Binding{Entity: Let, Type: t, Doc: doc, Let: item.Name.Name})
		}
	}
}

// resolveUse finds the document a `use` names.
func (c *Checker) resolveUse(u *ast.UseStmt) (*Exported, bool) {
	path := u.Path.String()
	var doc *Exported
	ok := false
	if c.Resolver != nil {
		doc, ok = c.Resolver(path)
	}
	switch {
	case !ok:
		c.errorf(u.Path, "`use` names a policy or module by the name in its header, wherever its file is", "unknown document `%s`", path)
		return nil, false
	case doc.Kind:
		c.errorf(u.Path, "a kind is the contract every document is checked against; it has nothing to import", "`%s` is a kind, not a policy or module", path)
		return nil, false
	}
	return doc, true
}

// importedLet returns the type of the pub let called name in doc, or
// reports why there isn't one.
func (c *Checker) importedLet(doc *Exported, name *ast.Ident) (types.Type, bool) {
	if t, ok := doc.Lets[name.Name]; ok {
		return t, true
	}
	if doc.Failed {
		return types.Invalid, false
	}
	if slices.Contains(doc.Private, name.Name) {
		c.errorf(name, fmt.Sprintf("only `pub let`s can be imported; mark it `pub let %s = ...` in %s", name.Name, doc.Name),
			"let `%s` of %s is private", name.Name, doc.Name)
		return nil, false
	}
	help := fmt.Sprintf("%s exports: %s", doc.Name, strings.Join(doc.LetNames(), ", "))
	if len(doc.Lets) == 0 {
		help = fmt.Sprintf("%s exports no lets", doc.Name)
	}
	if closest, ok := nearest(name.Name, doc.LetNames()); ok {
		help = fmt.Sprintf("did you mean `%s`? %s", closest, help)
	}
	c.errorf(name, help, "%s has no pub let `%s`", doc.Name, name.Name)
	return nil, false
}

// qualified types `common.cleared`, a pub let read through a whole
// import, and records the read for the evaluator.
func (c *Checker) qualified(x *ast.SelectorExpr, b Binding) types.Type {
	if x.Optional {
		c.errorf(x.Sel, "`?.` reads a field of an optional struct; an import is never absent", "`%s` isn't optional", x.X.(*ast.Ident).Name)
		return types.Invalid
	}
	t, ok := c.importedLet(b.Doc, x.Sel)
	if !ok {
		return types.Invalid
	}
	c.info.Reads[x] = Read{Doc: b.Doc.Name, Let: x.Sel.Name}
	return t
}

// invocation checks a policy invocation: named arguments only, each one
// a param the policy declares, of its type, given once, and static, so
// the call is an instantiation the compiler can bind before any input
// exists.
func (c *Checker) invocation(s *ast.CallStmt, doc *Exported, env *Env) {
	c.info.Invocations[s] = doc.Name
	if doc.Failed {
		for _, arg := range s.Args {
			c.Expr(arg.Value, env)
		}
		return
	}
	declares := fmt.Sprintf("%s declares: %s", doc.Name, strings.Join(doc.ParamNames(), ", "))
	if len(doc.Params) == 0 {
		declares = fmt.Sprintf("%s declares no params", doc.Name)
	}
	if s.Positional != nil {
		c.errorf(s.Positional, "an invocation binds params by name, like `guardrails(min_soak: 4h)`; "+declares, "invocation arguments are named")
		c.Expr(s.Positional, env)
	}
	given := map[string]bool{}
	for _, arg := range s.Args {
		p := doc.Param(arg.Name.Name)
		if p == nil {
			help := declares
			if closest, ok := nearest(arg.Name.Name, doc.ParamNames()); ok {
				help = fmt.Sprintf("did you mean `%s`? %s", closest, declares)
			}
			c.errorf(arg.Name, help, "policy %s has no param `%s`", doc.Name, arg.Name.Name)
			c.Expr(arg.Value, env)
			continue
		}
		if given[p.Name] {
			c.errorf(arg.Name, "bind each param once", "param `%s` is given twice", p.Name)
		}
		given[p.Name] = true
		if c.ExprAs(arg.Value, env, p.Type) != types.Invalid {
			c.static(arg.Value, env)
		}
	}
	for _, p := range doc.Params {
		if p.Required && !given[p.Name] {
			c.errorf(s, declares, "invocation of %s doesn't bind param `%s`", doc.Name, p.Name)
		}
	}
}

// static reports anything in an invocation argument that isn't known
// when the policy compiles: inputs, lets, quantifiers and host function
// calls. Only constants and the invoking policy's own params are.
func (c *Checker) static(x ast.Expr, env *Env) {
	const help = "an invocation is bound when the policy compiles, so its arguments are constants and the invoking policy's params; use a `when` around the call, or a param, for anything that depends on input"
	ast.Inspect(x, func(n ast.Expr) bool {
		switch n := n.(type) {
		case *ast.Ident:
			b, ok := env.Lookup(n.Name)
			if !ok || b.Entity == Param {
				return true
			}
			if b.Entity == Let && b.Doc != nil {
				c.errorf(n, help, "invocation argument reads imported let `%s`", n.Name)
				return false
			}
			c.errorf(n, help, "invocation argument reads %s `%s`", b.Entity, n.Name)
			return false
		case *ast.CallExpr:
			c.errorf(n, help, "invocation argument calls a host function")
			return false
		case *ast.QuantExpr:
			c.errorf(n, help, "invocation argument quantifies over input")
			return false
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok {
				if b, found := env.Lookup(id.Name); found && (b.Entity == Module || b.Entity == Invocable) {
					c.errorf(n, help, "invocation argument reads imported let `%s`", ast.Sprint(n))
					return false
				}
			}
		}
		return true
	})
}

// exported builds what other documents may see of the checked document.
func (c *Checker) buildExported(name string, module bool, env *Env, stmts []ast.Stmt) *Exported {
	e := &Exported{Name: name, Module: module, Lets: map[string]types.Type{}}
	for n, st := range c.letStates {
		if st.env != env {
			continue // a scoped let is never visible outside its body
		}
		if st.stmt.Pub {
			e.Lets[n] = env.names[n].Type
		} else {
			e.Private = append(e.Private, n)
		}
	}
	sort.Strings(e.Private)
	for _, s := range stmts {
		if p, ok := s.(*ast.ParamStmt); ok {
			e.Params = append(e.Params, &ExportedParam{Name: p.Name.Name, Type: c.info.Params[p], Required: p.Default == nil, Decl: p})
		}
	}
	return e
}
