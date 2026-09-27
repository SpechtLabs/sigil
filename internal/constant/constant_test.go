package constant_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
)

var (
	strList = &types.List{Elem: types.String}
	intList = &types.List{Elem: types.Int}
	strMap  = &types.Map{Key: types.String, Value: types.String}
	intMap  = &types.Map{Key: types.Int, Value: strList}
	service = &types.Struct{Name: "Service"}
)

func TestEval(t *testing.T) {
	tests := []struct {
		src  string
		want types.Type
		val  any
		msg  string
		help string
		span string
	}{
		// Literals.
		{src: "true", want: types.Bool, val: true},
		{src: "42", want: types.Int, val: int64(42)},
		{src: "0.5", want: types.Float, val: 0.5},
		{src: `"a"`, want: types.String, val: "a"},
		{src: "`a\\n`", want: types.String, val: `a\n`},
		{src: "1h30m", want: types.Duration, val: 90 * time.Minute},
		{src: "(42)", want: types.Int, val: int64(42)},
		{src: "((\"a\"))", want: types.String, val: "a"},

		// Arithmetic.
		{src: "-3", want: types.Int, val: int64(-3)},
		{src: "-(3)", want: types.Int, val: int64(-3)},
		{src: "- -3", want: types.Int, val: int64(3)},
		{src: "-0.5", want: types.Float, val: -0.5},
		{src: "-15m", want: types.Duration, val: -15 * time.Minute},
		{src: "1 + 2", want: types.Int, val: int64(3)},
		{src: "1 - 2 - 3", want: types.Int, val: int64(-4)},
		{src: "1h + 30m", want: types.Duration, val: 90 * time.Minute},
		{src: "24h - 1h30m", want: types.Duration, val: 22*time.Hour + 30*time.Minute},
		{src: "0.5 + 0.25", want: types.Float, val: 0.75},
		{src: "-(1 + 2)", want: types.Int, val: int64(-3)},
		{src: "9223372036854775807 - 1", want: types.Int, val: int64(math.MaxInt64 - 1)},
		{src: "-9223372036854775807 - 1", want: types.Int, val: int64(math.MinInt64)},
		{src: "-1 - 9223372036854775807", want: types.Int, val: int64(math.MinInt64)},

		// Lists and maps, including empty ones typed by context.
		{src: "[]", want: strList, val: []any{}},
		{src: `["a", "b"]`, want: strList, val: []any{"a", "b"}},
		{src: "[1, -2, 1 + 2]", want: intList, val: []any{int64(1), int64(-2), int64(3)}},
		{src: `[["a"], []]`, want: &types.List{Elem: strList}, val: []any{[]any{"a"}, []any{}}},
		{src: "{}", want: strMap, val: map[any]any{}},
		{src: `{"a": "b"}`, want: strMap, val: map[any]any{"a": "b"}},
		{src: `{1: ["x"], 2: []}`, want: intMap, val: map[any]any{int64(1): []any{"x"}, int64(2): []any{}}},
		{src: "([])", want: strList, val: []any{}},

		// Optionals take their element's constants.
		{src: `"a"`, want: &types.Optional{Elem: types.String}, val: "a"},
		{src: "[]", want: &types.Optional{Elem: strList}, val: []any{}},

		// Type mismatches.
		{src: "1", want: types.String, msg: "expected string, found int", span: "1:1-1:2"},
		{src: `"1h"`, want: types.Duration, msg: "expected duration, found string", span: "1:1-1:5"},
		{src: "30", want: types.Duration, msg: "expected duration, found int", span: "1:1-1:3"},
		{src: "3.0", want: types.Int, msg: "expected int, found float", span: "1:1-1:4"},
		{src: "3", want: types.Float, msg: "expected float, found int", span: "1:1-1:2"},
		{src: "true", want: types.Int, msg: "expected int, found bool", span: "1:1-1:5"},
		{src: "[1]", want: types.Int, msg: "expected int, found a list", span: "1:1-1:4"},
		{src: "{}", want: types.Int, msg: "expected int, found a map", span: "1:1-1:3"},
		{src: "1", want: strList, msg: "expected list<string>, found int", span: "1:1-1:2"},
		{src: "{}", want: strList, msg: "expected list<string>, found a map", span: "1:1-1:3"},
		{src: "[]", want: strMap, msg: "expected map<string, string>, found a list", span: "1:1-1:3"},
		{src: `["a", 1]`, want: strList, msg: "expected string, found int", span: "1:7-1:8"},
		{src: `{"a": 1}`, want: strMap, msg: "expected string, found int", span: "1:7-1:8"},
		{src: `{1: "a"}`, want: strMap, msg: "expected string, found int", span: "1:2-1:3"},
		{src: `{"a": "b", "a": "c"}`, want: strMap, msg: `duplicate key "a" in map constant`, span: "1:12-1:15"},
		{src: "-\"a\"", want: types.String, msg: "expected string, found a negated value", span: "1:1-1:5"},
		{src: "-true", want: types.Bool, msg: "expected bool, found a negated value", span: "1:1-1:6"},
		{src: `"a" + "b"`, want: types.String, msg: "expected string, found `+` arithmetic", span: "1:1-1:10"},
		{src: "1 + 2", want: types.Bool, msg: "expected bool, found `+` arithmetic", span: "1:1-1:6"},
		{src: "1 + 1h", want: types.Int, msg: "expected int, found duration", span: "1:5-1:7"},
		{src: "1h + 1", want: types.Duration, msg: "expected duration, found int", span: "1:6-1:7"},

		// Types without literals.
		{src: `"2026-09-27"`, want: types.Timestamp, msg: "expected timestamp, found string", help: "timestamps come from input; there's no literal for one", span: "1:1-1:13"},
		{src: "approve", want: types.Decision, msg: "expected decision, found `approve`", help: "decision values only come from `outcome`", span: "1:1-1:8"},
		{src: "x", want: service, msg: "Service has no literal form, so a constant can't be one", span: "1:1-1:2"},
		{src: "1", want: types.Invalid, msg: "invalid type", span: "1:1-1:2"},

		// Not constants.
		{src: "min_soak", want: types.Duration, msg: "`min_soak` isn't a constant", help: "a constant is a literal, a list or map of literals, or `+` and `-` applied to those", span: "1:1-1:9"},
		{src: "a.b", want: types.String, msg: "`a.b` isn't a constant", span: "1:1-1:4"},
		{src: "f(1)", want: types.Int, msg: "`f(1)` isn't a constant", span: "1:1-1:5"},
		{src: "not true", want: types.Bool, msg: "`not` isn't a constant", span: "1:1-1:9"},
		{src: "1 == 1", want: types.Bool, msg: "`==` isn't a constant", span: "1:1-1:7"},
		{src: "a ?? 1", want: types.Int, msg: "`??` isn't a constant", span: "1:1-1:7"},
		{src: "[x]", want: strList, msg: "`x` isn't a constant", span: "1:2-1:3"},
		{src: "x", want: strList, msg: "`x` isn't a constant", span: "1:1-1:2"},
		{src: "x", want: strMap, msg: "`x` isn't a constant", span: "1:1-1:2"},
		{src: "1 + x", want: types.Int, msg: "`x` isn't a constant", span: "1:5-1:6"},

		// Overflow.
		{src: "9223372036854775807 + 1", want: types.Int, msg: "integer overflow in constant", span: "1:1-1:24"},
		{src: "-9223372036854775807 - 2", want: types.Int, msg: "integer overflow in constant", span: "1:1-1:25"},
		{src: "-(-9223372036854775807 - 1)", want: types.Int, msg: "integer overflow in constant", span: "1:1-1:28"},
		{src: "106751d + 24h", want: types.Duration, msg: "duration overflow in constant", span: "1:1-1:14"},
		{src: "-106751d - 106751d", want: types.Duration, msg: "duration overflow in constant", span: "1:1-1:19"},
	}

	for _, tt := range tests {
		t.Run(tt.src+" as "+tt.want.String(), func(t *testing.T) {
			x, errs := parser.ParseExpr("test.sigil", []byte(tt.src))
			if errs != nil {
				t.Fatalf("parse: %v", errs)
			}
			got, err := constant.Eval(x, tt.want)
			if tt.msg == "" {
				if err != nil {
					t.Fatalf("Eval() error: %v", err)
				}
				if !reflect.DeepEqual(got, tt.val) {
					t.Errorf("Eval() = %#v, want %#v", got, tt.val)
				}
				return
			}
			if err == nil {
				t.Fatalf("Eval() = %#v, want error %q", got, tt.msg)
			}
			if err.Msg != tt.msg {
				t.Errorf("Msg  = %q\nwant   %q", err.Msg, tt.msg)
			}
			if tt.help != "" && err.Help != tt.help {
				t.Errorf("Help = %q\nwant   %q", err.Help, tt.help)
			}
			if got := err.Pos.String() + "-" + err.End.String(); got != tt.span {
				t.Errorf("span = %s, want %s", got, tt.span)
			}
		})
	}
}
