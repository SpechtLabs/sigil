package lexer

import (
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

// Unquote decodes a String literal, quotes included, using Go's escape
// sequences for interpreted string literals. An invalid escape is reported at
// its position inside the literal; any other failure carries no position.
// A raw string needs no decoding: its value is the text between the
// backticks.
func Unquote(lit string) (string, *diag.Error) {
	if len(lit) < 2 || lit[0] != '"' || lit[len(lit)-1] != '"' {
		return "", &diag.Error{Msg: "invalid string literal"}
	}

	body := lit[1 : len(lit)-1]
	col := 1 // the opening quote is character 0
	for i := 0; i < len(body); {
		r, w := utf8.DecodeRuneInString(body[i:])
		if r != '\\' {
			i += w
			col++
			continue
		}

		n := escapeLen(body[i+1:])
		if n == 0 {
			return "", badEscape(body, i, col)
		}
		i += 1 + n
		col += 1 + n
	}

	s, err := strconv.Unquote(lit)
	if err != nil {
		return "", &diag.Error{Msg: "invalid string literal"}
	}

	return s, nil
}

// str lexes a double-quoted string. The scanner only needs to know that a
// backslash protects the next character; which escapes are valid is decided
// by Unquote afterwards, over the whole literal.
func (l *Lexer) str(start token.Pos) token.Token {
	l.advance() // opening quote
	for {
		switch l.at(0) {
		case '"':
			l.advance()
			t := l.emit(token.String, start)
			if _, err := Unquote(t.Text); err != nil {
				// The error's positions are offsets into the literal; anchor
				// them to the token.
				return l.failIn(start, err)
			}
			return t

		case '\\':
			l.advance()
			if l.at(0) == '\n' || l.off >= len(l.src) {
				continue
			}
			l.advance()

		case '\n':
			return l.fail(start, "unterminated string literal", "a double-quoted string can't span lines; close it with `\"` or use a raw string in backticks")

		default:
			if l.off >= len(l.src) {
				return l.fail(start, "unterminated string literal", "close it with `\"`")
			}
			l.advance()
		}
	}
}

// rawString lexes a backtick string. Nothing inside is an escape and it may
// span lines.
func (l *Lexer) rawString(start token.Pos) token.Token {
	l.advance() // opening backtick
	for l.off < len(l.src) {
		if l.at(0) == '`' {
			l.advance()
			return l.emit(token.RawString, start)
		}
		l.advance()
	}
	return l.fail(start, "unterminated raw string literal", "close it with a backtick")
}

// escapeLen returns how many characters after a backslash form the escape
// that s starts with, or 0 when s doesn't start with a valid escape.
func escapeLen(s string) int {
	if s == "" {
		return 0
	}

	switch s[0] {
	case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\', '"':
		return 1

	case 'x':
		return hexLen(s, 2)

	case 'u':
		return hexLen(s, 4)

	case 'U':
		return hexLen(s, 8)

	case '0', '1', '2', '3', '4', '5', '6', '7':
		if len(s) >= 3 && isOctal(s[1]) && isOctal(s[2]) {
			return 3
		}
	}

	return 0
}

// hexLen returns 1+n when s[1:1+n] are hex digits, else 0.
func hexLen(s string, n int) int {
	if len(s) < 1+n {
		return 0
	}

	for _, c := range []byte(s[1 : 1+n]) {
		if !isHex(c) {
			return 0
		}
	}

	return 1 + n
}

// badEscape describes the invalid escape at body[i], which sits at character
// column col of the literal.
func badEscape(body string, i, col int) *diag.Error {
	// Show the backslash and the character after it, when there is one.
	end := i + 1
	if end < len(body) {
		_, w := utf8.DecodeRuneInString(body[end:])
		end += w
	}

	seq := body[i:end]

	help := `the escapes are \n, \t, \\, \", \xhh, \uhhhh and the other Go string escapes`
	if seq == `\'` {
		help = "a single quote needs no escape in a double-quoted string"
	}

	return &diag.Error{
		Msg:  fmt.Sprintf("unknown escape sequence `%s`", seq),
		Help: help,
		// +1 for the opening quote, which body doesn't include.
		Pos: relPos(1+i, col),
		End: relPos(1+end, col+utf8.RuneCountInString(seq)),
	}
}

func isOctal(c byte) bool {
	return '0' <= c && c <= '7'
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
