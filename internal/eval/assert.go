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
	Reason       string
	Policy       string
	File         string
	Conds        []*Cond
	Pos          token.Pos
	End          token.Pos
	ReadsOutcome bool
}

// Failure is an assert that didn't hold, or whose condition, or an
// enclosing condition, raised a runtime error, which Err then carries.
type Failure struct {
	Assert *Assert
	Err    *diag.Error
}
