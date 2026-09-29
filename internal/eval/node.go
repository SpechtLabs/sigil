package eval

import "github.com/spechtlabs/sigil/internal/token"

// node is one compiled statement of a policy body: a `when` block, a
// decision constructor, an assert or a policy invocation. Exactly one
// field is set.
type node struct {
	block  *block
	rule   *Rule
	assert *Assert
	invoke *invocation
}

// block is a compiled `when`: its condition, what's under it, and which
// phases have work there, so a phase skips a block it has nothing in.
type block struct {
	cond    Expr
	text    *Cond
	body    []*node
	summary summary
	id      int // index of the condition's memo in a frame
}

// invocation is a compiled policy invocation: the instance it created,
// whose body runs in a frame of its own.
type invocation struct {
	inst    *instance
	summary summary
	Site    Site
}

// summary says what a subtree holds, so a phase can skip the subtrees
// it has nothing in, and a condition that raises can fail every assert
// beneath it.
type summary struct {
	asserts  []*Assert // every assert beneath, at any depth
	hasRules bool      // a constructor is beneath
	hasInput bool      // an input assert is beneath
	hasOut   bool      // an outcome assert is beneath
}

// note records an assert beneath the subtree.
func (s *summary) note(a *Assert) {
	s.asserts = append(s.asserts, a)
	if a.ReadsOutcome {
		s.hasOut = true
	} else {
		s.hasInput = true
	}
}

// add folds another subtree's summary in.
func (s *summary) add(o summary) {
	s.hasRules = s.hasRules || o.hasRules
	for _, a := range o.asserts {
		s.note(a)
	}
}

// summarize describes a node list.
func summarize(nodes []*node) summary {
	var s summary
	for _, n := range nodes {
		switch {
		case n.rule != nil:
			s.hasRules = true
		case n.assert != nil:
			s.note(n.assert)
		case n.block != nil:
			s.add(n.block.summary)
		case n.invoke != nil:
			s.add(n.invoke.summary)
		}
	}
	return s
}

// wants reports whether the phase has anything to do beneath the subtree.
func (s summary) wants(ph phase) bool {
	switch ph {
	case phaseInput:
		return s.hasInput
	case phaseRules:
		return s.hasRules
	}
	return s.hasOut
}

// Site is one call on a candidate's chain: the invocation statement in
// the policy that made it.
type Site struct {
	Policy string    // the document the invocation is in
	File   string    // the file that document is in
	Pos    token.Pos // of the invocation statement
	End    token.Pos
}

// phase is one of the three passes over the tree.
type phase uint8

const (
	phaseInput phase = iota // check input asserts
	phaseRules              // fire constructors
	phaseOut                // check outcome asserts
)

// wants reports whether the phase checks a.
func (a *Assert) wants(ph phase) bool {
	return (ph == phaseOut) == a.ReadsOutcome && ph != phaseRules
}
