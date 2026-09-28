package check

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/types"
)

// expr types x, using hint, when given, to type an empty list or map
// literal. It records the type of every node it visits.
func (c *Checker) expr(x ast.Expr, env *Env, hint types.Type) types.Type {
	return c.record(x, c.exprNode(x, env, hint))
}

func (c *Checker) exprNode(x ast.Expr, env *Env, hint types.Type) types.Type {
	switch x := x.(type) {
	case *ast.BadExpr:
		return types.Invalid
	case *ast.Ident:
		return c.ident(x, env)
	case *ast.BoolLit:
		return types.Bool
	case *ast.IntLit:
		return types.Int
	case *ast.FloatLit:
		return types.Float
	case *ast.StringLit:
		return types.String
	case *ast.DurationLit:
		return types.Duration
	case *ast.Outcome:
		if !env.InAssert {
			c.errorf(x, "a rule that read its own outcome could fire exactly when it doesn't; only `assert` conditions may read it",
				"`outcome` can only be read in an assert condition")
			return types.Invalid
		}
		return &types.List{Elem: types.Decision}
	case *ast.ParenExpr:
		return c.expr(x.X, env, hint)
	case *ast.ListLit:
		return c.list(x, env, hint)
	case *ast.MapLit:
		return c.mapLit(x, env, hint)
	case *ast.UnaryExpr:
		return c.unary(x, env)
	case *ast.BinaryExpr:
		return c.binary(x, env)
	case *ast.SelectorExpr:
		return c.selector(x, env)
	case *ast.IndexExpr:
		return c.index(x, env)
	case *ast.CallExpr:
		return c.call(x, env)
	case *ast.QuantExpr:
		return c.quant(x, env)
	}
	c.errorf(x, "", "unexpected expression %T", x)
	return types.Invalid
}

// ident resolves a name. Host functions aren't values, so a bare function
// name is an error that says how to call it.
func (c *Checker) ident(x *ast.Ident, env *Env) types.Type {
	b, ok := env.Lookup(x.Name)
	if !ok {
		help := "names come from the kind's inputs, host functions and decisions, and the document's params, lets and imports"
		if closest, ok := env.Closest(x.Name); ok {
			help = fmt.Sprintf("did you mean `%s`?", closest)
		}
		c.errorf(x, help, "unknown name `%s`", x.Name)
		return types.Invalid
	}
	switch b.Entity {
	case Function:
		c.errorf(x, fmt.Sprintf("call it with its arguments; it's declared as: %s", b.Func.Signature()),
			"`%s` is a host function, not a value", x.Name)
		return types.Invalid
	case Param:
		c.readParam(x.Name)
	case Let:
		if b.Doc != nil {
			c.info.Reads[x] = Read{Doc: b.Doc.Name, Let: b.Let}
			return b.Type
		}
		// A let read before it's typed is typed now, so declaration order
		// doesn't matter; a let that reaches itself this way is a cycle.
		t := b.Type
		if t == nil {
			t = c.checkLet(x.Name, x)
		}
		if st, ok := c.letStates[x.Name]; ok {
			c.readParam(st.param)
		}
		return t
	case Module:
		c.errorf(x, fmt.Sprintf("read one of its pub lets as `%s.<let>`", x.Name), "`%s` is a module, not a value", x.Name)
		return types.Invalid
	case Invocable:
		c.errorf(x, fmt.Sprintf("invoke it as a statement, `%s(<param>: <value>)`, or read one of its pub lets as `%s.<let>`", x.Name, x.Name),
			"`%s` is an imported policy, not a value", x.Name)
		return types.Invalid
	case DecisionName:
		// A decision's name is a value only where outcome is: an assert
		// condition. Anywhere else it's a constructor that lost its call.
		if !env.InAssert {
			c.errorf(x, fmt.Sprintf("to produce the decision, construct it inside a rule: `when <condition> { %s(\"<reason>\") }`; its bare name is only a value in an assert condition", x.Name),
				"`%s` is a decision, not a value here", x.Name)
			return types.Invalid
		}
	}
	return b.Type
}

// list types a list literal. With a hint, every element is checked
// against the hinted element type, so a wrong element is reported where
// it is. Without one, the elements must share a type, and an empty
// literal stays untyped for the context to resolve.
func (c *Checker) list(x *ast.ListLit, env *Env, hint types.Type) types.Type {
	if l, ok := elemOf(hint).(*types.List); ok && l.Elem != nil {
		for _, e := range x.Elems {
			if c.ExprAs(e, env, l.Elem) == types.Invalid {
				return types.Invalid
			}
		}
		return &types.List{Elem: l.Elem}
	}
	var elem types.Type
	for i, e := range x.Elems {
		t := c.expr(e, env, nil)
		switch {
		case t == types.Invalid:
			return types.Invalid
		case elem == nil || untyped(elem) && !untyped(t):
			// The first typed element sets the type; earlier empty ones follow it.
			if !c.resolveEarlier(x.Elems[:i], t, "every element of a list has the same type") {
				return types.Invalid
			}
			elem = t
		default:
			if _, ok := c.unifyElem(e, t, elem); !ok {
				return types.Invalid
			}
		}
	}
	return &types.List{Elem: elem}
}

// resolveEarlier gives the untyped literals among xs the type t, now that
// a later element has settled it, reporting the first that can't take it.
func (c *Checker) resolveEarlier(xs []ast.Expr, t types.Type, help string) bool {
	for _, prev := range xs {
		if _, ok := c.resolve(prev, t); !ok && untyped(c.info.Types[prev]) {
			c.errorf(prev, help, "expected %s, found %s", t, describe(c.info.Types[prev]))
			return false
		}
	}
	return true
}

// unifyElem checks that element e of type t fits the list's element type
// elem, resolving an empty literal against it.
func (c *Checker) unifyElem(e ast.Expr, t, elem types.Type) (types.Type, bool) {
	if untyped(t) {
		if resolved, ok := c.resolve(e, elem); ok {
			return resolved, true
		}
	} else if types.Identical(t, elem) {
		return t, true
	}
	c.errorf(e, "every element of a list has the same type", "expected %s, found %s", elem, describe(t))
	return types.Invalid, false
}

// mapLit types a map literal: one key type, which must be usable as a
// key, and one value type. With a hint, keys and values are checked
// against it, as for lists.
func (c *Checker) mapLit(x *ast.MapLit, env *Env, hint types.Type) types.Type {
	if m, ok := elemOf(hint).(*types.Map); ok && m.Key != nil && m.Value != nil {
		for _, e := range x.Entries {
			if c.ExprAs(e.Key, env, m.Key) == types.Invalid || c.ExprAs(e.Value, env, m.Value) == types.Invalid {
				return types.Invalid
			}
		}
		return &types.Map{Key: m.Key, Value: m.Value}
	}
	var key, val types.Type
	for i, e := range x.Entries {
		kt := c.expr(e.Key, env, nil)
		if kt == types.Invalid {
			return types.Invalid
		}
		switch {
		case key == nil:
			if !types.IsKey(kt) {
				c.errorf(e.Key, "map keys are scalars: bool, int, float, string, duration or timestamp", "%s can't be a map key", describe(kt))
				return types.Invalid
			}
			key = kt
		case !types.Identical(kt, key):
			c.errorf(e.Key, "every key of a map has the same type", "expected %s, found %s", key, describe(kt))
			return types.Invalid
		}

		vt := c.expr(e.Value, env, nil)
		switch {
		case vt == types.Invalid:
			return types.Invalid
		case val == nil || untyped(val) && !untyped(vt):
			earlier := make([]ast.Expr, i)
			for j := range i {
				earlier[j] = x.Entries[j].Value
			}
			if !c.resolveEarlier(earlier, vt, "every value of a map has the same type") {
				return types.Invalid
			}
			val = vt
		default:
			if !c.fitsValue(e.Value, vt, val) {
				return types.Invalid
			}
		}
	}
	return &types.Map{Key: key, Value: val}
}

// fitsValue checks that a map value of type t matches the map's value
// type val, resolving an empty literal against it.
func (c *Checker) fitsValue(e ast.Expr, t, val types.Type) bool {
	if untyped(t) {
		if _, ok := c.resolve(e, val); ok {
			return true
		}
	} else if types.Identical(t, val) {
		return true
	}
	c.errorf(e, "every value of a map has the same type", "expected %s, found %s", val, describe(t))
	return false
}

func (c *Checker) unary(x *ast.UnaryExpr, env *Env) types.Type {
	switch x.Op {
	case ast.OpNot:
		if c.ExprAs(x.X, env, types.Bool) == types.Invalid {
			return types.Invalid
		}
		return types.Bool
	case ast.OpPresent:
		// `present` tests an optional without unwrapping it, so like `??`
		// and `?.` it only applies to a value that can be absent.
		t := c.Expr(x.X, env)
		if t == types.Invalid {
			return types.Invalid
		}
		if !isOptional(t) {
			c.errorf(x.X, "only an optional value can be absent", "`%s` is %s, which is always present", ast.Sprint(x.X), t)
			return types.Invalid
		}
		return types.Bool
	}
	t := c.Expr(x.X, env)
	switch t {
	case types.Invalid:
		return types.Invalid
	case types.Int, types.Float, types.Duration:
		return t
	}
	c.errorf(x, c.unwrapHint(x.X, t), "`-` needs int, float or duration, found %s", t)
	return types.Invalid
}

// unwrapHint is the hint for an optional used where its element type is
// needed, and empty for anything else.
func (c *Checker) unwrapHint(x ast.Expr, t types.Type) string {
	if isOptional(t) {
		return fmt.Sprintf("unwrap it with `??`, like `%s ?? <default>`", ast.Sprint(x))
	}
	return ""
}

func (c *Checker) binary(x *ast.BinaryExpr, env *Env) types.Type {
	switch x.Op {
	case ast.OpAnd, ast.OpOr, ast.OpXor:
		l := c.ExprAs(x.X, env, types.Bool)
		r := c.ExprAs(x.Y, env, types.Bool)
		if l == types.Invalid || r == types.Invalid {
			return types.Invalid
		}
		return types.Bool
	case ast.OpEq, ast.OpNotEq:
		return c.comparison(x, env, types.IsEquatable, "lists, maps and structs can't be compared yet")
	case ast.OpLt, ast.OpLtEq, ast.OpGt, ast.OpGtEq:
		return c.comparison(x, env, types.IsOrdered, "only int, float, duration and timestamp are ordered")
	case ast.OpIn, ast.OpNotIn:
		return c.membership(x, env)
	case ast.OpAllIn, ast.OpAnyIn, ast.OpOneIn, ast.OpExclusiveIn:
		return c.listOp(x, env)
	case ast.OpHas:
		return c.has(x, env)
	case ast.OpLike, ast.OpMatches:
		return c.pattern(x, env)
	case ast.OpCoalesce:
		return c.coalesce(x, env)
	case ast.OpAdd, ast.OpSub:
		return c.arith(x, env)
	}
	c.errorf(x, "", "unexpected operator %s", x.Op)
	return types.Invalid
}

// comparison checks `==`, `!=`, `<` and the rest: both sides of one type
// that supports the operator.
func (c *Checker) comparison(x *ast.BinaryExpr, env *Env, allowed func(types.Type) bool, why string) types.Type {
	l := c.Expr(x.X, env)
	r := c.Expr(x.Y, env)
	if l == types.Invalid || r == types.Invalid {
		return types.Invalid
	}
	for _, side := range []struct {
		x ast.Expr
		t types.Type
	}{{x.X, l}, {x.Y, r}} {
		if isOptional(side.t) {
			c.errorf(side.x, c.unwrapHint(side.x, side.t), "`%s` can't compare %s; unwrap it first", x.Op, side.t)
			return types.Invalid
		}
	}
	if !types.Identical(l, r) {
		c.errorf(x, sameTypeHint(l, r), "`%s` needs operands of the same type, found %s and %s", x.Op, l, r)
		return types.Invalid
	}
	if !allowed(l) {
		c.errorf(x, why, "`%s` isn't defined for %s", x.Op, l)
		return types.Invalid
	}
	return types.Bool
}

// sameTypeHint is the hint for two operands of different types.
func sameTypeHint(l, r types.Type) string {
	switch {
	case l == types.Duration && r == types.Int || l == types.Int && r == types.Duration:
		return "a bare number is never a duration; write a literal like `30m`"
	case isNumber(l) && isNumber(r):
		return "int and float don't convert implicitly"
	}
	return ""
}

// membership checks `x in y` and `x not in y`: an element in a list, a
// key in a map, or a substring in a string.
func (c *Checker) membership(x *ast.BinaryExpr, env *Env) types.Type {
	l := c.expr(x.X, env, nil)
	r := c.expr(x.Y, env, nil)
	if l == types.Invalid || r == types.Invalid {
		return types.Invalid
	}
	switch rt := r.(type) {
	case *types.List:
		elem := rt.Elem
		switch {
		case untyped(l) && !untyped(elem):
			if resolved, ok := c.resolve(x.X, elem); ok {
				l = resolved
			}
		case untyped(r) && !untyped(l):
			if resolved, ok := c.resolve(x.Y, &types.List{Elem: l}); ok {
				elem = resolved.(*types.List).Elem
			}
		}
		if untyped(l) || untyped(elem) {
			c.errorf(x, "add an element, or use it where a typed list is expected", "cannot infer the type of `%s`", ast.Sprint(x))
			return types.Invalid
		}
		if !types.Identical(l, elem) {
			c.errorf(x, sameTypeHint(l, elem), "`%s` needs an element of the list's type, found %s in list<%s>", x.Op, l, elem)
			return types.Invalid
		}
		if !c.comparable(x, elem) {
			return types.Invalid
		}
	case *types.Map:
		// A map key is tested with `has` only, so there's one way to write it.
		help := fmt.Sprintf("write `%s has %s`", ast.Sprint(x.Y), ast.Sprint(x.X))
		if x.Op == ast.OpNotIn {
			help = fmt.Sprintf("write `not %s has %s`", ast.Sprint(x.Y), ast.Sprint(x.X))
		}
		c.errorf(x, help, "`%s` doesn't apply to a map; a key is tested with `has`", x.Op)
		return types.Invalid
	default:
		if r == types.String {
			if l != types.String {
				c.errorf(x, "`in` on a string tests for a substring", "`%s` needs a string on the left of a string, found %s", x.Op, describe(l))
				return types.Invalid
			}
			return types.Bool
		}
		c.errorf(x.Y, c.unwrapHint(x.Y, r), "`%s` needs a list, map or string on the right, found %s", x.Op, describe(r))
		return types.Invalid
	}
	return types.Bool
}

// listOp checks `all in`, `any in`, `one in` and `exclusive in`: two
// lists of one element type.
func (c *Checker) listOp(x *ast.BinaryExpr, env *Env) types.Type {
	l := c.expr(x.X, env, nil)
	r := c.expr(x.Y, env, nil)
	if l == types.Invalid || r == types.Invalid {
		return types.Invalid
	}
	l, r = c.unify(x.X, l, x.Y, r)
	for _, side := range []struct {
		x ast.Expr
		t types.Type
	}{{x.X, l}, {x.Y, r}} {
		if _, ok := side.t.(*types.List); !ok {
			c.errorf(side.x, c.unwrapHint(side.x, side.t), "`%s` needs lists on both sides, found %s", x.Op, describe(side.t))
			return types.Invalid
		}
	}
	if untyped(l) || untyped(r) {
		c.errorf(x, "add an element, or use it where a typed list is expected", "cannot infer the type of `%s`", ast.Sprint(x))
		return types.Invalid
	}
	if !types.Identical(l, r) {
		c.errorf(x, "", "`%s` needs lists of one element type, found %s and %s", x.Op, l, r)
		return types.Invalid
	}
	if !c.comparable(x, l.(*types.List).Elem) {
		return types.Invalid
	}
	return types.Bool
}

// comparable reports whether elements of type elem can be compared by x,
// and reports it at x when they can't.
func (c *Checker) comparable(x *ast.BinaryExpr, elem types.Type) bool {
	if types.IsComparable(elem) {
		return true
	}
	c.errorf(x, "structs have no equality; compare a field that identifies them, such as a name",
		"`%s` can't compare elements of type %s", x.Op, elem)
	return false
}

// has checks `m has {...}` and `m has k`.
func (c *Checker) has(x *ast.BinaryExpr, env *Env) types.Type {
	l := c.Expr(x.X, env)
	if l == types.Invalid {
		return types.Invalid
	}
	m, ok := l.(*types.Map)
	if !ok {
		c.errorf(x.X, c.unwrapHint(x.X, l), "`has` needs a map on the left, found %s", l)
		return types.Invalid
	}
	// A map literal on the right is checked against the map's type, so a
	// wrong value is reported at the value; anything else must be the map's
	// type or its key type.
	if isMapLit(x.Y) {
		if c.ExprAs(x.Y, env, m) == types.Invalid || !c.comparable(x, m.Value) {
			return types.Invalid
		}
		return types.Bool
	}
	r := c.Expr(x.Y, env)
	switch {
	case r == types.Invalid:
		return types.Invalid
	case types.Identical(r, m.Key):
	case !types.Identical(r, m):
		c.errorf(x.Y, "", "`has` needs %s or a key of type %s, found %s", m, m.Key, r)
		return types.Invalid
	case !c.comparable(x, m.Value):
		return types.Invalid
	}
	return types.Bool
}

// pattern checks `like` and `matches`: a string on the left and a string
// literal pattern on the right, compiled here for `matches`.
func (c *Checker) pattern(x *ast.BinaryExpr, env *Env) types.Type {
	l := c.ExprAs(x.X, env, types.String)
	lit, ok := patternLit(x.Y)
	if !ok {
		c.record(x.Y, types.String)
		c.errorf(x.Y, "patterns compile once, when the policy loads, so they can't be computed",
			"the pattern after `%s` must be a string literal", x.Op)
		return types.Invalid
	}
	c.record(lit, types.String)
	if x.Op == ast.OpMatches {
		if _, err := regexp.Compile(lit.Value); err != nil {
			c.errorf(lit, "patterns are RE2 regular expressions, as Go's regexp package reads them", "invalid regular expression: %v", err)
			return types.Invalid
		}
	}
	if l == types.Invalid {
		return types.Invalid
	}
	return types.Bool
}

// isMapLit reports whether x is a map literal, looking through parentheses.
func isMapLit(x ast.Expr) bool {
	for {
		switch v := x.(type) {
		case *ast.ParenExpr:
			x = v.X
		case *ast.MapLit:
			return true
		default:
			return false
		}
	}
}

// patternLit returns the string literal x is, looking through parentheses.
func patternLit(x ast.Expr) (*ast.StringLit, bool) {
	for {
		switch v := x.(type) {
		case *ast.ParenExpr:
			x = v.X
		case *ast.StringLit:
			return v, true
		default:
			return nil, false
		}
	}
}

// coalesce checks `a ?? b`: an optional on the left, its element type on
// the right, and the element type as the result.
func (c *Checker) coalesce(x *ast.BinaryExpr, env *Env) types.Type {
	l := c.Expr(x.X, env)
	if l == types.Invalid {
		return types.Invalid
	}
	opt, ok := l.(*types.Optional)
	if !ok {
		c.errorf(x.X, "only an optional (`?T`) value needs a default; this one is always present", "`??` needs an optional on the left, found %s", l)
		return types.Invalid
	}
	if c.ExprAs(x.Y, env, opt.Elem) == types.Invalid {
		return types.Invalid
	}
	return opt.Elem
}

// arith checks `+` and `-` against the table on the expressions page.
func (c *Checker) arith(x *ast.BinaryExpr, env *Env) types.Type {
	l := c.Expr(x.X, env)
	r := c.Expr(x.Y, env)
	if l == types.Invalid || r == types.Invalid {
		return types.Invalid
	}
	switch {
	case l == types.Int && r == types.Int, l == types.Float && r == types.Float, l == types.Duration && r == types.Duration:
		return l
	case l == types.Timestamp && r == types.Duration:
		return types.Timestamp
	case l == types.Timestamp && r == types.Timestamp && x.Op == ast.OpSub:
		return types.Duration
	}
	help := sameTypeHint(l, r)
	if help == "" {
		switch {
		case isOptional(l):
			help = c.unwrapHint(x.X, l)
		case isOptional(r):
			help = c.unwrapHint(x.Y, r)
		case l == types.String || r == types.String:
			help = "there is no string concatenation"
		default:
			help = "`+` and `-` work on int, float and duration, and on a timestamp with a duration"
		}
	}
	c.errorf(x, help, "`%s` isn't defined for %s and %s", x.Op, l, r)
	return types.Invalid
}

// selector checks `x.name` or `x?.name`, the end of a chain; see chain.
func (c *Checker) selector(x *ast.SelectorExpr, env *Env) types.Type {
	return c.chain(x, env)
}

// chain types the end of an optional chain: a run of `.name`, `?.name`
// and `[index]` that no parentheses break. A `?.` whose operand is absent
// makes the whole run absent, as in TypeScript, so the chain's type is
// its last link's type made optional as soon as the run holds a `?.`.
// `release?.meta.owner` is then `?string` even though `meta` isn't
// optional. An optional link still needs its own `?.`: if `meta` were
// `?Meta`, reading `.owner` from it would be an error.
func (c *Checker) chain(x ast.Expr, env *Env) types.Type {
	t, short := c.link(x, env)
	if !short || t == types.Invalid {
		return t
	}
	if isOptional(t) {
		return t
	}
	return &types.Optional{Elem: t}
}

// link types one link of a chain as if no `?.` before it found its
// operand absent, and reports whether one can.
func (c *Checker) link(x ast.Expr, env *Env) (types.Type, bool) {
	switch x := x.(type) {
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			if b, found := env.Lookup(id.Name); found && (b.Entity == Module || b.Entity == Invocable) {
				return c.qualified(x, b), false
			}
		}
		base, short := c.linkBase(x.X, env)
		t, opt := c.field(x, base, env)
		return t, short || opt
	case *ast.IndexExpr:
		base, short := c.linkBase(x.X, env)
		return c.indexOf(x, base, env), short
	}
	return c.Expr(x, env), false
}

// linkBase types the operand of a link. The previous link of the same
// chain is recorded with its link type, which is what the evaluator
// compiles it by; anything else is an ordinary expression.
func (c *Checker) linkBase(x ast.Expr, env *Env) (types.Type, bool) {
	switch x.(type) {
	case *ast.SelectorExpr, *ast.IndexExpr:
		t, short := c.link(x, env)
		return c.record(x, t), short
	}
	return c.Expr(x, env), false
}

// field types the field x selects from base, and reports whether x is a
// `?.`, which reads the field from the struct inside an optional base.
func (c *Checker) field(x *ast.SelectorExpr, base types.Type, env *Env) (types.Type, bool) {
	if base == types.Invalid {
		return types.Invalid, false
	}
	operand := ast.Sprint(x.X)
	if base == types.Decision {
		return c.outcomeRef(x, env), false
	}
	if x.Optional {
		opt, ok := base.(*types.Optional)
		if !ok {
			c.errorf(x.Sel, fmt.Sprintf("read the field with `%s.%s`; `?.` is for a struct that may be absent", operand, x.Sel.Name),
				"`%s` isn't optional", operand)
			return types.Invalid, false
		}
		base = opt.Elem
	}
	switch t := base.(type) {
	case *types.Struct:
		decl := env.Kind().Type(t.Name)
		if decl == nil {
			decl = t
		}
		if f := decl.Field(x.Sel.Name); f != nil {
			return f.Type, x.Optional
		}
		help := fmt.Sprintf("%s declares: %s", decl.Name, decl.FieldNames())
		if closest, ok := c.closestField(decl, x.Sel.Name); ok {
			help = fmt.Sprintf("did you mean %q? %s", closest, help)
		}
		c.errorf(x.Sel, help, "unknown field %q on type %s", x.Sel.Name, decl.Name)
		return types.Invalid, false
	case *types.Optional:
		if _, ok := t.Elem.(*types.Struct); ok {
			c.errorf(x.Sel, fmt.Sprintf("read the field with `%s?.%s`", operand, x.Sel.Name),
				"`%s` is %s, which may be absent", operand, t)
			return types.Invalid, false
		}
	}
	if x.Optional {
		c.errorf(x.Sel, "", "`%s` holds %s, which has no fields", operand, base)
		return types.Invalid, false
	}
	c.errorf(x.Sel, c.unwrapHint(x.X, base), "`%s` is %s, which has no fields", operand, base)
	return types.Invalid, false
}

// outcomeRef types `decision.reason`, a decision value naming one reason.
// The operand has to be the decision's bare name, and the reason one the
// decision declares.
func (c *Checker) outcomeRef(x *ast.SelectorExpr, env *Env) types.Type {
	id, ok := x.X.(*ast.Ident)
	if !ok || x.Optional {
		c.errorf(x.Sel, "a reason follows a decision's name directly, like `approve.release_manager`", "`%s` names no reason", ast.Sprint(x))
		return types.Invalid
	}
	d := env.Kind().Decision(id.Name)
	if d == nil {
		return types.Invalid
	}
	if d.HasReason(x.Sel.Name) {
		return types.Decision
	}
	help := fmt.Sprintf("%s declares: %s", d.Name, strings.Join(d.Reasons, ", "))
	if closest, found := nearest(x.Sel.Name, d.Reasons); found {
		help = fmt.Sprintf("did you mean `%s`? %s", closest, help)
	}
	c.errorf(x.Sel, help, "decision %s has no reason `%s`", d.Name, x.Sel.Name)
	return types.Invalid
}

func (c *Checker) closestField(s *types.Struct, name string) (string, bool) {
	names := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		names[i] = f.Name
	}
	return nearest(name, names)
}

// index checks `x[i]`, the end of a chain; see chain.
func (c *Checker) index(x *ast.IndexExpr, env *Env) types.Type {
	return c.chain(x, env)
}

// indexOf types `x[i]` on base: a map by its key type, or a list by an
// int.
func (c *Checker) indexOf(x *ast.IndexExpr, base types.Type, env *Env) types.Type {
	switch t := base.(type) {
	case *types.Map:
		if c.ExprAs(x.Index, env, t.Key) == types.Invalid {
			return types.Invalid
		}
		return t.Value
	case *types.List:
		if c.ExprAs(x.Index, env, types.Int) == types.Invalid {
			return types.Invalid
		}
		return t.Elem
	case types.Basic:
		if t == types.Invalid {
			return types.Invalid
		}
	}
	c.errorf(x.X, c.unwrapHint(x.X, base), "`%s` is %s, which can't be indexed", ast.Sprint(x.X), base)
	return types.Invalid
}

// call checks `f(args)`: a host function with matching arguments.
func (c *Checker) call(x *ast.CallExpr, env *Env) types.Type {
	name, ok := x.Fun.(*ast.Ident)
	if !ok {
		c.errorf(x.Fun, "policies can only call the host functions the kind declares", "`%s` isn't a function", ast.Sprint(x.Fun))
		return types.Invalid
	}
	b, found := env.Lookup(name.Name)
	switch {
	case !found:
		help := "policies can only call the host functions the kind declares"
		if closest, ok := env.Closest(name.Name); ok {
			help = fmt.Sprintf("did you mean `%s`?", closest)
		}
		c.errorf(name, help, "unknown function `%s`", name.Name)
		return types.Invalid
	case b.Entity != Function:
		c.errorf(name, "policies can only call the host functions the kind declares", "`%s` is %s %s, not a function", name.Name, article(b.Entity), b.Entity)
		return types.Invalid
	}
	f := b.Func
	c.record(name, f.Result)
	if len(x.Args) != len(f.Params) {
		c.errorf(x, "it's declared as: "+f.Signature(), "`%s` takes %d argument%s, found %d", f.Name, len(f.Params), plural(len(f.Params)), len(x.Args))
		return types.Invalid
	}
	valid := true
	for i, arg := range x.Args {
		if c.ExprAs(arg, env, f.Params[i]) == types.Invalid {
			valid = false
		}
	}
	if !valid {
		return types.Invalid
	}
	return f.Result
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// quant checks `any x in xs: body` and `all x in xs: body`: a list to
// range over, a fresh variable of its element type, and a bool body.
func (c *Checker) quant(x *ast.QuantExpr, env *Env) types.Type {
	rng := c.Expr(x.Range, env)
	if rng == types.Invalid {
		return types.Invalid
	}
	l, ok := rng.(*types.List)
	if !ok {
		help := "quantifiers range over lists"
		if m, isMap := rng.(*types.Map); isMap {
			help = fmt.Sprintf("quantifiers range over lists; to test a map's keys, index it or use `has` on %s", m)
		} else if isOptional(rng) {
			help = c.unwrapHint(x.Range, rng)
		}
		c.errorf(x.Range, help, "`%s` needs a list to range over, found %s", x.Op, rng)
		return types.Invalid
	}

	inner := env.Child()
	b := Binding{Entity: QuantVar, Type: l.Elem}
	if prev, ok := inner.Declare(x.Var.Name, b); !ok {
		if !c.keeps(prev) {
			c.errorf(x.Var, "nothing shadows anything; pick a name that isn't in use",
				"`%s` is already the name of %s %s", x.Var.Name, article(prev.Entity), prev.Entity)
			return types.Invalid
		}
		inner.Bind(x.Var.Name, b)
		c.info.Shadows = append(c.info.Shadows, x.Var)
	}
	c.record(x.Var, l.Elem)
	if c.ExprAs(x.Body, inner, types.Bool) == types.Invalid {
		return types.Invalid
	}
	return types.Bool
}

func article(e Entity) string {
	if e == Input {
		return "an"
	}
	return "a"
}
