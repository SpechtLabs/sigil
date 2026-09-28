package eval

// node is one compiled statement of a policy body: a `when` block, a
// decision constructor or an assert. Exactly one field is set.
type node struct {
	block  *block
	rule   *Rule
	assert *Assert
}

// block is a compiled `when`: its condition, what's under it, and which
// phases have work there, so a phase skips a block it has nothing in.
type block struct {
	cond     Expr
	text     *Cond
	body     []*node
	asserts  []*Assert // every assert beneath, at any depth, for failing them all when the condition raises
	id       int       // index of the condition's memo in a frame
	hasRules bool      // a constructor is beneath
	hasInput bool      // an input assert is beneath
	hasOut   bool      // an outcome assert is beneath
}

// phase is one of the three passes over the tree.
type phase uint8

const (
	phaseInput phase = iota // check input asserts
	phaseRules              // fire constructors
	phaseOut                // check outcome asserts
)

// wants reports whether the phase has anything to do beneath b.
func (b *block) wants(ph phase) bool {
	switch ph {
	case phaseInput:
		return b.hasInput
	case phaseRules:
		return b.hasRules
	}
	return b.hasOut
}

// wants reports whether the phase checks a.
func (a *Assert) wants(ph phase) bool {
	return (ph == phaseOut) == a.ReadsOutcome && ph != phaseRules
}
