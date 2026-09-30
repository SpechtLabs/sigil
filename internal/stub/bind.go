package stub

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

var errorType = reflect.TypeFor[error]()

// ErrUnmatched is the error a stubbed call fails with when no entry of
// the stub's calls matches its args, and the stub gives no result for
// other calls.
type ErrUnmatched struct {
	gokind.StandIn
	Name string // the host function's name
	Args string // the call's args, as Sigil literals
}

// Failure is the error a stub's `error:` fails a call with.
type Failure struct {
	gokind.StandIn
	Msg string // the stub's message
}

// bound is a stubbed function, ready to answer calls.
type bound struct {
	binding  *gokind.Binding // the binding its values are Go values of
	kind     *kind.Kind      // the kind the binding is of, for its struct types
	fn       *kind.Func      // the function it stubs
	result   reflect.Type    // the Go type of its result
	fallback []reflect.Value // the results of a call no entry matches; nil for an *ErrUnmatched
	matches  []match
}

// match is one call entry, bound: the args it answers, in canonical
// form, and the results it returns.
type match struct {
	args []any //nolint:emptyinterface // canonical values are dynamically typed, like constants
	out  []reflect.Value
}

// Validate checks the set against the kind as [Set.Bind] does, without a
// binding: every value is decoded into the Go types the kind synthesizes,
// which fit the same values as a host's own.
func (s Set) Validate(k *kind.Kind) []*Error {
	if len(s) == 0 {
		return nil
	}
	_, errs := s.Bind(k, gokind.Synthesize(k))
	return errs
}

// Bind checks the set against the kind and returns a copy of b, the
// kind's binding, with every stubbed host function replaced; b doesn't
// change. The problems it reports, with their lines: a function the kind
// doesn't declare, with the nearest declared name; a call entry whose
// number of args isn't the function's number of params; and an arg or
// result that doesn't decode into its type. An empty set returns b
// itself.
//
// A stubbed function takes the Go params of the one it replaces and
// returns its result and an error, whatever the original returned. The
// args it compares with the entries are in canonical form (see
// [gokind.Binding.Canonical]), so a value matches whichever Go type
// carries it.
func (s Set) Bind(k *kind.Kind, b *gokind.Binding) (*gokind.Binding, []*Error) {
	if len(s) == 0 {
		return b, nil
	}
	funcs := map[string]reflect.Value{}
	var errs []*Error
	for _, name := range slices.Sorted(maps.Keys(s)) {
		fn, ferrs := s[name].bind(k, b)
		errs = append(errs, ferrs...)
		if ferrs == nil {
			funcs[name] = fn
		}
	}
	if errs != nil {
		return nil, byLine(errs)
	}
	return b.WithFuncs(funcs), nil
}

// Error implements the error interface. It names the call.
func (e *ErrUnmatched) Error() string {
	return fmt.Sprintf("no stubbed call matches %s(%s)", e.Name, e.Args)
}

// Help says how to answer the call, which the evaluator shows as the
// runtime error's help.
func (e *ErrUnmatched) Help() string {
	return "add these args under the stub's calls, or give the stub a returns or error for every other call"
}

// Error implements the error interface. It returns the stub's message.
func (e *Failure) Error() string { return e.Msg }

// Help says where the error comes from, which the evaluator shows as the
// runtime error's help.
func (e *Failure) Help() string {
	return "the stub's error: fails the call, as the host function failing would; a test case expects it with `expect: {error: ...}`"
}

// bind builds the function that replaces the stubbed one.
func (st *Func) bind(k *kind.Kind, b *gokind.Binding) (reflect.Value, []*Error) {
	f := k.Func(st.Name)
	if f == nil {
		return reflect.Value{}, []*Error{{Line: st.Line, Msg: fmt.Sprintf("the kind has no host function %s to stub", st.Name), Help: funcHint(k, st.Name)}}
	}
	impl, ok := b.Funcs[st.Name]
	if !ok {
		return reflect.Value{}, []*Error{{Line: st.Line, Msg: "host function " + st.Name + " isn't bound to Go", Help: "the binding and the kind disagree, which is a bug in sigil"}}
	}
	ft := impl.Type()
	bf := &bound{binding: b, kind: k, fn: f, result: ft.Out(0), matches: make([]match, 0, len(st.Calls))}
	what := "stub " + st.Name
	var errs []*Error
	bf.fallback, errs = bf.outcome(st.Returns, st.Error, what)
	for i, c := range st.Calls {
		m, merrs := bf.match(ft, c, fmt.Sprintf("%s: call %d", what, i+1))
		errs = append(errs, merrs...)
		bf.matches = append(bf.matches, m)
	}
	if errs != nil {
		return reflect.Value{}, errs
	}
	in := make([]reflect.Type, ft.NumIn())
	for i := range in {
		in[i] = ft.In(i)
	}
	return reflect.MakeFunc(reflect.FuncOf(in, []reflect.Type{bf.result, errorType}, false), bf.call), nil
}

// call answers one call: the first entry whose args equal the call's,
// or else the fallback, or else an [*ErrUnmatched].
func (bf *bound) call(args []reflect.Value) []reflect.Value {
	if len(bf.matches) == 0 && bf.fallback != nil {
		return bf.fallback
	}
	got := make([]any, len(args))
	for i, a := range args {
		got[i] = bf.binding.Canonical(bf.fn.Params[i], a)
	}
	for _, m := range bf.matches {
		if reflect.DeepEqual(m.args, got) {
			return m.out
		}
	}
	if bf.fallback != nil {
		return bf.fallback
	}
	parts := make([]string, len(got))
	for i, g := range got {
		parts[i] = constant.Format(g)
	}
	return fail(bf.result, &ErrUnmatched{Name: bf.fn.Name, Args: strings.Join(parts, ", ")})
}

// match binds one call entry: its args, decoded into the Go params of
// ft and put in canonical form, and its result.
func (bf *bound) match(ft reflect.Type, c *Call, what string) (match, []*Error) {
	params := bf.fn.Params
	if len(c.Args) != len(params) {
		return match{}, []*Error{{Line: c.Line, Msg: fmt.Sprintf("%s passes %s, but %s takes %d", what, count(len(c.Args)), bf.fn.Name, len(params)), Help: "the kind declares `" + bf.fn.Signature() + "`"}}
	}
	m := match{args: make([]any, len(c.Args))}
	var errs []*Error
	for i, a := range c.Args {
		v := reflect.New(ft.In(i)).Elem()
		if err := bf.decode(params[i], a, v, fmt.Sprintf("arg %d", i+1), what); err != nil {
			errs = append(errs, err)
			continue
		}
		m.args[i] = bf.binding.Canonical(params[i], v)
	}
	var oerrs []*Error
	m.out, oerrs = bf.outcome(c.Returns, c.Error, what)
	return m, append(errs, oerrs...)
}

// outcome builds the results of a stub or call entry that returns a
// value or fails with msg; nil when it gives neither.
func (bf *bound) outcome(returns *Value, msg, what string) ([]reflect.Value, []*Error) {
	if msg != "" {
		return fail(bf.result, &Failure{Msg: msg}), nil
	}
	if returns == nil {
		return nil, nil
	}
	v := reflect.New(bf.result).Elem()
	if err := bf.decode(bf.fn.Result, returns, v, "returns", what); err != nil {
		return nil, []*Error{err}
	}
	return []reflect.Value{v, reflect.Zero(errorType)}, nil
}

// decode sets v, a Go value of Sigil type t, from val, a value of the
// stub at path. A null where t can't be null is a stub's own problem,
// not an input's, so its advice is about the stub.
func (bf *bound) decode(t types.Type, val *Value, v reflect.Value, path, what string) *Error {
	raw := val.Raw
	if val.Node != nil {
		raw = typed(bf.kind, t, val.Node)
	}
	if raw == nil && !nullable(t) {
		return &Error{Line: val.Line, Msg: fmt.Sprintf("%s: %s is null, but its type is %s", what, path, t), Help: "write a value of that type; only an optional, a list or a map may be null"}
	}
	if err := bf.binding.Decode(t, raw, v, path); err != nil {
		return &Error{Line: val.Line, Msg: what + ": " + err.Error(), Help: strings.Join(err.Advice(), "; ")}
	}
	return nil
}

// fail returns the results of a call that fails with err.
func fail(res reflect.Type, err error) []reflect.Value {
	return []reflect.Value{reflect.Zero(res), reflect.ValueOf(&err).Elem()}
}

// funcHint lists the kind's host functions, with the one nearest name.
func funcHint(k *kind.Kind, name string) string {
	names := make([]string, len(k.Funcs))
	for i, f := range k.Funcs {
		names[i] = f.Name
	}
	if len(names) == 0 {
		return "the kind declares no host functions"
	}
	list := "the kind declares: " + strings.Join(names, ", ")
	if near, ok := diag.Nearest(name, names); ok {
		return fmt.Sprintf("did you mean %q? %s", near, list)
	}
	return list
}

// count says how many args a call entry passes.
func count(n int) string {
	if n == 1 {
		return "1 arg"
	}
	return fmt.Sprintf("%d args", n)
}

// typed reads a YAML node as a value of Sigil type t, the way YAML
// decodes it, except that a scalar where t is a string is the text it was
// written as: `2026-01-01` and `1.10` are strings for a string, not a
// timestamp and a number, and an enum value is its name, whatever YAML
// would make of it. Lists, maps, optionals and struct fields are
// read by their element, value and field types. A node t doesn't fit
// decodes as YAML would, for the binding to report.
func typed(k *kind.Kind, t types.Type, n *yaml.Node) any { //nolint:emptyinterface // a decoded YAML value
	n = resolve(n)
	switch t := t.(type) {
	case types.Basic:
		if t == types.String && n.Kind == yaml.ScalarNode && !null(n) {
			return n.Value
		}
	case *types.Enum:
		if n.Kind == yaml.ScalarNode && !null(n) {
			return n.Value
		}
	case *types.Optional:
		if !null(n) {
			return typed(k, t.Elem, n)
		}
	case *types.List:
		if n.Kind == yaml.SequenceNode {
			out := make([]any, len(n.Content))
			for i, c := range n.Content {
				out[i] = typed(k, t.Elem, c)
			}
			return out
		}
	case *types.Map:
		if n.Kind == yaml.MappingNode {
			out := map[string]any{}
			for i := 0; i+1 < len(n.Content); i += 2 {
				out[resolve(n.Content[i]).Value] = typed(k, t.Value, n.Content[i+1])
			}
			return out
		}
	case *types.Struct:
		if n.Kind == yaml.MappingNode {
			return typedStruct(k, t, n)
		}
	}
	var raw any
	_ = n.Decode(&raw) // it decoded when the stub was parsed
	return raw
}

// typedStruct reads a mapping as a struct of type t, each field by its
// type. A key t doesn't declare decodes as YAML would, for the binding to
// report as an unknown field.
func typedStruct(k *kind.Kind, t *types.Struct, n *yaml.Node) map[string]any { //nolint:emptyinterface // decoded YAML values
	decl := t
	if d := k.Type(t.Name); d != nil {
		decl = d
	}
	out := map[string]any{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := resolve(n.Content[i]).Value, n.Content[i+1]
		var ft types.Type = types.Invalid
		for _, f := range decl.Fields {
			if f.Name == key {
				ft = f.Type
			}
		}
		out[key] = typed(k, ft, val)
	}
	return out
}

// nullable reports whether a value of type t may be null.
func nullable(t types.Type) bool {
	switch t.(type) {
	case *types.Optional, *types.List, *types.Map:
		return true
	}
	return false
}
