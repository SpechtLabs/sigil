package server

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"
)

// day is Sigil's `d` unit: exactly 24 hours, with no calendar meaning.
const day = 24 * time.Hour

// maxDays is the most days a time.Duration holds.
const maxDays = int64(math.MaxInt64 / day)

// durationAdvice is how to write a duration, for every parse error.
const durationAdvice = `write durations like "6h", "1h30m" or "2d": an integer and a unit (d, h, m, s, ms), largest first`

// durationUnits are the units a duration renders with, largest first, the
// way Sigil writes a duration literal.
var durationUnits = []struct {
	name string
	size time.Duration
}{
	{"d", day},
	{"h", time.Hour},
	{"m", time.Minute},
	{"s", time.Second},
	{"ms", time.Millisecond},
}

// Duration is a time.Duration on the wire: a JSON string in Sigil's or Go's
// syntax ("6h", "1h30m", "2d", "500ms"), never a number. A bare number would
// be ambiguous between nanoseconds and seconds, which is exactly the mistake
// Sigil refuses to compile (`release.soak > 30`), so the API refuses it too.
type Duration time.Duration

// ParseDuration reads a duration in Go's syntax, extended with Sigil's `d`
// unit as a leading component: "2d", "1d12h" and "90m" all parse. A leading
// minus makes it negative. A duration longer than time.Duration holds, about
// 292 years, is an error rather than a value that silently wrapped around.
// Surrounding whitespace is ignored, and empty text is an error too.
func ParseDuration(text string) (time.Duration, humane.Error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return 0, humane.New("the duration is empty", durationAdvice)
	}

	sign := time.Duration(1)
	rest := s
	if after, ok := strings.CutPrefix(rest, "-"); ok {
		sign, rest = -1, after
	}
	// time.ParseDuration takes a sign of its own; one sign is enough.
	if rest == "" || rest[0] == '-' || rest[0] == '+' {
		return 0, errNotDuration(text)
	}

	var days time.Duration
	if i := strings.IndexByte(rest, 'd'); i > 0 && isDigits(rest[:i]) {
		n, err := strconv.ParseInt(rest[:i], 10, 64)
		if err != nil || n > maxDays {
			return 0, errTooLong(text)
		}
		days, rest = time.Duration(n)*day, rest[i+1:]
	}

	var clock time.Duration
	if rest != "" {
		d, err := time.ParseDuration(rest)
		if err != nil || d < 0 {
			return 0, errNotDuration(text)
		}
		if d > math.MaxInt64-days {
			return 0, errTooLong(text)
		}
		clock = d
	}

	return sign * (days + clock), nil
}

// FormatDuration renders d the way Sigil writes a duration literal: the
// largest units first, each at most once, "0s" for zero. So a policy's
// `bake: 15m` comes back as "15m" rather than Go's "15m0s". A sub-millisecond
// remainder, which Sigil can't write, is appended in nanoseconds so the value
// stays exact and ParseDuration still reads it.
func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}

	var b strings.Builder
	if d < 0 {
		b.WriteByte('-')
		d = -d
	}
	for _, u := range durationUnits {
		if n := d / u.size; n > 0 {
			fmt.Fprintf(&b, "%d%s", n, u.name)
			d -= n * u.size
		}
	}
	if d > 0 {
		fmt.Fprintf(&b, "%dns", d)
	}
	return b.String()
}

// String implements [fmt.Stringer]. It renders the duration like
// [FormatDuration].
func (d Duration) String() string {
	return FormatDuration(time.Duration(d))
}

// MarshalJSON implements [json.Marshaler]. It writes the duration as a string
// in Sigil's syntax.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON implements [json.Unmarshaler]. It reads a duration string
// with [ParseDuration]. A number is rejected with advice rather than guessed
// at, and so is text that isn't a duration; the error is a humane error.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return humane.New(fmt.Sprintf("durations are strings, found %s", data),
			`write durations as strings like "6h" or "1h30m"`)
	}
	parsed, herr := ParseDuration(text)
	if herr != nil {
		return herr
	}
	*d = Duration(parsed)
	return nil
}

// errNotDuration is the error for text that isn't a duration at all.
func errNotDuration(text string) humane.Error {
	return humane.New(fmt.Sprintf("%q isn't a duration", text), durationAdvice)
}

// errTooLong is the error for a duration beyond what time.Duration holds.
func errTooLong(text string) humane.Error {
	return humane.New(fmt.Sprintf("%q is longer than the longest duration, about 292 years", text),
		"use a realistic duration, such as \"6h\" or \"2d\"")
}

// isDigits reports whether s is a non-empty run of ASCII digits.
func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
