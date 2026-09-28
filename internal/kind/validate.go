package kind

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
	"github.com/spechtlabs/sigil/internal/types"
)

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Locator maps a validation key to the source span of the thing it names,
// so a kind loaded from a file can report rule violations at the right
// place. The keys are paths into the kind:
//
//	kind                          the header
//	kind.version                  the version number
//	type T                        a type's name
//	type T.field F                a field's name
//	type T.field F.type           a field's type
//	input I / input I.type        an input's name / type
//	fn F / fn F.result            a function's name / result type
//	fn F.param N                  the Nth parameter's type, from 1
//	decision D / decision D.field F / decision D.field F.type / decision D.field F.default
//	precedence / precedence.D     the declaration / one name in it
//	collect
//	default / default.reason / default.arg F
//
// A Locator that doesn't know a key returns false, and the diagnostic is
// left without a position.
type Locator func(key string) (start, end token.Pos, ok bool)

type validator struct {
	kind   *Kind
	locate Locator
	errs   diag.ErrorList
}

// Validate checks the rules a kind must satisfy and returns one
// diagnostic per violation. The model doesn't know where it came from, so
// positions come from locate, which may be nil: NewKind panics with the
// bare messages, while a kind-file loader supplies the spans.
func (k *Kind) Validate(locate Locator) diag.ErrorList {
	v := &validator{kind: k, locate: locate}
	v.header()
	v.types()
	v.inputsAndFuncs()
	v.decisions()
	v.resolution()
	return v.errs
}

// errorf records a violation at the source of key.
func (v *validator) errorf(key, help, format string, args ...any) {
	e := &diag.Error{Msg: fmt.Sprintf(format, args...), Help: help}
	if v.locate != nil {
		if start, end, ok := v.locate(key); ok {
			e.Pos, e.End = start, end
		}
	}
	v.errs = append(v.errs, e)
}

func (v *validator) header() {
	if !isIdent(v.kind.Name) {
		v.errorf("kind", "a kind's name is an identifier, like `DeployApproval`", "invalid kind name %q", v.kind.Name)
	}
	if v.kind.Version < 1 {
		v.errorf("kind.version", "the version is a positive integer that changes when the contract does", "invalid kind version %d", v.kind.Version)
	}
	if v.kind.Accepts < 0 || v.kind.Accepts > v.kind.Version {
		v.errorf("kind.accepts", "`accepts` names the oldest version policies may still pin, between 1 and the version",
			"kind %s at version %d can't accept version %d", v.kind.Name, v.kind.Version, v.kind.Accepts)
	}
}

// types checks that struct names are unique, unreserved identifiers, that
// fields are unique names with types that resolve, and that no struct
// reaches itself.
func (v *validator) types() {
	seen := map[string]bool{}
	for _, s := range v.kind.Types {
		key := "type " + s.Name
		switch {
		case !isIdent(s.Name):
			v.errorf(key, "a type name is an identifier, like `Service`", "invalid type name %q", s.Name)
		case types.IsReserved(s.Name):
			v.errorf(key, "the built-in type names are reserved; pick another name", "type %q shadows a built-in type", s.Name)
		case seen[s.Name]:
			v.errorf(key, "give each type one declaration", "type %q is declared twice", s.Name)
		}
		seen[s.Name] = true

		fields := map[string]bool{}
		for _, f := range s.Fields {
			fkey := key + ".field " + f.Name
			if !isName(f.Name) {
				v.errorf(fkey, "a field name is an identifier; keywords are allowed", "type %s: invalid field name %q", s.Name, f.Name)
			}
			if fields[f.Name] {
				v.errorf(fkey, "give each field one declaration", "type %s: field %q is declared twice", s.Name, f.Name)
			}
			fields[f.Name] = true
			v.typeResolves(f.Type, fkey+".type", fmt.Sprintf("type %s, field %q", s.Name, f.Name))
		}
	}

	for _, s := range v.kind.Types {
		if path := v.cycle(s, nil); path != nil {
			v.errorf("type "+s.Name, "policies can't loop, so a recursive type could only be read to a fixed depth; model the relation with an id instead",
				"type %s is recursive: %s", s.Name, strings.Join(path, " -> "))
		}
	}
}

// cycle returns the path from s back to itself through struct-typed
// fields, or nil. Each struct only reports the cycle that starts at it.
func (v *validator) cycle(s *types.Struct, path []string) []string {
	if s == nil {
		return nil
	}
	path = append(path, s.Name)
	for _, f := range s.Fields {
		next := structOf(f.Type)
		if next == nil {
			continue
		}
		if next.Name == path[0] {
			return append(path, next.Name)
		}
		if contains(path, next.Name) {
			continue // a cycle that doesn't pass through path[0]; reported from its own start
		}
		if decl := v.kind.Type(next.Name); decl != nil {
			if p := v.cycle(decl, path); p != nil {
				return p
			}
		}
	}
	return nil
}

// structOf returns the struct type inside t, looking through lists, maps
// and optionals, or nil.
func structOf(t types.Type) *types.Struct {
	switch t := t.(type) {
	case *types.Struct:
		return t
	case *types.List:
		return structOf(t.Elem)
	case *types.Map:
		return structOf(t.Value)
	case *types.Optional:
		return structOf(t.Elem)
	}
	return nil
}

func contains(xs []string, x string) bool {
	return slices.Contains(xs, x)
}

// typeResolves checks that every struct type inside t is declared, and
// that t is well-formed: no nested optionals, no optional lists or maps, no decision-typed data,
// scalar map keys. key locates the type in source; where names it in the
// message. types.Invalid passes: a loader puts it where a name didn't
// resolve, and has already reported that at the name.
func (v *validator) typeResolves(t types.Type, key, where string) {
	switch t := t.(type) {
	case types.Basic:
		if t == types.Decision {
			v.errorf(key, "`decision` values only come from `outcome`; data can't hold them", "%s: type can't be decision", where)
		}
	case *types.List:
		v.typeResolves(t.Elem, key, where)
	case *types.Map:
		if !types.IsKey(t.Key) {
			v.errorf(key, "map keys are scalars, as in Go: bool, int, float, string, duration or timestamp", "%s: map key type can't be %s", where, t.Key)
		} else {
			v.typeResolves(t.Key, key, where)
		}
		v.typeResolves(t.Value, key, where)
	case *types.Optional:
		switch t.Elem.(type) {
		case *types.Optional:
			v.errorf(key, "write `?T` with a single `?`", "%s: optional types don't nest", where)
		case *types.List:
			v.errorf(key, "use `list<T>`; an absent list already reads as an empty one", "%s: a list can't be optional", where)
		case *types.Map:
			v.errorf(key, "use `map<K, V>`; an absent map already reads as an empty one", "%s: a map can't be optional", where)
		}
		v.typeResolves(t.Elem, key, where)
	case *types.Struct:
		if v.kind.Type(t.Name) == nil {
			v.errorf(key, "declare it with `type "+t.Name+" { ... }`", "%s: undeclared type %s", where, t.Name)
		}
	default:
		v.errorf(key, "", "%s: invalid type", where)
	}
}

// inputsAndFuncs checks the shared namespace of inputs and host
// functions, and each function's signature.
func (v *validator) inputsAndFuncs() {
	seen := map[string]string{}
	for _, in := range v.kind.Inputs {
		key := "input " + in.Name
		if !isIdent(in.Name) {
			v.errorf(key, "an input name is a plain identifier, not a keyword", "invalid input name %q", in.Name)
		}
		if prev, dup := seen[in.Name]; dup {
			v.errorf(key, "inputs and host functions share one namespace", "input %q collides with %s %q", in.Name, prev, in.Name)
		}
		seen[in.Name] = "input"
		v.typeResolves(in.Type, key+".type", fmt.Sprintf("input %q", in.Name))
	}
	for _, f := range v.kind.Funcs {
		key := "fn " + f.Name
		if !isIdent(f.Name) {
			v.errorf(key, "a function name is a plain identifier, not a keyword", "invalid function name %q", f.Name)
		}
		if prev, dup := seen[f.Name]; dup {
			v.errorf(key, "inputs and host functions share one namespace", "function %q collides with %s %q", f.Name, prev, f.Name)
		}
		seen[f.Name] = "function"
		for i, p := range f.Params {
			v.typeResolves(p, fmt.Sprintf("%s.param %d", key, i+1), fmt.Sprintf("function %s, parameter %d", f.Name, i+1))
		}
		if _, opt := f.Result.(*types.Optional); opt {
			v.errorf(key+".result", "return the zero value and let the policy compare, or return a list", "function %s: the result can't be optional", f.Name)
		} else {
			v.typeResolves(f.Result, key+".result", fmt.Sprintf("function %s, result", f.Name))
		}
	}
}

// decisions checks decision names, payload fields and their defaults.
func (v *validator) decisions() {
	if len(v.kind.Decisions) == 0 {
		v.errorf("kind", "declare at least one decision", "kind %s declares no decisions", v.kind.Name)
	}
	seen := map[string]bool{}
	for _, d := range v.kind.Decisions {
		key := "decision " + d.Name
		if !isIdent(d.Name) {
			v.errorf(key, "a decision name is a plain identifier, not a keyword", "invalid decision name %q", d.Name)
		}
		if seen[d.Name] {
			v.errorf(key, "give each decision one declaration", "decision %q is declared twice", d.Name)
		}
		seen[d.Name] = true

		fields := map[string]bool{}
		for _, f := range d.Fields {
			fkey := key + ".field " + f.Name
			where := fmt.Sprintf("decision %s, field %q", d.Name, f.Name)
			switch {
			case f.Name == "reason":
				v.errorf(fkey, "every decision takes `reason: string` first; it isn't a payload field", "%s: reason is implied and can't be declared as a payload field", where)
			case !isName(f.Name):
				v.errorf(fkey, "a field name is an identifier; keywords are allowed", "%s: invalid field name", where)
			case fields[f.Name]:
				v.errorf(fkey, "give each field one declaration", "%s: declared twice", where)
			}
			fields[f.Name] = true
			v.typeResolves(f.Type, fkey+".type", where)
			// A nil default with HasDefault set means the source of the kind
			// couldn't produce the value and has reported why.
			if f.HasDefault && f.Default != nil && !constant.Conforms(f.Default, f.Type) {
				v.errorf(fkey+".default", "a default is a constant of the field's type", "%s: default %s is not a %s", where, constant.Format(f.Default), f.Type)
			}
		}
	}
}

// resolution checks precedence, collect and the default decision.
func (v *validator) resolution() {
	k := v.kind
	switch {
	case k.Collect == CollectUnset && len(k.Precedence) > 0:
		v.errorf("precedence", "declare `collect one` to return the highest-ranked decision", "kind %s has precedence but no collect", k.Name)
	case k.Collect == CollectUnset:
		v.errorf("kind", "declare `collect one` with a `precedence`, or `collect all`", "kind %s doesn't declare how many decisions it returns", k.Name)
	case k.Collect == CollectOne && len(k.Precedence) == 0:
		v.errorf("collect", "`collect one` returns the highest-ranked decision; rank them with `precedence deny > review > approve`, highest first", "kind %s collects one decision but has no precedence", k.Name)
	case k.Collect == CollectOne:
		v.precedence()
	case len(k.Precedence) > 0:
		v.errorf("precedence", "`collect all` with `precedence` is reserved; remove `precedence` to return every decision that fired", "kind %s has both precedence and collect all", k.Name)
	}

	if k.Default == nil {
		if k.Collect != CollectAll {
			v.errorf("kind", "declare `default <decision>(\"<reason>\")` for the case where no rule fires", "kind %s has no default decision", k.Name)
		}
		return
	}
	v.defaultCall()
}

func (v *validator) precedence() {
	k := v.kind
	seen := map[string]bool{}
	for _, name := range k.Precedence {
		if k.Decision(name) == nil {
			v.errorf("precedence."+name, "precedence lists the declared decisions, highest first", "precedence names undeclared decision %q", name)
		}
		if seen[name] {
			v.errorf("precedence."+name, "list every decision exactly once", "precedence names %q twice", name)
		}
		seen[name] = true
	}
	for _, d := range k.Decisions {
		if !seen[d.Name] {
			v.errorf("precedence", "list every decision exactly once, highest first", "precedence doesn't name decision %q", d.Name)
		}
	}
}

func (v *validator) defaultCall() {
	def := v.kind.Default
	d := v.kind.Decision(def.Decision)
	if d == nil {
		v.errorf("default", "the default constructs one of the kind's decisions", "default names undeclared decision %q", def.Decision)
		return
	}
	if def.Reason == "" {
		v.errorf("default.reason", "the reason is a stable identifier, like `no_rule_matched`", "default %s has an empty reason", d.Name)
	}
	for name, val := range def.Args {
		f := d.Field(name)
		if f == nil {
			v.errorf("default.arg "+name, d.Name+" is declared as: "+d.Signature(), "default: decision %s has no payload field %q", d.Name, name)
			continue
		}
		if !constant.Conforms(val, f.Type) {
			v.errorf("default.arg "+name, "a default is a constant of the field's type", "default: field %q value %s is not a %s", name, constant.Format(val), f.Type)
		}
	}
	for _, f := range d.Fields {
		if _, given := def.Args[f.Name]; !given && !f.HasDefault {
			v.errorf("default", d.Name+" is declared as: "+d.Signature(), "default: field %q is required and has no value", f.Name)
		}
	}
}

// isIdent reports whether name is a plain identifier: the right shape and
// not a keyword. Top-level names must be; field names may be keywords.
func isIdent(name string) bool {
	return identRE.MatchString(name) && token.Lookup(name) == token.Ident
}

// isName reports whether name can be a field or payload name, which may
// be spelled like a keyword.
func isName(name string) bool {
	return identRE.MatchString(name)
}
