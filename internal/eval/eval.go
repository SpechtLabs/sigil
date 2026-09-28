// Package eval evaluates checked expressions against a Go host's input.
//
// Compile turns an expression into a tree of closures once, using the
// types the checker recorded to pick each operator's implementation up
// front, so evaluation does no type dispatch. Values are reflect.Values
// over the host's own Go data: an input's field is read through the
// index path NewKind recorded, a list is indexed in place, a map is
// looked up in place. Nothing is converted or copied on the way in.
//
// Runtime errors (an index out of range, integer overflow, a host
// function returning an error) unwind through a recovered panic and come
// back from Run as a *diag.Error pointing at the expression that failed.
package eval

import (
	"fmt"
	"reflect"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/types"
)

// Value is a runtime value: a reflect.Value over the host's data, or over
// a constant. An absent optional is the invalid Value.
type Value = reflect.Value

// Frame is the state of one evaluation: the input and the values bound
// to slots (params, lets and quantifier variables).
type Frame struct {
	Input   Value
	Outcome Value // list<decision> for assert conditions; set by the policy evaluator
	slots   []Value
}

// NewFrame returns a frame over input, a struct value or a pointer to
// one, with room for the scope's slots.
func NewFrame(input any, scope *Scope) *Frame {
	v := reflect.ValueOf(input)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	n := 0
	if scope != nil {
		n = scope.nslots
	}
	return &Frame{Input: v, slots: make([]Value, n)}
}

// Set binds the value of a param or let by slot.
func (f *Frame) Set(slot int, v Value) { f.slots[slot] = v }

// Expr is a compiled expression.
type Expr func(f *Frame) Value

// Scope maps the names a document declares to frame slots. Inputs and
// host functions come from the binding; quantifier variables get slots
// as the compiler meets them.
type Scope struct {
	binding *gokind.Binding
	slots   map[string]int
	nslots  int
}

// NewScope returns a scope over the kind's binding.
func NewScope(b *gokind.Binding) *Scope {
	return &Scope{binding: b, slots: map[string]int{}}
}

// Declare gives name a slot and returns it. The caller binds the value
// with Frame.Set before evaluating.
func (s *Scope) Declare(name string) int {
	if slot, ok := s.slots[name]; ok {
		return slot
	}
	slot := s.nslots
	s.slots[name] = slot
	s.nslots++
	return slot
}

// Slots returns how many slots a frame for this scope needs.
func (s *Scope) Slots() int { return s.nslots }

// Compile turns x, checked into info, into an Expr. It fails only when
// info doesn't cover x, which means x wasn't checked or didn't check.
func Compile(x ast.Expr, info *check.Info, scope *Scope) (Expr, *diag.Error) {
	c := &compiler{info: info, scope: scope}
	var e Expr
	err := catch(func() { e = c.expr(x) })
	if err != nil {
		return nil, err
	}
	return e, nil
}

// Run evaluates e in f, turning a runtime error into a returned one.
func Run(e Expr, f *Frame) (v Value, err *diag.Error) {
	err = catch(func() { v = e(f) })
	return v, err
}

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

type compiler struct {
	info  *check.Info
	scope *Scope
}

// typeOf returns the checked type of x, or throws when there is none.
func (c *compiler) typeOf(x ast.Expr) types.Type {
	t := c.info.TypeOf(x)
	if t == nil || t == types.Invalid {
		throwf(x, "expression `%s` wasn't checked; compile only checked expressions", ast.Sprint(x))
	}
	return t
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

var timeType = reflect.TypeOf(time.Time{})

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
		if gt, ok := c.scope.binding.Structs[t.Name]; ok {
			return reflect.Zero(gt)
		}
	case *types.Optional:
		return Value{}
	}
	return Value{}
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
			bv := mapGet(b, iter.Key())
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

// mapGet looks key up in m, converting the key to the map's key type
// when the two Go types differ (an int64 literal against map[int]T, or a
// string against map[any]any).
func mapGet(m, key Value) Value {
	m, key = norm(m), norm(key)
	if !m.IsValid() || m.IsNil() {
		return Value{}
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
