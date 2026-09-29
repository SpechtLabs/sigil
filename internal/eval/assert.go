package eval

import (
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

// Assert is a compiled assertion, with the conditions that lead to it.
// An assert whose condition reads `outcome` is checked once the outcome
// exists; any other is an input assert, checked before any rule runs.
type Assert struct {
	cond         Expr
	Reason       string    // the assert's reason, empty when it gives none
	Policy       string    // the document the assert is in
	File         string    // the file that document is in
	Text         string    // the condition as written, params substituted
	Conds        []*Cond   // enclosing conditions, outermost first, the invoking blocks' included
	Chain        []Site    // the invocations the assert was reached through, outermost first
	Pos          token.Pos // of the assert statement
	End          token.Pos
	ReadsOutcome bool // the condition reads `outcome`, so it's an outcome assert
}

// Failure is an assert that didn't hold, or whose condition, or an
// enclosing condition, raised a runtime error, which Err then carries.
type Failure struct {
	Assert *Assert
	Err    *diag.Error // nil when the condition evaluated to false
}
