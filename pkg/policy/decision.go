package policy

import (
	"reflect"

	"github.com/spechtlabs/sigil/internal/gokind"
)

// None is the payload of a decision that carries only a reason.
type None struct{}

// Decision is a typed handle for one of a kind's decisions: its name,
// with the payload struct as the type parameter. Declare one per decision
// and pass them to Decisions or Collect.
//
//	var Review = policy.Decision[ReviewData]("review")
type Decision[T any] string

// DecisionRef is any Decision[T]. It exists so decisions with different
// payload types can be listed together.
type DecisionRef interface {
	Name() string
	ref() gokind.Decision
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
func (d Decision[T]) Name() string { return string(d) }

// Match reports whether res's decision is d and, if so, returns its
// payload as the decision's struct. It panics on a collecting kind's
// result, where "the" decision isn't defined; use MatchAll there.
func (d Decision[T]) Match(res *Result) (T, bool) {
	var zero T
	if res == nil {
		return zero, false
	}
	if res.collect {
		panic("policy: Match on a collecting kind's result; use MatchAll") //nolint:nopanic // a programming error, like calling Match on the wrong type
	}
	if res.entry == nil || res.entry.Decision != string(d) {
		return zero, false
	}
	return res.entry.typed.(T), true
}

// MatchAll returns every entry of decision d in res's outcome, in
// outcome order, with typed payloads. For a kind with precedence that's
// the winner when it is d, and nothing otherwise.
func (d Decision[T]) MatchAll(res *Result) []Matched[T] {
	if res == nil {
		return nil
	}
	var out []Matched[T]
	for _, e := range res.Outcome {
		if e.Decision != string(d) {
			continue
		}
		out = append(out, Matched[T]{Payload: e.typed.(T), Reason: e.Reason, Policy: e.Policy, Position: e.Position})
	}
	return out
}

// ref implements DecisionRef.
func (d Decision[T]) ref() gokind.Decision {
	return gokind.Decision{Name: string(d), Payload: reflect.TypeOf((*T)(nil)).Elem()}
}
