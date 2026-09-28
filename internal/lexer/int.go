package lexer

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

// ParseInt decodes an Int literal. The literal is decimal and must fit in a
// signed 64-bit integer.
func ParseInt(text string) (int64, *diag.Error) {
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, &diag.Error{
				Msg:  fmt.Sprintf("integer literal `%s` is out of range", text),
				Help: fmt.Sprintf("integers are signed 64-bit, at most %d", math.MaxInt64),
			}
		}
		return 0, &diag.Error{Msg: fmt.Sprintf("invalid integer literal `%s`", text)}
	}
	return v, nil
}

// number lexes an integer, float or duration literal. All three start with
// digits; what follows decides which one it is.
func (l *Lexer) number(start token.Pos) token.Token {
	l.digits()

	switch {
	case l.at(0) == '.':
		return l.float(start)
	case isLetter(l.at(0)):
		if t, ok := l.notation(start); ok {
			return t
		}
		return l.duration(start)
	}

	t := l.emit(token.Int, start)
	if _, err := ParseInt(t.Text); err != nil {
		return l.fail(start, err.Msg, err.Help)
	}
	return t
}

// notation lexes a number written in a notation Sigil doesn't have: hex,
// octal or binary, an exponent, or `_` digit separators. The cursor is
// right after the literal's digits, and after the fraction of a float.
// Without such a notation it consumes nothing and reports false, so a
// letter after digits goes on to be read as a duration unit.
func (l *Lexer) notation(start token.Pos) (token.Token, bool) {
	c, next := l.at(0), l.at(1)
	switch {
	case l.text(start) == "0" && strings.IndexByte("xXoObB", c) >= 0:
		name := "hex"
		switch c {
		case 'o', 'O':
			name = "octal"
		case 'b', 'B':
			name = "binary"
		}
		l.numberTail()
		text := l.text(start)
		help := "write the number in decimal"
		if v, err := strconv.ParseInt(text, 0, 64); err == nil {
			help = fmt.Sprintf("write the decimal integer `%d`", v)
		}
		return l.fail(start, fmt.Sprintf("`%s` is %s notation, which Sigil doesn't have", text, name), help), true

	case c == '_' && isDigit(next):
		l.numberTail()
		text := l.text(start)
		return l.fail(start, fmt.Sprintf("`%s` uses `_` as a digit separator, which Sigil doesn't have", text),
			fmt.Sprintf("write the digits without it: `%s`", strings.ReplaceAll(text, "_", ""))), true

	case (c == 'e' || c == 'E') && (isDigit(next) || (next == '+' || next == '-') && isDigit(l.at(2))):
		l.advance() // the e
		if !isDigit(l.at(0)) {
			l.advance() // the sign
		}
		l.numberTail()
		text := l.text(start)
		return l.fail(start, fmt.Sprintf("`%s` uses exponent notation, which Sigil doesn't have", text), exponentHint(text)), true
	}
	return token.Token{}, false
}

// numberTail consumes the rest of a malformed number: letters, digits,
// `_`, and a point followed by a digit, so the error covers all of it.
func (l *Lexer) numberTail() {
	for isLetter(l.at(0)) || isDigit(l.at(0)) || l.at(0) == '.' && isDigit(l.at(1)) {
		l.advance()
	}
}

// exponentHint writes the number an exponent literal stands for out in
// full: as an integer when it was written without a point and is one, as
// a float otherwise.
func exponentHint(text string) string {
	const generic = "write the number out in full, like `1000` or `0.001`"
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return generic
	}
	if !strings.Contains(text, ".") && v == math.Trunc(v) && math.Abs(v) < 1<<53 {
		return fmt.Sprintf("write the decimal integer `%d`", int64(v))
	}
	full := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.Contains(full, ".") {
		full += ".0"
	}
	if len(full) > 24 {
		return generic
	}
	return fmt.Sprintf("floats are written with a point and no exponent: `%s`", full)
}
