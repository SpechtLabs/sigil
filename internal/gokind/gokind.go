package gokind

import (
	"reflect"
	"time"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

var (
	durationType = reflect.TypeFor[time.Duration]()
	timeType     = reflect.TypeFor[time.Time]()
	errorType    = reflect.TypeFor[error]()
)

// Build reflects over o and returns the kind with its binding, or the
// problems found. Every problem is reported, not just the first, so a
// host fixes its types in one round. On error the kind and binding are
// nil. The diagnostics carry no position, since there's no source to
// point into; policy.NewKind panics with their messages.
//
// Build reports what only the Go side can get wrong, such as a field type
// policies can't read, a malformed tag, or mixing WithDecisions and
// WithCollect, and leaves every rule of the model to [kind.Kind.Validate].
func Build(o Options) (*kind.Kind, *Binding, diag.ErrorList) {
	b := &builder{
		kind: &kind.Kind{Name: o.Name, Version: o.Version, Accepts: accepts(o), Collect: collect(o)},
		binding: &Binding{
			Input:    o.Input,
			Structs:  map[string]reflect.Type{},
			Payloads: map[string]reflect.Type{},
			Funcs:    map[string]reflect.Value{},
			Fields:   map[string][]int{},
		},
		structs: map[reflect.Type]*types.Struct{},
	}
	b.binding.RecoverHostPanics = o.RecoverHostPanics
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
	switch {
	case len(o.Precedence) > 0 && !o.Collect:
		b.errorf("WithDecisions already ranks the decisions in argument order; WithPrecedence is for a WithCollect kind", "kind %s declares precedence twice", o.Name)
	case len(o.Precedence) > 0:
		b.kind.Precedence = append([]string{}, o.Precedence...)
	}
	for _, r := range o.Rankings {
		d := b.kind.Decision(r.Decision)
		switch {
		case d == nil:
			b.errorf("WithReasonPrecedence ranks the reasons of a declared decision", "precedence %s: undeclared decision", r.Decision)
		case d.Ranked != nil:
			b.errorf("rank a decision's reasons once", "precedence %s is declared twice", r.Decision)
		default:
			d.Ranked = append([]string{}, r.Reasons...)
		}
	}
	b.kind.Exclusive = o.Exclusive
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

// accepts is the oldest version the kind accepts: every version, from 1,
// unless WithAccepts set one. A version below 1 stays for Validate to
// report.
func accepts(o Options) int {
	if o.Accepts == nil {
		return 1
	}
	return *o.Accepts
}
