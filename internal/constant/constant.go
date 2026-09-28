// Package constant evaluates the constant expressions the language allows
// in a few places: payload field defaults and the default decision in a
// kind, param defaults, and invocation arguments. A constant is a
// literal, a list or map literal of constants, or `+`, `-` and unary
// minus applied to constants, as docs/reference/policy-files.md specifies
// for param defaults.
//
// Evaluation is checked against the type the constant must have, which
// every one of those places knows: the field's, the param's. That's what
// gives an empty `[]` its element type and what turns a mismatch into a
// message naming both types.
//
// The package also owns the Go representation of a constant value:
// Conforms says which Go values stand for which Sigil type, and Format
// prints one back as a literal.
package constant

import (
	"fmt"
	"math"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/types"
)

// help is the hint on every "not a constant" error.
const help = "a constant is a literal, a list or map of literals, or `+` and `-` applied to those"

// Eval evaluates x as a constant of type want and returns its value in
// the representation Conforms accepts: bool, int64, float64, string,
// time.Duration, []any and map[any]any. An optional type takes a constant
// of its element type. The error, if any, points into x.
func Eval(x ast.Expr, want types.Type) (any, *diag.Error) {
	if opt, ok := want.(*types.Optional); ok {
		want = opt.Elem
	}
	switch want := want.(type) {
	case types.Basic:
		return evalBasic(x, want)
	case *types.List:
		return evalList(x, want)
	case *types.Map:
		return evalMap(x, want)
	case *types.Struct:
		return nil, errorf(x, "", "%s has no literal form, so a constant can't be one", want.Name)
	}
	return nil, errorf(x, "", "invalid type")
}

// AddInt returns a+b, or a-b when sub is set, and false on overflow.
// Constant folding and the evaluator both use it, so an overflow is
// caught the same way at compile time and at run time.
func AddInt(a, b int64, sub bool) (int64, bool) {
	if sub {
		if b == math.MinInt64 {
			// -MinInt64 doesn't exist, but a - MinInt64 fits when a is negative.
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

func errorf(at ast.Node, hint, format string, args ...any) *diag.Error {
	return &diag.Error{Msg: fmt.Sprintf(format, args...), Help: hint, Pos: at.Pos(), End: at.End()}
}

// mismatch describes a constant of the wrong type.
func mismatch(x ast.Expr, want types.Type) *diag.Error {
	return errorf(x, "", "expected %s, found %s", want, describe(x))
}

// describe names what kind of constant x is, for a mismatch message.
func describe(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return describe(x.X)
	case *ast.BoolLit:
		return "bool"
	case *ast.IntLit:
		return "int"
	case *ast.FloatLit:
		return "float"
	case *ast.StringLit:
		return "string"
	case *ast.DurationLit:
		return "duration"
	case *ast.ListLit:
		return "a list"
	case *ast.MapLit:
		return "a map"
	}
	return "`" + ast.Sprint(x) + "`"
}

func evalBasic(x ast.Expr, want types.Basic) (any, *diag.Error) {
	switch want {
	case types.Timestamp:
		return nil, errorf(x, "timestamps come from input; there's no literal for one", "expected timestamp, found %s", describe(x))
	case types.Decision:
		return nil, errorf(x, "decision values only come from `outcome`", "expected decision, found %s", describe(x))
	case types.Invalid:
		return nil, errorf(x, "", "invalid type")
	}

	switch x := x.(type) {
	case *ast.ParenExpr:
		return evalBasic(x.X, want)
	case *ast.BoolLit:
		if want == types.Bool {
			return x.Value, nil
		}
	case *ast.IntLit:
		if want == types.Int {
			return x.Value, nil
		}
	case *ast.FloatLit:
		if want == types.Float {
			return x.Value, nil
		}
	case *ast.StringLit:
		if want == types.String {
			return x.Value, nil
		}
	case *ast.DurationLit:
		if want == types.Duration {
			return x.Value, nil
		}
	case *ast.UnaryExpr:
		if x.Op == ast.OpNeg {
			return evalNeg(x, want)
		}
		return nil, errorf(x, help, "`%s` isn't a constant", x.Op)
	case *ast.BinaryExpr:
		if x.Op == ast.OpAdd || x.Op == ast.OpSub {
			return evalArith(x, want)
		}
		return nil, errorf(x, help, "`%s` isn't a constant", x.Op)
	case *ast.ListLit, *ast.MapLit:
	default:
		return nil, errorf(x, help, "`%s` isn't a constant", ast.Sprint(x))
	}
	return nil, mismatch(x, want)
}

// evalNeg evaluates unary minus, which applies to int, float and duration.
func evalNeg(x *ast.UnaryExpr, want types.Basic) (any, *diag.Error) {
	if x == nil {
		return nil, nil
	}
	if !numeric(want) {
		return nil, errorf(x, "", "expected %s, found a negated value", want)
	}
	v, err := evalBasic(x.X, want)
	if err != nil {
		return nil, err
	}
	switch v := v.(type) {
	case int64:
		if v == math.MinInt64 {
			return nil, errorf(x, "", "integer overflow in constant")
		}
		return -v, nil
	case float64:
		return -v, nil
	case time.Duration:
		if v == math.MinInt64 {
			return nil, errorf(x, "", "duration overflow in constant")
		}
		return -v, nil
	}
	return nil, errorf(x, "", "invalid constant")
}

// evalArith evaluates `+` and `-` on two constants of the same numeric
// type, with overflow reported rather than wrapped.
func evalArith(x *ast.BinaryExpr, want types.Basic) (any, *diag.Error) {
	if x == nil {
		return nil, nil
	}
	if !numeric(want) {
		return nil, errorf(x, "", "expected %s, found `%s` arithmetic", want, x.Op)
	}
	l, err := evalBasic(x.X, want)
	if err != nil {
		return nil, err
	}
	r, err := evalBasic(x.Y, want)
	if err != nil {
		return nil, err
	}
	sub := x.Op == ast.OpSub
	switch l := l.(type) {
	case int64:
		v, ok := AddInt(l, r.(int64), sub)
		if !ok {
			return nil, errorf(x, "", "integer overflow in constant")
		}
		return v, nil
	case float64:
		v := l + r.(float64)
		if sub {
			v = l - r.(float64)
		}
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return nil, errorf(x, "constant floats must be finite so the kind can be exported", "float overflow in constant")
		}
		return v, nil
	case time.Duration:
		v, ok := AddInt(int64(l), int64(r.(time.Duration)), sub)
		if !ok {
			return nil, errorf(x, "", "duration overflow in constant")
		}
		return time.Duration(v), nil
	}
	return nil, errorf(x, "", "invalid constant")
}

func numeric(t types.Basic) bool {
	return t == types.Int || t == types.Float || t == types.Duration
}

func evalList(x ast.Expr, want *types.List) (any, *diag.Error) {
	if want == nil {
		return nil, errorf(x, "", "invalid type")
	}
	switch x := x.(type) {
	case *ast.ParenExpr:
		return evalList(x.X, want)
	case *ast.ListLit:
		elems := make([]any, 0, len(x.Elems))
		for _, e := range x.Elems {
			v, err := Eval(e, want.Elem)
			if err != nil {
				return nil, err
			}
			elems = append(elems, v)
		}
		return elems, nil
	case *ast.BoolLit, *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.DurationLit, *ast.MapLit, *ast.UnaryExpr, *ast.BinaryExpr:
		return nil, mismatch(x, want)
	}
	return nil, errorf(x, help, "`%s` isn't a constant", ast.Sprint(x))
}

func evalMap(x ast.Expr, want *types.Map) (any, *diag.Error) {
	if want == nil {
		return nil, errorf(x, "", "invalid type")
	}
	switch x := x.(type) {
	case *ast.ParenExpr:
		return evalMap(x.X, want)
	case *ast.MapLit:
		m := make(map[any]any, len(x.Entries))
		for _, e := range x.Entries {
			k, err := Eval(e.Key, want.Key)
			if err != nil {
				return nil, err
			}
			if _, dup := m[k]; dup {
				return nil, errorf(e.Key, "", "duplicate key %s in map constant", ast.Sprint(e.Key))
			}
			v, err := Eval(e.Value, want.Value)
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		return m, nil
	case *ast.BoolLit, *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.DurationLit, *ast.ListLit, *ast.UnaryExpr, *ast.BinaryExpr:
		return nil, mismatch(x, want)
	}
	return nil, errorf(x, help, "`%s` isn't a constant", ast.Sprint(x))
}
