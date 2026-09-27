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
	Msg  string    // what's wrong, one sentence without a trailing period
	Help string    // how to fix it, or empty when there's no obvious fix
	Pos  token.Pos // start of the offending source
	End  token.Pos // just after the offending source
}

// Error formats e as file:line:col: msg, leaving the file out when unknown.
func (e *Error) Error() string {
	var b strings.Builder
	if e.File != "" {
		b.WriteString(e.File)
		b.WriteByte(':')
	}
	b.WriteString(e.Pos.String())
	b.WriteString(": ")
	b.WriteString(e.Msg)
	return b.String()
}
