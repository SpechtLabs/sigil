package policy

import (
	"reflect"

	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// None is the payload of a decision that carries only a reason, such as a
// deny:
//
//	var Deny = policy.NewDecision[policy.None]("deny", "banned", "no_rule_matched")
type None struct{}

// Decision is a typed handle for one of a kind's decisions: its name, the
// reasons policies may construct it with, and the payload struct as the
// type parameter T. Declare one per decision with [NewDecision], pass them
// to [WithDecisions] or [WithCollect], and read results back with
// [Decision.Match] and [Decision.MatchAll].
//
// T's tagged fields are the decision's payload fields, which a policy
// sets by name when it constructs the decision:
//
//	approve(release_manager, bake: 30m)
//
// A field whose tag carries a default, `policy:"bake,default=1h"`, may be
// left out. Use [None] for a decision without a payload.
type Decision[T any] struct {
	name    string
	reasons []string
}

// NewDecision declares the decision called name, with payload struct T
// and its reasons. A constructor in a policy names one of the reasons,
// and any other name is a compile error, so the set is the contract, like
// the payload struct. NewDecision copies reasons.
//
//	var Approve = policy.NewDecision[ApproveData]("approve", "release_manager", "payments_sre")
func NewDecision[T any](name string, reasons ...string) Decision[T] {
	return Decision[T]{name: name, reasons: append([]string{}, reasons...)}
}

// DecisionRef is any [Decision], whatever its payload type. It exists so
// decisions with different payload types can be passed together to
// [WithDecisions], [WithCollect] and [WithPrecedence]. Only Decision
// implements it.
type DecisionRef interface {
	OutcomeRef
	Name() string
	ref() gokind.Decision
}

// OutcomeRef is a decision, or one reason of it, as [WithExclusive] takes
// them: a [Decision] stands for any of its reasons, and an [Outcome] from
// [Decision.Reason] for one. Only those two implement it.
type OutcomeRef interface {
	outcome() kind.Outcome
}

// Outcome is one reason of a decision, from [Decision.Reason], for
// [WithExclusive].
type Outcome struct {
	decision string
	reason   string
}

// Matched is one entry of a decision, with its typed payload, as
// [Decision.MatchAll] returns it. The fields other than Payload are those
// of the [Entry] it came from.
type Matched[T any] struct {
	Payload  T        // the payload struct, defaults filled in
	Reason   string   // the reason the policy gave
	Policy   string   // the policy whose rule produced it; empty for the kind's default
	Position Position // of the constructor; unknown for the kind's default
}

// Name returns the decision's name as policies write it, such as
// "approve".
func (d Decision[T]) Name() string { return d.name }

// Reasons returns the reasons the decision declares, in declaration
// order. The slice is a copy.
func (d Decision[T]) Reasons() []string { return append([]string{}, d.reasons...) }

// Reason names one reason of the decision, for [WithExclusive]. A name the
// decision doesn't declare makes [NewKind] panic.
func (d Decision[T]) Reason(name string) Outcome { return Outcome{decision: d.name, reason: name} }

// Match reports whether res's outcome is exactly one entry of d and, if
// so, returns its payload as the decision's struct. It returns the zero T
// and false for a nil res or another decision.
//
// On a [WithCollect] kind without [WithPrecedence], where "the" decision
// isn't defined, Match panics; use [Decision.MatchAll] there. With
// WithPrecedence, it returns false when the top rank holds more than one
// entry.
//
// The result [Policy.Eval] returns alongside an error holds the kind's
// default, so matching the default decision on it succeeds. Check the
// error before matching.
func (d Decision[T]) Match(res *Result) (T, bool) {
	var zero T
	if res == nil {
		return zero, false
	}
	if res.collect && !res.ranked {
		panic("policy: Match on a collecting kind's result; use MatchAll") //nolint:nopanic // a programming error, like calling Match on the wrong type
	}
	if len(res.Outcome) != 1 || res.Outcome[0].Decision != d.name {
		return zero, false
	}
	return res.Outcome[0].typed.(T), true
}

// MatchAll returns every entry of decision d in res's outcome, in
// outcome order, with typed payloads. It is how a [WithCollect] kind,
// which can grant a decision more than once, is read. For a
// [WithDecisions] kind that's the winner when it is d, and nothing
// otherwise. It returns nil for a nil res.
func (d Decision[T]) MatchAll(res *Result) []Matched[T] {
	if res == nil {
		return nil
	}
	var out []Matched[T]
	for _, e := range res.Outcome {
		if e.Decision != d.name {
			continue
		}
		out = append(out, Matched[T]{Payload: e.typed.(T), Reason: e.Reason, Policy: e.Policy, Position: e.Position})
	}
	return out
}

// ref implements DecisionRef.
func (d Decision[T]) ref() gokind.Decision {
	return gokind.Decision{Name: d.name, Payload: reflect.TypeFor[T](), Reasons: d.reasons}
}

func (d Decision[T]) outcome() kind.Outcome { return kind.Outcome{Decision: d.name} }

func (o Outcome) outcome() kind.Outcome { return kind.Outcome{Decision: o.decision, Reason: o.reason} }
