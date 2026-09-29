package policy

import (
	"fmt"
	"sort"
	"strings"
)

// Result is what one evaluation by [Policy.Eval] produced.
//
// For a [WithDecisions] kind (`collect one`), Decision, Reason and Payload
// describe the winner, or the kind's default when nothing fired, and
// Outcome holds that one entry.
//
// For a [WithCollect] kind (`collect all`) those fields are empty and
// Outcome holds every candidate, or with [WithPrecedence] every candidate
// at the top rank, sorted by the kind's declaration order and then by
// position. It may be empty. Equal candidates, the same decision, reason
// and payload, fold into one entry.
//
// Policy names the evaluated policy in both modes, and Trace lists every
// candidate either way. Use [Decision.Match] or [Decision.MatchAll] for
// typed payloads.
type Result struct {
	Decision string         // the winning decision's name
	Reason   string         // its reason
	Policy   string         // the policy the host evaluated
	Payload  map[string]any //nolint:emptyinterface // the untyped payload, by field name; Decision[T].Match gives the typed one
	Outcome  []Entry        // the entries the host acts on
	Trace    Trace          // every candidate, with the conditions that held for the winning decision
	collect  bool           // whether the kind collects
	ranked   bool           // whether the kind has a precedence
}

// Entry is one decision in a [Result]'s outcome.
type Entry struct {
	Decision string         // the decision's name, such as "approve"
	Reason   string         // the reason the policy gave, or the default's
	Policy   string         // the policy whose rule produced it; empty for the kind's default
	Payload  map[string]any //nolint:emptyinterface // the untyped payload, by field name, defaults filled in
	typed    any            // the payload struct, for Match
	Position Position       // of the constructor; unknown for the kind's default
}

// Trace explains a [Result]: every decision constructor evaluation
// reached, whether it won or not. `sigil eval` prints the same trace.
type Trace struct {
	Candidates []Candidate // sorted by precedence (or declaration order) and then position, so a winner comes first
}

// Candidate is a decision constructor that fired: its decision, reason
// and payload, and where it is. A rule in an invoked policy names that
// policy, with CallChain saying how evaluation got there from the
// evaluated policy.
type Candidate struct {
	Decision   string         // the decision's name
	Reason     string         // the reason the constructor gave
	Policy     string         // the policy the constructor is in
	Payload    map[string]any //nolint:emptyinterface // the untyped payload, by field name
	CallChain  []Position     // the invocations it was reached through, outermost first; empty for a rule in the evaluated policy itself
	Conditions []Condition    // the `when` conditions that held, outermost first; only for candidates of the winning decision
	Position   Position       // of the constructor in its policy
}

// Condition is a `when` condition that held on the way to a [Candidate],
// including the condition around an invocation that reached it.
type Condition struct {
	Text     string // the condition as written, on one line
	Position Position
}

// Location renders the candidate's whole call chain and position, each
// step as [Position.String] renders it, for example
//
//	payments/production.sigil:14:3 → deploy/production.sigil:16:5
func (c Candidate) Location() string {
	return chain(c.CallChain, c.Position)
}

// String renders the candidate on one line, as a trace prints it: the
// decision, the reason, [Candidate.Location], and the payload with its
// fields sorted by name.
//
//	approve release_manager at deploy.sigil:12:5 {bake: 1h0m0s}
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
