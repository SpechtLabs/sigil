package eval

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/types"
)

type compiler struct {
	info  *check.Info
	scope *Scope
}

// Compile turns x, checked into info, into an [Expr] that reads names
// through scope. It fails when info doesn't cover x, which means x wasn't
// checked or didn't check, and when scope can't supply what x reads, such
// as a name that is no param, let, slot or input, or a field or host
// function the binding lacks. Compiling adds the slots x's quantifiers
// and filters need to scope, so make frames with [NewFrame] after it.
func Compile(x ast.Expr, info *check.Info, scope *Scope) (Expr, *diag.Error) {
	c := &compiler{info: info, scope: scope}
	var e Expr
	err := catch(func() { e = c.expr(x) })
	if err != nil {
		return nil, err
	}
	return e, nil
}

// expr compiles x. Every case reads the checked types it needs up front
// and returns a closure that does only the work of that operator.
func (c *compiler) expr(x ast.Expr) Expr {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return c.expr(x.X)
	case *ast.Ident:
		return c.ident(x)
	case *ast.BoolLit:
		return constExpr(x.Value)
	case *ast.IntLit:
		return constExpr(x.Value)
	case *ast.FloatLit:
		return constExpr(x.Value)
	case *ast.StringLit:
		return constExpr(x.Value)
	case *ast.DurationLit:
		return constExpr(x.Value)
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
	case *ast.FilterExpr:
		return c.filter(x)
	}
	throwf(x, "can't evaluate %T", x)
	return nil
}

// fieldIndex looks a field path up in the binding, which a static
// compile doesn't have.
func (c *compiler) fieldIndex(key string) ([]int, bool) {
	if c.scope.binding == nil {
		return nil, false
	}
	idx, ok := c.scope.binding.Fields[key]
	return idx, ok
}

func constExpr(v any) Expr {
	val := reflect.ValueOf(v)
	return func(*Frame) Value { return val }
}

// ident compiles a name: a param's constant, a let evaluated on first
// use, a slot the frame binds, or an input read through its field index.
// Decision names are their own string.
func (c *compiler) ident(x *ast.Ident) Expr {
	if v, ok := c.scope.consts[x.Name]; ok {
		return func(*Frame) Value { return v }
	}
	if rd, ok := c.info.Reads[x]; ok {
		return c.imported(x, rd)
	}
	if i, ok := c.scope.names[x.Name]; ok {
		s := c.scope
		s.ensure(i)
		return func(f *Frame) Value { return s.value(f, i) }
	}
	if slot, ok := c.scope.slots[x.Name]; ok {
		return func(f *Frame) Value { return f.slots[slot] }
	}
	if idx, ok := c.fieldIndex("." + x.Name); ok {
		return func(f *Frame) Value { return f.Input.FieldByIndex(idx) }
	}
	if c.typeOf(x) == types.Decision {
		return constExpr(x.Name)
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
	switch x.Op {
	case ast.OpNot:
		return func(f *Frame) Value { return reflect.ValueOf(!norm(operand(f)).Bool()) }
	case ast.OpPresent:
		return func(f *Frame) Value {
			v := norm(operand(f))
			return reflect.ValueOf(v.IsValid() && !v.IsNil())
		}
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
	var test func(f *Frame, a, b Value) bool
	switch rt := c.typeOf(x.Y).(type) {
	case *types.List:
		elem := rt.Elem
		test = func(f *Frame, a, b Value) bool { return contains(f, elem, b, a) }
	default:
		test = func(_ *Frame, a, b Value) bool { return strings.Contains(norm(b).String(), norm(a).String()) }
	}
	return func(f *Frame) Value { return reflect.ValueOf(test(f, l(f), r(f)) == want) }
}

// contains reports whether list holds an element equal to v, counting
// each element it compares as a step of f.
func contains(f *Frame, elem types.Type, list, v Value) bool {
	list = norm(list)
	if !list.IsValid() {
		return false
	}
	same := func(a, b Value) bool { return equal(elem, a, b) }
	if elem == types.Decision {
		same = decisionMatch
	}
	for i := 0; i < list.Len(); i++ {
		f.step(1)
		if same(list.Index(i), v) {
			return true
		}
	}
	return false
}

// decisionMatch reports whether two decision values name the same
// outcome, where a bare decision matches any of its reasons. `==` stays
// exact; this is what `in` and the list operators use.
func decisionMatch(a, b Value) bool {
	x, y := norm(a).String(), norm(b).String()
	dx, rx, _ := strings.Cut(x, ".")
	dy, ry, _ := strings.Cut(y, ".")
	return dx == dy && (rx == "" || ry == "" || rx == ry)
}

// listOp compiles `all in`, `any in`, `one in` and `exclusive in`. The
// last two count distinct elements of the left list found in the right.
func (c *compiler) listOp(x *ast.BinaryExpr) Expr {
	l, r := c.expr(x.X), c.expr(x.Y)
	elem := c.typeOf(x.X).(*types.List).Elem
	var test func(f *Frame, a, b Value) bool
	switch x.Op {
	case ast.OpAllIn:
		test = func(f *Frame, a, b Value) bool {
			for i := 0; i < a.Len(); i++ {
				if !contains(f, elem, b, a.Index(i)) {
					return false
				}
			}
			return true
		}
	case ast.OpAnyIn:
		test = func(f *Frame, a, b Value) bool {
			for i := 0; i < a.Len(); i++ {
				if contains(f, elem, b, a.Index(i)) {
					return true
				}
			}
			return false
		}
	case ast.OpOneIn:
		test = func(f *Frame, a, b Value) bool { return distinctIn(f, elem, a, b) == 1 }
	default:
		test = func(f *Frame, a, b Value) bool { return distinctIn(f, elem, a, b) <= 1 }
	}
	return func(f *Frame) Value {
		a, b := norm(l(f)), norm(r(f))
		if !a.IsValid() {
			a = reflect.ValueOf([]any{})
		}
		return reflect.ValueOf(test(f, a, b))
	}
}

// distinctIn counts the distinct elements of a that b contains. It
// compares each element with every one before it, and counts those
// comparisons as steps of f too.
func distinctIn(f *Frame, elem types.Type, a, b Value) int {
	n := 0
	for i := 0; i < a.Len(); i++ {
		f.step(i + 1)
		v := a.Index(i)
		seen := false
		for j := 0; j < i; j++ {
			if equal(elem, a.Index(j), v) {
				seen = true
				break
			}
		}
		if !seen && contains(f, elem, b, v) {
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
			f.step(1)
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
			v, ok := constant.AddInt(norm(l(f)).Int(), norm(r(f)).Int(), sub)
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
			v, ok := constant.AddInt(norm(l(f)).Int(), norm(r(f)).Int(), sub)
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

// selector compiles `x.name` or `x?.name`, the end of a chain, or a
// decision value with its reason, `approve.release_manager`, which is
// its own string.
func (c *compiler) selector(x *ast.SelectorExpr) Expr {
	if rd, ok := c.info.Reads[x]; ok {
		return c.imported(x, rd)
	}
	if id, ok := x.X.(*ast.Ident); ok && c.info.TypeOf(id) == types.Decision {
		return constExpr(id.Name + "." + x.Sel.Name)
	}
	return c.chain(x)
}

// imported compiles a read of another document's pub let: the let is
// evaluated in that document's own frame, once per evaluation.
func (c *compiler) imported(x ast.Expr, rd check.Read) Expr {
	if c.scope.inst == nil || c.scope.inst.imports == nil {
		throwf(x, "imported let `%s` read outside a policy", rd.Let)
	}
	target := c.scope.inst.imports[rd.Doc]
	if target == nil {
		throwf(x, "document %s isn't linked", rd.Doc)
	}
	i, ok := target.scope.names[rd.Let]
	if !ok {
		throwf(x, "document %s has no let `%s`", rd.Doc, rd.Let)
	}
	target.scope.ensure(i)
	return func(f *Frame) Value {
		tf := f.frameOf(target)
		if tf.done[i] {
			return tf.lets[i]
		}
		var v Value
		if err := catch(func() { v = target.scope.value(tf, i) }); err != nil {
			// The let is the imported document's source, not the reader's.
			if err.File == "" {
				err.File, err.Doc = target.file, target.name
			}
			panic(err) //nolint:nopanic // runtime errors unwind to Run, which returns them
		}
		return v
	}
}

// index compiles `x[i]`, the end of a chain.
func (c *compiler) index(x *ast.IndexExpr) Expr { return c.chain(x) }

// chain compiles the end of an optional chain: a run of `.name`,
// `?.name` and `[index]` that no parentheses break. Without a `?.` it's
// its last link's value. With one it's optional: absent when a `?.` found
// its operand absent, and otherwise a pointer to the value, which is how
// an optional is represented and what `??` unwraps. A last link that is
// optional itself is a pointer already and stays one.
func (c *compiler) chain(x ast.Expr) Expr {
	l := c.link(x)
	if !optionalChain(x) {
		return func(f *Frame) Value {
			v, _ := l(f)
			return v
		}
	}
	return func(f *Frame) Value {
		v, ok := l(f)
		if !ok {
			return Value{}
		}
		return optional(v)
	}
}

// link compiles one link of a chain. It returns false once a `?.` has
// found its operand absent, and every later link passes that on without
// running, so nothing after it can fail.
func (c *compiler) link(x ast.Expr) func(*Frame) (Value, bool) {
	switch x := x.(type) {
	case *ast.SelectorExpr:
		base := c.linkBase(x.X)
		t := c.typeOf(x.X)
		if opt, ok := t.(*types.Optional); ok && x.Optional {
			t = opt.Elem
		}
		if l, ok := c.candidateLink(x, base, t); ok {
			return l
		}
		s := t.(*types.Struct)
		idx, ok := c.fieldIndex(s.Name + "." + x.Sel.Name)
		if !ok {
			throwf(x.Sel, "no binding for field %s.%s", s.Name, x.Sel.Name)
		}
		if x.Optional {
			return func(f *Frame) (Value, bool) {
				v, ok := base(f)
				if v = norm(v); !ok || !v.IsValid() || v.IsNil() {
					return Value{}, false
				}
				return v.Elem().FieldByIndex(idx), true
			}
		}
		return func(f *Frame) (Value, bool) {
			v, ok := base(f)
			if !ok {
				return Value{}, false
			}
			return norm(v).FieldByIndex(idx), true
		}
	case *ast.IndexExpr:
		base, get := c.linkBase(x.X), c.indexer(x)
		return func(f *Frame) (Value, bool) {
			v, ok := base(f)
			if !ok {
				return Value{}, false
			}
			return get(f, v), true
		}
	}
	e := c.expr(x)
	return func(f *Frame) (Value, bool) { return e(f), true }
}

// candidateLink compiles the selectors that read candidates, where t is
// the operand's type: `outcome.<decision>`, `<candidates>.<reason>` and
// a field of one candidate. It reports false for any other selector.
// Their bases are never absent, since `?.` doesn't apply to candidates.
// A list of candidates is a []*Candidate, so quantifiers and filters
// range over it like any other list.
func (c *compiler) candidateLink(x *ast.SelectorExpr, base func(*Frame) (Value, bool), t types.Type) (func(*Frame) (Value, bool), bool) {
	name := x.Sel.Name
	switch t := t.(type) {
	case *types.List:
		if _, ok := x.X.(*ast.Outcome); ok {
			return func(f *Frame) (Value, bool) {
				return reflect.ValueOf(keep(f.Candidates, func(c *Candidate) bool { return c.Decision.Name == name })), true
			}, true
		}
		if _, ok := t.Elem.(*types.Candidate); ok {
			return func(f *Frame) (Value, bool) {
				v, _ := base(f)
				cands, _ := reflect.TypeAssert[[]*Candidate](norm(v))
				return reflect.ValueOf(keep(cands, func(c *Candidate) bool { return c.Reason == name })), true
			}, true
		}
	case *types.Candidate:
		return func(f *Frame) (Value, bool) {
			v, _ := base(f)
			cand, _ := reflect.TypeAssert[*Candidate](norm(v))
			if name == "reason" {
				return reflect.ValueOf(cand.Outcome()), true
			}
			return reflect.ValueOf(cand.Payload[name]), true
		}, true
	}
	return nil, false
}

// keep returns the candidates for which ok holds, in order, never nil.
func keep(cands []*Candidate, ok func(*Candidate) bool) []*Candidate {
	out := []*Candidate{}
	for _, c := range cands {
		if ok(c) {
			out = append(out, c)
		}
	}
	return out
}

// linkBase compiles the operand of a link: the previous link of the same
// chain, or any other expression.
func (c *compiler) linkBase(x ast.Expr) func(*Frame) (Value, bool) {
	switch x.(type) {
	case *ast.SelectorExpr, *ast.IndexExpr:
		return c.link(x)
	}
	e := c.expr(x)
	return func(f *Frame) (Value, bool) { return e(f), true }
}

// indexer compiles the lookup of `m[k]` on a base value, which yields the
// value type's zero for a missing key, and of `xs[i]`, which fails on an
// index out of range.
func (c *compiler) indexer(x *ast.IndexExpr) func(*Frame, Value) Value {
	key := c.expr(x.Index)
	switch t := c.typeOf(x.X).(type) {
	case *types.Map:
		// A missing key yields the zero value: the Go map's own when it's
		// typed, the Sigil type's canonical one for a literal's map[any]any.
		zero := c.zero(t.Value)
		return func(f *Frame, base Value) Value {
			m := norm(base)
			if v := mapGet(m, key(f)); v.IsValid() {
				return v
			}
			if m.IsValid() && m.Type().Elem().Kind() != reflect.Interface {
				return reflect.Zero(m.Type().Elem())
			}
			return zero
		}
	default:
		return func(f *Frame, base Value) Value {
			list, i := norm(base), norm(key(f)).Int()
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
// result becomes a runtime error caused by it, and the context is polled
// once the function returns. A panic in the function becomes a runtime
// error caused by a *HostPanic when the binding asks for that. Otherwise
// it isn't recovered: it isn't a *diag.Error, so catch re-raises it to
// the host.
func (c *compiler) call(x *ast.CallExpr) Expr {
	name := x.Fun.(*ast.Ident).Name
	var fn reflect.Value
	ok := false
	if c.scope.binding != nil {
		fn, ok = c.scope.binding.Funcs[name]
	}
	if !ok {
		throwf(x.Fun, "no implementation bound for host function %s", name)
	}
	ft := fn.Type()
	args := make([]Expr, len(x.Args))
	for i, a := range x.Args {
		args[i] = c.expr(a)
	}
	invoke := fn.Call
	if c.scope.binding.RecoverHostPanics {
		invoke = func(in []Value) []Value { return callRecovered(fn, in, name, x) }
	}
	return func(f *Frame) Value {
		in := make([]Value, len(args))
		for i, a := range args {
			in[i] = convert(a(f), ft.In(i))
		}
		out := invoke(in)
		if f.run != nil {
			// A host function can take long, and nothing interrupts it:
			// stop as soon as it returns once the context is done.
			f.run.poll()
		}
		if len(out) == 2 && !out[1].IsNil() {
			err, _ := reflect.TypeAssert[error](out[1])
			e := &diag.Error{Msg: fmt.Sprintf("host function %s failed: %v", name, err), Pos: x.Pos(), End: x.End(), Cause: err}
			if _, ok := errors.AsType[*gokind.ErrUnbound](err); ok {
				e.Help = "this sigil binary has only " + name + "'s signature from the kind file; evaluate with the host's own binary, built with sigil's pkg/cli, which links the real function in"
			}
			panic(e) //nolint:nopanic // runtime errors unwind to Run, which returns them
		}
		return out[0]
	}
}

// callRecovered calls a host function and turns a panic in it into a
// runtime error at x. The message names the function and the panic
// value; the stack goes into the *HostPanic cause, not the message.
func callRecovered(fn Value, in []Value, name string, x ast.Node) []Value {
	defer func() {
		if r := recover(); r != nil {
			p := &HostPanic{Func: name, Value: r, Stack: debug.Stack()}
			panic(&diag.Error{Msg: p.Error(), Pos: x.Pos(), End: x.End(), Cause: p}) //nolint:nopanic // runtime errors unwind to Run, which returns them
		}
	}()
	return fn.Call(in)
}

// quant compiles a quantifier: the variable gets a slot of its own, and
// the body runs per element until the result is decided.
func (c *compiler) quant(x *ast.QuantExpr) Expr {
	rng := c.expr(x.Range)
	slot, restore := c.scope.bindVar(x.Var.Name)
	body := c.expr(x.Body)
	restore()
	isAny := x.Op == ast.OpAny
	return func(f *Frame) Value {
		list := norm(rng(f))
		n := 0
		if list.IsValid() {
			n = list.Len()
		}
		for i := 0; i < n; i++ {
			f.step(1)
			f.slots[slot] = list.Index(i)
			if norm(body(f)).Bool() == isAny {
				return reflect.ValueOf(isAny)
			}
		}
		return reflect.ValueOf(!isAny)
	}
}

// filter compiles a filter: the variable gets a slot of its own, the body
// runs once per element, and the elements it holds for are copied, in
// order, into a new list of the range's own Go type.
func (c *compiler) filter(x *ast.FilterExpr) Expr {
	rng := c.expr(x.Range)
	slot, restore := c.scope.bindVar(x.Var.Name)
	body := c.expr(x.Body)
	restore()
	return func(f *Frame) Value {
		list := norm(rng(f))
		if !list.IsValid() {
			return reflect.ValueOf([]any{})
		}
		out := reflect.MakeSlice(list.Type(), 0, list.Len())
		for i := 0; i < list.Len(); i++ {
			f.step(1)
			elem := list.Index(i)
			f.slots[slot] = elem
			if norm(body(f)).Bool() {
				out = reflect.Append(out, elem)
			}
		}
		return out
	}
}

// typeOf returns the checked type of x, or throws when there is none.
func (c *compiler) typeOf(x ast.Expr) types.Type {
	t := c.info.TypeOf(x)
	if t == nil || t == types.Invalid {
		throwf(x, "expression `%s` wasn't checked; compile only checked expressions", ast.Sprint(x))
	}
	return t
}

// zero returns the zero value of a Sigil type, as a missing map key
// yields. Struct types need their Go type, from the binding.
func (c *compiler) zero(t types.Type) Value {
	switch t := t.(type) {
	case types.Basic:
		switch t {
		case types.Bool:
			return reflect.ValueOf(false)
		case types.Int:
			return reflect.ValueOf(int64(0))
		case types.Float:
			return reflect.ValueOf(0.0)
		case types.String, types.Decision:
			return reflect.ValueOf("")
		case types.Duration:
			return reflect.ValueOf(time.Duration(0))
		case types.Timestamp:
			return reflect.Zero(timeType)
		}
	case *types.List:
		return reflect.ValueOf([]any{})
	case *types.Map:
		return reflect.ValueOf(map[any]any{})
	case *types.Struct:
		if c.scope.binding != nil {
			if gt, ok := c.scope.binding.Structs[t.Name]; ok {
				return reflect.Zero(gt)
			}
		}
	case *types.Optional:
		return Value{}
	}
	return Value{}
}
