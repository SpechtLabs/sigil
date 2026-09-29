package gokind

import (
	"reflect"
	"strconv"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// ErrUnbound is the error a synthesized host function returns: the
// kind file carries the function's signature, but nothing implements it.
type ErrUnbound struct {
	Name string // the host function's name
}

// synth is one Synthesize run.
type synth struct {
	binding *Binding
	kind    *kind.Kind
}

// Synthesize builds a binding for a kind that has no Go types behind it,
// such as one loaded from a kind file. Every struct type, the input and
// every payload become a Go struct built with [reflect.StructOf], so the
// evaluator reads them exactly like a host's own structs. Every host
// function is bound to one that fails with [*ErrUnbound] when it's called,
// so a policy that never reaches a call still evaluates. `int` becomes
// int64, `float` float64, and a decision value a string.
//
// The kind must be valid; [kind.Kind.Validate] rejects the recursive types
// StructOf couldn't build, and Synthesize doesn't check again.
func Synthesize(k *kind.Kind) *Binding {
	s := &synth{
		binding: &Binding{
			Structs:  map[string]reflect.Type{},
			Payloads: map[string]reflect.Type{},
			Funcs:    map[string]reflect.Value{},
			Fields:   map[string][]int{},
		},
		kind: k,
	}
	for _, t := range k.Types {
		s.structType(t)
	}
	inputs := make([]*types.Field, len(k.Inputs))
	for i, in := range k.Inputs {
		inputs[i] = &types.Field{Name: in.Name, Type: in.Type}
	}
	s.binding.Input = s.fields(inputs, ".")
	for _, d := range k.Decisions {
		fields := make([]*types.Field, len(d.Fields))
		for i, f := range d.Fields {
			fields[i] = &types.Field{Name: f.Name, Type: f.Type}
		}
		s.binding.Payloads[d.Name] = s.fields(fields, "decision "+d.Name+".")
	}
	for _, f := range k.Funcs {
		s.binding.Funcs[f.Name] = s.unbound(f)
	}
	return s.binding
}

// Error implements the error interface. It says the function has no
// implementation in this binary and how to build one that has.
func (e *ErrUnbound) Error() string {
	return "no implementation in this sigil binary; build a host binary with " + e.Name + " linked in (see sigil's pkg/cli)"
}

// structType returns the Go type of a kind's struct type, building it on
// first use, so a type used before its declaration still resolves.
func (s *synth) structType(t *types.Struct) reflect.Type {
	if gt, ok := s.binding.Structs[t.Name]; ok {
		return gt
	}
	decl := s.kind.Type(t.Name)
	if decl == nil {
		decl = t
	}
	gt := s.fields(decl.Fields, decl.Name+".")
	s.binding.Structs[t.Name] = gt
	return gt
}

// fields builds a struct with one exported Go field per Sigil field and
// records each field's index path under prefix+name. The Go names are
// positional, since Sigil names needn't be exported Go identifiers; the
// `policy` tag keeps the Sigil name for reading back.
func (s *synth) fields(fields []*types.Field, prefix string) reflect.Type {
	sf := make([]reflect.StructField, len(fields))
	for i, f := range fields {
		sf[i] = reflect.StructField{
			Name: "F" + strconv.Itoa(i),
			Type: s.goType(f.Type),
			Tag:  reflect.StructTag(`policy:"` + f.Name + `"`),
		}
		s.binding.Fields[prefix+f.Name] = []int{i}
	}
	return reflect.StructOf(sf)
}

// goType maps a Sigil type to the Go type NewKind would have read it
// from, using the canonical choice where several map to one: int64 for
// `int`, float64 for `float`.
func (s *synth) goType(t types.Type) reflect.Type {
	switch t := t.(type) {
	case types.Basic:
		switch t {
		case types.Bool:
			return reflect.TypeFor[bool]()
		case types.Int:
			return reflect.TypeFor[int64]()
		case types.Float:
			return reflect.TypeFor[float64]()
		case types.String, types.Decision:
			return reflect.TypeFor[string]()
		case types.Duration:
			return durationType
		case types.Timestamp:
			return timeType
		}
	case *types.List:
		return reflect.SliceOf(s.goType(t.Elem))
	case *types.Map:
		return reflect.MapOf(s.goType(t.Key), s.goType(t.Value))
	case *types.Optional:
		return reflect.PointerTo(s.goType(t.Elem))
	case *types.Struct:
		return s.structType(t)
	}
	return reflect.TypeFor[any]()
}

// unbound builds a function of f's signature that fails when called.
func (s *synth) unbound(f *kind.Func) reflect.Value {
	in := make([]reflect.Type, len(f.Params))
	for i, p := range f.Params {
		in[i] = s.goType(p)
	}
	result := s.goType(f.Result)
	ft := reflect.FuncOf(in, []reflect.Type{result, errorType}, false)
	err := reflect.ValueOf(error(&ErrUnbound{Name: f.Name}))
	return reflect.MakeFunc(ft, func([]reflect.Value) []reflect.Value {
		return []reflect.Value{reflect.Zero(result), err}
	})
}
