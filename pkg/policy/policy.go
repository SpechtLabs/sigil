// Package policy is Sigil's Go API: define a kind from Go types, compile
// policies against it, and evaluate them.
//
// It mirrors regexp: a kind is defined once at package level with NewKind,
// which panics on a contract that can't be exported, the way
// regexp.MustCompile panics on a pattern that can't be compiled.
//
//	var Deploy = policy.NewKind[Input]("DeployApproval",
//		policy.WithVersion(1),
//		policy.WithDecisions(Deny, Review, Approve),
//		policy.WithDefault(Deny, "no_rule_matched"),
//		policy.WithFunc("split", strings.Split),
//	)
package policy

import (
	"reflect"
	"strings"

	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// None is the payload of a decision that carries only a reason.
type None struct{}

// Decision is a typed handle for one of a kind's decisions: its name,
// with the payload struct as the type parameter. Declare one per decision
// and pass them to Decisions or Collect.
//
//	var Review = policy.Decision[ReviewData]("review")
type Decision[T any] string

// Name returns the decision's name as policies write it.
func (d Decision[T]) Name() string { return string(d) }

// ref implements DecisionRef.
func (d Decision[T]) ref() gokind.Decision {
	return gokind.Decision{Name: string(d), Payload: reflect.TypeOf((*T)(nil)).Elem()}
}

// DecisionRef is any Decision[T]. It exists so decisions with different
// payload types can be listed together.
type DecisionRef interface {
	Name() string
	ref() gokind.Decision
}

// Option configures NewKind.
type Option func(*gokind.Options)

// WithVersion sets the contract version, which sigil breaking compares.
func WithVersion(n int) Option {
	return func(o *gokind.Options) { o.Version = n }
}

// WithDecisions declares the decisions of a `collect one` kind in
// precedence order, highest first.
// Calls add up: WithDecisions(a, b) and WithDecisions(a), WithDecisions(b)
// declare the same kind. A kind uses WithDecisions or WithCollect, not
// both.
func WithDecisions(ds ...DecisionRef) Option {
	return func(o *gokind.Options) {
		o.Ranked = true
		o.Decisions = append(o.Decisions, refs(ds)...)
	}
}

// WithCollect declares the decisions of a `collect all` kind, where every fired
// decision applies, in declaration order. Calls add up, like
// WithDecisions.
func WithCollect(ds ...DecisionRef) Option {
	return func(o *gokind.Options) {
		o.Collect = true
		o.Decisions = append(o.Decisions, refs(ds)...)
	}
}

func refs(ds []DecisionRef) []gokind.Decision {
	out := make([]gokind.Decision, len(ds))
	for i, d := range ds {
		out[i] = d.ref()
	}
	return out
}

// WithDefault sets the decision and reason returned when no rule fires. Its
// payload fields take their defaults, so every field of d needs one.
func WithDefault(d DecisionRef, reason string) Option {
	name := ""
	if d != nil {
		name = d.Name() // a nil handle fails validation as an undeclared decision
	}
	return func(o *gokind.Options) {
		o.Default = &gokind.Default{Decision: name, Reason: reason}
	}
}

// WithFunc declares a host function; one call per function. Its Sigil
// signature is derived from fn's type: the parameter types, and a result
// of T or (T, error). Host functions must be pure.
func WithFunc(name string, fn any) Option {
	return func(o *gokind.Options) {
		o.Funcs = append(o.Funcs, gokind.Func{Name: name, Fn: fn})
	}
}

// Kind is a contract built from Go types. Its type parameter is the input
// struct, whose tagged fields are the inputs.
type Kind[In any] struct {
	kind    *kind.Kind
	binding *gokind.Binding
}

// NewKind builds the kind called name from the input struct In and the
// options. It panics with every problem found when the contract can't be
// exported, so a bad kind fails at init rather than at the first Load.
func NewKind[In any](name string, opts ...Option) *Kind[In] {
	o := gokind.Options{Name: name, Input: reflect.TypeOf((*In)(nil)).Elem()}
	for _, opt := range opts {
		opt(&o)
	}
	k, b, errs := gokind.Build(o)
	if errs != nil {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = "  " + e.Msg
			if e.Help != "" {
				msgs[i] += " (" + e.Help + ")"
			}
		}
		panic("policy.NewKind(" + name + "): invalid kind:\n" + strings.Join(msgs, "\n")) //nolint:nopanic // a kind that can't be exported is a programming error, caught at init like regexp.MustCompile
	}
	return &Kind[In]{kind: k, binding: b}
}

// Name returns the kind's name.
func (k *Kind[In]) Name() string { return k.kind.Name }

// Schema returns the kind as a kind file, which a policy repository
// checks in so the CLI and other services can type-check against it
// without importing the host.
func (k *Kind[In]) Schema() string { return k.kind.Source() }
