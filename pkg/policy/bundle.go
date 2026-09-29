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
