// Package gokind builds a kind from Go types by reflection: the input
// struct becomes the inputs and struct types, payload structs become
// decision fields, and Go functions become host function signatures. It
// is the machinery behind policy.NewKind and produces the same kind.Kind
// a kind file loads into, so both pass the same validation.
//
// Reflection happens here once; the Binding it returns is what the
// evaluator will use to read inputs without touching reflect at run time.
package gokind

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
)

// Options describes the kind to build.
type Options struct {
	Input     reflect.Type // the input struct
	Default   *Default     // nil for none
	Name      string
	Decisions []Decision // precedence order, or declaration order when Collect is set
	Funcs     []Func
	Version   int
	// Ranked and Collect record which of WithDecisions and WithCollect
	// added the decisions; both is an error.
	Ranked  bool
	Collect bool
}

// Decision is one decision and its payload struct. None is spelled as an
// empty struct.
type Decision struct {
	Payload reflect.Type
	Name    string
}

// Func is a host function: its name and the Go function.
type Func struct {
	Fn   any
	Name string
}

// Default is the default decision and its reason. Its payload comes from
// the payload fields' defaults.
type Default struct {
	Decision string
	Reason   string
}

// Binding ties the kind back to the Go types it was built from.
type Binding struct {
	Input    reflect.Type
	Structs  map[string]reflect.Type  // Sigil struct name to Go type
	Payloads map[string]reflect.Type  // decision name to payload type
	Funcs    map[string]reflect.Value // function name to implementation
	Fields   map[string][]int         // "Struct.field" to the Go field index path
}

// Build reflects over o and returns the kind with its binding, or the
// problems found. Every problem is reported, not just the first, so a
// host fixes its types in one round.
func Build(o Options) (*kind.Kind, *Binding, diag.ErrorList) {
	b := &builder{
		kind: &kind.Kind{Name: o.Name, Version: o.Version, Collect: collect(o)},
		binding: &Binding{
			Input:    o.Input,
			Structs:  map[string]reflect.Type{},
			Payloads: map[string]reflect.Type{},
			Funcs:    map[string]reflect.Value{},
			Fields:   map[string][]int{},
		},
		structs: map[reflect.Type]*types.Struct{},
	}
	if o.Ranked && o.Collect {
		b.errorf("a kind ranks its decisions with WithDecisions, or applies them all with WithCollect; use one",
			"kind %s mixes WithDecisions and WithCollect", o.Name)
	}
	b.inputs(o.Input)
	for _, f := range o.Funcs {
		b.fn(f)
	}
	for _, d := range o.Decisions {
		b.decision(d)
		if !o.Collect {
			b.kind.Precedence = append(b.kind.Precedence, d.Name)
		}
	}
	if o.Default != nil {
		b.kind.Default = &kind.Default{Decision: o.Default.Decision, Reason: o.Default.Reason, Args: map[string]any{}}
	}
	b.errs = append(b.errs, b.kind.Validate(nil)...)
	if len(b.errs) > 0 {
		return nil, nil, b.errs
	}
	return b.kind, b.binding, nil
}

type builder struct {
	kind    *kind.Kind
	binding *Binding
	structs map[reflect.Type]*types.Struct // Go struct types already converted
	errs    diag.ErrorList
}

func (b *builder) errorf(help, format string, args ...any) {
	b.errs = append(b.errs, &diag.Error{Msg: fmt.Sprintf(format, args...), Help: help})
}

var (
	durationType = reflect.TypeOf(time.Duration(0))
	timeType     = reflect.TypeOf(time.Time{})
	errorType    = reflect.TypeOf((*error)(nil)).Elem()
)

// inputs turns each tagged field of the input struct into an input.
func (b *builder) inputs(t reflect.Type) {
	if t == nil || t.Kind() != reflect.Struct {
		b.errorf("NewKind's type parameter is the input struct; its tagged fields become the inputs", "input type %v is not a struct", t)
		return
	}
	for _, f := range b.taggedFields(t, "input") {
		b.binding.Fields["."+f.name] = f.field.Index
		b.kind.Inputs = append(b.kind.Inputs, &kind.Input{Name: f.name, Type: b.convert(f.field.Type, f.name)})
	}
}

// tagged is a struct field with a `policy` tag.
type tagged struct {
	name    string
	options string // everything after the name in the tag
	field   reflect.StructField
}

// taggedFields returns the fields of t that carry a `policy:"name"` tag,
// reporting tagged fields that can't be read.
func (b *builder) taggedFields(t reflect.Type, where string) []tagged {
	var out []tagged
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, ok := f.Tag.Lookup("policy")
		if !ok || tag == "-" {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		switch {
		case name == "":
			b.errorf("write the name policies use, like `policy:\"tier\"`", "%s: field %s has an empty policy tag", where, f.Name)
			continue
		case !f.IsExported():
			b.errorf("reflection can only read exported fields; export it or drop the tag", "%s: field %s is unexported but tagged %q", where, f.Name, name)
			continue
		case f.Anonymous:
			b.errorf("give the embedded struct a field name, or tag its fields directly", "%s: embedded field %s can't be tagged", where, f.Name)
			continue
		}
		out = append(out, tagged{field: f, name: name, options: options})
	}
	return out
}

// convert maps a Go type to a Sigil type, registering struct types as it
// meets them. path names the field for messages.
func (b *builder) convert(t reflect.Type, path string) types.Type {
	if t == durationType {
		return types.Duration
	}
	if t == timeType {
		return types.Timestamp
	}
	switch t.Kind() {
	case reflect.Bool:
		return types.Bool
	case reflect.Int, reflect.Int64:
		return types.Int
	case reflect.Float64:
		return types.Float
	case reflect.String:
		return types.String
	case reflect.Slice:
		return &types.List{Elem: b.convert(t.Elem(), path)}
	case reflect.Map:
		return &types.Map{Key: b.convert(t.Key(), path), Value: b.convert(t.Elem(), path)}
	case reflect.Pointer:
		switch t.Elem().Kind() {
		case reflect.Pointer:
			b.errorf("optionals don't nest; use a single pointer", "%s: %v is a pointer to a pointer", path, t)
			return types.Invalid
		case reflect.Slice:
			b.errorf("use the slice itself; a nil slice already reads as an empty list", "%s: %v is a pointer to a slice", path, t)
			return types.Invalid
		case reflect.Map:
			b.errorf("use the map itself; a nil map already reads as an empty map", "%s: %v is a pointer to a map", path, t)
			return types.Invalid
		}
		return &types.Optional{Elem: b.convert(t.Elem(), path)}
	case reflect.Struct:
		return b.structType(t, path)
	}
	b.errorf(supportedTypes, "%s: unsupported type %v", path, t)
	return types.Invalid
}

const supportedTypes = "policies read bool, int, int64, float64, string, time.Duration, time.Time, slices, maps, pointers and tagged structs"

// structType returns the Sigil struct for a Go struct type, converting
// it on first sight. The shell is registered before its fields, so a
// recursive type resolves to itself and Validate reports the cycle.
func (b *builder) structType(t reflect.Type, path string) types.Type {
	if s, ok := b.structs[t]; ok {
		return s
	}
	name := t.Name()
	if name == "" {
		b.errorf("declare a named struct type; its name becomes the type's name in policies", "%s: anonymous struct types can't be exported", path)
		return types.Invalid
	}
	if prev, clash := b.binding.Structs[name]; clash {
		b.errorf("two Go types would export as one Sigil type; rename one", "%s: type %v and type %v both export as `%s`", path, prev, t, name)
		return types.Invalid
	}

	s := &types.Struct{Name: name}
	b.structs[t] = s
	b.binding.Structs[name] = t
	b.kind.Types = append(b.kind.Types, s)
	for _, f := range b.taggedFields(t, "type "+name) {
		b.binding.Fields[name+"."+f.name] = f.field.Index
		s.Fields = append(s.Fields, &types.Field{Name: f.name, Type: b.convert(f.field.Type, name+"."+f.name)})
	}
	return s
}

// decision turns a payload struct into a decision's fields. A tag's
// `default=` option is parsed as a Sigil constant of the field's type.
func (b *builder) decision(d Decision) {
	dec := &kind.Decision{Name: d.Name}
	b.kind.Decisions = append(b.kind.Decisions, dec)
	if d.Payload == nil || d.Payload.Kind() != reflect.Struct {
		b.errorf("a decision's payload is a struct with tagged fields, or policy.None", "decision %s: payload type %v is not a struct", d.Name, d.Payload)
		return
	}
	b.binding.Payloads[d.Name] = d.Payload
	for _, f := range b.taggedFields(d.Payload, "decision "+d.Name) {
		b.binding.Fields["decision "+d.Name+"."+f.name] = f.field.Index
		field := &kind.Field{Name: f.name, Type: b.convert(f.field.Type, "decision "+d.Name+"."+f.name)}
		if src, ok := strings.CutPrefix(f.options, "default="); ok {
			b.setDefault(field, d.Name, src)
		} else if f.options != "" {
			b.errorf("the only tag option is `default=<constant>`", "decision %s: field %s has unknown tag option %q", d.Name, f.name, f.options)
		}
		dec.Fields = append(dec.Fields, field)
	}
}

// setDefault parses a tag's default as a Sigil constant of the field's
// type. On failure the field keeps HasDefault with a nil value, which
// tells Validate the problem is already reported.
func (b *builder) setDefault(field *kind.Field, decision, src string) {
	if field == nil {
		return
	}
	field.HasDefault = true
	where := fmt.Sprintf("decision %s: field %s: default %q", decision, field.Name, src)
	x, errs := parser.ParseExpr("", []byte(src))
	if errs != nil {
		b.errorf("write the default as a Sigil literal, like `default=1h` or `default=[\"a\"]`", "%s: %s", where, errs[0].Msg)
		return
	}
	v, err := constant.Eval(x, field.Type)
	if err != nil {
		b.errorf(err.Help, "%s: %s", where, err.Msg)
		return
	}
	field.Default = v
}

// fn derives a host function's signature from the Go function's type.
// It takes any parameters policies can pass and returns a value, or a
// value and an error.
func (b *builder) fn(f Func) {
	v := reflect.ValueOf(f.Fn)
	if !v.IsValid() || v.Kind() != reflect.Func {
		b.errorf("pass a Go function, like `policy.WithFunc(\"split\", strings.Split)`", "function %s: %v is not a function", f.Name, f.Fn)
		return
	}
	t := v.Type()
	fn := &kind.Func{Name: f.Name}
	b.kind.Funcs = append(b.kind.Funcs, fn)
	b.binding.Funcs[f.Name] = v

	if t.IsVariadic() {
		b.errorf("policies pass a fixed number of arguments; wrap the function", "function %s: variadic functions aren't supported", f.Name)
	}
	for i := 0; i < t.NumIn(); i++ {
		fn.Params = append(fn.Params, b.convert(t.In(i), fmt.Sprintf("function %s, parameter %d", f.Name, i+1)))
	}

	switch {
	case t.NumOut() == 1 && t.Out(0) != errorType:
		fn.Result = b.convert(t.Out(0), "function "+f.Name+", result")
	case t.NumOut() == 2 && t.Out(1) == errorType:
		fn.Result = b.convert(t.Out(0), "function "+f.Name+", result")
	default:
		b.errorf("a host function returns a value, or a value and an error", "function %s: unsupported results %v", f.Name, t)
		fn.Result = types.Invalid
	}
}

// collect maps the options to the kind's collect mode: WithCollect is
// `collect all`, and ranked decisions are `collect one` with precedence.
// A kind without decisions leaves it unset, which Validate reports.
func collect(o Options) kind.Collect {
	switch {
	case o.Collect:
		return kind.CollectAll
	case len(o.Decisions) > 0:
		return kind.CollectOne
	}
	return kind.CollectUnset
}
