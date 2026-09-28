package eval

import (
	"reflect"

	"github.com/spechtlabs/sigil/internal/diag"
)

// Frame is the state of one evaluation: the input, the values bound to
// slots (quantifier variables and whatever a test binds directly), the
// lets evaluated so far and the `when` conditions evaluated so far.
type Frame struct {
	Input   Value
	Outcome Value // list<decision> for assert conditions; set by the policy evaluator
	slots   []Value
	lets    []Value // a let's value once evaluated, by let index
	done    []bool  // whether lets[i] has been evaluated in this frame
	conds   []condMemo
}

// condMemo is a `when` condition's result, once evaluated in a frame, so
// the phases that share a block evaluate its condition once.
type condMemo struct {
	err  *diag.Error
	done bool
	held bool
}

// NewFrame returns a frame over input, a struct value or a pointer to
// one, with room for the scope's slots, lets and conditions.
func NewFrame(input any, scope *Scope) *Frame {
	v := reflect.ValueOf(input)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	nslots, nlets, nconds := 0, 0, 0
	if scope != nil {
		nslots, nlets, nconds = scope.nslots, len(scope.lets), scope.nconds
	}
	return &Frame{Input: v, slots: make([]Value, nslots), lets: make([]Value, nlets), done: make([]bool, nlets), conds: make([]condMemo, nconds)}
}

// Set binds the value of a slot.
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
