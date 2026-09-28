package gokind

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/lexer"
	"github.com/spechtlabs/sigil/internal/types"
)

// DecodeInput decodes a document into a new value of the input struct.
// raw is what encoding/json or a YAML decoder produced: maps, slices,
// strings, bools and numbers. The rules are strict where a typo would
// otherwise go unnoticed: a key the kind doesn't declare is an error,
// while a missing one reads as its zero value, as it would from Go. A
// duration is a string in Sigil's syntax, `"1h30m"`, and a timestamp an
// RFC 3339 string. null is allowed for optionals, lists and maps only.
// The error names the path to the value that doesn't fit, like
// `release.soak`, with the fix as its advice.
func (b *Binding) DecodeInput(k *kind.Kind, raw any) (reflect.Value, humane.Error) { //nolint:emptyinterface // raw is a decoded JSON or YAML document
	v := reflect.New(b.Input).Elem()
	obj, ok := object(raw)
	if !ok {
		return v, decodeErr("", "the input must be an object with one key per input, found "+describe(raw), "the kind declares: "+strings.Join(inputNames(k), ", "))
	}
	for _, key := range sortedKeys(obj) {
		in := k.Input(key)
		if in == nil {
			return v, unknownKey(key, "", fmt.Sprintf("unknown input %q", key), inputNames(k))
		}
		if err := b.Decode(in.Type, obj[key], v.FieldByIndex(b.Fields["."+key]), key); err != nil {
			return v, err
		}
	}
	return v, nil
}

// Decode sets v, a Go value of Sigil type t under the binding, from raw.
// path names the value in errors.
func (b *Binding) Decode(t types.Type, raw any, v reflect.Value, path string) humane.Error { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	if raw == nil {
		switch t.(type) {
		case *types.Optional, *types.List, *types.Map:
			v.Set(reflect.Zero(v.Type()))
			return nil
		}
		return decodeErr(path, "null for "+article(t), "only optionals, lists and maps may be null; leave the key out to get the zero value")
	}
	switch t := t.(type) {
	case types.Basic:
		return scalar(t, raw, v, path)
	case *types.Optional:
		p := reflect.New(v.Type().Elem())
		if err := b.Decode(t.Elem, raw, p.Elem(), path); err != nil {
			return err
		}
		v.Set(p)
		return nil
	case *types.List:
		items, ok := raw.([]any)
		if !ok {
			return mismatch(path, t, raw)
		}
		s := reflect.MakeSlice(v.Type(), len(items), len(items))
		for i, item := range items {
			if err := b.Decode(t.Elem, item, s.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		v.Set(s)
		return nil
	case *types.Map:
		return b.decodeMap(t, raw, v, path)
	case *types.Struct:
		return b.decodeStruct(t, raw, v, path)
	}
	return decodeErr(path, "values of type "+t.String()+" can't be decoded", "")
}

// Canonical returns v, a Go value of Sigil type t, in the representation
// constants use (see constant.Format): int64, float64, string, bool,
// time.Duration, time.Time in UTC, []any, map[any]any, nil for an absent
// optional, and a struct as a map[any]any by field name. Two values of
// one Sigil type are equal when their canonical forms are deeply equal,
// whatever Go types they came from.
func (b *Binding) Canonical(t types.Type, v reflect.Value) any { //nolint:emptyinterface // canonical values are dynamically typed, like constants
	for v.IsValid() && v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil
	}
	switch t := t.(type) {
	case types.Basic:
		return canonicalScalar(t, v)
	case *types.Optional:
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return nil
			}
			v = v.Elem()
		}
		return b.Canonical(t.Elem, v)
	case *types.List:
		out := make([]any, v.Len())
		for i := range out {
			out[i] = b.Canonical(t.Elem, v.Index(i))
		}
		return out
	case *types.Map:
		out := make(map[any]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[b.Canonical(t.Key, iter.Key())] = b.Canonical(t.Value, iter.Value())
		}
		return out
	case *types.Struct:
		out := map[any]any{}
		for _, f := range t.Fields {
			if idx, ok := b.Fields[t.Name+"."+f.Name]; ok {
				out[f.Name] = b.Canonical(f.Type, v.FieldByIndex(idx))
			}
		}
		return out
	}
	return v.Interface()
}

// decodeStruct decodes an object into a struct, rejecting keys the type
// doesn't declare.
func (b *Binding) decodeStruct(t *types.Struct, raw any, v reflect.Value, path string) humane.Error { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	obj, ok := object(raw)
	if !ok {
		return mismatch(path, t, raw)
	}
	names := make([]string, len(t.Fields))
	for i, f := range t.Fields {
		names[i] = f.Name
	}
	for _, key := range sortedKeys(obj) {
		var field *types.Field
		for _, f := range t.Fields {
			if f.Name == key {
				field = f
			}
		}
		if field == nil {
			return unknownKey(key, path, fmt.Sprintf("unknown field %q on type %s", key, t.Name), names)
		}
		idx, ok := b.Fields[t.Name+"."+key]
		if !ok {
			return decodeErr(join(path, key), "field isn't bound to Go", "")
		}
		if err := b.Decode(field.Type, obj[key], v.FieldByIndex(idx), join(path, key)); err != nil {
			return err
		}
	}
	return nil
}

// decodeMap decodes an object into a map. Keys are always strings in the
// document, so a non-string key type parses them the way its values are
// written: `"3"` for an int key, `"45m"` for a duration key.
func (b *Binding) decodeMap(t *types.Map, raw any, v reflect.Value, path string) humane.Error { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	obj, ok := object(raw)
	if !ok {
		return mismatch(path, t, raw)
	}
	m := reflect.MakeMapWithSize(v.Type(), len(obj))
	for _, key := range sortedKeys(obj) {
		at := fmt.Sprintf("%s[%q]", path, key)
		k := reflect.New(v.Type().Key()).Elem()
		if err := scalar(t.Key.(types.Basic), keyValue(t.Key, key), k, at); err != nil {
			return err
		}
		val := reflect.New(v.Type().Elem()).Elem()
		if err := b.Decode(t.Value, obj[key], val, at); err != nil {
			return err
		}
		m.SetMapIndex(k, val)
	}
	v.Set(m)
	return nil
}

// keyValue turns a map key, a string in the document, into the raw value
// scalar expects for the key type.
func keyValue(t types.Type, key string) any { //nolint:emptyinterface // raw values are decoded JSON or YAML
	switch t {
	case types.Int, types.Float:
		return json.Number(key)
	case types.Bool:
		if b, err := strconv.ParseBool(key); err == nil {
			return b
		}
	}
	return key
}

// scalar decodes a scalar.
func scalar(t types.Basic, raw any, v reflect.Value, path string) humane.Error { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	switch t {
	case types.Bool:
		b, ok := raw.(bool)
		if !ok {
			return mismatch(path, t, raw)
		}
		v.SetBool(b)
	case types.String:
		s, ok := raw.(string)
		if !ok {
			return mismatch(path, t, raw)
		}
		v.SetString(s)
	case types.Int:
		n, ok := integer(raw)
		if !ok || !setInt(v, n) {
			return mismatch(path, t, raw)
		}
	case types.Float:
		f, ok := float(raw)
		if !ok {
			return mismatch(path, t, raw)
		}
		v.SetFloat(f)
	case types.Duration:
		s, ok := raw.(string)
		if !ok {
			return decodeErr(path, "expected a duration, found "+describe(raw), `write durations as strings in Sigil's syntax, like "45m" or "1h30m"`)
		}
		d, err := lexer.ParseDuration(s)
		if err != nil || s == "" {
			return decodeErr(path, fmt.Sprintf("invalid duration %q", s), "units are d, h, m, s and ms, largest first, each at most once: \"1h30m\"")
		}
		v.SetInt(int64(d))
	case types.Timestamp:
		ts, ok := timestamp(raw)
		if !ok {
			return decodeErr(path, "expected a timestamp, found "+describe(raw), `write timestamps in RFC 3339, like "2026-09-28T14:00:00Z"`)
		}
		v.Set(reflect.ValueOf(ts))
	default:
		return decodeErr(path, "values of type "+t.String()+" can't be decoded", "")
	}
	return nil
}

// setInt sets an integer, reporting false when it doesn't fit v's Go type.
func setInt(v reflect.Value, n int64) bool {
	if v.OverflowInt(n) {
		return false
	}
	v.SetInt(n)
	return true
}

// integer reads a whole number from any of the number types JSON and YAML
// decoders produce.
func integer(raw any) (int64, bool) { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	switch n := raw.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case uint64:
		if n > math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case float64:
		if n != math.Trunc(n) || n < math.MinInt64 || n >= math.MaxInt64 {
			return 0, false
		}
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

func float(raw any) (float64, bool) { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	switch n := raw.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func timestamp(raw any) (time.Time, bool) { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	switch ts := raw.(type) {
	case time.Time:
		return ts, true
	case string:
		t, err := time.Parse(time.RFC3339Nano, ts)
		return t, err == nil
	}
	return time.Time{}, false
}

func canonicalScalar(t types.Basic, v reflect.Value) any { //nolint:emptyinterface // canonical values are dynamically typed, like constants
	switch t {
	case types.Bool:
		return v.Bool()
	case types.Int:
		if v.CanUint() {
			return int64(v.Uint()) //nolint:gosec // Sigil ints are int64; a host's uint field is out of contract anyway
		}
		return v.Int()
	case types.Float:
		return v.Float()
	case types.String, types.Decision:
		return v.String()
	case types.Duration:
		return time.Duration(v.Int())
	case types.Timestamp:
		if ts, ok := reflect.TypeAssert[time.Time](v); ok {
			return ts.UTC().Round(0)
		}
	}
	return v.Interface()
}

// object returns raw as an object with string keys. YAML decoders may
// produce map[any]any.
func object(raw any) (map[string]any, bool) { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	switch m := raw.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, x := range m {
			out[fmt.Sprint(k)] = x
		}
		return out, true
	}
	return nil, false
}

func sortedKeys(m map[string]any) []string { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func unknownKey(key, path, msg string, declared []string) humane.Error {
	help := "declared: " + strings.Join(declared, ", ")
	if near, ok := check.Nearest(key, declared); ok {
		help = fmt.Sprintf("did you mean %q? %s", near, help)
	}
	if len(declared) == 0 {
		help = "none are declared"
	}
	return decodeErr(join(path, key), msg, help)
}

func mismatch(path string, t types.Type, raw any) humane.Error { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	return decodeErr(path, fmt.Sprintf("expected %s, found %s", article(t), describe(raw)), "")
}

// describe names a raw value's JSON type for a message.
func describe(raw any) string { //nolint:emptyinterface // raw is a decoded JSON or YAML value
	switch raw.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case string:
		return "a string"
	case int, int64, uint64, float64, json.Number:
		return "a number"
	case []any:
		return "a list"
	case map[string]any, map[any]any:
		return "an object"
	case time.Time:
		return "a timestamp"
	}
	return fmt.Sprintf("a %T", raw)
}

func article(t types.Type) string {
	s := t.String()
	if strings.ContainsRune("aeiou", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}

// decodeErr describes a value that doesn't fit, with its path.
func decodeErr(path, msg, help string) humane.Error {
	if path != "" {
		msg = path + ": " + msg
	}
	if help == "" {
		help = "give the value the type the kind declares for it"
	}
	return humane.New(msg, help)
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func inputNames(k *kind.Kind) []string {
	names := make([]string, len(k.Inputs))
	for i, in := range k.Inputs {
		names[i] = in.Name
	}
	return names
}
