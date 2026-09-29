package lexer

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

// ParseFloat decodes a Float literal to the nearest float64. It checks
// text with [strconv.ParseFloat], which accepts more than the lexer does,
// such as `NaN`; the lexer's scanner is what enforces Sigil's float syntax.
// It fails when strconv rejects text or the value is out of range for a
// float64. A returned error carries no position.
func ParseFloat(text string) (float64, *diag.Error) {
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, &diag.Error{Msg: fmt.Sprintf("float literal `%s` is out of range", text)}
		}
		return 0, &diag.Error{Msg: fmt.Sprintf("invalid float literal `%s`", text)}
	}
	return v, nil
}

// float lexes the rest of a float literal; the cursor is on the point.
func (l *Lexer) float(start token.Pos) token.Token {
	l.advance() // the point
	if !isDigit(l.at(0)) {
		return l.fail(start, "a float literal needs digits on both sides of the point",
			fmt.Sprintf("write `%s0`", l.text(start)))
	}
	l.digits()
	if t, ok := l.notation(start); ok {
		return t
	}
	if isLetter(l.at(0)) {
		l.letters()
		return l.fail(start, "a duration can't have a fractional component",
			"write the fraction as a smaller unit, like `1h30m` instead of `1.5h`")
	}
	t := l.emit(token.Float, start)
	if _, err := ParseFloat(t.Text); err != nil {
		return l.fail(start, err.Msg, err.Help)
	}
	return t
}
