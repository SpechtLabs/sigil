package types_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/types"
)

var (
	service = &types.Struct{Name: "Service", Fields: []*types.Field{
		{Name: "name", Type: types.String},
		{Name: "tier", Type: types.String},
		{Name: "owners", Type: &types.List{Elem: types.String}},
		{Name: "labels", Type: &types.Map{Key: types.String, Value: types.String}},
	}}
	service2 = &types.Struct{Name: "Service"} // same name, no fields: still identical
	actor    = &types.Struct{Name: "Actor"}
)

func TestString(t *testing.T) {
	tests := []struct {
		typ  types.Type
		want string
	}{
		{types.Bool, "bool"},
		{types.Int, "int"},
		{types.Float, "float"},
		{types.String, "string"},
		{types.Duration, "duration"},
		{types.Timestamp, "timestamp"},
		{types.Decision, "decision"},
		{types.Invalid, "invalid"},
		{types.Basic(99), "invalid"},
		{&types.List{Elem: types.String}, "list<string>"},
		{&types.List{Elem: &types.List{Elem: types.Int}}, "list<list<int>>"},
		{&types.Map{Key: types.String, Value: types.String}, "map<string, string>"},
		{&types.Map{Key: types.String, Value: &types.List{Elem: types.Int}}, "map<string, list<int>>"},
		{&types.Optional{Elem: types.String}, "?string"},
		{&types.Optional{Elem: service}, "?Service"},
		{service, "Service"},
		{&types.List{Elem: &types.Optional{Elem: service}}, "list<?Service>"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.typ.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIdentical(t *testing.T) {
	tests := []struct {
		name string
		a, b types.Type
		want bool
	}{
		{"same basic", types.Int, types.Int, true},
		{"int and float differ", types.Int, types.Float, false},
		{"decision is its own type", types.Decision, types.String, false},
		{"lists by element", &types.List{Elem: types.String}, &types.List{Elem: types.String}, true},
		{"lists differ by element", &types.List{Elem: types.String}, &types.List{Elem: types.Int}, false},
		{"list is not its element", &types.List{Elem: types.String}, types.String, false},
		{"maps by key and value", &types.Map{Key: types.String, Value: types.Int}, &types.Map{Key: types.String, Value: types.Int}, true},
		{"maps differ by value", &types.Map{Key: types.String, Value: types.Int}, &types.Map{Key: types.String, Value: types.String}, false},
		{"optional by element", &types.Optional{Elem: types.String}, &types.Optional{Elem: types.String}, true},
		{"optional is not its element", &types.Optional{Elem: types.String}, types.String, false},
		{"structs by name", service, service2, true},
		{"structs differ by name", service, actor, false},
		{"struct is not a list", service, &types.List{Elem: service}, false},
		{"nested", &types.Map{Key: types.String, Value: &types.List{Elem: &types.Optional{Elem: service}}},
			&types.Map{Key: types.String, Value: &types.List{Elem: &types.Optional{Elem: service2}}}, true},
		{"nil is nothing", nil, types.Int, false},
		{"both nil", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := types.Identical(tt.a, tt.b); got != tt.want {
				t.Errorf("Identical(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			if got := types.Identical(tt.b, tt.a); got != tt.want {
				t.Errorf("Identical(%v, %v) = %v, want %v (symmetry)", tt.b, tt.a, got, tt.want)
			}
		})
	}
}

func TestPredicates(t *testing.T) {
	tests := []struct {
		typ       types.Type
		reserved  bool // IsReserved(typ.String())
		ordered   bool
		equatable bool
		key       bool
	}{
		{types.Bool, true, false, true, true},
		{types.Int, true, true, true, true},
		{types.Float, true, true, true, true},
		{types.String, true, false, true, true},
		{types.Duration, true, true, true, true},
		{types.Timestamp, true, true, true, true},
		{types.Decision, false, false, true, false},
		{types.Invalid, false, false, false, false},
		{&types.List{Elem: types.Int}, false, false, false, false},
		{&types.Map{Key: types.String, Value: types.Int}, false, false, false, false},
		{&types.Optional{Elem: types.Int}, false, false, false, false},
		{service, false, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.typ.String(), func(t *testing.T) {
			if got := types.IsReserved(tt.typ.String()); got != tt.reserved {
				t.Errorf("IsReserved(%q) = %v, want %v", tt.typ.String(), got, tt.reserved)
			}
			if got := types.IsOrdered(tt.typ); got != tt.ordered {
				t.Errorf("IsOrdered(%v) = %v, want %v", tt.typ, got, tt.ordered)
			}
			if got := types.IsEquatable(tt.typ); got != tt.equatable {
				t.Errorf("IsEquatable(%v) = %v, want %v", tt.typ, got, tt.equatable)
			}
			if got := types.IsKey(tt.typ); got != tt.key {
				t.Errorf("IsKey(%v) = %v, want %v", tt.typ, got, tt.key)
			}
		})
	}
	for _, name := range []string{"list", "map"} {
		if !types.IsReserved(name) {
			t.Errorf("IsReserved(%q) = false, want true", name)
		}
	}
}
