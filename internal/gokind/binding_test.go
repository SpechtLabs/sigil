package gokind_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/types"
)

// TestBindingTypeOf checks the Go-to-Sigil mapping a host's param values
// are checked with.
func TestBindingTypeOf(t *testing.T) {
	_, b, errs := gokind.Build(deploy())
	if errs != nil {
		t.Fatal(errs)
	}
	type other struct{ X int }
	tests := []struct {
		name string
		t    reflect.Type
		want types.Type // nil when the type isn't one policies read
	}{
		{name: "nil", t: nil},
		{name: "bool", t: typeOf[bool](), want: types.Bool},
		{name: "int", t: typeOf[int](), want: types.Int},
		{name: "int64", t: typeOf[int64](), want: types.Int},
		{name: "float64", t: typeOf[float64](), want: types.Float},
		{name: "string", t: typeOf[string](), want: types.String},
		{name: "duration", t: typeOf[time.Duration](), want: types.Duration},
		{name: "timestamp", t: typeOf[time.Time](), want: types.Timestamp},
		{name: "slice", t: typeOf[[]string](), want: &types.List{Elem: types.String}},
		{name: "map", t: typeOf[map[string]int](), want: &types.Map{Key: types.String, Value: types.Int}},
		{name: "pointer", t: typeOf[*string](), want: &types.Optional{Elem: types.String}},
		{name: "nested", t: typeOf[map[string][]*time.Duration](),
			want: &types.Map{Key: types.String, Value: &types.List{Elem: &types.Optional{Elem: types.Duration}}}},
		{name: "the kind's struct", t: typeOf[Service](), want: &types.Struct{Name: "Service"}},
		{name: "a struct the kind doesn't declare", t: typeOf[other]()},
		{name: "int32", t: typeOf[int32]()},
		{name: "slice of an unsupported type", t: typeOf[[]int32]()},
		{name: "map with an unsupported key", t: typeOf[map[int32]string]()},
		{name: "map with an unsupported value", t: typeOf[map[string]int32]()},
		{name: "pointer to an unsupported type", t: typeOf[*int32]()},
		{name: "func", t: typeOf[func()]()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := b.TypeOf(tt.t)
			if ok != (tt.want != nil) {
				t.Fatalf("TypeOf(%v) = %v, %v; want ok=%v", tt.t, got, ok, tt.want != nil)
			}
			if ok && !types.Identical(got, tt.want) {
				t.Errorf("TypeOf(%v) = %v, want %v", tt.t, got, tt.want)
			}
		})
	}
}
