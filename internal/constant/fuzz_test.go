package constant_test

import (
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
)

func FuzzAddInt(f *testing.F) {
	for _, pair := range [][2]int64{{0, 0}, {math.MaxInt64, 1}, {math.MinInt64, -1}, {math.MinInt64, math.MinInt64}} {
		f.Add(pair[0], pair[1], false)
		f.Add(pair[0], pair[1], true)
	}
	f.Fuzz(func(t *testing.T, a, b int64, sub bool) {
		want := new(big.Int)
		if sub {
			want.Sub(big.NewInt(a), big.NewInt(b))
		} else {
			want.Add(big.NewInt(a), big.NewInt(b))
		}
		got, ok := constant.AddInt(a, b, sub)
		if ok != want.IsInt64() || ok && got != want.Int64() {
			t.Fatalf("AddInt(%d, %d, %v) = %d, %v; want %s", a, b, sub, got, ok, want)
		}
	})
}

func FuzzConstantRoundTrip(f *testing.F) {
	f.Add(int64(0), 0.0, "", false)
	f.Add(int64(math.MaxInt64), math.MaxFloat64, "\x00\xff世界", true)
	f.Add(int64(math.MinInt64), math.SmallestNonzeroFloat64, "\n\"\\", false)
	f.Add(int64(1), 1.5, "critical", true)
	f.Add(int64(2), -1.5, "internal", false)
	f.Add(int64(0), 2.5, "Tier.critical", true)
	f.Fuzz(func(t *testing.T, n int64, number float64, s string, b bool) {
		value := tier.Values[uint64(n)%uint64(len(tier.Values))]
		values := []struct {
			value any
			typ   types.Type
		}{
			{n, types.Int}, {s, types.String}, {b, types.Bool},
			// The language's smallest duration unit is one millisecond.
			{time.Duration(n / int64(time.Millisecond) * int64(time.Millisecond)), types.Duration},
			{[]any{n}, &types.List{Elem: types.Int}},
			{map[any]any{s: n}, &types.Map{Key: types.String, Value: types.Int}},
			{constant.EnumValue(value), tier},
			{map[any]any{constant.EnumValue(value): []any{constant.EnumValue(value)}}, &types.Map{Key: tier, Value: &types.List{Elem: tier}}},
		}
		if !math.IsNaN(number) && !math.IsInf(number, 0) {
			values = append(values, struct {
				value any
				typ   types.Type
			}{number, types.Float})
		}
		for _, v := range values {
			src := constant.Format(v.value)
			x, errs := parser.ParseExpr("constant.sigil", []byte(src))
			if errs != nil {
				t.Fatalf("formatted constant %q does not parse: %v", src, errs)
			}
			got, err := constant.Eval(x, v.typ)
			if err != nil || !reflect.DeepEqual(got, v.value) {
				t.Fatalf("constant %q round trip = %v, %v; want %v", src, got, err, v.value)
			}
		}
		// The qualified spelling of an enum value evaluates to the same value.
		src := tier.Name + "." + value
		x, errs := parser.ParseExpr("constant.sigil", []byte(src))
		if errs != nil {
			t.Fatalf("qualified value %q does not parse: %v", src, errs)
		}
		if got, err := constant.Eval(x, tier); err != nil || got != constant.EnumValue(value) {
			t.Fatalf("qualified value %q = %v, %v; want %s", src, got, err, value)
		}
	})
}
