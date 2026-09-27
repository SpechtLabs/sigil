package lexer

import (
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

// The literal decoders return *diag.Error rather than error so the lexer can
// read the message and hint. The positions in an Error they return are
// relative to the literal: Offset counts bytes and Column counts characters
// from the literal's first character, and Line is unset. failIn anchors them
// to the source.

// fail records an error spanning start to the cursor and returns the Illegal
// token covering the same text.
func (l *Lexer) fail(start token.Pos, msg, help string) token.Token {
	l.errs = append(l.errs, &diag.Error{Msg: msg, Help: help, Pos: start, End: l.pos()})
	return l.emit(token.Illegal, start)
}

// failIn records err, whose positions are relative to the literal starting
// at start, as an error in the source, and returns the Illegal token for the
// whole literal.
func (l *Lexer) failIn(start token.Pos, err *diag.Error) token.Token {
	if err == nil {
		return l.emit(token.Illegal, start)
	}
	errStart := start
	errStart.Offset += err.Pos.Offset
	errStart.Column += err.Pos.Column
	errEnd := start
	errEnd.Offset += err.End.Offset
	errEnd.Column += err.End.Column
	l.errs = append(l.errs, &diag.Error{Msg: err.Msg, Help: err.Help, Pos: errStart, End: errEnd})
	return l.emit(token.Illegal, start)
}

// relPos builds a position relative to a literal: byte offset off and
// character column col from the literal's first character.
func relPos(off, col int) token.Pos {
	return token.Pos{Offset: off, Column: col}
}
