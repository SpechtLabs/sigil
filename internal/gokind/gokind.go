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
	"reflect"
	"time"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

var (
	durationType = reflect.TypeOf(time.Duration(0))
	timeType     = reflect.TypeOf(time.Time{})
	errorType    = reflect.TypeOf((*error)(nil)).Elem()
)

// Build reflects over o and returns the kind with its binding, or the
// problems found. Every problem is reported, not just the first, so a
// host fixes its types in one round.
func Build(o Options) (*kind.Kind, *Binding, diag.ErrorList) {
	b := &builder{
		kind: &kind.Kind{Name: o.Name, Version: o.Version, Accepts: o.Accepts, Collect: collect(o)},
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
