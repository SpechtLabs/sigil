package eval

import (
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/types"
)

// expr compiles x. Every case reads the checked types it needs up front
// and returns a closure that does only the work of that operator.
func (c *compiler) expr(x ast.Expr) Expr {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return c.expr(x.X)
	case *ast.Ident:
		return c.ident(x)
	case *ast.BoolLit:
		return constant(x.Value)
	case *ast.IntLit:
		return constant(x.Value)
	case *ast.FloatLit:
		return constant(x.Value)
	case *ast.StringLit:
		return constant(x.Value)
	case *ast.DurationLit:
		return constant(x.Value)
	case *ast.Outcome:
		return func(f *Frame) Value { return f.Outcome }
	case *ast.ListLit:
		return c.list(x)
	case *ast.MapLit:
		return c.mapLit(x)
	case *ast.UnaryExpr:
		return c.unary(x)
	case *ast.BinaryExpr:
		return c.binary(x)
	case *ast.SelectorExpr:
		return c.selector(x)
	case *ast.IndexExpr:
		return c.index(x)
	case *ast.CallExpr:
		return c.call(x)
	case *ast.QuantExpr:
		return c.quant(x)
	}
	throwf(x, "can't evaluate %T", x)
	return nil
}

func constant(v any) Expr {
	val := reflect.ValueOf(v)
	return func(*Frame) Value { return val }
}

// ident compiles a name: an input read through its field index, or a
// slot the frame binds. Decision names are their own string.
func (c *compiler) ident(x *ast.Ident) Expr {
	if slot, ok := c.scope.slots[x.Name]; ok {
		return func(f *Frame) Value { return f.slots[slot] }
	}
	if idx, ok := c.scope.binding.Fields["."+x.Name]; ok {
		return func(f *Frame) Value { return f.Input.FieldByIndex(idx) }
	}
	if c.typeOf(x) == types.Decision {
		return constant(x.Name)
	}
	throwf(x, "`%s` has no slot and isn't an input", x.Name)
	return nil
}

// list compiles a list literal into a []any built on each evaluation.
func (c *compiler) list(x *ast.ListLit) Expr {
	elems := make([]Expr, len(x.Elems))
	for i, e := range x.Elems {
		elems[i] = c.expr(e)
	}
	return func(f *Frame) Value {
		out := make([]any, len(elems))
		for i, e := range elems {
			out[i] = iface(e(f))
		}
		return reflect.ValueOf(out)
	}
}

// mapLit compiles a map literal into a map[any]any built on each
// evaluation. Keys are normalized to the canonical Go type of their Sigil
// type, so an int key from a literal and one from input agree.
func (c *compiler) mapLit(x *ast.MapLit) Expr {
	keys := make([]Expr, len(x.Entries))
	vals := make([]Expr, len(x.Entries))
	for i, e := range x.Entries {
		keys[i] = c.expr(e.Key)
		vals[i] = c.expr(e.Value)
	}
	keyType := c.typeOf(x).(*types.Map).Key
	return func(f *Frame) Value {
		out := make(map[any]any, len(keys))
		for i := range keys {
			out[canonical(keyType, keys[i](f))] = iface(vals[i](f))
		}
		return reflect.ValueOf(out)
	}
}

// iface returns v as an interface value, nil for an absent optional.
func iface(v Value) any {
	v = norm(v)
	if !v.IsValid() {
		return nil
	}
	return v.Interface()
}

// canonical returns a scalar key in the Go type literals use for its
// Sigil type, so map[any]any lookups match regardless of where the key
// came from.
func canonical(t types.Type, v Value) any {
	v = norm(v)
	switch t {
	case types.Int:
		return v.Int()
	case types.Duration:
		return time.Duration(v.Int())
	case types.Float:
		return v.Float()
	case types.String, types.Decision:
		return v.String()
	case types.Bool:
		return v.Bool()
	}
	return iface(v)
}

func (c *compiler) unary(x *ast.UnaryExpr) Expr {
	operand := c.expr(x.X)
	if x.Op == ast.OpNot {
		return func(f *Frame) Value { return reflect.ValueOf(!norm(operand(f)).Bool()) }
	}
	switch c.typeOf(x) {
	case types.Int:
		return func(f *Frame) Value {
			v := norm(operand(f)).Int()
			if v == math.MinInt64 {
				throwf(x, "integer overflow negating %d", v)
			}
			return reflect.ValueOf(-v)
		}
	case types.Duration:
		return func(f *Frame) Value {
			v := norm(operand(f)).Int()
			if v == math.MinInt64 {
				throwf(x, "duration overflow negating %s", time.Duration(v))
			}
			return reflect.ValueOf(time.Duration(-v))
		}
	}
	return func(f *Frame) Value { return reflect.ValueOf(-norm(operand(f)).Float()) }
}

func (c *compiler) binary(x *ast.BinaryExpr) Expr {
	switch x.Op {
	case ast.OpAnd:
		l, r := c.expr(x.X), c.expr(x.Y)
		return func(f *Frame) Value { return reflect.ValueOf(norm(l(f)).Bool() && norm(r(f)).Bool()) }
	case ast.OpOr:
		l, r := c.expr(x.X), c.expr(x.Y)
		return func(f *Frame) Value { return reflect.ValueOf(norm(l(f)).Bool() || norm(r(f)).Bool()) }
	case ast.OpXor:
		l, r := c.expr(x.X), c.expr(x.Y)
		return func(f *Frame) Value { return reflect.ValueOf(norm(l(f)).Bool() != norm(r(f)).Bool()) }
	case ast.OpEq, ast.OpNotEq:
		return c.equality(x)
	case ast.OpLt, ast.OpLtEq, ast.OpGt, ast.OpGtEq:
		return c.ordering(x)
	case ast.OpIn, ast.OpNotIn:
		return c.membership(x)
	case ast.OpAllIn, ast.OpAnyIn, ast.OpOneIn, ast.OpExclusiveIn:
		return c.listOp(x)
	case ast.OpHas:
		return c.has(x)
	case ast.OpLike, ast.OpMatches:
		return c.pattern(x)
	case ast.OpCoalesce:
		l, r := c.expr(x.X), c.expr(x.Y)
		return func(f *Frame) Value {
			if v := norm(l(f)); v.IsValid() && !v.IsNil() {
				return v.Elem()
			}
			return r(f)
		}
	case ast.OpAdd, ast.OpSub:
		return c.arith(x)
	}
	throwf(x, "can't evaluate operator %s", x.Op)
	return nil
}

func (c *compiler) equality(x *ast.BinaryExpr) Expr {
	l, r := c.expr(x.X), c.expr(x.Y)
	t := c.typeOf(x.X)
	want := x.Op == ast.OpEq
	return func(f *Frame) Value { return reflect.ValueOf(equal(t, l(f), r(f)) == want) }
}

func (c *compiler) ordering(x *ast.BinaryExpr) Expr {
	l, r := c.expr(x.X), c.expr(x.Y)
	t := c.typeOf(x.X)
	var holds func(int) bool
	switch x.Op {
	case ast.OpLt:
		holds = func(n int) bool { return n < 0 }
	case ast.OpLtEq:
		holds = func(n int) bool { return n <= 0 }
	case ast.OpGt:
		holds = func(n int) bool { return n > 0 }
	default:
		holds = func(n int) bool { return n >= 0 }
	}
	return func(f *Frame) Value { return reflect.ValueOf(holds(compare(t, l(f), r(f)))) }
}

// membership compiles `in` and `not in` by the right side's type: an
// element of a list, a key of a map, or a substring.
func (c *compiler) membership(x *ast.BinaryExpr) Expr {
	l, r := c.expr(x.X), c.expr(x.Y)
	want := x.Op == ast.OpIn
	var test func(a, b Value) bool
	switch rt := c.typeOf(x.Y).(type) {
	case *types.List:
		elem := rt.Elem
		test = func(a, b Value) bool { return contains(elem, b, a) }
	case *types.Map:
		test = func(a, b Value) bool { return mapGet(b, a).IsValid() }
	default:
		test = func(a, b Value) bool { return strings.Contains(norm(b).String(), norm(a).String()) }
	}
	return func(f *Frame) Value { return reflect.ValueOf(test(l(f), r(f)) == want) }
}

// contains reports whether list holds an element equal to v.
func contains(elem types.Type, list, v Value) bool {
	list = norm(list)
	if !list.IsValid() {
		return false
	}
	for i := 0; i < list.Len(); i++ {
		if equal(elem, list.Index(i), v) {
			return true
		}
	}
	return false
}

// listOp compiles `all in`, `any in`, `one in` and `exclusive in`. The
// last two count distinct elements of the left list found in the right.
func (c *compiler) listOp(x *ast.BinaryExpr) Expr {
	l, r := c.expr(x.X), c.expr(x.Y)
	elem := c.typeOf(x.X).(*types.List).Elem
	var test func(a, b Value) bool
	switch x.Op {
	case ast.OpAllIn:
		test = func(a, b Value) bool {
			for i := 0; i < a.Len(); i++ {
				if !contains(elem, b, a.Index(i)) {
					return false
				}
			}
			return true
		}
	case ast.OpAnyIn:
		test = func(a, b Value) bool {
			for i := 0; i < a.Len(); i++ {
				if contains(elem, b, a.Index(i)) {
					return true
				}
			}
			return false
		}
	case ast.OpOneIn:
		test = func(a, b Value) bool { return distinctIn(elem, a, b) == 1 }
	default:
		test = func(a, b Value) bool { return distinctIn(elem, a, b) <= 1 }
	}
	return func(f *Frame) Value {
		a, b := norm(l(f)), norm(r(f))
		if !a.IsValid() {
			a = reflect.ValueOf([]any{})
		}
		return reflect.ValueOf(test(a, b))
	}
}

// distinctIn counts the distinct elements of a that b contains.
func distinctIn(elem types.Type, a, b Value) int {
	n := 0
	for i := 0; i < a.Len(); i++ {
		v := a.Index(i)
		seen := false
		for j := 0; j < i; j++ {
			if equal(elem, a.Index(j), v) {
				seen = true
				break
			}
		}
		if !seen && contains(elem, b, v) {
			n++
		}
	}
	return n
}

// has compiles `m has {...}` and `m has k`.
func (c *compiler) has(x *ast.BinaryExpr) Expr {
	l, r := c.expr(x.X), c.expr(x.Y)
	m := c.typeOf(x.X).(*types.Map)
	if _, isMap := c.typeOf(x.Y).(*types.Map); !isMap {
		return func(f *Frame) Value { return reflect.ValueOf(mapGet(l(f), r(f)).IsValid()) }
	}
	return func(f *Frame) Value {
		a, b := norm(l(f)), norm(r(f))
		if !b.IsValid() || b.IsNil() {
			return reflect.ValueOf(true)
		}
		iter := b.MapRange()
		for iter.Next() {
			av := mapGet(a, iter.Key())
			if !av.IsValid() || !equal(m.Value, av, iter.Value()) {
				return reflect.ValueOf(false)
			}
		}
		return reflect.ValueOf(true)
	}
}

// pattern compiles `like` and `matches`, with the pattern compiled once
// here. A glob's `*` matches any run of characters and `?` one.
func (c *compiler) pattern(x *ast.BinaryExpr) Expr {
	l := c.expr(x.X)
	lit := patternLit(x.Y)
	var re *regexp.Regexp
	if x.Op == ast.OpMatches {
		re = regexp.MustCompile(lit) // the checker compiled it already
	} else {
		re = regexp.MustCompile(globToRegexp(lit))
	}
	return func(f *Frame) Value { return reflect.ValueOf(re.MatchString(norm(l(f)).String())) }
}

func patternLit(x ast.Expr) string {
	for {
		switch v := x.(type) {
		case *ast.ParenExpr:
			x = v.X
		case *ast.StringLit:
			return v.Value
		default:
			return ""
		}
	}
}

// globToRegexp translates a glob into an anchored regular expression.
func globToRegexp(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return b.String()
}

// arith compiles `+` and `-` for the type pairs the checker allows, with
// overflow reported for ints and durations.
func (c *compiler) arith(x *ast.BinaryExpr) Expr {
	l, r := c.expr(x.X), c.expr(x.Y)
	lt, rt := c.typeOf(x.X), c.typeOf(x.Y)
	sub := x.Op == ast.OpSub
	switch {
	case lt == types.Int:
		return func(f *Frame) Value {
			v, ok := addInt(norm(l(f)).Int(), norm(r(f)).Int(), sub)
			if !ok {
				throwf(x, "integer overflow in `%s`", ast.Sprint(x))
			}
			return reflect.ValueOf(v)
		}
	case lt == types.Float:
		if sub {
			return func(f *Frame) Value { return reflect.ValueOf(norm(l(f)).Float() - norm(r(f)).Float()) }
		}
		return func(f *Frame) Value { return reflect.ValueOf(norm(l(f)).Float() + norm(r(f)).Float()) }
	case lt == types.Duration:
		return func(f *Frame) Value {
			v, ok := addInt(norm(l(f)).Int(), norm(r(f)).Int(), sub)
			if !ok {
				throwf(x, "duration overflow in `%s`", ast.Sprint(x))
			}
			return reflect.ValueOf(time.Duration(v))
		}
	case lt == types.Timestamp && rt == types.Duration:
		return func(f *Frame) Value {
			t, d := norm(l(f)).Interface().(time.Time), time.Duration(norm(r(f)).Int())
			if sub {
				d = -d
			}
			return reflect.ValueOf(t.Add(d))
		}
	}
	return func(f *Frame) Value {
		a, b := norm(l(f)).Interface().(time.Time), norm(r(f)).Interface().(time.Time)
		return reflect.ValueOf(a.Sub(b))
	}
}

// addInt returns a+b, or a-b when sub is set, and false on overflow.
func addInt(a, b int64, sub bool) (int64, bool) {
	if sub {
		if b == math.MinInt64 {
			if a < 0 {
				return a - b, true
			}
			return 0, false
		}
		b = -b
	}
	sum := a + b
	if (sum > a) != (b > 0) {
		return 0, false
	}
	return sum, true
}

// selector compiles a field read through the index path the binding
// recorded for the struct type.
func (c *compiler) selector(x *ast.SelectorExpr) Expr {
	base := c.expr(x.X)
	s := c.typeOf(x.X).(*types.Struct)
	idx, ok := c.scope.binding.Fields[s.Name+"."+x.Sel.Name]
	if !ok {
		throwf(x.Sel, "no binding for field %s.%s", s.Name, x.Sel.Name)
	}
	return func(f *Frame) Value { return norm(base(f)).FieldByIndex(idx) }
}

// index compiles `m[k]`, which yields the value type's zero for a missing
// key, and `xs[i]`, which fails on an index out of range.
func (c *compiler) index(x *ast.IndexExpr) Expr {
	base, key := c.expr(x.X), c.expr(x.Index)
	switch t := c.typeOf(x.X).(type) {
	case *types.Map:
		// A missing key yields the zero value: the Go map's own when it's
		// typed, the Sigil type's canonical one for a literal's map[any]any.
		zero := c.zero(t.Value)
		return func(f *Frame) Value {
			m := norm(base(f))
			if v := mapGet(m, key(f)); v.IsValid() {
				return v
			}
			if m.IsValid() && m.Type().Elem().Kind() != reflect.Interface {
				return reflect.Zero(m.Type().Elem())
			}
			return zero
		}
	default:
		return func(f *Frame) Value {
			list, i := norm(base(f)), norm(key(f)).Int()
			n := 0
			if list.IsValid() {
				n = list.Len()
			}
			if i < 0 || i >= int64(n) {
				throwf(x, "index %d out of range for a list of %d", i, n)
			}
			return list.Index(int(i))
		}
	}
}

// call compiles a host function call. Arguments are converted to the Go
// parameter types when a literal's representation differs; an error
// result becomes a runtime error.
func (c *compiler) call(x *ast.CallExpr) Expr {
	name := x.Fun.(*ast.Ident).Name
	fn, ok := c.scope.binding.Funcs[name]
	if !ok {
		throwf(x.Fun, "no implementation bound for host function %s", name)
	}
	ft := fn.Type()
	args := make([]Expr, len(x.Args))
	for i, a := range x.Args {
		args[i] = c.expr(a)
	}
	return func(f *Frame) Value {
		in := make([]Value, len(args))
		for i, a := range args {
			in[i] = convert(a(f), ft.In(i))
		}
		out := fn.Call(in)
		if len(out) == 2 && !out[1].IsNil() {
			throwf(x, "host function %s failed: %v", name, out[1].Interface())
		}
		return out[0]
	}
}

// quant compiles a quantifier: the variable gets a slot, and the body
// runs per element until the result is decided.
func (c *compiler) quant(x *ast.QuantExpr) Expr {
	rng := c.expr(x.Range)
	slot := c.scope.Declare(x.Var.Name)
	body := c.expr(x.Body)
	isAny := x.Op == ast.OpAny
	return func(f *Frame) Value {
		list := norm(rng(f))
		n := 0
		if list.IsValid() {
			n = list.Len()
		}
		for i := 0; i < n; i++ {
			f.slots[slot] = list.Index(i)
			if norm(body(f)).Bool() == isAny {
				return reflect.ValueOf(isAny)
			}
		}
		return reflect.ValueOf(!isAny)
	}
}

// Bool is a convenience for tests and the policy evaluator: the bool a
// condition evaluated to.
func Bool(v Value) bool { return norm(v).Bool() }
