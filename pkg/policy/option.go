package policy

import (
	"github.com/spechtlabs/sigil/internal/gokind"
)

// Option configures NewKind.
type Option func(*gokind.Options)

// WithVersion sets the contract version, which sigil breaking compares.
// Every change to the kind bumps it.
func WithVersion(n int) Option {
	return func(o *gokind.Options) { o.Version = n }
}

// WithAccepts sets the oldest version a policy or module may pin with
// `Kind@N`. Raise it with a breaking change, so documents written for an
// older version are rejected instead of compiled against a contract they
// weren't written for. Without it, every version is accepted.
func WithAccepts(n int) Option {
	return func(o *gokind.Options) { o.Accepts = n }
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

func refs(ds []DecisionRef) []gokind.Decision {
	out := make([]gokind.Decision, len(ds))
	for i, d := range ds {
		out[i] = d.ref()
	}
	return out
}
