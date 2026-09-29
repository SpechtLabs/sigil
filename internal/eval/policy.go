package eval

import (
	stdcmp "cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/token"
)

// Policy is a compiled policy: the root document's statements as a tree
// of closures over a Frame, with every invoked policy instantiated
// inside it, and what's needed to resolve the candidates they produce
// into an outcome by the rules at https://sigil.specht-labs.de/reference/evaluation/.
//
// A Policy is immutable once compiled and safe for concurrent use; every
// evaluation gets its own frames.
type Policy struct {
	kind    *kind.Kind
	root    *instance
	def     *Candidate // the kind's default, nil for a collecting kind without one
	ranks   map[string]int
	Name    string // the root document's name
	File    string // the file the root document is in
	nframes int    // frame slots assigned to instances at compile time
	static  bool
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
	Msg        string       // what conflicts, for the error message
	Candidates []*Candidate // every candidate of the exclusive set's members, or every one at the top rank
}

// Requirement is how the root reaches a policy: through top-level
// invocations only, which is what
// [github.com/spechtlabs/sigil/pkg/policy.Require] asks for, or only
// through gated ones, or not at all.
type Requirement struct {
	Gated         []Site // invocations under a `when`, when none is unconditional
	Invoked       bool   // some invocation reaches the policy
	Unconditional bool   // some chain of invocations with no `when` on it reaches the policy
}

// run is the mutable state of one evaluation: the frames of every
// instance reached, indexed by its compiled slot, what the phases
// collected, and the context that can end it early.
type run struct {
	// ctx is the evaluation's context, or nil when it can never be done,
	// like context.Background(), so a poll has nothing to do.
	ctx     context.Context //nolint:containedctx // one evaluation's context, polled while it runs
	frames  []*Frame
	input   Value
	outcome Value
	top     []*Candidate // what `outcome.<decision>` reads
	cands   []*Candidate
	failed  []Failure
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
// with the failure in the outcome: failed input asserts leave only
// Failed set, and a conflict leaves Candidates and Conflict with no
// outcome assert checked. A runtime error in a rule comes back as the
// error, with no outcome; one in an assert is that assert's failure.
//
// Eval doesn't check input's Go type, which must be the binding's input
// struct. A policy compiled with [Options.Static] returns an error. A
// panic in a host function isn't recovered unless the binding sets
// [gokind.Binding.RecoverHostPanics]. Eval is safe to call from several
// goroutines at once.
func (p *Policy) Eval(input any) (*Outcome, *diag.Error) {
	out, err := p.EvalContext(context.Background(), input)
	if err != nil {
		// A background context never ends, so the error is a runtime error.
		derr, _ := errors.AsType[*diag.Error](err)
		return nil, derr
	}
	return out, nil
}

// EvalContext is [Policy.Eval] under a context. It polls ctx before it
// starts, before every rule and assert, after every host function call,
// and every few hundred steps of a loop over a list or map, and stops
// once ctx is done. The error is then ctx.Err(), unwrapped, with no
// outcome, so nothing half-evaluated reaches the host. Any other error
// is a runtime error in a rule, a *[diag.Error].
func (p *Policy) EvalContext(ctx context.Context, input any) (out *Outcome, err error) { //nolint:humaneerror // a *diag.Error or the context's own error, which callers tell apart
	if p.static {
		return nil, &diag.Error{File: p.File, Msg: "policy was compiled for explanation only and can't be evaluated"}
	}
	if done := ctx.Err(); done != nil {
		return nil, done //nolint:errorwrap // returned as is, so errors.Is(err, context.Canceled) holds
	}
	defer func() {
		if rec := recover(); rec != nil {
			c, ok := rec.(*canceled)
			if !ok {
				panic(rec) //nolint:nopanic // not ours: re-raise it unchanged
			}
			out, err = nil, c.err
		}
	}()
	return p.evaluate(ctx, input)
}

// evaluate is the body of EvalContext, which recovers a cancellation
// from it.
func (p *Policy) evaluate(ctx context.Context, input any) (*Outcome, error) {
	v := reflect.ValueOf(input)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	r := &run{frames: make([]*Frame, p.nframes), input: v}
	if ctx.Done() != nil {
		r.ctx = ctx
	}
	f := r.frame(p.root)

	p.walk(f, r, p.root.body, phaseInput)
	if len(r.failed) > 0 {
		return &Outcome{Failed: r.failures()}, nil
	}

	if err := catch(func() { p.walk(f, r, p.root.body, phaseRules) }); err != nil {
		return nil, err
	}
	slices.SortStableFunc(r.cands, func(a, b *Candidate) int {
		if a.rank != b.rank {
			return stdcmp.Compare(a.rank, b.rank)
		}
		if a.rrank != b.rrank {
			return stdcmp.Compare(a.rrank, b.rrank)
		}
		return compareAt(a.Chain, a.Pos, b.Chain, b.Pos)
	})
	out := &Outcome{Candidates: r.cands}
	out.Top, out.Conflict = p.resolve(r.cands)
	if out.Conflict != nil {
		return out, nil
	}
	r.setOutcome(reflect.ValueOf(p.outcomeNames(out)), p.outcomeCandidates(out))

	p.walk(f, r, p.root.body, phaseOut)
	out.Failed = r.failures()
	return out, nil
}

// frame returns the instance's frame in this evaluation, creating it on
// first use.
func (r *run) frame(inst *instance) *Frame {
	if f := r.frames[inst.index]; f != nil {
		return f
	}
	f := newFrame(r.input, inst.scope)
	f.run = r
	f.file, f.doc = inst.file, inst.name
	f.Outcome, f.Candidates = r.outcome, r.top
	r.frames[inst.index] = f
	return f
}

// poll ends the evaluation when its context is done. The panic isn't a
// *[diag.Error], so every catch passes it on: a cancellation never
// becomes a runtime error, an assert's failure or a condition's memo,
// and only [Policy.EvalContext] recovers it.
func (r *run) poll() {
	if r.ctx == nil {
		return
	}
	select {
	case <-r.ctx.Done():
		panic(&canceled{err: r.ctx.Err()}) //nolint:nopanic // unwinds to EvalContext, which returns the context's error
	default:
	}
}

// setOutcome makes the outcome visible to every frame, for the outcome
// asserts: the decision values and the candidates behind them.
func (r *run) setOutcome(v Value, top []*Candidate) {
	r.outcome, r.top = v, top
	for _, f := range r.frames {
		if f != nil {
			f.Outcome, f.Candidates = v, top
		}
	}
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

// fireIn fires a rule in f, naming the rule's file on a runtime error.
func fireIn(r *Rule, f *Frame) *Candidate {
	var c *Candidate
	if err := catch(func() { c = fire(r, f) }); err != nil {
		if err.File == "" {
			err.File, err.Doc = r.File, r.Policy
		}
		panic(err) //nolint:nopanic // a rule's runtime error unwinds to Eval, which returns it
	}
	return c
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

// outcomeCandidates is what `outcome.<decision>` reads from: the
// candidates the host gets back, or the default when nothing fired.
func (p *Policy) outcomeCandidates(out *Outcome) []*Candidate {
	if len(out.Top) == 0 && p.def != nil {
		return []*Candidate{p.def}
	}
	return out.Top
}

// walk runs one phase over the nodes. In the rules phase a runtime error
// unwinds to Eval; in the assert phases it becomes the failure of every
// assert it kept from being checked. An invocation's body runs in the
// invoked instance's own frame.
func (p *Policy) walk(f *Frame, r *run, nodes []*node, ph phase) {
	for _, n := range nodes {
		switch {
		case n.rule != nil && ph == phaseRules:
			r.poll()
			r.cands = append(r.cands, fireIn(n.rule, f))
		case n.assert != nil && n.assert.wants(ph):
			r.poll()
			r.check(f, n.assert)
		case n.block != nil && n.block.summary.wants(ph):
			p.enter(f, r, n.block, ph)
		case n.invoke != nil && n.invoke.summary.wants(ph):
			p.walk(r.frame(n.invoke.inst), r, n.invoke.inst.body, ph)
		}
	}
}

// enter evaluates a block's condition and walks its body when it holds.
// A condition that raises fails every assert of the phase beneath it,
// or unwinds as a runtime error in the rules phase.
func (p *Policy) enter(f *Frame, r *run, b *block, ph phase) {
	held, err := f.cond(b)
	if err != nil && err.File == "" {
		err.File, err.Doc = f.file, f.doc
	}
	switch {
	case err != nil && ph == phaseRules:
		panic(err) //nolint:nopanic // a rule's runtime error unwinds to Eval, which returns it
	case err != nil:
		for _, a := range b.summary.asserts {
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
		if err.File == "" {
			err.File, err.Doc = a.File, a.Policy
		}
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
	slices.SortStableFunc(r.failed, func(a, b Failure) int {
		return compareAt(a.Assert.Chain, a.Assert.Pos, b.Assert.Chain, b.Assert.Pos)
	})
	for _, fl := range r.failed {
		if fl.Err != nil && fl.Err.File == "" {
			fl.Err.File, fl.Err.Doc = fl.Assert.File, fl.Assert.Policy
		}
	}
	return r.failed
}

// compareAt orders call chains by each invocation's position, followed by
// the constructor or assert position in the invoked file.
func compareAt(ca []Site, pa token.Pos, cb []Site, pb token.Pos) int {
	for i := 0; i < len(ca) && i < len(cb); i++ {
		if n := comparePos(ca[i].Pos, cb[i].Pos); n != 0 {
			return n
		}
	}
	switch {
	case len(ca) < len(cb):
		return comparePos(pa, cb[len(ca)].Pos)
	case len(ca) > len(cb):
		return comparePos(ca[len(cb)].Pos, pb)
	}
	return comparePos(pa, pb)
}

// comparePos orders two positions in one file by line, then column.
func comparePos(a, b token.Pos) int {
	if n := stdcmp.Compare(a.Line, b.Line); n != 0 {
		return n
	}
	return stdcmp.Compare(a.Column, b.Column)
}

// Requirement reports how the root reaches the policy called name.
func (p *Policy) Requirement(name string) Requirement {
	w := &requirer{name: name, seen: map[*instance]bool{}}
	w.visit(p.root.body, false)
	if w.req.Unconditional {
		w.req.Gated = nil
	}
	return w.req
}

// requirer walks the invocation tree for one policy's call sites.
type requirer struct {
	seen map[*instance]bool
	name string
	req  Requirement
}

// visit records every invocation of the policy under nodes; gated says
// whether a `when` encloses them.
func (w *requirer) visit(nodes []*node, gated bool) {
	for _, n := range nodes {
		switch {
		case n.block != nil:
			w.visit(n.block.body, true)
		case n.invoke != nil:
			w.site(n.invoke, gated)
		}
	}
}

// site records one invocation and walks into it once.
func (w *requirer) site(inv *invocation, gated bool) {
	if inv.inst.name == w.name {
		w.req.Invoked = true
		if gated {
			w.req.Gated = append(w.req.Gated, inv.Site)
		} else {
			w.req.Unconditional = true
		}
	}
	if !w.seen[inv.inst] {
		w.seen[inv.inst] = true
		w.visit(inv.inst.body, gated)
	}
}

// Rules returns every decision constructor the policy can reach, in
// source order with invocations inlined where they're called: what
// sigil explain lists.
func (p *Policy) Rules() []*Rule {
	var out []*Rule
	var visit func(nodes []*node)
	visit = func(nodes []*node) {
		for _, n := range nodes {
			switch {
			case n.rule != nil:
				out = append(out, n.rule)
			case n.block != nil:
				visit(n.block.body)
			case n.invoke != nil:
				visit(n.invoke.inst.body)
			}
		}
	}
	visit(p.root.body)
	return out
}

// Asserts returns every assert the policy can reach, in the same order
// as Rules.
func (p *Policy) Asserts() []*Assert {
	var out []*Assert
	var visit func(nodes []*node)
	visit = func(nodes []*node) {
		for _, n := range nodes {
			switch {
			case n.assert != nil:
				out = append(out, n.assert)
			case n.block != nil:
				visit(n.block.body)
			case n.invoke != nil:
				visit(n.invoke.inst.body)
			}
		}
	}
	visit(p.root.body)
	return out
}

// Instances returns the names of every document the policy compiled
// in, the root first: the invoked policies and the imported documents.
func (p *Policy) Instances() []string {
	c := &collector{seen: map[string]bool{p.root.name: true}, names: []string{p.root.name}}
	c.visit(p.root)
	return c.names
}

// collector gathers the documents reachable from an instance.
type collector struct {
	seen  map[string]bool
	names []string
}

// add records a document once.
func (c *collector) add(name string) bool {
	if c.seen[name] {
		return false
	}
	c.seen[name] = true
	c.names = append(c.names, name)
	return true
}

// visit records the instance's imports and invoked instances.
func (c *collector) visit(inst *instance) {
	for name := range inst.imports {
		c.add(name)
	}
	for _, n := range inst.body {
		c.visitNode(n)
	}
}

func (c *collector) visitNode(n *node) {
	switch {
	case n.block != nil:
		for _, m := range n.block.body {
			c.visitNode(m)
		}
	case n.invoke != nil:
		if c.add(n.invoke.inst.name) {
			c.visit(n.invoke.inst)
		}
	}
}
