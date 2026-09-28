package policy

import (
	"reflect"

	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// None is the payload of a decision that carries only a reason.
type None struct{}

// Decision is a typed handle for one of a kind's decisions: its name, the
// reasons policies may construct it with, and the payload struct as the
// type parameter. Declare one per decision with NewDecision and pass
// them to WithDecisions or WithCollect.
type Decision[T any] struct {
	name    string
	reasons []string
}

// NewDecision declares a decision with its reasons. A constructor in a
// policy names one of the reasons, and any other name is a compile
// error, so the set is the contract, like the payload struct.
//
//	var Approve = policy.NewDecision[ApproveData]("approve", "release_manager", "payments_sre")
func NewDecision[T any](name string, reasons ...string) Decision[T] {
	return Decision[T]{name: name, reasons: append([]string{}, reasons...)}
}

// DecisionRef is any Decision[T]. It exists so decisions with different
// payload types can be listed together.
type DecisionRef interface {
	OutcomeRef
	Name() string
	ref() gokind.Decision
}

// OutcomeRef is a decision, or one reason of it, as WithExclusive takes
// them: a Decision[T] stands for any of its reasons, and
// Decision[T].Reason for one.
type OutcomeRef interface {
	outcome() kind.Outcome
}

// Outcome is one reason of a decision, from Decision[T].Reason.
type Outcome struct {
	decision string
	reason   string
}

// Matched is one entry of a decision, with its typed payload, as MatchAll
// returns it.
type Matched[T any] struct {
	Payload  T
	Reason   string
	Policy   string
	Position Position
}

// Name returns the decision's name as policies write it.
func (d Decision[T]) Name() string { return d.name }

// Reasons returns the reasons the decision declares.
func (d Decision[T]) Reasons() []string { return append([]string{}, d.reasons...) }

// Reason names one reason of the decision, for WithExclusive.
func (d Decision[T]) Reason(name string) Outcome { return Outcome{decision: d.name, reason: name} }

// Match reports whether res's outcome is exactly one entry of d and, if
// so, returns its payload as the decision's struct. On a `collect all`
// kind without `precedence`, where "the" decision isn't defined, it
// panics; use MatchAll there. With `precedence`, it returns false when
// the top rank holds more than one entry.
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
// outcome order, with typed payloads. For a `collect one` kind that's
// the winner when it is d, and nothing otherwise.
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
