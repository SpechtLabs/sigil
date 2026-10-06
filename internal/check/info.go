package check

import (
	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/token"
	"github.com/spechtlabs/sigil/internal/types"
)

// Info holds what the checker learned: the type of every expression, the
// type of every param, the lets in dependency order, the decision each
// constructor builds and the reason it names, the policy each invocation
// instantiates and the imported let each read refers to. The evaluator
// compiles from it, and the lints read it. The maps are keyed by AST node,
// so an Info is only meaningful with the syntax tree it was built from.
type Info struct {
	Types        map[ast.Expr]types.Type          // every checked expression node, [types.Invalid] where it failed
	Params       map[*ast.ParamStmt]types.Type    // the declared type of each param
	Constructors map[*ast.CallStmt]*kind.Decision // the decision a constructor call builds
	Invocations  map[*ast.CallStmt]string         // the invoked policy's name
	Reasons      map[*ast.CallStmt]*ast.Ident     // the reason a constructor names, its `reason:` argument
	Reads        map[ast.Expr]Read                // imported lets, by the identifier or `qualifier.let` that reads them
	Lets         []*ast.LetStmt                   // in an order where every let follows the lets it reads
	// Shadows are the document's names that take the name of an input,
	// host function, decision, enum or enum value the kind added after
	// the document's pin. The shadowed-kind-name lint reports them.
	Shadows []*ast.Ident
	// Scopes are the regions of the checked documents and the names
	// visible in each, in the order the checker entered them, so an
	// enclosing scope comes before the scopes inside it: a document, the
	// body of a `when` that declares lets, an assert, and the body of a
	// quantifier or a filter. [Info.ScopeAt] finds the one at a position,
	// for tools such as the language server. Only a checker with
	// [Checker.Scopes] set records them.
	Scopes []Scope
}

// Scope is a region of a document and the names visible in it.
type Scope struct {
	Env  *Env
	From token.Pos // the region's first character
	To   token.Pos // just after its last character
	Doc  bool      // the scope of a whole document
}

// TypeOf returns the recorded type of x, or nil when x wasn't checked or
// i is nil.
func (i *Info) TypeOf(x ast.Expr) types.Type {
	if i == nil {
		return nil
	}
	return i.Types[x]
}

// ScopeAt returns the names visible at offset, a byte offset into the
// checked source: those of the innermost scope that holds it, its end
// included, so a name being typed at the end of a body sees the body's
// names. An offset past every scope gets the scope of the document that
// starts last before it, where a statement still being written goes. It
// returns nil when no document starts at or before offset, or i is nil.
func (i *Info) ScopeAt(offset int) *Env {
	if i == nil {
		return nil
	}
	var inner, doc *Scope
	for n := range i.Scopes {
		s := &i.Scopes[n]
		if s.From.Offset > offset {
			continue
		}
		if s.Doc && (doc == nil || s.From.Offset >= doc.From.Offset) {
			doc = s
		}
		if offset <= s.To.Offset && (inner == nil || s.From.Offset >= inner.From.Offset) {
			inner = s
		}
	}
	switch {
	case inner != nil && (doc == nil || inner.From.Offset >= doc.From.Offset):
		return inner.Env
	case doc != nil:
		return doc.Env
	}
	return nil
}

// scope records that env holds the names visible from from to to, when
// the checker records scopes.
func (c *Checker) scope(env *Env, from, to token.Pos, doc bool) {
	if !c.Scopes {
		return
	}
	c.info.Scopes = append(c.info.Scopes, Scope{Env: env, From: from, To: to, Doc: doc})
}
