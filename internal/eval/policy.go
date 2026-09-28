package eval

import (
	"reflect"
	"sort"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/token"
)

// Policy is a compiled policy document: its statements as a tree of
// closures over a Frame, and what's needed to resolve the candidates
// they produce into an outcome by the rules in
// docs/reference/evaluation.md.
//
// A Policy is immutable once compiled and safe for concurrent use; every
// evaluation gets its own Frame.
type Policy struct {
	kind  *kind.Kind
	scope *Scope
	def   *Candidate // the kind's default, nil for a collecting kind without one
	ranks map[string]int
	Name  string // the document's name
	File  string
	body  []*node
}

// Outcome is the result of one evaluation. For a `collect one` kind,
// Winner is the candidate that won or nil when the default applies; for
// a `collect all` kind every candidate is the outcome and Winner is nil.
// Failed holds the failing asserts of the phase that stopped the
// evaluation, by position: input asserts, in which case no rule ran and
// there are no candidates, or outcome asserts.
type Outcome struct {
	Winner     *Candidate
	Candidates []*Candidate // every candidate, sorted by rank and then position
	Failed     []Failure
}

// run is the mutable state of one evaluation.
type run struct {
	cands  []*Candidate
	failed []Failure
}

// Default returns the kind's default as a candidate without a position,
// or nil for a collecting kind that declares none.
func (p *Policy) Default() *Candidate { return p.def }

// Collect reports whether the policy's kind collects every candidate.
func (p *Policy) Collect() bool { return p.kind.Collect == kind.CollectAll }

// Eval evaluates the policy against input, a struct value of the kind's
// input type or a pointer to one, in three phases: input asserts, rules,
// outcome asserts. A failing phase ends the evaluation with its failures
// in the outcome. A runtime error in a rule comes back as the error,
// with no outcome; one in an assert is that assert's failure.
func (p *Policy) Eval(input any) (*Outcome, *diag.Error) {
	f := NewFrame(input, p.scope)
	r := &run{}

	p.walk(f, r, p.body, phaseInput)
	if len(r.failed) > 0 {
		return &Outcome{Failed: r.failures()}, nil
	}

	if err := catch(func() { p.walk(f, r, p.body, phaseRules) }); err != nil {
		err.File = p.File
		return nil, err
	}
	sort.SliceStable(r.cands, func(i, j int) bool {
		a, b := r.cands[i], r.cands[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		return before(a.Pos, b.Pos)
	})
	out := &Outcome{Candidates: r.cands}
	if !p.Collect() && len(r.cands) > 0 {
		out.Winner = r.cands[0]
	}
	f.Outcome = reflect.ValueOf(p.outcomeNames(out))

	p.walk(f, r, p.body, phaseOut)
	out.Failed = r.failures()
	return out, nil
}

// outcomeNames is the value of `outcome` for assert conditions: each
// distinct decision the host gets back, in outcome order, or the
// default's when nothing fired.
func (p *Policy) outcomeNames(out *Outcome) []string {
	names := []string{}
	switch {
	case out.Winner != nil:
		names = append(names, out.Winner.Decision.Name)
	case len(out.Candidates) > 0:
		seen := map[string]bool{}
		for _, c := range out.Candidates {
			if !seen[c.Decision.Name] {
				seen[c.Decision.Name] = true
				names = append(names, c.Decision.Name)
			}
		}
	case p.def != nil:
		names = append(names, p.def.Decision.Name)
	}
	return names
}

// walk runs one phase over the nodes. In the rules phase a runtime error
// unwinds to Eval; in the assert phases it becomes the failure of every
// assert it kept from being checked.
func (p *Policy) walk(f *Frame, r *run, nodes []*node, ph phase) {
	for _, n := range nodes {
		switch {
		case n.rule != nil && ph == phaseRules:
			r.cands = append(r.cands, fire(n.rule, f))
		case n.assert != nil && n.assert.wants(ph):
			r.check(f, n.assert)
		case n.block != nil && n.block.wants(ph):
			p.enter(f, r, n.block, ph)
		}
	}
}

// enter evaluates a block's condition and walks its body when it holds.
// A condition that raises fails every assert of the phase beneath it,
// or unwinds as a runtime error in the rules phase.
func (p *Policy) enter(f *Frame, r *run, b *block, ph phase) {
	held, err := f.cond(b)
	switch {
	case err != nil && ph == phaseRules:
		panic(err) //nolint:nopanic // a rule's runtime error unwinds to Eval, which returns it
	case err != nil:
		for _, a := range b.asserts {
			if a.wants(ph) {
				r.failed = append(r.failed, Failure{Assert: a, Err: err})
			}
		}
	case held:
		p.walk(f, r, b.body, ph)
	}
}

// check evaluates an assert's condition and records a failure when it
// doesn't hold or raises a runtime error.
func (r *run) check(f *Frame, a *Assert) {
	held := false
	if err := catch(func() { held = Bool(a.cond(f)) }); err != nil {
		r.failed = append(r.failed, Failure{Assert: a, Err: err})
		return
	}
	if !held {
		r.failed = append(r.failed, Failure{Assert: a})
	}
}

// failures returns the failures so far, sorted by position, with the
// file set on every runtime error.
func (r *run) failures() []Failure {
	sort.SliceStable(r.failed, func(i, j int) bool { return before(r.failed[i].Assert.Pos, r.failed[j].Assert.Pos) })
	for _, fl := range r.failed {
		if fl.Err != nil {
			fl.Err.File = fl.Assert.File
		}
	}
	return r.failed
}

// before orders two positions in one file: by line, then column.
func before(a, b token.Pos) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column < b.Column
}
