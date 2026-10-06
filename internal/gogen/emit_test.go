package gogen

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

func TestGoType(t *testing.T) {
	tier := &types.Enum{Name: "tier", Values: []string{"a"}}
	svc := &types.Struct{Name: "Service"}
	g := &generator{time: "gotime", exact: map[string]string{"tier": "tier", "Service": "Service"}}
	tests := []struct {
		typ  types.Type
		want string
	}{
		{typ: types.Bool, want: "bool"},
		{typ: types.Int, want: "int64"},
		{typ: types.Float, want: "float64"},
		{typ: types.String, want: "string"},
		{typ: types.Duration, want: "gotime.Duration"},
		{typ: types.Timestamp, want: "gotime.Time"},
		{typ: &types.List{Elem: &types.List{Elem: types.Int}}, want: "[][]int64"},
		{typ: &types.Map{Key: tier, Value: &types.List{Elem: svc}}, want: "map[tier][]Service"},
		{typ: &types.Optional{Elem: svc}, want: "*Service"},
		{typ: &types.Optional{Elem: tier}, want: "*tier"},
		{typ: types.Invalid, want: "any"},
	}
	for _, tt := range tests {
		t.Run(tt.typ.String(), func(t *testing.T) {
			if got := g.goType(tt.typ); got != tt.want {
				t.Errorf("goType(%s) = %q, want %q", tt.typ, got, tt.want)
			}
		})
	}
}

func TestTag(t *testing.T) {
	tests := []struct {
		name, def string
		want      string
	}{
		{name: "tier", want: "`policy:\"tier\"`"},
		{name: "bake", def: "1h", want: "`policy:\"bake,default=1h\"`"},
		{name: "note", def: `"hi"`, want: "`policy:\"note,default=\\\"hi\\\"\"`"},
		{name: "tags", def: `["a", "b"]`, want: "`policy:\"tags,default=[\\\"a\\\", \\\"b\\\"]\"`"},
		{name: "note", def: "\"`x`\"", want: `"policy:\"note,default=\\\"` + "`x`" + `\\\"\""`},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tag(tt.name, tt.def); got != tt.want {
				t.Errorf("tag(%q, %q) = %s, want %s", tt.name, tt.def, got, tt.want)
			}
		})
	}
}

func TestDoc(t *testing.T) {
	tests := []struct {
		name, text string
		want       string
	}{
		{name: "short", text: "A is a thing.", want: "// A is a thing.\n"},
		{
			name: "wrapped",
			text: strings.Repeat("word ", 20),
			want: "// " + strings.TrimSpace(strings.Repeat("word ", 14)) + "\n// " + strings.TrimSpace(strings.Repeat("word ", 6)) + "\n",
		},
		{name: "paragraphs and code", text: "A is:\n\n\tenum A: x | y", want: "// A is:\n//\n//\tenum A: x | y\n"},
		{name: "a word longer than a line", text: strings.Repeat("x", 90), want: "// " + strings.Repeat("x", 90) + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &generator{}
			g.doc(tt.text)
			if got := g.b.String(); got != tt.want {
				t.Errorf("doc(%q) wrote\n%s\nwant\n%s", tt.text, got, tt.want)
			}
		})
	}
}

func TestUsesTime(t *testing.T) {
	deny := &kind.Decision{Name: "deny", Reasons: []string{"no"}}
	tests := []struct {
		name string
		k    *kind.Kind
		want bool
	}{
		{name: "none", k: &kind.Kind{Decisions: []*kind.Decision{deny}, Inputs: []*kind.Input{{Name: "n", Type: types.Int}}}},
		{name: "struct field", k: &kind.Kind{Types: []*types.Struct{{Name: "S", Fields: []*types.Field{{Name: "t", Type: &types.List{Elem: types.Timestamp}}}}}}, want: true},
		{name: "input", k: &kind.Kind{Inputs: []*kind.Input{{Name: "d", Type: &types.Optional{Elem: types.Duration}}}}, want: true},
		{name: "map key", k: &kind.Kind{Inputs: []*kind.Input{{Name: "m", Type: &types.Map{Key: types.Timestamp, Value: types.Int}}}}, want: true},
		{name: "function result", k: &kind.Kind{Funcs: []*kind.Func{{Name: "now", Result: types.Timestamp}}}, want: true},
		{name: "function parameter", k: &kind.Kind{Funcs: []*kind.Func{{Name: "f", Params: []types.Type{types.String, types.Duration}, Result: types.Bool}}}, want: true},
		{name: "function without time", k: &kind.Kind{Funcs: []*kind.Func{{Name: "f", Params: []types.Type{types.String}, Result: types.Bool}}}},
		{name: "payload field", k: &kind.Kind{Decisions: []*kind.Decision{{Name: "a", Fields: []*kind.Field{{Name: "ttl", Type: types.Duration}}}}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (&generator{k: tt.k}).usesTime(); got != tt.want {
				t.Errorf("usesTime() = %v, want %v", got, tt.want)
			}
		})
	}
}
