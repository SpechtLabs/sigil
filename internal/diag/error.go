// Package diag defines the diagnostic every stage of the compiler reports.
// The lexer, the parser, the type checker, the linter, the bundle and kind
// loaders and the evaluator all produce the same [Error], so the CLI
// renders them with one printer and tests compare them the same way.
//
// An [Error] carries its message, an optional fix, and the source span it
// refers to as [token.Pos] values. A stage that can find several problems
// returns them as an [ErrorList]. Lint findings are Errors too, with a
// [Severity] and the lint's name in Code. [Render] and [RenderWith] print a
// diagnostic in the layout the documentation shows, quoting the offending
// line; a [Theme] styles that layout for a terminal without changing it.
package diag

import (
	"strings"

	"github.com/spechtlabs/sigil/internal/token"
)

// Severity is how serious a diagnostic is.
type Severity int

// The severities, from the most serious down. The zero value is an error,
// so a diagnostic that says nothing is one.
const (
	SeverityError   Severity = iota // fails `sigil check`
	SeverityWarning                 // a lint finding, reported without failing the check
)

// String names the severity the way a diagnostic's header spells it.
func (s Severity) String() string {
	if s == SeverityWarning {
		return "warning"
	}
	return "error"
}

// Error is one diagnostic: what's wrong, where, and how to fix it. The
// zero Severity makes it an error. Stages pass *Error around, so a caller
// can fill in what a stage didn't know: the parser, for one, sets File on
// the lexer's errors.
type Error struct {
	File     string    // the file the positions refer to; empty when unknown
	Doc      string    // the document the position is in, when the stage knows it
	Msg      string    // what's wrong, one sentence without a trailing period
	Help     string    // how to fix it, or empty when there's no obvious fix
	Code     string    // the lint it comes from, or empty for a compiler error
	Pos      token.Pos // start of the offending source
	End      token.Pos // just after the offending source
	Severity Severity  // an error unless a lint says otherwise
}

// Error implements the error interface. It formats e as
// file:line:col: msg, leaving out the file when it's
// unknown and the position when there is none, as for a rule of the kind
// model that has no source. When the document is known and the file's
// path doesn't already say which document it is, its name follows the
// position: `policies.sigil:42:5 (payments.production): msg`.
func (e *Error) Error() string {
	return e.where() + e.Msg
}

// where formats the location prefix of a message: `file:line:col (doc): `,
// or as much of it as is known, or nothing.
func (e *Error) where() string {
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
	return b.String()
}

// PathMatches reports whether file is where a document called name is
// expected: its path, with each `.` as a directory separator and `.sigil`
// appended, possibly under a directory. Backslashes in file count as
// slashes. For example, the document `deploy.production` matches
// `deploy/production.sigil` and `policies/deploy/production.sigil`.
func PathMatches(file, name string) bool {
	want := strings.ReplaceAll(name, ".", "/") + ".sigil"
	file = strings.ReplaceAll(file, "\\", "/")
	return file == want || strings.HasSuffix(file, "/"+want)
}
