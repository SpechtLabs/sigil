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
			Enums:    map[reflect.Type]*types.Enum{},
		},
		structs: map[reflect.Type]*types.Struct{},
		reached: map[*types.Enum]bool{},
	}
	b.binding.RecoverHostPanics = o.RecoverHostPanics
	if o.Ranked && o.Collect {
		b.errorf("a kind ranks its decisions with WithDecisions, or applies them all with WithCollect; use one",
			"kind %s mixes WithDecisions and WithCollect", o.Name)
	}
	registered := b.registerEnums(o.Enums)
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
	// What nothing reached prints after what something did, in
	// registration order.
	for _, e := range registered {
		b.reach(e)
	}
	switch {
	case len(o.Precedence) > 0 && !o.Collect:
		b.errorf("WithDecisions already ranks the decisions in argument order; WithPrecedence is for a WithCollect kind", "kind %s declares precedence twice", o.Name)
	case len(o.Precedence) > 0:
		b.kind.Precedence = append([]string{}, o.Precedence...)
	}
	for _, r := range o.Rankings {
		b.ranking(r)
	}
	b.kind.Exclusive = o.Exclusive
	if o.Default != nil {
		b.kind.Default = &kind.Default{Decision: o.Default.Decision, Reason: o.Default.Reason, Args: map[string]any{}}
	}
	if o.Conflict != nil {
		b.kind.Conflict = &kind.Default{Decision: o.Conflict.Decision, Reason: o.Conflict.Reason, Args: map[string]any{}}
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

// ranking sets the reason ranking of one decision. What Validate checks
// against the decision's reasons is left to it; what the kind model has
// no room for is checked here: a ranking without reasons, which in a kind
// file the grammar rules out, and reasons of other decisions.
func (b *builder) ranking(r Ranking) {
	if len(r.Reasons) == 0 {
		b.errorf("pass one decision's reasons, highest first, like `WithReasonPrecedence(Approve.Reason(\"release_manager\"), Approve.Reason(\"payments_sre\"))`",
			"WithReasonPrecedence names no reasons")
		return
	}
	for _, m := range r.Mixed {
		b.errorf("a ranking orders the reasons of one decision; rank each decision's reasons in a WithReasonPrecedence of its own",
			"precedence %s: reason %s belongs to decision %s", r.Decision, m.Reason, m.Decision)
	}
	d := b.kind.Decision(r.Decision)
	switch {
	case d == nil:
		b.errorf("WithReasonPrecedence ranks the reasons of a declared decision", "precedence: undeclared decision %q", r.Decision)
	case d.Ranked != nil:
		b.errorf("rank a decision's reasons once", "precedence %s is declared twice", r.Decision)
	default:
		d.Ranked = append([]string{}, r.Reasons...)
	}
}

// registerEnums records the host's enum types, so convert maps a field of
// one to its enum, and returns them in registration order. A kind file
// can't make `string` itself an enum or declare an enum twice, so those
// are checked here; every other rule of an enum is Validate's.
func (b *builder) registerEnums(enums []Enum) []*types.Enum {
	out := make([]*types.Enum, 0, len(enums))
	for _, e := range enums {
		switch {
		case e.Type == nil || e.Type.Name() == "" || e.Type.PkgPath() == "":
			b.errorf("declare a named type, like `type Tier string`; its name becomes the enum's name in policies",
				"WithEnum: %v is not a named type", e.Type)
			continue
		case b.binding.Enums[e.Type] != nil:
			b.errorf("pass each enum type to WithEnum once, with all its values",
				"enum %s is registered twice", e.Type.Name())
			continue
		}
		en := &types.Enum{Name: e.Type.Name(), Values: append([]string{}, e.Values...)}
		b.binding.Enums[e.Type] = en
		out = append(out, en)
	}
	return out
}

// reach adds an enum to the kind the first time something reaches it, so
// enums print in the order the kind's inputs, functions and payloads
// first use them.
func (b *builder) reach(e *types.Enum) {
	if !b.reached[e] {
		b.reached[e] = true
		b.kind.Enums = append(b.kind.Enums, e)
	}
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
