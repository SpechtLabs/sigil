package check

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// Policy checks a policy document against k and records what the
// evaluator needs: every expression's type, the lets in an order that
// respects their dependencies, and the decision each constructor builds.
// Imports and invocations belong to the composition milestone and are
// reported as unsupported for now.
func (c *Checker) Policy(doc *ast.PolicyDoc, k *kind.Kind) {
	if doc == nil || !c.checkKind(doc.Kind, k) {
		return
	}
	env := NewEnv(k)
	c.uses(doc.Uses)
	c.params(doc.Stmts, env)
	c.lets(letsOf(doc.Stmts), env)
	for _, s := range doc.Stmts {
		switch s := s.(type) {
		case *ast.WhenStmt:
			c.when(s, env)
		case *ast.AssertStmt:
			c.assert(s, env)
		case *ast.CallStmt:
			c.topCall(s, env)
		}
	}
}

// Module checks a module document against k. A module holds only lets.
func (c *Checker) Module(doc *ast.ModuleDoc, k *kind.Kind) {
	if doc == nil || !c.checkKind(doc.Kind, k) {
		return
	}
	env := NewEnv(k)
	c.uses(doc.Uses)
	c.lets(doc.Lets, env)
}

// checkKind reports a document written for another kind.
func (c *Checker) checkKind(name *ast.Ident, k *kind.Kind) bool {
	if k == nil || name == nil {
		return false
	}
	if name.Name != k.Name {
		c.errorf(name, fmt.Sprintf("this document can only be checked against kind %s", name.Name),
			"document is for kind %s, not %s", name.Name, k.Name)
		return false
	}
	return true
}

func (c *Checker) uses(uses []*ast.UseStmt) {
	for _, u := range uses {
		c.errorf(u, "modules, imports and policy invocation come with the composition milestone", "`use` isn't supported yet")
	}
}

// params declares the policy's params: unique names, types from the
// kind, constant defaults of the declared type.
func (c *Checker) params(stmts []ast.Stmt, env *Env) {
	for _, s := range stmts {
		p, ok := s.(*ast.ParamStmt)
		if !ok {
			continue
		}
		t := c.resolveType(p.Type, env.Kind(), false)
		if opt, isOpt := t.(*types.Optional); isOpt {
			c.errorf(p.Type, fmt.Sprintf("an optional param is a param with a default; write `param %s: %s = <default>`", p.Name.Name, opt.Elem),
				"a param can't be optional")
			t = types.Invalid
		}
		if t == types.Decision || isDecisionList(t) {
			c.errorf(p.Type, "decision values only come from `outcome`", "a param can't hold decision values")
			t = types.Invalid
		}
		c.declare(p.Name, env, Binding{Entity: Param, Type: t})
		if p.Default != nil && t != types.Invalid {
			if _, err := constant.Eval(p.Default, t); err != nil {
				err.File = c.file
				c.errs = append(c.errs, err)
			}
		}
	}
}

func isDecisionList(t types.Type) bool {
	l, ok := t.(*types.List)
	return ok && l.Elem == types.Decision
}

// declare binds name in env, reporting a collision with what already
// holds the name.
func (c *Checker) declare(name *ast.Ident, env *Env, b Binding) bool {
	prev, ok := env.Declare(name.Name, b)
	if !ok {
		c.errorf(name, "every name in a document means one thing; rename one of them",
			"`%s` is already the name of %s %s", name.Name, article(prev.Entity), prev.Entity)
	}
	return ok
}

// resolveType turns a type expression into a type against k's struct
// types, for a param or for a declaration in the kind itself, which
// inKind says. An unknown name or a map key that isn't a scalar is
// reported here with a hint and becomes types.Invalid, and so does any
// type built around one; kind.Validate lets types.Invalid pass.
func (c *Checker) resolveType(t ast.Type, k *kind.Kind, inKind bool) types.Type {
	switch t := t.(type) {
	case *ast.NamedType:
		if b, ok := types.Lookup(t.Name.Name); ok {
			return b
		}
		if s := k.Type(t.Name.Name); s != nil {
			return s
		}
		candidates := types.ScalarNames()
		for _, s := range k.Types {
			candidates = append(candidates, s.Name)
		}
		help := "types are the built-ins and the struct types the kind declares"
		if inKind {
			help = "declare it with `type " + t.Name.Name + " { ... }`, or use a built-in type"
		}
		if closest, ok := nearest(t.Name.Name, candidates); ok {
			help = fmt.Sprintf("did you mean `%s`?", closest)
		}
		c.errorf(t, help, "unknown type `%s`", t.Name.Name)
		return types.Invalid
	case *ast.OptionalType:
		elem := c.resolveType(t.Elem, k, inKind)
		if elem == types.Invalid {
			return types.Invalid
		}
		return &types.Optional{Elem: elem}
	case *ast.ListType:
		elem := c.resolveType(t.Elem, k, inKind)
		if elem == types.Invalid {
			return types.Invalid
		}
		return &types.List{Elem: elem}
	case *ast.MapType:
		key, val := c.resolveType(t.Key, k, inKind), c.resolveType(t.Value, k, inKind)
		if key == types.Invalid || val == types.Invalid {
			return types.Invalid
		}
		if !types.IsKey(key) {
			c.errorf(t.Key, "map keys are scalars: bool, int, float, string, duration or timestamp", "%s can't be a map key", key)
			return types.Invalid
		}
		return &types.Map{Key: key, Value: val}
	}
	return types.Invalid
}

func letsOf(stmts []ast.Stmt) []*ast.LetStmt {
	var lets []*ast.LetStmt
	for _, s := range stmts {
		if l, ok := s.(*ast.LetStmt); ok {
			lets = append(lets, l)
		}
	}
	return lets
}

// letState tracks a let while its type is being worked out.
type letState struct {
	stmt  *ast.LetStmt
	env   *Env
	state uint8 // 0 unchecked, 1 checking, 2 done
}

// lets declares every let, then types each one, following references to
// other lets as it meets them so declaration order doesn't matter. A let
// that reaches itself is a cycle, reported once at the let that closes it.
func (c *Checker) lets(lets []*ast.LetStmt, env *Env) {
	c.letStates = map[string]*letState{}
	for _, l := range lets {
		if c.declare(l.Name, env, Binding{Entity: Let}) {
			c.letStates[l.Name.Name] = &letState{stmt: l, env: env}
		}
	}
	for _, l := range lets {
		c.checkLet(l.Name.Name, nil)
	}
	c.letStates = nil
}

// checkLet types the let called name, if it hasn't been yet, and returns
// its type. use is the reference that led here, for the cycle message.
func (c *Checker) checkLet(name string, use *ast.Ident) types.Type {
	st, ok := c.letStates[name]
	if !ok {
		return types.Invalid
	}
	switch st.state {
	case 2:
		return st.env.names[name].Type
	case 1:
		at := ast.Node(st.stmt.Name)
		if use != nil {
			at = use
		}
		c.errorf(at, "lets form a directed acyclic graph; a let can't depend on itself, even through other lets",
			"let `%s` depends on itself", name)
		st.env.names[name] = Binding{Entity: Let, Type: types.Invalid}
		return types.Invalid
	}
	st.state = 1
	t := c.Expr(st.stmt.Value, st.env)
	st.state = 2
	if _, done := st.env.names[name]; done && st.env.names[name].Type == nil {
		st.env.names[name] = Binding{Entity: Let, Type: t}
	}
	c.info.Lets = append(c.info.Lets, st.stmt)
	return st.env.names[name].Type
}

// when checks a rule: a bool condition and a body of rules, asserts and
// constructors.
func (c *Checker) when(s *ast.WhenStmt, env *Env) {
	c.ExprAs(s.Cond, env, types.Bool)
	for _, inner := range s.Body {
		switch inner := inner.(type) {
		case *ast.WhenStmt:
			c.when(inner, env)
		case *ast.AssertStmt:
			c.assert(inner, env)
		case *ast.CallStmt:
			c.callStmt(inner, env)
		}
	}
}

// assert checks an assertion: a bool condition that may read outcome, and
// a reason.
func (c *Checker) assert(s *ast.AssertStmt, env *Env) {
	inner := env.Child()
	inner.InAssert = true
	c.ExprAs(s.Cond, inner, types.Bool)
	if s.Reason != nil && s.Reason.Value == "" {
		c.errorf(s.Reason, "the reason is a stable identifier for metrics and grep, like `sod_customer_dev`", "an assert's reason can't be empty")
	}
}

// topCall checks a call at the top level, where only a policy invocation
// belongs: a constructor there would fire unconditionally.
func (c *Checker) topCall(s *ast.CallStmt, env *Env) {
	if b, ok := env.Lookup(s.Name.Name); ok && b.Entity == DecisionName {
		c.errorf(s.Name, fmt.Sprintf("a constructor here would fire for every input; wrap it in `when true { %s(...) }` if that's what you mean", s.Name.Name),
			"decision constructors go inside a `when` block")
		c.constructor(s, env)
		return
	}
	c.callStmt(s, env)
}

// callStmt checks a constructor or an invocation statement by its name.
func (c *Checker) callStmt(s *ast.CallStmt, env *Env) {
	b, ok := env.Lookup(s.Name.Name)
	switch {
	case !ok:
		help := "constructors name one of the kind's decisions; invocations name an imported policy"
		if closest, found := env.Closest(s.Name.Name); found {
			help = fmt.Sprintf("did you mean `%s`?", closest)
		}
		c.errorf(s.Name, help, "unknown decision `%s`", s.Name.Name)
	case b.Entity == DecisionName:
		c.constructor(s, env)
	default:
		c.errorf(s.Name, "only the kind's decisions can be constructed, and only imported policies invoked",
			"`%s` is %s %s, not a decision", s.Name.Name, article(b.Entity), b.Entity)
	}
}

// constructor checks a decision constructor: a literal reason first, then
// the payload fields by name, each of its declared type, with every field
// without a default given exactly once.
func (c *Checker) constructor(s *ast.CallStmt, env *Env) {
	d := env.Kind().Decision(s.Name.Name)
	if d == nil {
		return
	}
	c.info.Constructors[s] = d

	switch reason := s.Positional.(type) {
	case nil:
		c.errorf(s, fmt.Sprintf("write `%s(\"<reason>\")`; %s is declared as: %s", d.Name, d.Name, d.Signature()),
			"decision %s needs a reason", d.Name)
	case *ast.StringLit:
		c.record(reason, types.String)
		switch {
		case reason.Raw:
			c.errorf(reason, "raw strings are for patterns; write the reason as \"...\"", "decision reason must be a double-quoted string")
		case reason.Value == "":
			c.errorf(reason, "the reason is a stable identifier for metrics and grep, like `soak_too_short`", "decision reason can't be empty")
		}
	default:
		c.errorf(s.Positional, fmt.Sprintf("put dynamic text in a `detail` field; declare `detail: string = \"\"` on decision %s in the kind", d.Name),
			"decision reason must be a string literal")
		c.Expr(s.Positional, env)
	}

	given := map[string]bool{}
	for _, arg := range s.Args {
		f := d.Field(arg.Name.Name)
		if f == nil {
			help := fmt.Sprintf("%s is declared as: %s", d.Name, d.Signature())
			if closest, ok := closestField(d, arg.Name.Name); ok {
				help = fmt.Sprintf("did you mean %q? %s", closest, help)
			}
			c.errorf(arg.Name, help, "decision %s has no payload field %q", d.Name, arg.Name.Name)
			c.Expr(arg.Value, env)
			continue
		}
		if given[f.Name] {
			c.errorf(arg.Name, "pass each field once", "field %q is given twice", f.Name)
		}
		given[f.Name] = true
		c.ExprAs(arg.Value, env, f.Type)
	}
	for _, f := range d.Fields {
		if !given[f.Name] && !f.HasDefault {
			c.errorf(s, fmt.Sprintf("%s is declared as: %s", d.Name, d.Signature()), "decision %s needs field %q", d.Name, f.Name)
		}
	}
}

func closestField(d *kind.Decision, name string) (string, bool) {
	names := make([]string, len(d.Fields))
	for i, f := range d.Fields {
		names[i] = f.Name
	}
	return nearest(name, names)
}
