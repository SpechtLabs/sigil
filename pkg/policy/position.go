package policy

import (
	"strconv"
	"strings"

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
func (p Position) String() string {
	if !p.IsValid() {
		return "-"
	}
	var b strings.Builder
	if p.File != "" {
		b.WriteString(p.File)
		b.WriteByte(':')
	}
	b.WriteString(strconv.Itoa(p.Line))
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(p.Column))
	if p.Document != "" && !pathMatches(p.File, p.Document) {
		b.WriteString(" (" + p.Document + ")")
	}
	return b.String()
}

// pathMatches reports whether file is where a document called name is
// expected: its path, with each `.` as a directory separator and `.sigil`
// appended, possibly under a directory.
func pathMatches(file, name string) bool {
	want := strings.ReplaceAll(name, ".", "/") + ".sigil"
	file = strings.ReplaceAll(file, "\\", "/")
	return file == want || strings.HasSuffix(file, "/"+want)
}

// position converts a source position.
func position(file, doc string, p token.Pos) Position {
	return Position{File: file, Document: doc, Line: p.Line, Column: p.Column}
}

// chain formats a call chain: the positions joined by arrows.
func chain(ps []Position) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.String()
	}
	return strings.Join(parts, " → ")
}
