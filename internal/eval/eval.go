package eval

import (
	"fmt"
	"reflect"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/types"
)

// pollEvery is how many loop steps pass between two polls of the
// context. A step is one element of a quantifier, filter, membership
// test, list operator or map `has`, and costs 10 to 20 ns; a poll is a
// non-blocking receive on ctx.Done(). Counting a step is a decrement and
// a branch, about 2.5% of the tightest loop, a quantifier comparing two
// ints, and polling every 256 steps adds nothing measurable on top while
// noticing a cancellation within a few microseconds of loop work.
const pollEvery = 256

var (
	timeType      = reflect.TypeFor[time.Time]()
	int64Type     = reflect.TypeFor[int64]()
	float64Type   = reflect.TypeFor[float64]()
	stringType    = reflect.TypeFor[string]()
	boolType      = reflect.TypeFor[bool]()
	enumValueType = reflect.TypeFor[constant.EnumValue]()
)

// Value is a runtime value: a [reflect.Value] over the host's data, or
// over a constant. An absent optional is the invalid Value, and a present
// one is a pointer to its value.
type Value = reflect.Value

// Expr is a compiled expression. Calling it evaluates the expression in f
// and panics with a *[diag.Error] on a runtime error; [Run] turns that
// panic into a returned error.
type Expr func(f *Frame) Value

// HostPanic is what a host function panicked with, when the kind
// recovers host panics ([gokind.Binding.RecoverHostPanics]): the cause of
// the runtime error the panic became, with the stack at the panic for the
// host to log.
type HostPanic struct {
	Value any    //nolint:emptyinterface // whatever the function passed to panic
	Func  string // the host function's name in the kind
	Stack []byte // the panicking goroutine's stack, from runtime/debug.Stack
}

// canceled unwinds an evaluation whose context is done. It isn't a
// *[diag.Error], so catch passes it on to [Policy.EvalContext], which
// returns err.
type canceled struct {
	err error
}

// Error names the function and the panic value, like the message of the
// runtime error the panic became.
func (p *HostPanic) Error() string {
	return fmt.Sprintf("host function %s panicked: %v", p.Func, p.Value)
}

// Unwrap returns the panic value when it's an error, such as the
// [runtime.Error] of a nil dereference, so [errors.As] reaches it.
func (p *HostPanic) Unwrap() error { //nolint:humaneerror // returns the panic value unchanged, as errors.Unwrap expects
	err, _ := p.Value.(error)
	return err
}

// Run evaluates e in f and returns its value, or the runtime error it
// raised. A panic that isn't a runtime error, such as one inside a host
// function or the cancellation of a policy evaluation, is re-raised
// unchanged.
func Run(e Expr, f *Frame) (v Value, err *diag.Error) {
	err = catch(func() { v = e(f) })
	return v, err
}

// Bool returns the bool a condition evaluated to, unwrapping an
// interface value first. It's a convenience for tests and the policy
// evaluator, and panics when v doesn't hold a bool.
func Bool(v Value) bool { return norm(v).Bool() }

// catch runs fn and returns the runtime error it threw, if any.
func catch(fn func()) (err *diag.Error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*diag.Error)
			if !ok {
				panic(r) //nolint:nopanic // not ours: re-raise it unchanged
			}
			err = e
		}
	}()
	fn()
	return nil
}

// throwf raises a runtime error at node n.
func throwf(n ast.Node, format string, args ...any) {
	panic(&diag.Error{Msg: fmt.Sprintf(format, args...), Pos: n.Pos(), End: n.End()}) //nolint:nopanic // runtime errors unwind to Run, which returns them
}

// norm unwraps an interface value, so an element of a literal list
// ([]any) reads like an element of an input's typed slice.
func norm(v Value) Value {
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return Value{}
		}
		return v.Elem()
	}
	return v
}

// optional returns v as an optional value: v itself when it's a pointer
// already, which a nil one keeps absent, and otherwise a pointer to it.
func optional(v Value) Value {
	v = norm(v)
	if !v.IsValid() || v.Kind() == reflect.Pointer {
		return v
	}
	if v.CanAddr() {
		return v.Addr()
	}
	p := reflect.New(v.Type())
	p.Elem().Set(v)
	return p
}

// optionalChain reports whether the chain of links ending at x holds a
// `?.`, which makes the whole chain optional.
func optionalChain(x ast.Expr) bool {
	for {
		switch n := x.(type) {
		case *ast.SelectorExpr:
			if n.Optional {
				return true
			}
			x = n.X
		case *ast.IndexExpr:
			x = n.X
		default:
			return false
		}
	}
}

// equal compares two values of Sigil type t.
func equal(t types.Type, a, b Value) bool {
	a, b = norm(a), norm(b)
	switch t := t.(type) {
	case types.Basic:
		switch t {
		case types.Bool:
			return a.Bool() == b.Bool()
		case types.Int, types.Duration:
			return a.Int() == b.Int()
		case types.Float:
			return a.Float() == b.Float()
		case types.String, types.Decision:
			return a.String() == b.String()
		case types.Timestamp:
			return a.Interface().(time.Time).Equal(b.Interface().(time.Time))
		}
	case *types.Enum:
		return a.String() == b.String()
	case *types.List:
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !equal(t.Elem, a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case *types.Map:
		if a.Len() != b.Len() {
			return false
		}
		iter := a.MapRange()
		for iter.Next() {
			bv := mapGet(b, iter.Key(), t.Key)
			if !bv.IsValid() || !equal(t.Value, iter.Value(), bv) {
				return false
			}
		}
		return true
	case *types.Optional:
		if !a.IsValid() || a.IsNil() || !b.IsValid() || b.IsNil() {
			return (!a.IsValid() || a.IsNil()) && (!b.IsValid() || b.IsNil())
		}
		return equal(t.Elem, a.Elem(), b.Elem())
	}
	return false
}

// compare orders two values of an ordered type: -1, 0 or 1.
func compare(t types.Type, a, b Value) int {
	a, b = norm(a), norm(b)
	switch t {
	case types.Int, types.Duration:
		return cmp(a.Int(), b.Int())
	case types.Float:
		return cmp(a.Float(), b.Float())
	case types.Timestamp:
		x, y := a.Interface().(time.Time), b.Interface().(time.Time)
		switch {
		case x.Before(y):
			return -1
		case x.After(y):
			return 1
		}
	}
	return 0
}

func cmp[T int64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// mapGet looks key, of Sigil type kt, up in m. A typed Go map gets the
// key converted to its key type (an int64 literal against map[int]T). A
// literal's map[any]any holds its keys in the Go type canonical gives
// them, so a key of another Go type, like a host's named string type,
// is converted to that first.
func mapGet(m, key Value, kt types.Type) Value {
	m, key = norm(m), norm(key)
	if !m.IsValid() || m.IsNil() {
		return Value{}
	}
	if m.Type().Key().Kind() == reflect.Interface && key.IsValid() {
		if ct := canonicalType(kt); ct != nil && key.Type() != ct {
			key = reflect.ValueOf(canonical(kt, key))
		}
		return norm(m.MapIndex(key))
	}
	return norm(m.MapIndex(convert(key, m.Type().Key())))
}

// convert makes v usable as a t: itself when it already is, an
// interface-boxed copy for any, or a converted value when Go allows it,
// with slices and maps converted element by element.
func convert(v Value, t reflect.Type) Value {
	v = norm(v)
	switch {
	case !v.IsValid():
		return reflect.Zero(t)
	case v.Type() == t:
		return v
	case t.Kind() == reflect.Interface:
		return v
	case t.Kind() == reflect.Pointer:
		// A present value for an optional field: box it.
		p := reflect.New(t.Elem())
		p.Elem().Set(convert(v, t.Elem()))
		return p
	case t.Kind() == reflect.Slice && v.Kind() == reflect.Slice:
		out := reflect.MakeSlice(t, v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(convert(v.Index(i), t.Elem()))
		}
		return out
	case t.Kind() == reflect.Map && v.Kind() == reflect.Map:
		out := reflect.MakeMapWithSize(t, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(convert(iter.Key(), t.Key()), convert(iter.Value(), t.Elem()))
		}
		return out
	case v.Type().ConvertibleTo(t):
		return v.Convert(t)
	}
	return v
}
