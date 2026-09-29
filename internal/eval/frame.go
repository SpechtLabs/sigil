package eval

import (
	"reflect"

	"github.com/spechtlabs/sigil/internal/diag"
)

// Frame is the state of one instance in one evaluation: the input, the
// values bound to slots (quantifier variables and whatever a test binds
// directly), the lets evaluated so far and the `when` conditions
// evaluated so far. Every instance a policy invokes gets a frame of its
// own, sharing the input and the outcome through the run.
//
// A Frame is mutated as expressions evaluate in it, so it isn't safe for
// concurrent use.
type Frame struct {
	Input   Value // the input struct, never a pointer to it
	Outcome Value // list<decision> for assert conditions; set by the policy evaluator
	// Candidates is what `outcome.<decision>` reads: the candidates the host
	// gets back, or the default when nothing fired. The policy evaluator
	// sets it with Outcome.
	Candidates []*Candidate
	run        *run // the evaluation this frame belongs to, nil for a bare frame
	file       string
	doc        string // the instance's document name, for runtime errors
	slots      []Value
	lets       []Value // a let's value once evaluated, by let index
	done       []bool  // whether lets[i] has been evaluated in this frame
	conds      []condMemo
}

// condMemo is a `when` condition's result, once evaluated in a frame, so
// the phases that share a block evaluate its condition once.
type condMemo struct {
	err  *diag.Error
	done bool
	held bool
}

// NewFrame returns a frame over input, a struct value or a pointer to
// one, with room for the scope's slots, lets and conditions. Make it after
// compiling every expression it evaluates, since compiling adds slots to
// the scope. The frame belongs to no policy evaluation, so reading an
// imported let in it is a runtime error. A nil scope gives a frame with
// no slots.
func NewFrame(input any, scope *Scope) *Frame {
	v := reflect.ValueOf(input)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	return newFrame(v, scope)
}

func newFrame(input Value, scope *Scope) *Frame {
	nslots, nlets, nconds := 0, 0, 0
	if scope != nil {
		nslots, nlets, nconds = scope.nslots, len(scope.lets), scope.nconds
	}
	return &Frame{Input: input, slots: make([]Value, nslots), lets: make([]Value, nlets), done: make([]bool, nlets), conds: make([]condMemo, nconds)}
}

// Set binds slot, as [Scope.Declare] returned it, to v. It panics when
// the slot is outside the scope the frame was made for.
func (f *Frame) Set(slot int, v Value) { f.slots[slot] = v }

// cond evaluates a block's condition once per frame and returns whether
// it held, or the runtime error it raised.
func (f *Frame) cond(b *block) (bool, *diag.Error) {
	m := &f.conds[b.id]
	if !m.done {
		m.err = catch(func() { m.held = Bool(b.cond(f)) })
		m.done = true
	}
	return m.held, m.err
}

// frameOf returns the frame of another instance in the same evaluation,
// for reading an imported let.
func (f *Frame) frameOf(inst *instance) *Frame {
	if f.run == nil {
		throwf(nil, "no evaluation to read an imported let in")
	}
	return f.run.frame(inst)
}
