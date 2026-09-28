package constant

import (
	"cmp"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/internal/types"
)

// Conforms reports whether v is a constant of type t, in the Go
// representation constants use: bool, int64, float64, string,
// time.Duration, time.Time, []any for lists, map[any]any for maps and
// nil for an absent optional.
func Conforms(v any, t types.Type) bool {
	switch t := t.(type) {
	case types.Basic:
		switch t {
		case types.Bool:
			_, ok := v.(bool)
			return ok
		case types.Int:
			_, ok := v.(int64)
			return ok
		case types.Float:
			_, ok := v.(float64)
			return ok
		case types.String:
			_, ok := v.(string)
			return ok
		case types.Duration:
			_, ok := v.(time.Duration)
			return ok
		case types.Timestamp:
			_, ok := v.(time.Time)
			return ok
		}
	case *types.List:
		xs, ok := v.([]any)
		if !ok {
			return false
		}
		for _, x := range xs {
			if !Conforms(x, t.Elem) {
				return false
			}
		}
		return true
	case *types.Map:
		m, ok := v.(map[any]any)
		if !ok {
			return false
		}
		for k, x := range m {
			if !Conforms(k, t.Key) || !Conforms(x, t.Value) {
				return false
			}
		}
		return true
	case *types.Optional:
		return v == nil || Conforms(v, t.Elem)
	}
	return false
}

// Format renders a constant as a Sigil literal, for signatures and
// messages.
func Format(v any) string {
	if v == nil {
		return "none"
	}
	switch v := v.(type) {
	case bool:
		return fmt.Sprint(v)
	case int64:
		return fmt.Sprint(v)
	case float64:
		s := fmt.Sprintf("%g", v)
		if !strings.ContainsAny(s, ".e") {
			s += ".0"
		}
		return s
	case string:
		return fmt.Sprintf("%q", v)
	case time.Duration:
		return FormatDuration(v)
	case time.Time:
		return fmt.Sprintf("%q", v.Format(time.RFC3339))
	case []any:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = Format(x)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[any]any:
		return formatMap(v)
	}
	return fmt.Sprintf("<%T>", v)
}

// FormatDuration renders d as a duration literal: the largest units
// first, each at most once, and `0s` for zero. Negative durations get a
// leading minus, which is the unary operator in source.
func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	var b strings.Builder
	if d < 0 {
		b.WriteByte('-')
		d = -d
	}
	units := []struct {
		name string
		size time.Duration
	}{{"d", 24 * time.Hour}, {"h", time.Hour}, {"m", time.Minute}, {"s", time.Second}, {"ms", time.Millisecond}}
	for _, u := range units {
		if n := d / u.size; n > 0 {
			fmt.Fprintf(&b, "%d%s", n, u.name)
			d -= n * u.size
		}
	}
	if d > 0 {
		// Sub-millisecond remainders have no literal; keep the value exact
		// with a millisecond fraction the lexer won't accept, so it's
		// visible rather than silently rounded.
		fmt.Fprintf(&b, "+%dns", d)
	}
	return b.String()
}

// Compare orders two constants of one ordered type with a literal, int64,
// float64 or time.Duration: negative when a is less than b, zero when
// they're equal, positive when a is greater. Anything else is equal.
func Compare(a, b any) int { //nolint:emptyinterface // constants are typed by their Sigil type; see Conforms
	switch a := a.(type) {
	case int64:
		return cmp.Compare(a, b.(int64))
	case float64:
		return cmp.Compare(a, b.(float64))
	case time.Duration:
		return cmp.Compare(a, b.(time.Duration))
	}
	return 0
}

// formatMap renders the entries sorted by their formatted key, so the
// output is stable.
func formatMap(m map[any]any) string { //nolint:emptyinterface // constants are typed by their Sigil type; see Conforms
	parts := make([]string, 0, len(m))
	for k, x := range m {
		parts = append(parts, Format(k)+": "+Format(x))
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ", ") + "}"
}

// Ordered returns a Go value of an ordered type in the representation
// Compare takes: int64, float64 or time.Duration. Anything else comes
// back unchanged.
func Ordered(v any) any { //nolint:emptyinterface // constants are typed by their Sigil type; see Conforms
	switch x := v.(type) {
	case time.Duration, int64, float64:
		return x
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int64:
		return rv.Int()
	case reflect.Float64:
		return rv.Float()
	}
	return v
}
