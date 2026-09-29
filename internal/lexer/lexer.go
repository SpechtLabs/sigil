package lexer

import (
	"unicode/utf8"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

// Lexer produces tokens from a source buffer on demand. Create one with
// [New]; the zero Lexer reports invalid positions. A Lexer is not safe for
// concurrent use.
type Lexer struct {
	src  []byte
	errs []*diag.Error
	off  int // byte offset of the next unread byte
	line int // line of the next unread byte, starting at 1
	col  int // column of the next unread byte in characters, starting at 1
}

// New returns a lexer over src. The lexer doesn't copy src, so the caller
// must not modify it while lexing.
func New(src []byte) *Lexer {
	return &Lexer{src: src, line: 1, col: 1}
}

// Errors returns every lexical error found so far, in source order. The
// errors have no File set. The slice and the errors in it are the lexer's
// own, not copies, and later calls to [Lexer.Next] may append to the slice.
func (l *Lexer) Errors() []*diag.Error {
	return l.errs
}

// Next skips whitespace and returns the next token, which may be a
// [token.Comment]. Text the lexer can't read comes back as a
// [token.Illegal] token, with an error recorded for [Lexer.Errors]. After
// the source is exhausted Next returns [token.EOF] forever.
func (l *Lexer) Next() token.Token {
	l.skipWhitespace()
	start := l.pos()
	if l.off >= len(l.src) {
		return token.Token{Kind: token.EOF, Pos: start, End: start}
	}

	c := l.src[l.off]
	switch {
	case isLetter(c):
		return l.ident(start)

	case isDigit(c):
		return l.number(start)

	case c == '"':
		return l.str(start)

	case c == '`':
		return l.rawString(start)

	case c == '/' && l.at(1) == '/':
		return l.comment(start)
	}

	return l.operator(start)
}

// pos returns the position of the next unread byte.
func (l *Lexer) pos() token.Pos {
	return token.Pos{Offset: l.off, Line: l.line, Column: l.col}
}

// at returns the byte n bytes ahead of the cursor, or 0 past the end. It's
// for peeking at ASCII structure; advance handles multi-byte characters.
func (l *Lexer) at(n int) byte {
	if l.off+n < len(l.src) {
		return l.src[l.off+n]
	}
	return 0
}

// advance moves the cursor over one character, keeping line and column
// current. A tab counts as one column.
func (l *Lexer) advance() {
	if l.off >= len(l.src) {
		return
	}
	c := l.src[l.off]
	if c < utf8.RuneSelf {
		l.off++
	} else {
		_, w := utf8.DecodeRune(l.src[l.off:])
		l.off += w
	}
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
}

// emit builds the token from start to the cursor.
func (l *Lexer) emit(kind token.Kind, start token.Pos) token.Token {
	return token.Token{
		Kind: kind,
		Text: string(l.src[start.Offset:l.off]),
		Pos:  start,
		End:  l.pos(),
	}
}

// text returns the source from start to the cursor.
func (l *Lexer) text(start token.Pos) string {
	return string(l.src[start.Offset:l.off])
}

func (l *Lexer) skipWhitespace() {
	for l.off < len(l.src) {
		switch l.src[l.off] {
		case ' ', '\t', '\r', '\n':
			l.advance()
		default:
			return
		}
	}
}

func (l *Lexer) digits() {
	for isDigit(l.at(0)) {
		l.advance()
	}
}

func (l *Lexer) letters() {
	for isLetter(l.at(0)) || isDigit(l.at(0)) {
		l.advance()
	}
}

// isLetter reports whether c can start an identifier. Identifiers are ASCII
// by the grammar; `_` counts as a letter.
func isLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_'
}

func isDigit(c byte) bool {
	return '0' <= c && c <= '9'
}
