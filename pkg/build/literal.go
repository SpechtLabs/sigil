package build

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
)

// litNode is a Go value written as a Sigil literal.
type litNode struct {
	v reflect.Value
	t reflect.Type
	s Site
}

// rawNode is Sigil source spliced into an expression.
type rawNode struct {
	src string
	s   Site
}

// lower writes the value as a literal of the Sigil type its Go type maps
// to under the kind.
func (n *litNode) lower(l *lowerer) ast.Expr {
	t, ok := l.binding.TypeOf(n.t)
	if !ok {
		l.errorf(n.s, "Go type %v has no Sigil type in kind %s", n.t, l.model.Name)
		return &ast.BadExpr{}
	}
	if why := noLiteral(t); why != "" {
		l.errorf(n.s, "%s has no literal: %s", t, why)
		return &ast.BadExpr{}
	}
	x, err := l.literal(l.binding.Canonical(t, n.v), t)
	if err != "" {
		l.errorf(n.s, "%s", err)
		return &ast.BadExpr{}
	}
	return x
}

// lower parses the source as an expression.
func (n *rawNode) lower(l *lowerer) ast.Expr {
	x, errs := parser.ParseExpr("", []byte(n.src))
	if errs != nil {
		l.errorf(n.s, "%q doesn't parse: %s", n.src, errs[0].Msg)
		return &ast.BadExpr{}
	}
	return x
}

// literal builds the literal for c, a constant of type t in the
// representation [constant.Conforms] describes, or says why there is
// none.
func (l *lowerer) literal(c any, t types.Type) (ast.Expr, string) {
	switch t := t.(type) {
	case *types.Enum:
		name := string(c.(constant.EnumValue))
		if !t.Has(name) {
			return nil, fmt.Sprintf("%s has no value %q; %s declares: %s", t.Name, name, t.Name, t.ValueNames())
		}
		if len(l.model.EnumsWith(name)) > 1 {
			// A bare name two enums declare is ambiguous outside a context
			// that expects one of them.
			return &ast.SelectorExpr{X: &ast.Ident{Name: t.Name}, Sel: &ast.Ident{Name: name}}, ""
		}
		return &ast.Ident{Name: name}, ""
	case *types.List:
		xs := c.([]any)
		lit := &ast.ListLit{Elems: make([]ast.Expr, len(xs))}
		for i, x := range xs {
			e, err := l.literal(x, t.Elem)
			if err != "" {
				return nil, err
			}
			lit.Elems[i] = e
		}
		return lit, ""
	case *types.Map:
		return l.mapLiteral(c.(map[any]any), t)
	}
	return scalar(c)
}

// mapLiteral builds a map literal with its entries sorted by key, so the
// output is stable.
func (l *lowerer) mapLiteral(m map[any]any, t *types.Map) (ast.Expr, string) { //nolint:emptyinterface // constants are typed by their Sigil type
	keys := make([]any, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return constant.Format(keys[i]) < constant.Format(keys[j]) })
	lit := &ast.MapLit{Entries: make([]ast.MapEntry, len(keys))}
	for i, k := range keys {
		key, err := l.literal(k, t.Key)
		if err != "" {
			return nil, err
		}
		val, err := l.literal(m[k], t.Value)
		if err != "" {
			return nil, err
		}
		lit.Entries[i] = ast.MapEntry{Key: key, Value: val}
	}
	return lit, ""
}

// scalar builds the literal of a bool, int64, float64, string or
// duration constant. A negative number is unary minus on its magnitude,
// as in source.
func scalar(c any) (ast.Expr, string) {
	switch c := c.(type) {
	case bool:
		return &ast.BoolLit{Value: c}, ""
	case int64:
		switch {
		case c == math.MinInt64:
			// The magnitude isn't a valid literal; spell it as arithmetic.
			return &ast.BinaryExpr{Op: ast.OpSub, X: neg(&ast.IntLit{Text: "9223372036854775807"}), Y: &ast.IntLit{Text: "1"}}, ""
		case c < 0:
			return neg(&ast.IntLit{Text: strconv.FormatInt(-c, 10)}), ""
		}
		return &ast.IntLit{Text: strconv.FormatInt(c, 10)}, ""
	case float64:
		if math.IsNaN(c) || math.IsInf(c, 0) {
			return nil, fmt.Sprintf("%v has no literal; a float literal is finite", c)
		}
		text := constant.Format(math.Abs(c))
		if math.Signbit(c) && c != 0 {
			return neg(&ast.FloatLit{Text: text}), ""
		}
		return &ast.FloatLit{Text: text}, ""
	case string:
		return &ast.StringLit{Text: strconv.Quote(c)}, ""
	case time.Duration:
		if c%time.Millisecond != 0 {
			return nil, fmt.Sprintf("duration %v has no literal; the smallest unit is a millisecond", c)
		}
		if c < 0 {
			return neg(&ast.DurationLit{Text: duration(-c)}), ""
		}
		return &ast.DurationLit{Text: duration(c)}, ""
	}
	return nil, fmt.Sprintf("Go value %v of type %T has no literal", c, c)
}

// duration writes d, a positive whole number of milliseconds, the way
// Go's [time.Duration.String] does, without a fraction: hours, minutes,
// seconds and milliseconds, the largest first, each at most once. Days
// aren't used, so a day reads `24h`, as in Go.
func duration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	var b strings.Builder
	for _, u := range []struct {
		name string
		size time.Duration
	}{{"h", time.Hour}, {"m", time.Minute}, {"s", time.Second}, {"ms", time.Millisecond}} {
		if q := d / u.size; q > 0 {
			b.WriteString(strconv.FormatInt(int64(q), 10) + u.name)
			d -= q * u.size
		}
	}
	return b.String()
}

// noLiteral says why values of t can't be written as literals, or
// returns "" when they can.
func noLiteral(t types.Type) string {
	switch t := t.(type) {
	case types.Basic:
		if t == types.Timestamp {
			return "a timestamp comes from input only"
		}
	case *types.Optional:
		return "an optional value comes from input only; write its element type, or use build.Coalesce"
	case *types.Struct:
		return "a struct value comes from input only; take its fields with build.Field"
	case *types.List:
		return noLiteral(t.Elem)
	case *types.Map:
		if why := noLiteral(t.Key); why != "" {
			return why
		}
		return noLiteral(t.Value)
	}
	return ""
}

// neg is unary minus on x.
func neg(x ast.Expr) ast.Expr {
	return &ast.UnaryExpr{Op: ast.OpNeg, X: x}
}
