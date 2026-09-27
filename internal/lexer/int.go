package lexer

import (
	"errors"
	"fmt"
	"math"
	"strconv"

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
		return l.duration(start)
	}

	t := l.emit(token.Int, start)
	if _, err := ParseInt(t.Text); err != nil {
		return l.fail(start, err.Msg, err.Help)
	}
	return t
}
