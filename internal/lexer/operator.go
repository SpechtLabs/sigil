package lexer

import (
	"fmt"
	"unicode/utf8"

	"github.com/spechtlabs/sigil/internal/token"
)

// singles maps each character that is an operator on its own to its kind.
// A zero entry (Illegal) means the character starts no single-character
// token. Characters that also start a longer token (`=`, `<`, `>`, `?`, `-`)
// are here too; operator checks pair and the separator first. So is `|`,
// though `||` is an error rather than two pipes.
var singles = [256]token.Kind{
	'=': token.Assign,
	'<': token.Lt,
	'>': token.Gt,
	'?': token.Question,
	'@': token.At,
	'-': token.Minus,
	'+': token.Plus,
	'.': token.Dot,
	',': token.Comma,
	':': token.Colon,
	'(': token.LParen,
	')': token.RParen,
	'[': token.LBracket,
	']': token.RBracket,
	'{': token.LBrace,
	'}': token.RBrace,
	'|': token.Pipe,
}

// pair returns the two-character operator that c and next spell, or Illegal.
func pair(c, next byte) token.Kind {
	switch {
	case c == '=' && next == '=':
		return token.Eq
	case c == '!' && next == '=':
		return token.NotEq
	case c == '<' && next == '=':
		return token.LtEq
	case c == '>' && next == '=':
		return token.GtEq
	case c == '?' && next == '?':
		return token.Coalesce
	case c == '?' && next == '.':
		return token.OptDot
	case c == '-' && next == '>':
		return token.Arrow
	}
	return token.Illegal
}

// operator lexes operators and punctuation by longest match: the three
// character separator, then two-character operators, then single characters.
// Anything else is an error, with a hint for the symbols other languages use
// where Sigil uses words.
func (l *Lexer) operator(start token.Pos) token.Token {
	c, next := l.at(0), l.at(1)

	if c == '-' && next == '-' && l.at(2) == '-' {
		l.advance()
		l.advance()
		l.advance()
		return l.emit(token.Separator, start)
	}

	if kind := pair(c, next); kind != token.Illegal {
		l.advance()
		l.advance()
		return l.emit(kind, start)
	}

	if c == '|' && next == '|' {
		l.advance()
		return l.failChar(start, "use `or`")
	}

	if kind := singles[c]; kind != token.Illegal {
		l.advance()
		return l.emit(kind, start)
	}

	switch c {
	case '!':
		return l.failChar(start, "use `not` to negate a condition, or `!=` to compare")

	case '&':
		if next == '&' {
			l.advance()
		}
		return l.failChar(start, "use `and`")
	}

	return l.failChar(start, "")
}

// failChar records an error for the character under the cursor (plus any
// already consumed since start) and consumes it.
func (l *Lexer) failChar(start token.Pos, help string) token.Token {
	l.advance()
	return l.fail(start, fmt.Sprintf("unexpected character %s", quoteChar(l.text(start))), help)
}

// quoteChar formats a short run of source for a message: readable characters
// in backticks, anything else as its code point.
func quoteChar(s string) string {
	r, _ := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError || r < ' ' || r == 0x7f || r == 0xa0 {
		return fmt.Sprintf("U+%04X", r)
	}

	return "`" + s + "`"
}
