package gokind

import (
	"reflect"

	"github.com/spechtlabs/sigil/internal/types"
)

// Binding ties the kind back to the Go types it was built from, or that
// [Synthesize] built for it. The evaluator reads a field with
// [reflect.Value.FieldByIndex] and the index path recorded here, so it
// never looks a field up by name at run time.
//
// Fields keys a path by where the field sits:
//
//	.tier                  the input `tier`
//	Service.owners         the field `owners` of struct type Service
//	decision review.bake   the payload field `bake` of decision review
//
// A Binding is filled once, by [Build] or [Synthesize], and only read
// afterwards.
type Binding struct {
	Input    reflect.Type             // the input struct
	Structs  map[string]reflect.Type  // Sigil struct name to Go type
	Payloads map[string]reflect.Type  // decision name to payload type
	Funcs    map[string]reflect.Value // function name to implementation
	Fields   map[string][]int         // field path, as above, to the Go field index path
	// RecoverHostPanics makes a panic in a host function a runtime error,
	// as [Options.RecoverHostPanics] asks.
	RecoverHostPanics bool
}

// TypeOf returns the Sigil type a Go type maps to under the binding,
// which is how a value bound to a param from Go is checked against the
// param's declaration. A struct type is the kind's struct it was built
// from; any other struct, or a type policies can't read, reports false.
// The struct type it returns carries only the name, which is all
// [types.Identical] compares.
func (b *Binding) TypeOf(t reflect.Type) (types.Type, bool) { //nolint:returninterface // a type is any of five kinds
	if t == nil {
		return nil, false
	}
	if t == durationType {
		return types.Duration, true
	}
	if t == timeType {
		return types.Timestamp, true
	}
	switch t.Kind() {
	case reflect.Bool:
		return types.Bool, true
	case reflect.Int, reflect.Int64:
		return types.Int, true
	case reflect.Float64:
		return types.Float, true
	case reflect.String:
		return types.String, true
	case reflect.Slice:
		elem, ok := b.TypeOf(t.Elem())
		return &types.List{Elem: elem}, ok
	case reflect.Map:
		key, ok := b.TypeOf(t.Key())
		val, ok2 := b.TypeOf(t.Elem())
		return &types.Map{Key: key, Value: val}, ok && ok2
	case reflect.Pointer:
		elem, ok := b.TypeOf(t.Elem())
		return &types.Optional{Elem: elem}, ok
	case reflect.Struct:
		for name, st := range b.Structs {
			if st == t {
				return &types.Struct{Name: name}, true
			}
		}
	}
	return nil, false
}
