// Package result turns one evaluation of a compiled policy into what a
// host acts on. A [Result] holds the outcome, which falls back to the
// kind's default when nothing fired or the evaluation failed, or to its
// conflict outcome after a conflict when the kind declares one, the
// trace, with the conditions that held for the candidates of the winning
// decisions, and the failure that stopped it, if any.
//
// It sits after the evaluator: [Evaluate] runs an
// [github.com/spechtlabs/sigil/internal/eval.Policy] and shapes its
// Outcome into a [Result] whose outcome follows the table under Failed
// evaluations at https://sigil.specht-labs.de/reference/evaluation/. Package
// [github.com/spechtlabs/sigil/pkg/policy] converts a Result into its
// public Result and error types; the sigil CLI reads it directly, since it
// has no host Go type to instantiate a policy.Policy with.
package result

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/token"
)

// Result is one evaluation, successful or not.
type Result struct {
	Failure *Failure // why the evaluation failed; nil when it didn't
	Policy  string   // the root policy
	// Outcome is what the host acts on: the winner, every top-ranked
	// candidate of a collecting kind, or the kind's default. After a
	// failure it's the default, or the kind's conflict outcome after a
	// conflict when it declares one, and empty for a collecting kind.
	Outcome []Entry
	Trace   []Candidate // every candidate the rules produced, winners first
	Collect bool        // the kind collects every candidate
	Ranked  bool        // the kind ranks its decisions
}

// Entry is a decision in an outcome.
type Entry struct {
	Payload  map[string]any //nolint:emptyinterface // the untyped payload, as the host's Go values
	Typed    any            //nolint:emptyinterface // the payload struct, when the kind has a binding; nil in a Candidate
	Decision string
	Reason   string
	Policy   string   // the policy whose rule produced it; empty for the default
	Position Position // of the constructor; invalid for the default
}

// Candidate is a constructor that fired. Its Entry has no Typed payload:
// only outcome entries are read as typed, by
// [github.com/spechtlabs/sigil/pkg/policy.Decision.Match] and MatchAll,
// so a candidate in the trace, a conflict or an assert's outcome carries
// only the untyped Payload.
type Candidate struct {
	Chain      []Position  // the invocations it was reached through, outermost first
	Conditions []Condition // the conditions that held, for candidates of a winning decision
	Entry
}

// Condition is a `when` condition that held.
type Condition struct {
	Text     string // the condition's source, whitespace collapsed and params replaced by their values
	Position Position
}

// Failure is why an evaluation failed. Exactly one field is set.
type Failure struct {
	Runtime  *Runtime  // a rule raised a runtime error
	Conflict *Conflict // resolution failed
	Canceled error     // the context was done before or during the evaluation; ctx.Err()
	Asserts  []Assert  // the failing asserts of the phase that stopped the evaluation
	// OutcomeAsserts says which phase Asserts come from: the outcome
	// asserts, checked once the outcome exists, or when false the input
	// asserts, checked before any rule. It qualifies Asserts and isn't one
	// of the fields of which exactly one is set.
	OutcomeAsserts bool
}

// Runtime is a runtime error.
type Runtime struct {
	Cause    error // the host function's error, or the *eval.HostPanic it raised; nil for an error in the language
	Msg      string
	Help     string // what to do about it, when the error knows better than the generic advice
	Position Position
}

// Conflict is a set of candidates the kind says can't stand together.
type Conflict struct {
	Msg        string      // what conflicts, for the error message
	Candidates []Candidate // the candidates that conflict, with the conditions that held
}

// Assert is a failing assert.
type Assert struct {
	Cause    *Runtime    // set when the condition, or an enclosing one, raised
	Reason   string      // the assert's reason, empty when it gives none
	Policy   string      // the document the assert is in
	Chain    []Position  // the invocations it was reached through, outermost first
	Outcome  []Candidate // for an outcome assert, the candidates that formed the outcome it read
	Position Position
}

// Position is a place in a bundle, with the document it's in.
type Position struct {
	File     string
	Document string
	Line     int // starting at 1; 0 when the position is unknown
	Column   int // in characters, starting at 1
}

// Evaluate evaluates prog against input, a value of the kind's input
// struct or a pointer to one, and never returns nil. When the evaluation
// fails, Failure says why and the outcome is the fallback: the kind's
// default, its conflict outcome after a conflict when it declares one, or
// empty for a collecting kind. The trace then holds every
// candidate the rules produced, which is none after failed input asserts,
// a runtime error or a cancellation. It's safe for concurrent use, as
// prog is.
func Evaluate(prog *eval.Policy, input any) *Result { //nolint:emptyinterface // the input struct, whatever its Go type
	return EvaluateContext(context.Background(), prog, input)
}

// EvaluateContext is [Evaluate] under a context, which
// [eval.Policy.EvalContext] polls while it runs. When ctx is done before
// or during the evaluation, Failure.Canceled holds ctx.Err() and the
// result is the fallback with an empty trace: nothing the rules
// produced before the cancellation is kept, so the result doesn't depend
// on when it came.
func EvaluateContext(ctx context.Context, prog *eval.Policy, input any) *Result { //nolint:emptyinterface // the input struct, whatever its Go type
	b := builder{prog: prog}
	out, err := prog.EvalContext(ctx, input)
	if err != nil {
		res := b.fallback(prog.Default(), nil)
		if rerr, ok := errors.AsType[*diag.Error](err); ok {
			res.Failure = &Failure{Runtime: b.runtime(rerr, prog.Name)}
		} else {
			res.Failure = &Failure{Canceled: err}
		}
		return res
	}
	res := b.result(out)
	if out.Conflict != nil {
		c := &Conflict{Msg: out.Conflict.Msg, Candidates: make([]Candidate, 0, len(out.Conflict.Candidates))}
		for _, cand := range out.Conflict.Candidates {
			c.Candidates = append(c.Candidates, b.candidate(cand, true))
		}
		failed := b.fallback(b.onConflict(), res.Trace)
		failed.Failure = &Failure{Conflict: c}
		return failed
	}
	if len(out.Failed) == 0 {
		return res
	}
	// Every failure comes from the phase that stopped the evaluation, so
	// the first says which one it was.
	f := &Failure{Asserts: make([]Assert, 0, len(out.Failed)), OutcomeAsserts: out.Failed[0].Assert.ReadsOutcome}
	for _, fl := range out.Failed {
		a := fl.Assert
		af := Assert{Reason: a.Reason, Policy: a.Policy, Position: at(a.File, a.Policy, a.Pos), Chain: sites(a.Chain)}
		if a.ReadsOutcome && len(out.Top) > 0 {
			af.Outcome = make([]Candidate, 0, len(out.Top))
			for _, c := range out.Top {
				af.Outcome = append(af.Outcome, b.candidate(c, true))
			}
		}
		if fl.Err != nil {
			af.Cause = b.runtime(fl.Err, a.Policy)
		}
		f.Asserts = append(f.Asserts, af)
	}
	failed := b.fallback(prog.Default(), res.Trace)
	failed.Failure = f
	return failed
}

// IsValid reports whether p names a place in a source.
func (p Position) IsValid() bool { return p.Line > 0 }

// String formats p as file:line:col, followed by the document's name in
// parentheses when the file's path doesn't already say which document it
// is: `policies.sigil:42:5 (payments.production)`, but
// `deploy/production.sigil:16:5` for the document deploy.production. A
// position without a file is line:col, and one without a line is "-".
func (p Position) String() string {
	if !p.IsValid() {
		return "-"
	}
	var b strings.Builder
	if p.File != "" {
		b.WriteString(p.File)
		b.WriteByte(':')
	}
	b.WriteString(strconv.Itoa(p.Line))
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(p.Column))
	if p.Document != "" && !diag.PathMatches(p.File, p.Document) {
		b.WriteString(" (" + p.Document + ")")
	}
	return b.String()
}

// Chain formats positions as a call chain, each as [Position.String]
// formats it, joined by arrows:
// `payments/production.sigil:14:3 → deploy/production.sigil:16:5`.
func Chain(ps []Position) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.String()
	}
	return strings.Join(parts, " → ")
}

// builder builds results for one compiled policy.
type builder struct {
	prog *eval.Policy
}

// fallback is the result of an evaluation that failed: def, the kind's
// default or its conflict outcome, or an empty outcome for a collecting
// kind, with the given trace and no Failure yet.
func (b builder) fallback(def *eval.Candidate, trace []Candidate) *Result {
	res := b.empty()
	res.Trace = trace
	if def != nil && !res.Collect {
		res.Outcome = []Entry{b.entry(def)}
	}
	return res
}

// onConflict is the outcome a conflict falls back to: the kind's
// conflict outcome when it declares one, so the result names the
// conflict instead of repeating the default's reason, and the default
// otherwise.
func (b builder) onConflict() *eval.Candidate {
	if c := b.prog.ConflictOutcome(); c != nil {
		return c
	}
	return b.prog.Default()
}

// result builds the Result for an outcome. Conditions are recorded for
// the candidates of the decisions in the outcome.
func (b builder) result(out *eval.Outcome) *Result {
	res := b.empty()
	if len(out.Candidates) > 0 {
		res.Trace = make([]Candidate, 0, len(out.Candidates))
	}
	if len(out.Top) > 0 {
		res.Outcome = make([]Entry, 0, len(out.Top))
	}
	winning := map[string]bool{}
	for _, c := range out.Top {
		winning[c.Decision.Name] = true
	}
	for _, c := range out.Candidates {
		res.Trace = append(res.Trace, b.candidate(c, winning[c.Decision.Name]))
	}
	for _, c := range out.Top {
		res.Outcome = append(res.Outcome, b.entry(c))
	}
	if len(res.Outcome) == 0 && b.prog.Default() != nil {
		res.Outcome = []Entry{b.entry(b.prog.Default())}
	}
	return res
}

func (b builder) empty() *Result {
	return &Result{Policy: b.prog.Name, Collect: b.prog.Collect(), Ranked: b.prog.Ranked()}
}

// entry converts a candidate of the outcome, with its typed payload.
func (b builder) entry(c *eval.Candidate) Entry {
	e := untyped(c)
	if c.Typed.IsValid() {
		e.Typed = c.Typed.Interface()
	}
	return e
}

// candidate converts a candidate of the trace, a conflict or an assert's
// outcome. It leaves Typed nil: boxing the payload struct copies it onto
// the heap for every candidate, and nothing reads it outside the outcome.
func (b builder) candidate(c *eval.Candidate, withConds bool) Candidate {
	out := Candidate{Entry: untyped(c), Chain: sites(c.Chain)}
	if !withConds {
		return out
	}
	out.Conditions = make([]Condition, 0, len(c.Conds))
	for _, cond := range c.Conds {
		out.Conditions = append(out.Conditions, Condition{Text: cond.Text, Position: at(c.File, c.Policy, cond.Pos)})
	}
	return out
}

// untyped converts c into an Entry without its typed payload.
func untyped(c *eval.Candidate) Entry {
	return Entry{Decision: c.Decision.Name, Reason: c.Reason, Policy: c.Policy, Payload: c.Payload, Position: at(c.File, c.Policy, c.Pos)}
}

// runtime converts a runtime error, naming the document it happened in,
// or fallback when the evaluator didn't say.
func (b builder) runtime(err *diag.Error, fallback string) *Runtime {
	doc := err.Doc
	if doc == "" {
		doc = fallback
	}
	return &Runtime{Cause: err.Cause, Msg: err.Msg, Help: err.Help, Position: at(err.File, doc, err.Pos)}
}

// sites converts a call chain.
func sites(chain []eval.Site) []Position {
	if len(chain) == 0 {
		return nil
	}
	out := make([]Position, len(chain))
	for i, s := range chain {
		out[i] = at(s.File, s.Policy, s.Pos)
	}
	return out
}

func at(file, doc string, p token.Pos) Position {
	return Position{File: file, Document: doc, Line: p.Line, Column: p.Column}
}
