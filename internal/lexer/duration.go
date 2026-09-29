package lexer

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

// durationUnits lists the units largest first, so an index doubles as the
// rank the "largest first" rule compares.
var durationUnits = [...]string{"d", "h", "m", "s", "ms"}

// ParseDuration decodes a Duration literal such as `1h30m`. The units are
// d, h, m, s and ms. Each unit may appear at most once, in descending order,
// and the total must fit in a [time.Duration]. Text that isn't a sequence
// of components, each digits followed by a unit, is rejected as invalid,
// so ParseDuration is also safe on text the lexer didn't scan, such as a
// duration a host passes as a string.
// The empty string decodes to zero without an error. A returned error
// carries no position.
//
// The rules are Sigil's, so the validation is ours. The arithmetic is
// [time.ParseDuration]'s: once the text is known to be well-formed, the only
// way it can still fail is by overflowing. Go has no `d` unit, so day
// components are rewritten as hours first.
func ParseDuration(text string) (time.Duration, *diag.Error) {
	if text == "" {
		return 0, nil
	}

	var goText strings.Builder
	lastRank := -1
	rest := text
	for rest != "" {
		i := 0
		for i < len(rest) && '0' <= rest[i] && rest[i] <= '9' {
			i++
		}
		digits, unitText := rest[:i], rest[i:]

		// Longest match, so `ms` wins over `m`.
		rank := -1
		for r, u := range durationUnits {
			if strings.HasPrefix(unitText, u) && (rank < 0 || len(u) > len(durationUnits[rank])) {
				rank = r
			}
		}
		if i == 0 || rank < 0 {
			return 0, &diag.Error{Msg: fmt.Sprintf("invalid duration literal `%s`", text)}
		}
		unit := durationUnits[rank]
		rest = unitText[len(unit):]

		switch {
		case rank == lastRank:
			return 0, &diag.Error{
				Msg:  fmt.Sprintf("unit `%s` appears twice in `%s`", unit, text),
				Help: "each unit may appear once in a duration; add the components together",
			}
		case rank < lastRank:
			return 0, &diag.Error{
				Msg:  fmt.Sprintf("units in `%s` aren't in descending order", text),
				Help: "write the largest unit first, like `1h30m`",
			}
		}
		lastRank = rank

		if unit == "d" {
			n, err := strconv.ParseInt(digits, 10, 64)
			if err != nil || n > math.MaxInt64/24 {
				return 0, outOfRange(text)
			}
			digits, unit = strconv.FormatInt(n*24, 10), "h"
		}
		goText.WriteString(digits)
		goText.WriteString(unit)
	}

	d, err := time.ParseDuration(goText.String())
	if err != nil {
		return 0, outOfRange(text)
	}
	return d, nil
}

// duration lexes the rest of a duration literal; the cursor is on the first
// unit letter. It scans the shape ( digits unit )+ and leaves unit order and
// range to ParseDuration, so the lexer and the parser agree on what a
// duration means.
func (l *Lexer) duration(start token.Pos) token.Token {
	for {
		unitStart := l.pos()
		if !l.unit() || isLetter(l.at(0)) {
			l.letters()
			return l.fail(start, fmt.Sprintf("unknown duration unit `%s` in `%s`", l.text(unitStart), l.text(start)),
				"the units are ms, s, m, h and d, like `30m` or `1h30m`")
		}
		if !isDigit(l.at(0)) {
			break
		}
		compStart := l.pos()
		l.digits()
		if !isLetter(l.at(0)) {
			comp := l.text(compStart)
			return l.fail(start, fmt.Sprintf("`%s` in `%s` is missing a unit", comp, l.text(start)),
				fmt.Sprintf("every component of a duration needs a unit, like `%sm`", comp))
		}
	}

	t := l.emit(token.Duration, start)
	if _, err := ParseDuration(t.Text); err != nil {
		return l.fail(start, err.Msg, err.Help)
	}
	return t
}

// unit consumes one duration unit, taking `ms` over `m` by longest match.
// It reports false when the cursor isn't on a unit.
func (l *Lexer) unit() bool {
	switch l.at(0) {
	case 'm':
		l.advance()
		if l.at(0) == 's' {
			l.advance()
		}
		return true
	case 's', 'h', 'd':
		l.advance()
		return true
	}
	return false
}

// outOfRange is the error for a well-formed duration that doesn't fit in
// a [time.Duration]. ParseDuration builds it only when it fails, since it
// runs on every duration literal the lexer scans.
func outOfRange(text string) *diag.Error {
	return &diag.Error{
		Msg:  fmt.Sprintf("duration literal `%s` is out of range", text),
		Help: "a duration is at most about 292 years",
	}
}
