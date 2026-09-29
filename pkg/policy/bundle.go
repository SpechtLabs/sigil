package policy

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/types"
)

// compile compiles root from b with the host's options: params checked
// against their declarations and bounds, and the required policies
// checked for unconditional invocation.
func (k *Kind[In]) compile(b *bundle.Bundle, root string, o *loadOptions) (*Policy[In], error) {
	b.Check()
	var params map[string]eval.Value
	var errs diag.ErrorList
	if d := b.Document(root); d != nil && d.Info != nil {
		if doc, ok := d.Node.(*ast.PolicyDoc); ok {
			params, errs = k.params(d.File, doc, d.Info, o.params)
		}
	}
	if errs != nil {
		errs = append(errs, b.Errors()...)
		return nil, k.compileError(b, errs)
	}
	prog, errs := b.Compile(root, bundle.Options{Params: params, Binding: k.binding, Require: o.required()})
	if errs != nil {
		return nil, k.compileError(b, errs)
	}
	return &Policy[In]{kind: k, prog: prog, name: root}, nil
}

// compileError turns the bundle's diagnostics into a *CompileError.
func (k *Kind[In]) compileError(b *bundle.Bundle, errs diag.ErrorList) *CompileError {
	e := &CompileError{rendered: b.Render(errs)}
	for _, d := range errs {
		e.Diagnostics = append(e.Diagnostics, Diagnostic{
			Message:  d.Msg,
			Help:     d.Help,
			Position: position(d.File, b.DocumentAt(d.File, d.Pos), d.Pos),
			End:      position(d.File, "", d.End),
		})
	}
	return e
}

// params turns the host's bindings into evaluator values, checking each
// against its param's type and bounds and rejecting names the policy
// doesn't declare.
func (k *Kind[In]) params(file string, doc *ast.PolicyDoc, info *check.Info, given Params) (map[string]eval.Value, diag.ErrorList) {
	declared := map[string]*ast.ParamStmt{}
	names := make([]string, 0, len(given))
	for _, s := range doc.Stmts {
		if p, ok := s.(*ast.ParamStmt); ok {
			declared[p.Name.Name] = p
			names = append(names, p.Name.Name)
		}
	}
	var errs diag.ErrorList
	out := map[string]eval.Value{}
	for _, name := range slices.Sorted(maps.Keys(given)) {
		v := given[name]
		p, ok := declared[name]
		if !ok {
			help := fmt.Sprintf("%s declares no params", doc.Name)
			if len(names) > 0 {
				help = fmt.Sprintf("%s declares: %s", doc.Name, strings.Join(names, ", "))
			}
			errs = append(errs, &diag.Error{Msg: fmt.Sprintf("policy %s has no param %q", doc.Name, name), Help: help})
			continue
		}
		want := info.Params[p]
		if got, ok := k.conforms(v, want); !ok {
			errs = append(errs, &diag.Error{
				File: file, Pos: p.Pos(), End: p.End(),
				Msg:  fmt.Sprintf("param %s: expected %s, found %s", name, want, got),
				Help: "Params values are Go values of the shape NewKind accepts for the param's type",
			})
			continue
		}
		if err := inBounds(p, want, v); err != nil {
			err.File = file
			errs = append(errs, err)
			continue
		}
		if hasEnum(want) {
			// The evaluator holds enum values as constants, and a Go
			// string can hold a value the enum lacks.
			v = k.binding.Canonical(want, reflect.ValueOf(v))
			if bad, e := outsideEnum(v, want); e != nil {
				errs = append(errs, &diag.Error{
					File: file, Pos: p.Pos(), End: p.End(),
					Msg:  fmt.Sprintf("param %s: %q is not a value of %s", name, bad, e.Name),
					Help: e.Name + " declares: " + e.ValueNames(),
				})
				continue
			}
		}
		out[name] = reflect.ValueOf(v)
	}
	return out, errs
}

// inBounds checks a bound value against the param's `min` and `max`,
// either of which may be absent.
func inBounds(p *ast.ParamStmt, t types.Type, v any) *diag.Error {
	if p.Min == nil && p.Max == nil {
		return nil
	}
	got := constant.Ordered(v)
	if p.Min != nil {
		if lo, err := constant.Eval(p.Min, t); err == nil && constant.Compare(got, lo) < 0 {
			return &diag.Error{Pos: p.Pos(), End: p.End(),
				Msg:  fmt.Sprintf("param %s: %s is below the minimum %s", p.Name.Name, constant.Format(got), constant.Format(lo)),
				Help: "the policy bounds the param; bind a value it accepts"}
		}
	}
	if p.Max != nil {
		if hi, err := constant.Eval(p.Max, t); err == nil && constant.Compare(got, hi) > 0 {
			return &diag.Error{Pos: p.Pos(), End: p.End(),
				Msg:  fmt.Sprintf("param %s: %s is above the maximum %s", p.Name.Name, constant.Format(got), constant.Format(hi)),
				Help: "the policy bounds the param; bind a value it accepts"}
		}
	}
	return nil
}

// hasEnum reports whether values of t hold an enum value.
func hasEnum(t types.Type) bool {
	switch t := t.(type) {
	case *types.Enum:
		return true
	case *types.List:
		return hasEnum(t.Elem)
	case *types.Map:
		return hasEnum(t.Key) || hasEnum(t.Value)
	case *types.Optional:
		return hasEnum(t.Elem)
	}
	return false
}

// outsideEnum returns the first enum value in v, a constant of type t,
// that its enum doesn't declare, with the enum, or a nil enum when every
// value is declared.
func outsideEnum(v any, t types.Type) (string, *types.Enum) { //nolint:emptyinterface // constants are typed by their Sigil type
	switch t := t.(type) {
	case *types.Enum:
		if s, _ := v.(constant.EnumValue); !t.Has(string(s)) {
			return string(s), t
		}
	case *types.List:
		xs, _ := v.([]any)
		for _, x := range xs {
			if bad, e := outsideEnum(x, t.Elem); e != nil {
				return bad, e
			}
		}
	case *types.Map:
		m, _ := v.(map[any]any)
		for _, key := range slices.SortedFunc(maps.Keys(m), constant.Compare) {
			if bad, e := outsideEnum(key, t.Key); e != nil {
				return bad, e
			}
			if bad, e := outsideEnum(m[key], t.Value); e != nil {
				return bad, e
			}
		}
	case *types.Optional:
		if v != nil {
			return outsideEnum(v, t.Elem)
		}
	}
	return "", nil
}

// conforms reports whether v, a Go value bound to a param, is a t: either
// a constant in the evaluator's representation or a Go value whose type
// maps to t. On failure it describes what was found.
func (k *Kind[In]) conforms(v any, t types.Type) (string, bool) {
	if v == nil {
		return "nil", false
	}
	if constant.Conforms(v, t) {
		return "", true
	}
	got, ok := k.binding.TypeOf(reflect.TypeOf(v))
	if !ok {
		return fmt.Sprintf("Go type %T", v), false
	}
	if !types.Identical(got, t) {
		return got.String(), false
	}
	return "", true
}
