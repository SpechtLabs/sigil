package gogen

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// reach is the order in which policy.NewKind meets a kind's struct types
// and enums: it walks the input struct's fields, then each host
// function's parameters and result, then each decision's payload fields,
// and declares a type or an enum the first time it meets it, a struct
// before its own fields.
type reach struct {
	k       *kind.Kind
	seen    map[string]bool
	structs []string
	enums   []string
}

// expressible reports what keeps Go code from declaring the kind exactly
// as k is: names Go can't declare a type under, declarations in an order
// policy.NewKind can't produce, a struct type nothing uses, a `collect
// one` precedence other than the declaration order, and constructor
// arguments on the default or the conflict outcome. Each would make the
// generated kind's Schema() differ from the kind file, which Kind.Load
// rejects in a bundle that holds the file.
func (g *generator) expressible() {
	k := g.k
	for _, e := range k.Enums {
		if why := reserved(e.Name); why != "" {
			g.errorf("enum "+e.Name, keepNameHelp("enum", e.Name), "Go code can't declare enum %s: %s", e.Name, why)
		}
	}
	for _, t := range k.Types {
		if why := reserved(t.Name); why != "" {
			g.errorf("type "+t.Name, keepNameHelp("type", t.Name), "Go code can't declare type %s: %s", t.Name, why)
		}
	}

	r := walk(k)
	g.typeOrder(r)
	g.enumOrder(r)

	if k.Collect == kind.CollectOne && !slices.Equal(k.Precedence, decisionNames(k)) {
		g.errorf("precedence", fmt.Sprintf("declare the decisions in precedence order, %s, which changes no policy", strings.Join(k.Precedence, ", ")),
			"a Go kind can't rank its decisions in another order than it declares them: policy.WithDecisions takes them in precedence order")
	}
	g.noArgs("default", k.Default)
	g.noArgs("conflict", k.Conflict)
}

// keepNameHelp says why a reserved name can't be generated under another,
// and what to do.
func keepNameHelp(what, name string) string {
	return fmt.Sprintf("a generated type keeps the kind's name, which is the one policies write; rename %s %s in the kind, which is a breaking change", what, name)
}

// walk follows policy.NewKind's walk over k's inputs, host functions and
// payloads.
func walk(k *kind.Kind) *reach {
	r := &reach{k: k, seen: map[string]bool{}}
	if k == nil {
		return r
	}
	for _, in := range k.Inputs {
		r.visit(in.Type)
	}
	for _, f := range k.Funcs {
		for _, p := range f.Params {
			r.visit(p)
		}
		r.visit(f.Result)
	}
	for _, d := range k.Decisions {
		for _, f := range d.Fields {
			r.visit(f.Type)
		}
	}
	return r
}

// visit records the struct types and enums t reaches, in the order
// policy.NewKind meets them.
func (r *reach) visit(t types.Type) {
	switch t := t.(type) {
	case *types.List:
		r.visit(t.Elem)
	case *types.Map:
		r.visit(t.Key)
		r.visit(t.Value)
	case *types.Optional:
		r.visit(t.Elem)
	case *types.Enum:
		if !r.seen["enum "+t.Name] {
			r.seen["enum "+t.Name] = true
			r.enums = append(r.enums, t.Name)
		}
	case *types.Struct:
		if r.seen["type "+t.Name] {
			return
		}
		r.seen["type "+t.Name] = true
		r.structs = append(r.structs, t.Name)
		decl := r.k.Type(t.Name)
		if decl == nil {
			decl = t
		}
		for _, f := range decl.Fields {
			r.visit(f.Type)
		}
	}
}

// typeOrder reports a struct type nothing uses, which a Go kind has no
// way to declare, and struct types declared in another order than
// policy.NewKind declares them.
func (g *generator) typeOrder(r *reach) {
	declared := make([]string, 0, len(g.k.Types))
	for _, t := range g.k.Types {
		if !r.seen["type "+t.Name] {
			g.errorf("type "+t.Name, "use it in an input, a host function or a payload, or remove it",
				"type %s is used by no input, host function or payload, so a Go kind can't declare it", t.Name)
			continue
		}
		declared = append(declared, t.Name)
	}
	if i := firstDifference(declared, r.structs); i >= 0 {
		g.errorf("type "+declared[i], fmt.Sprintf("a Go kind declares struct types in the order its inputs, host functions and payloads first use them; declare them as %s, which changes no policy", strings.Join(r.structs, ", ")),
			"a Go kind can't declare type %s here", declared[i])
	}
}

// enumOrder reports enums declared in another order than policy.NewKind
// declares them: the ones the kind uses in the order it first uses them,
// then the others. The generated code registers every enum in the kind
// file's order, which keeps the others' order.
func (g *generator) enumOrder(r *reach) {
	declared := make([]string, len(g.k.Enums))
	want := slices.Clone(r.enums)
	for i, e := range g.k.Enums {
		declared[i] = e.Name
		if !r.seen["enum "+e.Name] {
			want = append(want, e.Name)
		}
	}
	if i := firstDifference(declared, want); i >= 0 {
		g.errorf("enum "+declared[i], fmt.Sprintf("a Go kind declares the enums its inputs, host functions and payloads use in the order they first use them, then the others; declare them as %s, which changes no policy", strings.Join(want, ", ")),
			"a Go kind can't declare enum %s here", declared[i])
	}
}

// noArgs reports the payload arguments of the default or the conflict
// outcome, which policy.WithDefault and policy.WithConflict can't pass:
// they take a reason, and the payload comes from the fields' defaults.
func (g *generator) noArgs(decl string, d *kind.Default) {
	if d == nil {
		return
	}
	dec := g.k.Decision(d.Decision)
	for _, f := range dec.Fields {
		name := f.Name
		if _, ok := d.Args[name]; !ok {
			continue
		}
		help := fmt.Sprintf("a Go kind's %s takes its payload from the fields' defaults; give %s.%s the default instead, and drop the argument", decl, d.Decision, name)
		if f.HasDefault && reflect.DeepEqual(f.Default, d.Args[name]) {
			help = fmt.Sprintf("the argument equals the default of %s.%s; drop it", d.Decision, name)
		}
		g.errorf(decl+".arg "+name, help, "a Go kind can't pass %s to its %s", name, decl)
	}
}

// decisionNames lists k's decisions in declaration order.
func decisionNames(k *kind.Kind) []string {
	if k == nil {
		return nil
	}
	names := make([]string, len(k.Decisions))
	for i, d := range k.Decisions {
		names[i] = d.Name
	}
	return names
}

// firstDifference returns the first index where a and b differ, or -1
// when they're equal. a must not be longer than b.
func firstDifference(a, b []string) int {
	for i := range a {
		if a[i] != b[i] {
			return i
		}
	}
	return -1
}
