package policy

import (
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// Option configures [NewKind]. Each option corresponds to a declaration
// of a kind file, which [Kind.Schema] writes out. Options that take
// several values add up across calls.
type Option func(*gokind.Options)

// WithVersion sets the contract version, `kind Name version n` in a kind
// file. It is required and at least 1. Every change to the kind bumps it,
// and `sigil breaking` compares two versions of a kind file to report the
// changes that break existing policies.
func WithVersion(n int) Option {
	return func(o *gokind.Options) { o.Version = n }
}

// WithAccepts sets the oldest version a policy or module may pin with
// `Kind@N`, `accepts: n` in a kind file. Raise it with a breaking change,
// so documents written for an older version are rejected instead of
// compiled against a contract they weren't written for. Without it, every
// version is accepted; n is from 1 to the kind's version.
func WithAccepts(n int) Option {
	return func(o *gokind.Options) { o.Accepts = &n }
}

// WithDecisions declares the decisions of a `collect one` kind in
// precedence order, highest first. An evaluation's outcome is the single
// candidate of the highest-ranked decision that fired, or the kind's
// [WithDefault] when none did, which makes WithDefault required.
//
// Calls add up: WithDecisions(a, b) and WithDecisions(a), WithDecisions(b)
// declare the same kind. A kind uses WithDecisions or [WithCollect], not
// both.
func WithDecisions(ds ...DecisionRef) Option {
	return func(o *gokind.Options) {
		o.Ranked = true
		o.Decisions = append(o.Decisions, refs(ds)...)
	}
}

// WithCollect declares the decisions of a `collect all` kind, where every
// fired decision applies. The outcome lists the candidates in the order
// the decisions are declared here, then by position, and may be empty; use
// [Decision.MatchAll] to read it. [WithPrecedence] narrows the outcome to
// the top rank. A collecting kind may leave out [WithDefault].
//
// Calls add up, like [WithDecisions]. A kind uses WithCollect or
// WithDecisions, not both.
func WithCollect(ds ...DecisionRef) Option {
	return func(o *gokind.Options) {
		o.Collect = true
		o.Decisions = append(o.Decisions, refs(ds)...)
	}
}

// WithPrecedence ranks the decisions of a [WithCollect] kind, highest
// first, which makes the outcome every candidate at the top rank instead
// of every candidate that fired. It must list every decision. A
// [WithDecisions] kind is ranked by its argument order already, and
// passing WithPrecedence to one makes [NewKind] panic.
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
// `precedence approve: release_manager > payments_sre` in a kind file.
// The ranking decides between two candidates of that decision. Each
// reason is an [Outcome] from [Decision.Reason]; the list must name every
// reason of one decision and only its reasons, once per decision, since
// decisions rank against each other through [WithDecisions] or
// [WithPrecedence]. A decision without a ranking has tied reasons: fine
// wherever two of them can't fire together, and a [*ConflictError] under
// `collect one` where they can.
//
//	policy.WithReasonPrecedence(Approve.Reason("release_manager"), Approve.Reason("payments_sre"))
func WithReasonPrecedence(reasons ...Outcome) Option {
	r := gokind.Ranking{}
	for i, o := range reasons {
		switch {
		case i == 0:
			r.Decision = o.decision
		case o.decision != r.Decision:
			r.Mixed = append(r.Mixed, o.outcome())
			continue
		}
		r.Reasons = append(r.Reasons, o.reason)
	}
	return func(o *gokind.Options) {
		o.Rankings = append(o.Rankings, r)
	}
}

// WithExclusive declares outcomes that can't fire together: decisions, or
// single reasons through [Decision.Reason]. Candidates from two of them in
// one evaluation are a [*ConflictError], checked before anything is
// ranked, under both collect modes. Each call declares one exclusive set,
// which names at least two outcomes.
//
//	policy.WithExclusive(GrantA, GrantB)
//	policy.WithExclusive(Approve.Reason("release_manager"), Approve.Reason("payments_sre"))
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

// WithDefault sets the decision and reason returned when no rule fires,
// `default deny(no_rule_matched)` in a kind file, given as an [Outcome]
// from [Decision.Reason]. Its payload fields take their defaults, so
// every field of the decision's payload struct needs a `default=` in its
// tag. It is required for a [WithDecisions] kind and optional for a
// [WithCollect] kind.
//
// [Policy.Eval] also returns the default alongside a runtime error, a
// conflict or a failed assert, so a host that fails closed can use the
// result directly. A kind with [WithConflict] returns its conflict
// outcome for a conflict instead.
//
//	policy.WithDefault(Deny.Reason("no_rule_matched"))
func WithDefault(reason Outcome) Option {
	return func(o *gokind.Options) {
		// The zero Outcome fails validation as an undeclared decision.
		o.Default = &gokind.Default{Decision: reason.decision, Reason: reason.reason}
	}
}

// WithConflict sets the decision and reason a [WithDecisions] kind
// returns when resolution ends in a conflict, `conflict
// deny(conflicting_rules)` in a kind file, given as an [Outcome] from
// [Decision.Reason]. Like [WithDefault], its payload fields take their
// defaults, so every field of the decision's payload struct needs a
// `default=` in its tag. It's optional; without it a conflict returns the
// kind's default.
//
// [Policy.Eval] still returns the [*ConflictError], and the result's trace
// still lists every candidate. What changes is the outcome that comes
// with the error: with the default, a log line or a dashboard that reads
// the result reports "no rule matched" for an evaluation where several
// rules fired, and a dedicated reason says what happened.
//
// A [WithCollect] kind can't take it, and [NewKind] panics: a collecting
// kind returns an empty outcome on every failed evaluation, because
// granting anything on a defect in the policy would fail open.
//
//	policy.WithConflict(Deny.Reason("conflicting_rules"))
func WithConflict(reason Outcome) Option {
	return func(o *gokind.Options) {
		// The zero Outcome fails validation as an undeclared decision.
		o.Conflict = &gokind.Default{Decision: reason.decision, Reason: reason.reason}
	}
}

// WithFunc declares a host function that policies call by name; one call
// per function. Its Sigil signature is derived from fn's type: the
// parameter types, and a result of T or (T, error), mapped like input
// fields. WithFunc("split", strings.Split) declares
//
//	fn split(string, string) -> list<string>
//
// An error the function returns becomes a [*RuntimeError] whose Err is
// that error.
//
// The name is written out rather than derived from fn, because it is part
// of the policy contract: renaming a Go function must not rename it in
// every policy.
//
// Host functions must be pure, must terminate and must not panic.
// [Policy.Eval] can't interrupt a function that never returns, though it
// stops once the function returns after the context is done. A panic
// propagates to the caller unless the kind sets [WithRecoverHostPanics].
func WithFunc(name string, fn any) Option {
	return func(o *gokind.Options) {
		o.Funcs = append(o.Funcs, gokind.Func{Name: name, Fn: fn})
	}
}

// WithRecoverHostPanics makes a panic in a host function fail the
// evaluation instead of unwinding out of [Policy.Eval]: Eval returns a
// [*RuntimeError] naming the function and the panic value, whose Err is
// a [*HostPanicError] with the stack, and the result holds the kind's
// default, as when the function returns an error.
//
// It's off by default because a panic is a bug in the host, and the
// usual Go answer is to let it surface where it happened: net/http
// already recovers a handler's panic and logs it with the stack. Turn it
// on where nothing above Eval recovers, such as a queue consumer or a
// reconcile loop, so one bad input fails closed instead of killing the
// worker.
func WithRecoverHostPanics() Option {
	return func(o *gokind.Options) { o.RecoverHostPanics = true }
}

func refs(ds []DecisionRef) []gokind.Decision {
	out := make([]gokind.Decision, len(ds))
	for i, d := range ds {
		out[i] = d.ref()
	}
	return out
}
