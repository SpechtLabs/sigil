package check

import (
	"fmt"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// Policy checks a policy document against k and records what the
// evaluator needs: every expression's type, the lets in an order that
// respects their dependencies, the decision each constructor builds and
// the policy each invocation instantiates. Imports resolve through the
// checker's Resolver.
func (c *Checker) Policy(doc *ast.PolicyDoc, k *kind.Kind) {
	if doc == nil || !c.checkKind(doc.Kind, doc.Pin, k) {
		return
	}
	env := NewEnv(k)
	c.letStates = map[string]*letState{}
	defer func() { c.letStates = nil }()
	c.uses(doc.Uses, env)
	c.params(doc.Stmts, env)
	lets := letsOf(doc.Stmts)
	c.lets(lets, env)
	c.pubLets(lets)
	defer func() { c.exported = c.buildExported(doc.Name.String(), false, env, doc.Stmts) }()
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
	if doc == nil || !c.checkKind(doc.Kind, doc.Pin, k) {
		return
	}
	env := NewEnv(k)
	c.letStates = map[string]*letState{}
	defer func() { c.letStates = nil }()
	c.uses(doc.Uses, env)
	c.lets(doc.Lets, env)
	c.pubLets(doc.Lets)
	c.exported = c.buildExported(doc.Name.String(), true, env, nil)
}

// checkKind reports a document written for another kind, and a pin the
// kind doesn't accept. A bad pin doesn't stop the check: the rest of the
// document is still checked against the kind the host has.
func (c *Checker) checkKind(name *ast.Ident, pin *ast.IntLit, k *kind.Kind) bool {
	if k == nil || name == nil {
		return false
	}
	if name.Name != k.Name {
		c.errorf(name, fmt.Sprintf("this document can only be checked against kind %s", name.Name),
			"document is for kind %s, not %s", name.Name, k.Name)
		return false
	}
	c.older = false
	switch {
	case pin == nil:
		c.errorf(name, fmt.Sprintf("pin the kind version the document was written against, like `%s@%d`", k.Name, k.Version),
			"`%s` needs a version", k.Name)
	case int(pin.Value) > k.Version:
		c.errorf(pin, "the host's kind is older than the document; upgrade the host, or check the document against this version and lower the pin",
			"%s@%d is newer than the kind, which is at version %d", k.Name, pin.Value, k.Version)
	case int(pin.Value) < k.Oldest():
		c.errorf(pin, fmt.Sprintf("review the document against the kind's changes since version %d, then raise the pin", pin.Value),
			"%s@%d is no longer accepted; the kind accepts version %d and later", k.Name, pin.Value, k.Oldest())
	default:
		c.older = int(pin.Value) < k.Version
	}
	return true
}

// params declares the policy's params: unique names, types from the
// kind, constant defaults of the declared type, and bounds around them.
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
		c.info.Params[p] = t
		if t == types.Invalid {
			continue
		}
		c.bounds(p, t, c.constant(p.Default, t))
	}
}

// bounds checks a param's `min` and `max`: constants of an ordered type
// with a literal, in order, with def, the default's value if it has one,
// between them. Invocation arguments are checked against them where the
// policy is invoked.
func (c *Checker) bounds(p *ast.ParamStmt, t types.Type, def any) {
	if p.Min == nil && p.Max == nil {
		return
	}
	if t != types.Int && t != types.Float && t != types.Duration {
		at := p.Min
		if at == nil {
			at = p.Max
		}
		c.errorf(at, "bounds apply to int, float and duration params", "a param of type %s can't have bounds", t)
		return
	}
	lo, hi := c.constant(p.Min, t), c.constant(p.Max, t)
	if lo != nil && hi != nil && constant.Compare(lo, hi) > 0 {
		c.errorf(p.Max, "", "max %s is below min %s", ast.Sprint(p.Max), ast.Sprint(p.Min))
		return
	}
	switch {
	case def == nil:
	case lo != nil && constant.Compare(def, lo) < 0:
		c.errorf(p.Default, "a default has to be a value the param accepts", "default %s is below the minimum %s", ast.Sprint(p.Default), ast.Sprint(p.Min))
	case hi != nil && constant.Compare(def, hi) > 0:
		c.errorf(p.Default, "a default has to be a value the param accepts", "default %s is above the maximum %s", ast.Sprint(p.Default), ast.Sprint(p.Max))
	}
}

// constant evaluates x as a constant of type t and returns its value, or
// nil when x is nil or isn't a constant of t, which it reports.
func (c *Checker) constant(x ast.Expr, t types.Type) any { //nolint:emptyinterface // constants are typed by their Sigil type; see constant.Conforms
	if x == nil {
		return nil
	}
	v, err := constant.Eval(x, t)
	if err != nil {
		err.File = c.file
		c.errs = append(c.errs, err)
		return nil
	}
	return v
}

func isDecisionList(t types.Type) bool {
	l, ok := t.(*types.List)
	return ok && l.Elem == types.Decision
}

// declare binds name in env, reporting a collision with what already
// holds the name. See keeps for the one collision that isn't an error.
func (c *Checker) declare(name *ast.Ident, env *Env, b Binding) bool {
	prev, ok := env.Declare(name.Name, b)
	switch {
	case ok:
	case c.keeps(prev):
		env.Bind(name.Name, b)
		c.info.Shadows = append(c.info.Shadows, name)
		return true
	default:
		c.errorf(name, "every name in a document means one thing; rename one of them",
			"`%s` is already the name of %s %s", name.Name, article(prev.Entity), prev.Entity)
	}
	return ok
}

// keeps reports whether a document name may take the name prev holds. A
// document pinned to an older kind version compiled against that version,
// where any collision was an error, so a kind input, host function or
// decision it collides with must have been added since. The document keeps
// its own name, and adding a name to a kind never breaks a policy.
func (c *Checker) keeps(prev Binding) bool {
	switch prev.Entity {
	case Input, Function, DecisionName:
		return c.older
	}
	return false
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
	param string // the first param the let reads, directly or through other lets
	state uint8  // 0 unchecked, 1 checking, 2 done
}

// lets declares the lets of one scope, the top level or a `when` body, then
// types each one, following references to other lets as it meets them so
// declaration order doesn't matter. A let that reaches itself is a cycle,
// reported once at the let that closes it. A let named like one in another
// `when` body is an error even though the two scopes don't overlap: names
// are unique per document, so a trace can name every let unambiguously.
func (c *Checker) lets(lets []*ast.LetStmt, env *Env) {
	for _, l := range lets {
		name := l.Name.Name
		if other, taken := c.letStates[name]; taken && other.env != env {
			if _, visible := env.Lookup(name); !visible {
				c.errorf(l.Name, "let names are unique in a document, so a trace can name each one; rename one of them",
					"`%s` is already the name of a let in another `when` body", name)
				env.names[name] = Binding{Entity: Let, Type: types.Invalid}
				continue
			}
		}
		if c.declare(l.Name, env, Binding{Entity: Let}) {
			c.letStates[name] = &letState{stmt: l, env: env}
		}
	}
	for _, l := range lets {
		if st, ok := c.letStates[l.Name.Name]; ok && st.stmt == l {
			c.checkLet(l.Name.Name, nil)
		}
	}
}

// pubLets reports a `pub let` that reads a param. A param has no value
// outside an invocation, so a document importing the let couldn't
// evaluate it.
func (c *Checker) pubLets(lets []*ast.LetStmt) {
	for _, l := range lets {
		st, ok := c.letStates[l.Name.Name]
		if !l.Pub || !ok || st.stmt != l || st.param == "" {
			continue
		}
		c.errorf(l.Name, "a param has no value outside an invocation; move the let to a module, or drop `pub`",
			"let `%s` can't be `pub`: it reads param `%s`", l.Name.Name, st.param)
	}
}

// readParam records that the let being typed reads param, directly or
// through another let.
func (c *Checker) readParam(param string) {
	if c.typing != nil && c.typing.param == "" {
		c.typing.param = param
	}
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
	outer := c.typing
	c.typing = st
	t := c.Expr(st.stmt.Value, st.env)
	c.typing = outer
	st.state = 2
	if _, done := st.env.names[name]; done && st.env.names[name].Type == nil {
		st.env.names[name] = Binding{Entity: Let, Type: t}
	}
	c.info.Lets = append(c.info.Lets, st.stmt)
	return st.env.names[name].Type
}

// when checks a rule: a bool condition and a body of rules, lets, asserts
// and constructors. The body's lets are visible in the body only, not in
// the condition.
func (c *Checker) when(s *ast.WhenStmt, env *Env) {
	c.ExprAs(s.Cond, env, types.Bool)
	if lets := letsOf(s.Body); len(lets) > 0 {
		env = env.Child()
		c.lets(lets, env)
	}
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
	case b.Entity == Invocable:
		c.invocation(s, b.Doc, env)
	case b.Entity == Module:
		c.errorf(s.Name, fmt.Sprintf("a module holds only lets; read one as `%s.<let>`, or import a policy to invoke", s.Name.Name),
			"`%s` is a module and can't be invoked", s.Name.Name)
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

	c.reason(s, d, env)

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

// reason checks a constructor's first argument: a bare name, one of the
// decision's declared reasons. The name is a reason, not a value, so it's
// never resolved against the document's names.
func (c *Checker) reason(s *ast.CallStmt, d *kind.Decision, env *Env) {
	declares := fmt.Sprintf("%s declares: %s", d.Name, strings.Join(d.Reasons, ", "))
	switch r := s.Positional.(type) {
	case nil:
		c.errorf(s, fmt.Sprintf("write `%s(<reason>)`; %s", d.Name, declares), "decision %s needs a reason", d.Name)
	case *ast.Ident:
		if d.HasReason(r.Name) {
			c.record(r, types.Decision)
			return
		}
		help := declares
		if closest, ok := nearest(r.Name, d.Reasons); ok {
			help = fmt.Sprintf("did you mean `%s`? %s", closest, declares)
		}
		c.errorf(r, help, "decision %s has no reason `%s`", d.Name, r.Name)
	case *ast.StringLit:
		help := fmt.Sprintf("reasons are declared names, not strings; %s", declares)
		if d.HasReason(r.Value) {
			help = fmt.Sprintf("reasons are declared names, not strings; write `%s(%s)`", d.Name, r.Value)
		}
		c.errorf(r, help, "decision reason must be a bare name")
	default:
		c.errorf(s.Positional, fmt.Sprintf("a reason is one of the names the kind declares; put dynamic text in a `detail` field. %s", declares),
			"decision reason must be a bare name")
		c.Expr(s.Positional, env)
	}
}

func closestField(d *kind.Decision, name string) (string, bool) {
	names := make([]string, len(d.Fields))
	for i, f := range d.Fields {
		names[i] = f.Name
	}
	return nearest(name, names)
}
