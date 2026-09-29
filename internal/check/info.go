package check

import (
	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// Info holds what the checker learned: the type of every expression, the
// type of every param, the lets in dependency order, the decision each
// constructor builds, the policy each invocation instantiates and the
// imported let each read refers to. The evaluator compiles from it, and
// the lints read it. The maps are keyed by AST node, so an Info is only
// meaningful with the syntax tree it was built from.
type Info struct {
	Types        map[ast.Expr]types.Type          // every checked expression node, [types.Invalid] where it failed
	Params       map[*ast.ParamStmt]types.Type    // the declared type of each param
	Constructors map[*ast.CallStmt]*kind.Decision // the decision a constructor call builds
	Invocations  map[*ast.CallStmt]string         // the invoked policy's name
	Reads        map[ast.Expr]Read                // imported lets, by the identifier or `qualifier.let` that reads them
	Lets         []*ast.LetStmt                   // in an order where every let follows the lets it reads
	// Shadows are the document's names that take the name of an input,
	// host function or decision the kind added after the document's pin.
	// The shadowed-kind-name lint reports them.
	Shadows []*ast.Ident
}

// TypeOf returns the recorded type of x, or nil when x wasn't checked or
// i is nil.
func (i *Info) TypeOf(x ast.Expr) types.Type {
	if i == nil {
		return nil
	}
	return i.Types[x]
}
