package check

import (
	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// Info holds what the checker learned: the type of every expression, the
// type of every param, the lets in dependency order, and the decision each
// constructor builds.
type Info struct {
	Types        map[ast.Expr]types.Type
	Params       map[*ast.ParamStmt]types.Type
	Constructors map[*ast.CallStmt]*kind.Decision
	Lets         []*ast.LetStmt // in an order where every let follows the lets it reads
	// Shadows are the document's names that take the name of an input,
	// host function or decision the kind added after the document's pin.
	// The shadowed-kind-name lint reports them.
	Shadows []*ast.Ident
}

// TypeOf returns the recorded type of x, or nil when x wasn't checked.
func (i *Info) TypeOf(x ast.Expr) types.Type {
	if i == nil {
		return nil
	}
	return i.Types[x]
}
