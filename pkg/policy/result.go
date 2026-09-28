package policy

import (
	"fmt"
	"sort"
	"strings"
)

// Result is what one evaluation produced. For a kind with precedence,
// Decision, Reason, Policy and Payload describe the winner, or the
// kind's default when nothing fired, and Outcome holds that one entry.
// For a collecting kind the single fields are empty and Outcome holds
// every candidate that fired, sorted by the kind's declaration order and
// then by position; it may be empty. Trace lists every candidate either
// way.
type Result struct {
	Decision string         // the winning decision's name
	Reason   string         // its reason literal
	Policy   string         // the policy the host evaluated
	Payload  map[string]any //nolint:emptyinterface // the untyped payload, by field name; Decision[T].Match gives the typed one
	entry    *Entry         // the winner, for Match; nil for a collecting kind
	Outcome  []Entry        // the entries the host acts on
	Trace    Trace          // every candidate, with the conditions that held for the winning decision
	collect  bool           // whether the kind collects
}

// Entry is one decision in an outcome.
type Entry struct {
	Decision string
	Reason   string
	Policy   string         // the policy whose rule produced it; empty for the kind's default
	Payload  map[string]any //nolint:emptyinterface // the untyped payload, by field name, defaults filled in
	typed    any            // the payload struct, for Match
	Position Position       // of the constructor; unknown for the kind's default
}

// Trace explains a result: every constructor evaluation reached, whether
// it won or not.
type Trace struct {
	Candidates []Candidate // sorted by precedence (or declaration order) and then position, so a winner comes first
}

// Candidate is a decision constructor that fired.
type Candidate struct {
	Decision   string
	Reason     string
	Policy     string         // the policy the constructor is in
	Payload    map[string]any //nolint:emptyinterface // the untyped payload, by field name
	CallChain  []Position     // the invocations it was reached through, outermost first; empty until composition lands
	Conditions []Condition    // the `when` conditions that held, outermost first; only for candidates of the winning decision
	Position   Position       // of the constructor in its policy
}

// Condition is a `when` condition that held on the way to a candidate.
type Condition struct {
	Text     string // the condition as written, on one line
	Position Position
}

// Location renders the candidate's whole call chain and position, for
// example `payments/production.sigil:14:3 → deploy/production.sigil:16:5`.
func (c Candidate) Location() string {
	return chain(append(append([]Position(nil), c.CallChain...), c.Position))
}

// String renders the candidate on one line, as a trace prints it.
func (c Candidate) String() string {
	s := c.Decision + " " + c.Reason + " at " + c.Location()
	if len(c.Payload) > 0 {
		s += " " + formatPayload(c.Payload)
	}
	return s
}

// formatPayload renders a payload as `{a: x, b: y}`, fields sorted.
func formatPayload(p map[string]any) string { //nolint:emptyinterface // payload values are the host's Go values
	parts := make([]string, 0, len(p))
	for name, v := range p {
		parts = append(parts, fmt.Sprintf("%s: %v", name, v))
	}
	sortStrings(parts)
	return "{" + strings.Join(parts, ", ") + "}"
}

func sortStrings(xs []string) { sort.Strings(xs) }
