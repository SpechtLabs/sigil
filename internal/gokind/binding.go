package gokind

import (
	"reflect"

	"github.com/spechtlabs/sigil/internal/types"
)

// Binding ties the kind back to the Go types it was built from.
type Binding struct {
	Input    reflect.Type
	Structs  map[string]reflect.Type  // Sigil struct name to Go type
	Payloads map[string]reflect.Type  // decision name to payload type
	Funcs    map[string]reflect.Value // function name to implementation
	Fields   map[string][]int         // "Struct.field" to the Go field index path
}

// TypeOf returns the Sigil type a Go type maps to under the binding,
// which is how a value bound to a param from Go is checked against the
// param's declaration. A struct type is the kind's struct it was built
// from; any other struct, or a type policies can't read, reports false.
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
