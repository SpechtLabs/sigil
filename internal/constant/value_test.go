package constant_test

import (
	"math"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/types"
)

func TestConforms(t *testing.T) {
	tests := []struct {
		v    any
		t    types.Type
		want bool
	}{
		{true, types.Bool, true},
		{int64(1), types.Int, true},
		{1, types.Int, false}, // constants are int64, never int
		{1.5, types.Float, true},
		{"a", types.String, true},
		{time.Hour, types.Duration, true},
		{int64(3600), types.Duration, false},
		{time.Now(), types.Timestamp, true},
		{"x", types.Decision, false},
		{nil, types.Invalid, false},
		{[]any{}, strList, true},
		{[]any{"a"}, strList, true},
		{[]any{int64(1)}, strList, false},
		{[]string{"a"}, strList, false},
		{[]any{[]any{"a"}}, &types.List{Elem: strList}, true},
		{map[any]any{}, strMap, true},
		{map[any]any{"a": "b"}, strMap, true},
		{map[any]any{"a": int64(1)}, strMap, false},
		{map[any]any{int64(1): "b"}, strMap, false},
		{map[any]any{int64(1): "b"}, &types.Map{Key: types.Int, Value: types.String}, true},
		{map[any]any{true: []any{"b"}}, &types.Map{Key: types.Bool, Value: strList}, true},
		{map[string]string{"a": "b"}, strMap, false},
		{nil, &types.Optional{Elem: types.String}, true},
		{"a", &types.Optional{Elem: types.String}, true},
		{int64(1), &types.Optional{Elem: types.String}, false},
		{"a", &types.Struct{Name: "S"}, false},
		{constant.EnumValue("critical"), tier, true},
		{constant.EnumValue("gold"), tier, false},
		{"critical", tier, false}, // a string is never an enum value
		{constant.EnumValue("critical"), types.String, false},
		{[]any{constant.EnumValue("internal")}, &types.List{Elem: tier}, true},
		{map[any]any{constant.EnumValue("standard"): int64(1)}, &types.Map{Key: tier, Value: types.Int}, true},
		{map[any]any{constant.EnumValue("gold"): int64(1)}, &types.Map{Key: tier, Value: types.Int}, false},
		{nil, &types.Optional{Elem: tier}, true},
	}
	for _, tt := range tests {
		t.Run(constant.Format(tt.v)+" as "+tt.t.String(), func(t *testing.T) {
			if got := constant.Conforms(tt.v, tt.t); got != tt.want {
				t.Errorf("Conforms(%v, %v) = %v, want %v", tt.v, tt.t, got, tt.want)
			}
		})
	}
}

func TestFormat(t *testing.T) {
	ts := time.Date(2026, 9, 27, 22, 30, 0, 0, time.UTC)
	tests := []struct {
		v    any
		want string
	}{
		{nil, "none"},
		{true, "true"},
		{int64(42), "42"},
		{int64(-3), "-3"},
		{0.5, "0.5"},
		{3.0, "3.0"},
		{1e21, "1000000000000000000000.0"},
		{"deployer", `"deployer"`},
		{"say \"hi\"\n", `"say \"hi\"\n"`},
		{time.Duration(0), "0s"},
		{time.Hour, "1h"},
		{90 * time.Minute, "1h30m"},
		{48 * time.Hour, "2d"},
		{26*time.Hour + 3*time.Minute + 4*time.Second + 5*time.Millisecond, "1d2h3m4s5ms"},
		{500 * time.Millisecond, "500ms"},
		{-15 * time.Minute, "-15m"},
		{1500 * time.Microsecond, "1ms+500000ns"},
		{time.Duration(math.MaxInt64), "106751d23h47m16s854ms+775807ns"},
		{time.Duration(math.MinInt64), "-106751d23h47m16s854ms+775808ns"},
		{ts, `"2026-09-27T22:30:00Z"`},
		{[]any{}, "[]"},
		{[]any{"a", int64(1)}, `["a", 1]`},
		{map[any]any{"b": int64(2), "a": "x"}, `{"a": "x", "b": 2}`},
		{map[any]any{int64(10): "x", int64(9): "y"}, `{10: "x", 9: "y"}`},
		{constant.EnumValue("critical"), "critical"},
		{[]any{constant.EnumValue("critical"), constant.EnumValue("internal")}, "[critical, internal]"},
		{map[any]any{constant.EnumValue("standard"): int64(2), constant.EnumValue("critical"): int64(1)}, "{critical: 1, standard: 2}"},
		{[]int{1}, "<[]int>"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := constant.Format(tt.v); got != tt.want {
				t.Errorf("Format(%v) = %q, want %q", tt.v, got, tt.want)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b any
		want int
	}{
		{int64(1), int64(2), -1},
		{2.5, 2.5, 0},
		{time.Hour, time.Minute, 1},
		{constant.EnumValue("critical"), constant.EnumValue("standard"), -1},
		{constant.EnumValue("standard"), constant.EnumValue("critical"), 1},
		{constant.EnumValue("internal"), constant.EnumValue("internal"), 0},
		{"b", "a", 0}, // strings aren't ordered constants
	}
	for _, tt := range tests {
		if got := constant.Compare(tt.a, tt.b); got != tt.want {
			t.Errorf("Compare(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
