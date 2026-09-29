package policy

import (
	"context"

	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/result"
)

// Eval evaluates the policy against one input and returns the result
// with its trace. It's safe to call from any goroutine.
//
// Eval never returns a nil [*Result]. On an error, the result holds the
// kind's default, or an empty outcome for a collecting kind, so a host
// that fails closed can use it directly. The error is one of:
//
//   - [*RuntimeError], when an expression failed, such as a list index out
//     of range, or a host function returned an error, or panicked under
//     [WithRecoverHostPanics].
//   - [*ConflictError], when two candidates can't both stand; its
//     Candidates field names them.
//   - [*AssertionError], when an assert failed. The result's trace lists
//     every candidate the rules produced: none when an input assert
//     failed, since no rule ran.
//   - ctx.Err(), unwrapped, when ctx is done before or during the
//     evaluation. The result's trace is then empty, however far the
//     evaluation got.
//
// Eval checks ctx before it starts, before every rule and assert, after
// every host function call, and every few hundred elements a quantifier,
// filter or list operator goes through, so a deadline cuts off nested
// quantifiers over a large input. It can't interrupt a host function
// that doesn't return. Host functions run on the calling goroutine, and a
// panic in one propagates out of Eval unless the kind sets
// [WithRecoverHostPanics].
func (p *Policy[In]) Eval(ctx context.Context, input In) (*Result, error) {
	return convert(result.EvaluateContext(ctx, p.prog, &input))
}

// convert turns an evaluation into the public Result and the error that
// comes with it.
func convert(r *result.Result) (*Result, error) {
	res := &Result{Policy: r.Policy, collect: r.Collect, ranked: r.Ranked}
	if len(r.Trace) > 0 {
		res.Trace.Candidates = make([]Candidate, 0, len(r.Trace))
	}
	if len(r.Outcome) > 0 {
		res.Outcome = make([]Entry, 0, len(r.Outcome))
	}
	for _, c := range r.Trace {
		res.Trace.Candidates = append(res.Trace.Candidates, candidate(c))
	}
	for _, e := range r.Outcome {
		res.Outcome = append(res.Outcome, entry(e))
	}
	if !res.collect && len(res.Outcome) == 1 {
		e := res.Outcome[0]
		res.Decision, res.Reason, res.Payload = e.Decision, e.Reason, e.Payload
	}
	if r.Failure == nil {
		return res, nil
	}
	return res, failure(r.Policy, r.Failure)
}

// failure converts why an evaluation failed into its error type.
func failure(policy string, f *result.Failure) error {
	switch {
	case f.Canceled != nil:
		return f.Canceled //nolint:errorwrap,humaneerror // returned as is, so errors.Is(err, context.Canceled) holds
	case f.Runtime != nil:
		return runtimeError(policy, f.Runtime)
	case f.Conflict != nil:
		ce := &ConflictError{Message: f.Conflict.Msg, Policy: policy, Candidates: make([]Candidate, 0, len(f.Conflict.Candidates))}
		for _, c := range f.Conflict.Candidates {
			ce.Candidates = append(ce.Candidates, candidate(c))
		}
		return ce
	}
	ae := &AssertionError{Failures: make([]AssertFailure, 0, len(f.Asserts)), Phase: InputAsserts}
	if f.OutcomeAsserts {
		ae.Phase = OutcomeAsserts
	}
	for _, a := range f.Asserts {
		af := AssertFailure{Reason: a.Reason, Policy: a.Policy, Position: Position(a.Position), CallChain: positions(a.Chain)}
		if len(a.Outcome) > 0 {
			af.Outcome = make([]Candidate, 0, len(a.Outcome))
		}
		for _, c := range a.Outcome {
			af.Outcome = append(af.Outcome, candidate(c))
		}
		if a.Cause != nil {
			af.Cause = runtimeError(policy, a.Cause)
		}
		ae.Failures = append(ae.Failures, af)
	}
	return ae
}

// runtimeError converts a runtime error, turning the evaluator's record
// of a recovered host panic into a [*HostPanicError].
func runtimeError(policy string, r *result.Runtime) *RuntimeError {
	e := &RuntimeError{Message: r.Msg, Help: r.Help, Policy: policy, Position: Position(r.Position), Err: r.Cause}
	if p, ok := r.Cause.(*eval.HostPanic); ok {
		e.Err = &HostPanicError{Value: p.Value, Func: p.Func, Stack: p.Stack}
	}
	return e
}

func entry(e result.Entry) Entry {
	return Entry{Decision: e.Decision, Reason: e.Reason, Policy: e.Policy, Payload: e.Payload, Position: Position(e.Position), typed: e.Typed}
}

func candidate(c result.Candidate) Candidate {
	out := Candidate{Decision: c.Decision, Reason: c.Reason, Policy: c.Policy, Payload: c.Payload, Position: Position(c.Position), CallChain: positions(c.Chain)}
	if c.Conditions != nil {
		out.Conditions = make([]Condition, len(c.Conditions))
		for i, cond := range c.Conditions {
			out.Conditions[i] = Condition{Text: cond.Text, Position: Position(cond.Position)}
		}
	}
	return out
}

func positions(ps []result.Position) []Position {
	if len(ps) == 0 {
		return nil
	}
	out := make([]Position, len(ps))
	for i, p := range ps {
		out[i] = Position(p)
	}
	return out
}
