package policy

import (
	"context"

	"github.com/spechtlabs/sigil/internal/result"
)

// Eval evaluates the policy against one input and returns the result
// with its trace. It's safe to call from any goroutine.
//
// On a runtime error, the error is a *RuntimeError and the result holds
// the kind's default (an empty outcome for a collecting kind), so a host
// that fails closed can use the result directly. On a failed assert the
// error is an *AssertionError, the result holds the default the same way,
// and its trace lists every candidate the rules produced: none when an
// input assert failed, since no rule ran. A context that's already done
// returns its error with the default result, without evaluating.
func (p *Policy[In]) Eval(ctx context.Context, input In) (*Result, error) {
	if err := ctx.Err(); err != nil {
		res, _ := convert(result.Fallback(p.prog, nil))
		return res, err //nolint:errorwrap,humaneerror // returned as is, so errors.Is(err, context.Canceled) holds
	}
	return convert(result.Evaluate(p.prog, &input))
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
	case f.Runtime != nil:
		return runtimeError(policy, f.Runtime)
	case f.Conflict != nil:
		ce := &ConflictError{Message: f.Conflict.Msg, Policy: policy, Candidates: make([]Candidate, 0, len(f.Conflict.Candidates))}
		for _, c := range f.Conflict.Candidates {
			ce.Candidates = append(ce.Candidates, candidate(c))
		}
		return ce
	}
	ae := &AssertionError{Failures: make([]AssertFailure, 0, len(f.Asserts))}
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

func runtimeError(policy string, r *result.Runtime) *RuntimeError {
	return &RuntimeError{Message: r.Msg, Policy: policy, Position: Position(r.Position)}
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
