package policy

import (
	"strings"

	"github.com/spechtlabs/sigil/internal/result"
	"github.com/spechtlabs/sigil/internal/token"
)

// Position is a place in a bundle: the file, the line and column in it,
// and the name of the document at that place. Line and Column are 1-based
// and Column counts characters, not bytes. A Position with Line 0 is
// unknown, as for the kind's default decision, which has no source.
type Position struct {
	File     string // the path in the fs.FS given to Load; empty for a source given to Compile
	Document string // the name in the header of the document at this place, such as "payments.production"
	Line     int
	Column   int
}

// IsValid reports whether p names a place in a source, that is whether
// its Line is set.
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

// chain formats a call chain followed by the final position, without copying
// the caller's positions into temporary slices.
func chain(ps []Position, end Position) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.String())
		b.WriteString(" → ")
	}
	b.WriteString(end.String())
	return b.String()
}
