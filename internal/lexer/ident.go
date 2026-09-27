package lexer

import "github.com/spechtlabs/sigil/internal/token"

// ident lexes an identifier or keyword. Keywords are identifiers the
// token package knows by name, so the two share a scanner.
func (l *Lexer) ident(start token.Pos) token.Token {
	for isLetter(l.at(0)) || isDigit(l.at(0)) {
		l.advance()
	}
	t := l.emit(token.Ident, start)
	t.Kind = token.Lookup(t.Text)
	return t
}
