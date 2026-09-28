package policy

import (
	"github.com/spechtlabs/sigil/internal/result"
	"github.com/spechtlabs/sigil/internal/token"
)

// Position is a place in a bundle: the file, the line and column in it,
// and the name of the document at that place. Line and Column are 1-based
// and Column counts characters. A Position with Line 0 is unknown, as
// for the kind's default decision, which has no source.
type Position struct {
	File     string
	Document string
	Line     int
	Column   int
}

// IsValid reports whether p names a place in a source.
func (p Position) IsValid() bool { return p.Line > 0 }

// String formats p as file:line:col, followed by the document's name in
// parentheses when the file's path doesn't already say which document it
// is: `policies.sigil:42:5 (payments.production)`, but
// `deploy/production.sigil:16:5` for the document deploy.production. A
// position without a file is line:col, and one without a line is "-".
func (p Position) String() string { return result.Position(p).String() }

// position converts a source position.
func position(file, doc string, p token.Pos) Position {
	return Position{File: file, Document: doc, Line: p.Line, Column: p.Column}
}

// chain formats a call chain: the positions joined by arrows.
func chain(ps []Position) string {
	rs := make([]result.Position, len(ps))
	for i, p := range ps {
		rs[i] = result.Position(p)
	}
	return result.Chain(rs)
}
