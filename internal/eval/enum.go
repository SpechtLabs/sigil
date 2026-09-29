package eval

import (
	"fmt"
	"reflect"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/types"
)

// badEnum is a value a host passed for an enum that the enum doesn't
// declare.
type badEnum struct {
	enum  *types.Enum
	path  string // from the value read to the bad one, like `[2]`; empty for the value itself
	value string
	key   bool // the bad value is a map key
}

// guard reports the first value v holds that its enum doesn't declare,
// or nil, counting each list element and map entry as a step of f.
type guard func(f *Frame, v Value) *badEnum

// hostRead wraps read, which reads a host value of Sigil type t at x,
// with the check that every enum value it holds is declared: the host's
// Go type is a string, which can hold anything. A plain enum value comes
// back as its [constant.EnumValue]; a list, map or optional is checked
// and returned as it is, since converting it would copy it. A read of a
// type without enums is read itself.
func hostRead(x ast.Expr, t types.Type, read func(*Frame) (Value, bool)) func(*Frame) (Value, bool) {
	if e, ok := t.(*types.Enum); ok {
		values := enumValues(e)
		return func(f *Frame) (Value, bool) {
			v, ok := read(f)
			if !ok {
				return v, false
			}
			s := norm(v).String()
			ev, declared := values[s]
			if !declared {
				throwEnum(x, &badEnum{enum: e, value: s})
			}
			return ev, true
		}
	}
	g := guardFor(t)
	if g == nil {
		return read
	}
	return func(f *Frame) (Value, bool) {
		v, ok := read(f)
		if ok {
			if bad := g(f, v); bad != nil {
				throwEnum(x, bad)
			}
		}
		return v, ok
	}
}

// hostExpr is [hostRead] for a read that can't be absent.
func hostExpr(x ast.Expr, t types.Type, read Expr) Expr {
	checked := hostRead(x, t, func(f *Frame) (Value, bool) { return read(f), true })
	return func(f *Frame) Value {
		v, _ := checked(f)
		return v
	}
}

// enumValues maps each value of e to its constant, boxed once so a read
// returns it without allocating.
func enumValues(e *types.Enum) map[string]Value {
	out := make(map[string]Value, len(e.Values))
	for _, v := range e.Values {
		out[v] = reflect.ValueOf(constant.EnumValue(v))
	}
	return out
}

// guardFor returns the guard for values of type t, or nil when t holds
// no enum value to check. A struct's fields are checked when they're
// read, not when the struct is.
func guardFor(t types.Type) guard {
	switch t := t.(type) {
	case *types.Enum:
		values := enumValues(t)
		return func(_ *Frame, v Value) *badEnum {
			s := norm(v).String()
			if _, ok := values[s]; !ok {
				return &badEnum{enum: t, value: s}
			}
			return nil
		}
	case *types.Optional:
		elem := guardFor(t.Elem)
		if elem == nil {
			return nil
		}
		return func(f *Frame, v Value) *badEnum {
			v = norm(v)
			if !v.IsValid() || (v.Kind() == reflect.Pointer && v.IsNil()) {
				return nil
			}
			if v.Kind() == reflect.Pointer {
				v = v.Elem()
			}
			return elem(f, v)
		}
	case *types.List:
		elem := guardFor(t.Elem)
		if elem == nil {
			return nil
		}
		return func(f *Frame, v Value) *badEnum {
			v = norm(v)
			for i := 0; i < v.Len(); i++ {
				f.step(1)
				if bad := elem(f, v.Index(i)); bad != nil {
					bad.path = fmt.Sprintf("[%d]", i) + bad.path
					return bad
				}
			}
			return nil
		}
	case *types.Map:
		return mapGuard(guardFor(t.Key), guardFor(t.Value))
	}
	return nil
}

// mapGuard checks a map's keys and values. Map order varies, so it
// reports the problem under the least key, which keeps the error the
// same from one evaluation to the next.
func mapGuard(key, val guard) guard {
	if key == nil && val == nil {
		return nil
	}
	return func(f *Frame, v Value) *badEnum {
		v = norm(v)
		if !v.IsValid() || v.IsNil() {
			return nil
		}
		var first *badEnum
		var firstKey string
		for iter := v.MapRange(); iter.Next(); {
			f.step(1)
			k := norm(iter.Key())
			var bad *badEnum
			if key != nil {
				if bad = key(f, k); bad != nil {
					bad.key = true
				}
			}
			if bad == nil && val != nil {
				if bad = val(f, iter.Value()); bad != nil {
					bad.path = "[" + constant.Format(literal(k)) + "]" + bad.path
				}
			}
			if s := fmt.Sprint(k.Interface()); bad != nil && (first == nil || s < firstKey) {
				first, firstKey = bad, s
			}
		}
		return first
	}
}

// throwEnum raises the runtime error for a bad enum value read at x.
func throwEnum(x ast.Expr, bad *badEnum) {
	what := fmt.Sprintf("%q", bad.value)
	if bad.key {
		what = "key " + what
	}
	panic(&diag.Error{ //nolint:nopanic // runtime errors unwind to Run, which returns them
		Msg:  fmt.Sprintf("%s%s: %s is not a value of %s", ast.Sprint(x), bad.path, what, bad.enum.Name),
		Help: bad.enum.Name + " declares: " + bad.enum.ValueNames(),
		Pos:  x.Pos(),
		End:  x.End(),
	})
}

// qualifiedEnum reports whether x, of checked type t, is an enum value
// qualified by its enum's name, `Tier.standard`, rather than a field read:
// the qualifier names the enum and is no expression, so the checker
// recorded no type for it (xt).
func qualifiedEnum(x *ast.SelectorExpr, t, xt types.Type) bool {
	e, ok := t.(*types.Enum)
	if !ok || x.Optional || xt != nil {
		return false
	}
	id, ok := x.X.(*ast.Ident)
	return ok && id.Name == e.Name
}
