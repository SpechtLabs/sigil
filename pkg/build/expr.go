package build

import (
	"reflect"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
)

// Expr is a typed Sigil expression. T is the Go type a host value of it
// has: string, bool, int or int64, float64, [time.Duration], [time.Time],
// []E, map[K]V, a type the kind registers as an enum, *T for an optional,
// or a struct type of the kind.
//
// T only guides the Go compiler: it keeps a duration from being compared
// with a string where the builder's signatures can say so. Go can't
// restrict T further, so `Lt` on two strings builds, and the Sigil
// checker, which [Check] runs, is the authority on what's well typed.
//
// The zero Expr holds no expression. A document that uses one fails to
// render, with an error at the builder call that received it.
type Expr[T any] struct {
	n node
}

// Value is any [Expr], whatever its type. It's what [Call] takes for a
// host function's arguments. Only Expr implements it.
type Value interface {
	expr() node
}

// Lit is v written as a Sigil literal of the type its Go type maps to: a
// string quoted as Go quotes it, a number, a duration such as `24h`, a
// value of an enum type bare, as `critical`, and a slice or map as a list
// or map literal, its map entries sorted. An enum value two enums of the
// kind declare is qualified, as `Tier.critical`.
//
// A timestamp, an optional and a struct have no literal, and neither has
// a duration with a remainder below a millisecond or a float that isn't
// finite; using one is an error when the document renders.
//
// An empty slice or map renders `[]` or `{}`, which takes its type from
// where it stands, such as the other operand of `in` or a param's type.
// Standing alone, as in `let none = []`, it fails [Check].
func Lit[T any](v T) Expr[T] {
	return Expr[T]{&litNode{v: reflect.ValueOf(&v).Elem(), t: reflect.TypeFor[T](), s: callSite("build.Lit")}}
}

// Raw is Sigil source spliced into an expression, for a construct the
// builder has no function for. It must parse as one expression; the
// printer adds parentheses around it where the surrounding operators
// need them. The source is reprinted from its syntax tree, so a comment
// in it is dropped. Prefer the typed functions: Raw names inputs and lets
// by hand, so a rename in Go doesn't reach it.
func Raw[T any](src string) Expr[T] {
	return Expr[T]{&rawNode{src: src, s: callSite("build.Raw")}}
}

// List is a list literal of expressions, `[a, b]`. For a list of
// constants, [Lit] of a slice is shorter. With no expressions it's `[]`,
// which needs a type from where it stands, as [Lit] of an empty slice
// does.
func List[E any](xs ...Expr[E]) Expr[[]E] {
	n := &listNode{xs: make([]node, len(xs)), s: callSite("build.List")}
	for i, x := range xs {
		n.xs[i] = x.n
	}
	return Expr[[]E]{n}
}

// Eq is `x == y`.
func (x Expr[T]) Eq(y Expr[T]) Expr[bool] {
	return Expr[bool]{binary(callSite("Expr.Eq"), ast.OpEq, x.n, y.n)}
}

// NotEq is `x != y`.
func (x Expr[T]) NotEq(y Expr[T]) Expr[bool] {
	return Expr[bool]{binary(callSite("Expr.NotEq"), ast.OpNotEq, x.n, y.n)}
}

// Lt is `x < y`.
func (x Expr[T]) Lt(y Expr[T]) Expr[bool] {
	return Expr[bool]{binary(callSite("Expr.Lt"), ast.OpLt, x.n, y.n)}
}

// Le is `x <= y`.
func (x Expr[T]) Le(y Expr[T]) Expr[bool] {
	return Expr[bool]{binary(callSite("Expr.Le"), ast.OpLtEq, x.n, y.n)}
}

// Gt is `x > y`.
func (x Expr[T]) Gt(y Expr[T]) Expr[bool] {
	return Expr[bool]{binary(callSite("Expr.Gt"), ast.OpGt, x.n, y.n)}
}

// Ge is `x >= y`.
func (x Expr[T]) Ge(y Expr[T]) Expr[bool] {
	return Expr[bool]{binary(callSite("Expr.Ge"), ast.OpGtEq, x.n, y.n)}
}

// Within is `x in s` on strings: x is a substring of s.
func (x Expr[T]) Within(s Expr[string]) Expr[bool] {
	return Expr[bool]{binary(callSite("Expr.Within"), ast.OpIn, x.n, s.n)}
}

// Like is `x like "glob"`: `*` matches any run of characters and `?`
// exactly one, over the whole string.
func (x Expr[T]) Like(glob string) Expr[bool] {
	return Expr[bool]{&patternNode{x: x.n, pattern: glob, op: ast.OpLike, s: callSite("Expr.Like")}}
}

// Matches is `x matches "re"`, a Go RE2 regular expression matched
// anywhere in the string. A pattern with a backslash renders as a raw
// string, so it reads as written. An invalid pattern is an error when the
// document renders.
func (x Expr[T]) Matches(re string) Expr[bool] {
	return Expr[bool]{&patternNode{x: x.n, pattern: re, op: ast.OpMatches, s: callSite("Expr.Matches")}}
}

// Add is `x + y` on numbers and durations. For a timestamp, see
// [TimeAdd].
func (x Expr[T]) Add(y Expr[T]) Expr[T] {
	return Expr[T]{binary(callSite("Expr.Add"), ast.OpAdd, x.n, y.n)}
}

// Sub is `x - y` on numbers and durations. For timestamps, see [TimeSub]
// and [TimeDiff].
func (x Expr[T]) Sub(y Expr[T]) Expr[T] {
	return Expr[T]{binary(callSite("Expr.Sub"), ast.OpSub, x.n, y.n)}
}

// And is `a and b and …`, true when every x is. It needs at least one
// operand; with one, it's that operand.
func And(xs ...Expr[bool]) Expr[bool] {
	return Expr[bool]{chain(callSite("build.And"), ast.OpAnd, xs)}
}

// Or is `a or b or …`, true when any x is. It needs at least one
// operand; with one, it's that operand.
func Or(xs ...Expr[bool]) Expr[bool] {
	return Expr[bool]{chain(callSite("build.Or"), ast.OpOr, xs)}
}

// Xor is `a xor b`, true when exactly one of the two is. Sigil doesn't
// chain `xor`; for exactly one of several, use [OneIn].
func Xor(a, b Expr[bool]) Expr[bool] {
	return Expr[bool]{binary(callSite("build.Xor"), ast.OpXor, a.n, b.n)}
}

// Not is `not x`.
func Not(x Expr[bool]) Expr[bool] {
	return Expr[bool]{&unaryNode{op: ast.OpNot, x: x.n, s: callSite("build.Not")}}
}

// In is `x in xs`: xs has an element equal to x. It's a function, not
// a method of Expr, because a method of Expr[T] can't take an Expr[[]T].
func In[T any](x Expr[T], xs Expr[[]T]) Expr[bool] {
	return Expr[bool]{binary(callSite("build.In"), ast.OpIn, x.n, xs.n)}
}

// NotIn is `x not in xs`.
func NotIn[T any](x Expr[T], xs Expr[[]T]) Expr[bool] {
	return Expr[bool]{binary(callSite("build.NotIn"), ast.OpNotIn, x.n, xs.n)}
}

// AllIn is `a all in b`: every element of a is in b.
func AllIn[E any](a, b Expr[[]E]) Expr[bool] {
	return Expr[bool]{binary(callSite("build.AllIn"), ast.OpAllIn, a.n, b.n)}
}

// AnyIn is `a any in b`: some element of a is in b.
func AnyIn[E any](a, b Expr[[]E]) Expr[bool] {
	return Expr[bool]{binary(callSite("build.AnyIn"), ast.OpAnyIn, a.n, b.n)}
}

// OneIn is `a one in b`: exactly one distinct element of a is in b.
func OneIn[E any](a, b Expr[[]E]) Expr[bool] {
	return Expr[bool]{binary(callSite("build.OneIn"), ast.OpOneIn, a.n, b.n)}
}

// ExclusiveIn is `a exclusive in b`: at most one distinct element of a
// is in b.
func ExclusiveIn[E any](a, b Expr[[]E]) Expr[bool] {
	return Expr[bool]{binary(callSite("build.ExclusiveIn"), ast.OpExclusiveIn, a.n, b.n)}
}

// Index is `xs[i]`, an element of a list. Go's int and int64 both map to
// Sigil's int, so i may be either. An index out of range is a runtime
// error of the policy.
func Index[E any, I int | int64](xs Expr[[]E], i Expr[I]) Expr[E] {
	return Expr[E]{&indexNode{x: xs.n, i: i.n, s: callSite("build.Index")}}
}

// Get is `m[k]`, a map value. A missing key reads as the value type's
// zero value.
func Get[K comparable, V any](m Expr[map[K]V], k Expr[K]) Expr[V] {
	return Expr[V]{&indexNode{x: m.n, i: k.n, s: callSite("build.Get")}}
}

// HasKey is `m has k`: m has the key k.
func HasKey[K comparable, V any](m Expr[map[K]V], k Expr[K]) Expr[bool] {
	return Expr[bool]{binary(callSite("build.HasKey"), ast.OpHas, m.n, k.n)}
}

// HasAll is `m has sub`: every pair of sub is in m with an equal value.
func HasAll[K comparable, V any](m, sub Expr[map[K]V]) Expr[bool] {
	return Expr[bool]{binary(callSite("build.HasAll"), ast.OpHas, m.n, sub.n)}
}

// Coalesce is `x ?? def`: the value of the optional x, or def when x is
// absent.
func Coalesce[T any](x Expr[*T], def Expr[T]) Expr[T] {
	return Expr[T]{binary(callSite("build.Coalesce"), ast.OpCoalesce, x.n, def.n)}
}

// Present is `present x`: the optional x holds a value.
func Present[T any](x Expr[*T]) Expr[bool] {
	return Expr[bool]{&unaryNode{op: ast.OpPresent, x: x.n, s: callSite("build.Present")}}
}

// Neg is `-x`, on a number or a duration.
func Neg[T any](x Expr[T]) Expr[T] {
	return Expr[T]{&unaryNode{op: ast.OpNeg, x: x.n, s: callSite("build.Neg")}}
}

// TimeAdd is `t + d`, the timestamp d after t.
func TimeAdd(t Expr[time.Time], d Expr[time.Duration]) Expr[time.Time] {
	return Expr[time.Time]{binary(callSite("build.TimeAdd"), ast.OpAdd, t.n, d.n)}
}

// TimeSub is `t - d`, the timestamp d before t.
func TimeSub(t Expr[time.Time], d Expr[time.Duration]) Expr[time.Time] {
	return Expr[time.Time]{binary(callSite("build.TimeSub"), ast.OpSub, t.n, d.n)}
}

// TimeDiff is `a - b`, the duration from b to a.
func TimeDiff(a, b Expr[time.Time]) Expr[time.Duration] {
	return Expr[time.Duration]{binary(callSite("build.TimeDiff"), ast.OpSub, a.n, b.n)}
}

// Call is `name(args)`, a call of the host function the kind declares as
// name. R is its Go result type. [Func1], [Func2] and [Func3] type the
// arguments too.
func Call[R any](name string, args ...Value) Expr[R] {
	return Expr[R]{call(callSite("build.Call"), name, args)}
}

// Func1 returns a typed caller of the one-argument host function name.
// Declare it once and call it like the host function:
//
//	lower := build.Func1[string, string]("lower")
//	lower(build.Field(&in.Service.Name)) // lower(service.name)
func Func1[A, R any](name string) func(Expr[A]) Expr[R] {
	return func(a Expr[A]) Expr[R] {
		return Expr[R]{call(callSite("build.Func1"), name, []Value{a})}
	}
}

// Func2 returns a typed caller of the two-argument host function name:
//
//	split := build.Func2[string, string, []string]("split")
//	split(build.Get(labels, build.Lit("regions")), build.Lit(",")) // split(service.labels["regions"], ",")
func Func2[A, B, R any](name string) func(Expr[A], Expr[B]) Expr[R] {
	return func(a Expr[A], b Expr[B]) Expr[R] {
		return Expr[R]{call(callSite("build.Func2"), name, []Value{a, b})}
	}
}

// Func3 returns a typed caller of the three-argument host function name.
func Func3[A, B, C, R any](name string) func(Expr[A], Expr[B], Expr[C]) Expr[R] {
	return func(a Expr[A], b Expr[B], c Expr[C]) Expr[R] {
		return Expr[R]{call(callSite("build.Func3"), name, []Value{a, b, c})}
	}
}

func (x Expr[T]) expr() node { return x.n }

// binary is the node of an infix operator.
func binary(s Site, op ast.Op, x, y node) node {
	return &binaryNode{op: op, x: x, y: y, s: s}
}

// chain joins xs with op, left to right.
func chain(s Site, op ast.Op, xs []Expr[bool]) node {
	if len(xs) == 0 {
		return &errNode{s.errorf("needs at least one operand")}
	}
	n := xs[0].n
	if n == nil {
		n = &nilNode{s: s}
	}
	for _, x := range xs[1:] {
		n = binary(s, op, n, x.n)
	}
	return n
}

// call is the node of a host function call.
func call(s Site, name string, args []Value) node {
	if msg := identError(name); msg != "" {
		return &errNode{s.errorf("host function %s", msg)}
	}
	n := &callNode{name: name, args: make([]node, len(args)), s: s}
	for i, a := range args {
		if a != nil {
			n.args[i] = a.expr()
		}
	}
	return n
}
