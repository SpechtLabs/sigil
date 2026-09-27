package lexer

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		text string
		want time.Duration
		msg  string
		help string
	}{
		{text: "0s", want: 0},
		{text: "500ms", want: 500 * time.Millisecond},
		{text: "30m", want: 30 * time.Minute},
		{text: "1h30m", want: 90 * time.Minute},
		{text: "2d", want: 48 * time.Hour},
		{text: "1d2h3m4s5ms", want: 26*time.Hour + 3*time.Minute + 4*time.Second + 5*time.Millisecond},
		{text: "1d5ms", want: 24*time.Hour + 5*time.Millisecond},
		// Components can exceed the next unit; only order and uniqueness are rules.
		{text: "90m", want: 90 * time.Minute},
		{text: "1h90m", want: 150 * time.Minute},
		{text: "106751d", want: 106751 * 24 * time.Hour},

		{text: "30m1h", msg: "units in `30m1h` aren't in descending order", help: "write the largest unit first, like `1h30m`"},
		{text: "1ms1s", msg: "units in `1ms1s` aren't in descending order", help: "write the largest unit first, like `1h30m`"},
		{text: "1h1h", msg: "unit `h` appears twice in `1h1h`", help: "each unit may appear once in a duration; add the components together"},
		{text: "1h30m2h", msg: "units in `1h30m2h` aren't in descending order", help: "write the largest unit first, like `1h30m`"},
		{text: "106752d", msg: "duration literal `106752d` is out of range", help: "a duration is at most about 292 years"},
		{text: "99999999999999999999s", msg: "duration literal `99999999999999999999s` is out of range", help: "a duration is at most about 292 years"},
		{text: "106751d24h", msg: "duration literal `106751d24h` is out of range", help: "a duration is at most about 292 years"},
		// Shapes the lexer never produces still fail cleanly.
		{text: "", want: 0},
		{text: "1", msg: "invalid duration literal `1`"},
		{text: "h", msg: "invalid duration literal `h`"},
		{text: "1x", msg: "invalid duration literal `1x`"},
		{text: "1h x", msg: "invalid duration literal `1h x`"},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got, err := ParseDuration(tt.text)
			checkLiteralErr(t, err, tt.msg)
			if err != nil && err.Help != tt.help {
				t.Errorf("Help = %q, want %q", err.Help, tt.help)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
