package policy

import (
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
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

// WithPrecedence ranks the decisions of a WithCollect kind, highest
// first, which makes the outcome every candidate at the top rank instead
// of every candidate that fired. It must list every decision. A
// WithDecisions kind is ranked by its argument order already.
func WithPrecedence(ds ...DecisionRef) Option {
	names := make([]string, 0, len(ds))
	for _, d := range ds {
		if d != nil {
			names = append(names, d.Name())
		}
	}
	return func(o *gokind.Options) {
		o.Precedence = append(o.Precedence, names...)
	}
}

// WithReasonPrecedence ranks the reasons of one decision, highest first,
// which is what decides between two candidates of that decision. It must
// list every reason of d. A decision without a ranking has tied reasons:
// fine wherever two of them can't fire together, and a conflict under
// `collect one` where they can.
func WithReasonPrecedence(d DecisionRef, reasons ...string) Option {
	name := ""
	if d != nil {
		name = d.Name()
	}
	return func(o *gokind.Options) {
		o.Rankings = append(o.Rankings, gokind.Ranking{Decision: name, Reasons: reasons})
	}
}

// WithExclusive declares outcomes that can't fire together: decisions, or
// single reasons through Decision[T].Reason. Candidates from two of them
// in one evaluation are a conflict, checked before anything is ranked,
// under both collect modes.
//
//	policy.WithExclusive(GrantA, GrantB)
//	policy.WithExclusive(Approve.Reason("release_manager"), Approve.Reason("lgtm"))
func WithExclusive(outcomes ...OutcomeRef) Option {
	set := make([]kind.Outcome, 0, len(outcomes))
	for _, o := range outcomes {
		if o != nil {
			set = append(set, o.outcome())
		}
	}
	return func(o *gokind.Options) {
		o.Exclusive = append(o.Exclusive, set)
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
