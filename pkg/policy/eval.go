package policy

import (
	"context"

	"github.com/spechtlabs/sigil/internal/eval"
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
		return p.fallback(nil), err //nolint:errorwrap,humaneerror // returned as is, so errors.Is(err, context.Canceled) holds
	}
	out, rerr := p.prog.Eval(&input)
	if rerr != nil {
		return p.fallback(nil), &RuntimeError{Message: rerr.Msg, Policy: p.name, Position: position(rerr.File, p.name, rerr.Pos)}
	}
	res := p.result(out)
	if len(out.Failed) == 0 {
		return res, nil
	}
	ae := &AssertionError{}
	for _, fl := range out.Failed {
		a := fl.Assert
		f := AssertFailure{Reason: a.Reason, Policy: a.Policy, Position: position(a.File, a.Policy, a.Pos)}
		if a.ReadsOutcome {
			f.Outcome = p.outcomeCandidates(out)
		}
		if fl.Err != nil {
			f.Cause = &RuntimeError{Message: fl.Err.Msg, Policy: p.name, Position: position(fl.Err.File, a.Policy, fl.Err.Pos)}
		}
		ae.Failures = append(ae.Failures, f)
	}
	return p.fallback(res.Trace.Candidates), ae
}

// result builds the Result for an outcome.
func (p *Policy[In]) result(out *eval.Outcome) *Result {
	res := &Result{Policy: p.name, collect: p.prog.Collect()}
	for _, c := range out.Candidates {
		res.Trace.Candidates = append(res.Trace.Candidates, p.candidate(c, res.collect || (out.Winner != nil && c.Decision == out.Winner.Decision)))
	}
	if res.collect {
		for _, c := range out.Candidates {
			res.Outcome = append(res.Outcome, p.entry(c))
		}
		if len(res.Outcome) == 0 && p.prog.Default() != nil {
			res.Outcome = []Entry{p.entry(p.prog.Default())}
		}
		return res
	}
	winner := out.Winner
	if winner == nil {
		winner = p.prog.Default()
	}
	e := p.entry(winner)
	res.entry = &e
	res.Decision, res.Reason, res.Payload = e.Decision, e.Reason, e.Payload
	res.Outcome = []Entry{e}
	return res
}

// fallback is the result Eval returns with an error: the kind's default,
// or an empty outcome for a collecting kind, with the given trace.
func (p *Policy[In]) fallback(trace []Candidate) *Result {
	res := &Result{Policy: p.name, collect: p.prog.Collect(), Trace: Trace{Candidates: trace}}
	if res.collect {
		return res
	}
	e := p.entry(p.prog.Default())
	res.entry = &e
	res.Decision, res.Reason, res.Payload = e.Decision, e.Reason, e.Payload
	res.Outcome = []Entry{e}
	return res
}

// outcomeCandidates returns the candidates that formed the outcome an
// assert read: the winner, or every candidate for a collecting kind.
func (p *Policy[In]) outcomeCandidates(out *eval.Outcome) []Candidate {
	var cs []Candidate
	switch {
	case p.prog.Collect():
		for _, c := range out.Candidates {
			cs = append(cs, p.candidate(c, true))
		}
	case out.Winner != nil:
		cs = append(cs, p.candidate(out.Winner, true))
	}
	return cs
}

func (p *Policy[In]) entry(c *eval.Candidate) Entry {
	e := Entry{Decision: c.Decision.Name, Reason: c.Reason, Policy: c.Policy, Payload: c.Payload, Position: position(c.File, c.Policy, c.Pos)}
	if c.Typed.IsValid() {
		e.typed = c.Typed.Interface()
	}
	return e
}

func (p *Policy[In]) candidate(c *eval.Candidate, withConds bool) Candidate {
	out := Candidate{Decision: c.Decision.Name, Reason: c.Reason, Policy: c.Policy, Payload: c.Payload, Position: position(c.File, c.Policy, c.Pos)}
	if !withConds {
		return out
	}
	out.Conditions = make([]Condition, 0, len(c.Conds))
	for _, cond := range c.Conds {
		out.Conditions = append(out.Conditions, Condition{Text: cond.Text, Position: position(c.File, c.Policy, cond.Pos)})
	}
	return out
}
