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
// Outcome holds that one entry. After a failed evaluation they describe
// the fallback: the default, or the kind's [WithConflict] outcome after
// a conflict.
//
// For a [WithCollect] kind (`collect all`) those fields are empty and
// Outcome holds every candidate, or with [WithPrecedence] every candidate
// at the top rank, sorted by the kind's declaration order and then by
// position. It may be empty. Equal candidates, the same decision, reason
// and payload, fold into one entry.
//
// Policy names the evaluated policy in both modes, and Trace lists every
// candidate either way. Switch on [Result.Value] for the winner's typed
// payload and on [Result.Why] for its reason handle, or use
// [Decision.Match] and [Decision.MatchAll] for one decision.
type Result struct {
	Decision string         // the winning decision's name
	Reason   string         // its reason
	Policy   string         // the policy the host evaluated
	Payload  map[string]any //nolint:emptyinterface // the untyped payload, by field name; Value gives the typed one
	Outcome  []Entry        // the entries the host acts on
	Trace    Trace          // every candidate, with the conditions that held for the winning decision
	collect  bool           // whether the kind collects
	ranked   bool           // whether the kind has a precedence
}

// Entry is one decision in a [Result]'s outcome.
type Entry struct {
	Decision string         // the decision's name, such as "approve"
	Reason   string         // the reason the policy gave, or the default's or conflict outcome's
	Policy   string         // the policy whose rule produced it; empty for the kind's default and conflict outcome
	Payload  map[string]any //nolint:emptyinterface // the untyped payload, by field name, defaults filled in
	typed    any            // the payload struct, for Match
	Position Position       // of the constructor; unknown for the kind's default and conflict outcome
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

// Value returns the winner's payload as its decision's struct, the T of
// its [Decision], so a type switch reads the result the way one reads an
// error's type:
//
//	switch d := res.Value().(type) {
//	case PageData:
//		pageOncall(d.Target, res.Reason)
//	case NotifyData:
//		notifySlack(d.Channel, res.Reason)
//	case policy.None: // drop
//	}
//
// [NewKind] gives every decision of a kind its own payload type, so each
// case is exactly one decision.
//
// Value returns nil for a nil res, and with [WithPrecedence] when the top
// rank holds no entry or more than one. On a [WithCollect] kind without
// WithPrecedence it panics, like [Decision.Match]; switch on each entry's
// [Entry.Value] there.
//
// The result [Policy.Eval] returns alongside an error holds the kind's
// default, or its [WithConflict] outcome, so Value returns that payload.
// Check the error first.
func (r *Result) Value() any { //nolint:emptyinterface // one case per payload type, read with a type switch
	e, ok := r.single("Value")
	if !ok {
		return nil
	}
	return e.typed
}

// Why returns the winner's reason as an [Outcome], the handle
// [Decision.Reason] returns, so a switch compares it against the
// handles the way one compares an error against sentinels:
//
//	switch res.Why() {
//	case Muted, NotProduction:
//		return nil
//	case Sustained:
//		escalate(res)
//	}
//
// Outcome is comparable, and a handle equals Why's result when they name
// the same decision and reason.
//
// Why returns the zero Outcome, which equals no handle, wherever
// [Result.Value] returns nil, and panics where it panics. The result
// [Policy.Eval] returns alongside an error holds the kind's default, so
// check the error first.
func (r *Result) Why() Outcome {
	e, ok := r.single("Why")
	if !ok {
		return Outcome{}
	}
	return e.Why()
}

// Value returns the entry's payload as its decision's struct, the T of
// its [Decision]. It is how a [WithCollect] kind's outcome is read with a
// type switch, one entry at a time:
//
//	for _, e := range res.Outcome {
//		switch d := e.Value().(type) {
//		case GrantData:
//			grant(e.Reason, d.TTL)
//		case AuditorData:
//			audit(e.Reason)
//		}
//	}
func (e Entry) Value() any { return e.typed } //nolint:emptyinterface // one case per payload type, read with a type switch

// Why returns the entry's reason as an [Outcome], comparable with the
// handles [Decision.Reason] returns. See [Result.Why].
func (e Entry) Why() Outcome { return Outcome{decision: e.Decision, reason: e.Reason} }

// single returns res's only outcome entry, and false for a nil res or an
// outcome of zero or several entries. It panics on a collecting kind's
// result without a precedence, where there is no single winner to read,
// naming the method the host called.
func (r *Result) single(method string) (Entry, bool) {
	if r == nil {
		return Entry{}, false
	}
	if r.collect && !r.ranked {
		panic("policy: " + method + " on a collecting kind's result; use MatchAll or range over Outcome") //nolint:nopanic // a programming error, like calling Match on the wrong type
	}
	if len(r.Outcome) != 1 {
		return Entry{}, false
	}
	return r.Outcome[0], true
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
