// Package diag defines the diagnostic every stage of the compiler reports:
// the lexer, the parser and, later, the type checker all produce the same
// Error, so the CLI renders them with one printer and tests compare them the
// same way.
package diag

import (
	"strings"

	"github.com/spechtlabs/sigil/internal/token"
)

// Error is one diagnostic: what's wrong, where, and how to fix it.
type Error struct {
	File string    // the file the positions refer to; empty when unknown
	Doc  string    // the document the position is in, when the stage knows it
	Msg  string    // what's wrong, one sentence without a trailing period
	Help string    // how to fix it, or empty when there's no obvious fix
	Pos  token.Pos // start of the offending source
	End  token.Pos // just after the offending source
}

// Error formats e as file:line:col: msg, leaving out the file when it's
// unknown and the position when there is none, as for a rule of the kind
// model that has no source. When the document is known and the file's
// path doesn't already say which document it is, its name follows the
// position: `policies.sigil:42:5 (payments.production): msg`.
func (e *Error) Error() string {
	var b strings.Builder
	if e.File != "" {
		b.WriteString(e.File)
		b.WriteByte(':')
	}
	if e.Pos.IsValid() {
		b.WriteString(e.Pos.String())
		if e.Doc != "" && !PathMatches(e.File, e.Doc) {
			b.WriteString(" (" + e.Doc + ")")
		}
		b.WriteString(": ")
	} else if e.File != "" {
		b.WriteByte(' ')
	}
	b.WriteString(e.Msg)
	return b.String()
}

// PathMatches reports whether file is where a document called name is
// expected: its path, with each `.` as a directory separator and `.sigil`
// appended, possibly under a directory.
func PathMatches(file, name string) bool {
	want := strings.ReplaceAll(name, ".", "/") + ".sigil"
	file = strings.ReplaceAll(file, "\\", "/")
	return file == want || strings.HasSuffix(file, "/"+want)
}
