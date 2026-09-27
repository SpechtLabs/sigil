package lexer

import "github.com/spechtlabs/sigil/internal/token"

// comment lexes a line comment. The text runs to the end of the line and
// leaves the newline for skipWhitespace, so a comment never swallows the
// line break that follows it.
func (l *Lexer) comment(start token.Pos) token.Token {
	for l.off < len(l.src) && l.at(0) != '\n' && l.at(0) != '\r' {
		l.advance()
	}
	return l.emit(token.Comment, start)
}
