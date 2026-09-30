package kind_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// Generate valid contracts directly so mutations exercise export/import on
// every run, including nested types, defaults, the conflict outcome's
// constant payload and both resolution modes.
func FuzzKindRoundTrip(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5}, int64(42), "hello", false)
	f.Add([]byte{5, 4, 3, 2, 1}, int64(-9223372036854775808), "\x00\xff", true)
	f.Add([]byte{4, 3, 0, 3}, int64(2), "critical", false)
	f.Fuzz(func(t *testing.T, shape []byte, n int64, s string, all bool) {
		level := &types.Enum{Name: "Level", Values: []string{"low", "high", "critical"}}
		pick := constant.EnumValue(level.Values[uint64(n)%uint64(len(level.Values))])
		var typ types.Type = types.Int
		for _, b := range shape[:min(len(shape), 8)] {
			switch b % 5 {
			case 0:
				typ = &types.List{Elem: typ}
			case 1:
				typ = &types.Map{Key: types.String, Value: typ}
			case 2:
				typ = &types.Map{Key: types.Int, Value: typ}
			case 3:
				typ = &types.Map{Key: level, Value: typ}
			case 4:
				typ = &types.List{Elem: level} // restart: a bare enum can't be a map value
			}
		}
		st := &types.Struct{Name: "Record", Fields: []*types.Field{{Name: "values", Type: typ}, {Name: "label", Type: types.String}}}
		k := &kind.Kind{
			Name: "Generated", Version: 3, Accepts: 2, Collect: kind.CollectOne,
			Enums:  []*types.Enum{level},
			Types:  []*types.Struct{st},
			Inputs: []*kind.Input{{Name: "record", Type: &types.Optional{Elem: st}}},
			Funcs:  []*kind.Func{{Name: "lookup", Params: []types.Type{types.String, typ}, Result: types.Bool}},
			Decisions: []*kind.Decision{
				{Name: "deny", Reasons: []string{"fallback", "blocked"}, Ranked: []string{"blocked", "fallback"}},
				{Name: "allow", Reasons: []string{"ok"}, Fields: []*kind.Field{
					{Name: "count", Type: types.Int, HasDefault: true, Default: n},
					{Name: "text", Type: types.String, HasDefault: true, Default: s},
					{Name: "ttl", Type: types.Duration, HasDefault: true, Default: time.Duration(n / 1000000 * 1000000)},
					{Name: "tags", Type: &types.List{Elem: types.String}, HasDefault: true, Default: []any{s}},
					{Name: "level", Type: level, HasDefault: true, Default: pick},
					{Name: "levels", Type: &types.Map{Key: level, Value: &types.List{Elem: level}}, HasDefault: true, Default: map[any]any{pick: []any{pick}}},
					{Name: "floor", Type: &types.Optional{Elem: level}},
				}},
			},
			Precedence: []string{"deny", "allow"},
			Exclusive:  [][]kind.Outcome{{{Decision: "deny", Reason: "blocked"}, {Decision: "allow"}}},
			Default:    &kind.Default{Decision: "deny", Reason: "fallback", Args: map[string]any{}},
			Conflict:   &kind.Default{Decision: "allow", Reason: "ok", Args: map[string]any{"count": n, "text": s, "floor": pick}},
		}
		if all {
			k.Collect, k.Precedence, k.Default, k.Conflict = kind.CollectAll, nil, nil, nil
		}
		if errs := k.Validate(nil); errs != nil {
			t.Fatal(errs)
		}
		src := k.Source()
		got, errs := check.LoadKind("generated.sigil", []byte(src))
		if errs != nil {
			t.Fatalf("export did not load: %v\n%s", errs, src)
		}
		if !reflect.DeepEqual(k, got) {
			t.Fatalf("export/import changed contract:\n%s\nthen:\n%s", src, got.Source())
		}
	})
}
