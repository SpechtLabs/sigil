package eval

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

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

// Outcome is the result of one evaluation. Candidates is every
// constructor that fired, sorted by rank and then position. Top is what
// resolution left at the top rank, the outcome the host gets: one
// candidate for a `collect one` kind, every top-ranked one for `collect
// all`, and none when nothing fired, when the default applies. Conflict
// is set when resolution failed, and Failed holds the failing asserts of
// the phase that stopped the evaluation.
type Outcome struct {
	Conflict   *Conflict
	Candidates []*Candidate
	Top        []*Candidate
	Failed     []Failure
}

// Conflict is a set of candidates the kind says can't stand together:
// members of an exclusive set, or several candidates at the top rank of
// a `collect one` kind.
type Conflict struct {
	Msg        string
	Candidates []*Candidate
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

// Ranked reports whether the policy's kind ranks decisions with a
// precedence.
func (p *Policy) Ranked() bool { return len(p.kind.Precedence) > 0 }

// Eval evaluates the policy against input, a struct value of the kind's
// input type or a pointer to one, in three phases: input asserts, rules,
// outcome asserts. A failing phase, or a conflict, ends the evaluation
// with the failure in the outcome. A runtime error in a rule comes back
// as the error, with no outcome; one in an assert is that assert's
// failure.
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
		if a.rrank != b.rrank {
			return a.rrank < b.rrank
		}
		return before(a.Pos, b.Pos)
	})
	out := &Outcome{Candidates: r.cands}
	out.Top, out.Conflict = p.resolve(r.cands)
	if out.Conflict != nil {
		return out, nil
	}
	f.Outcome = reflect.ValueOf(p.outcomeNames(out))

	p.walk(f, r, p.body, phaseOut)
	out.Failed = r.failures()
	return out, nil
}

// resolve turns the sorted candidates into the outcome: fold equal
// candidates, check the exclusive sets, then take the top rank, which a
// `collect one` kind needs to be a single candidate.
func (p *Policy) resolve(cands []*Candidate) ([]*Candidate, *Conflict) {
	folded := fold(cands)
	if c := p.exclusive(folded); c != nil {
		return nil, c
	}
	if !p.Ranked() {
		return folded, nil
	}
	var top []*Candidate
	for _, c := range folded {
		if c.rank != folded[0].rank || c.rrank != folded[0].rrank {
			break
		}
		top = append(top, c)
	}
	if !p.Collect() && len(top) > 1 {
		return nil, &Conflict{Msg: fmt.Sprintf("collect one: %d candidates at the top rank", len(top)), Candidates: top}
	}
	return top, nil
}

// fold drops every candidate equal to an earlier one in decision, reason
// and payload: they're one outcome, from several branches.
func fold(cands []*Candidate) []*Candidate {
	var out []*Candidate
next:
	for _, c := range cands {
		for _, kept := range out {
			if kept.Decision == c.Decision && kept.Reason == c.Reason && reflect.DeepEqual(kept.Payload, c.Payload) {
				continue next
			}
		}
		out = append(out, c)
	}
	return out
}

// exclusive returns the first exclusive set with candidates from two of
// its members, as a conflict.
func (p *Policy) exclusive(cands []*Candidate) *Conflict {
	for _, set := range p.kind.Exclusive {
		members := 0
		var hit []*Candidate
		for _, o := range set {
			matched := matching(o, cands)
			hit = append(hit, matched...)
			if len(matched) > 0 {
				members++
			}
		}
		if members >= 2 {
			names := make([]string, len(set))
			for i, o := range set {
				names[i] = o.String()
			}
			return &Conflict{Msg: "exclusive " + strings.Join(names, ", ") + ": more than one fired", Candidates: hit}
		}
	}
	return nil
}

// matching returns the candidates the outcome names.
func matching(o kind.Outcome, cands []*Candidate) []*Candidate {
	var out []*Candidate
	for _, c := range cands {
		if o.Matches(c.Decision.Name, c.Reason) {
			out = append(out, c)
		}
	}
	return out
}

// outcomeNames is the value of `outcome` for assert conditions: each
// distinct outcome the host gets back as `decision.reason`, in outcome
// order, or the default's when nothing fired.
func (p *Policy) outcomeNames(out *Outcome) []string {
	names := []string{}
	seen := map[string]bool{}
	for _, c := range out.Top {
		if !seen[c.Outcome()] {
			seen[c.Outcome()] = true
			names = append(names, c.Outcome())
		}
	}
	if len(names) == 0 && p.def != nil {
		names = append(names, p.def.Outcome())
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
