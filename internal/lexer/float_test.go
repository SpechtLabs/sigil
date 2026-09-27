package lexer

import "testing"

func TestParseFloat(t *testing.T) {
	tests := []struct {
		text string
		want float64
		msg  string
	}{
		{text: "0.5", want: 0.5},
		{text: "3.0", want: 3},
		{text: "1" + zeros(400) + ".0", msg: "float literal `1" + zeros(400) + ".0` is out of range"},
		{text: "x", msg: "invalid float literal `x`"},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got, err := ParseFloat(tt.text)
			checkLiteralErr(t, err, tt.msg)
			if got != tt.want {
				t.Errorf("ParseFloat(%q) = %g, want %g", tt.text, got, tt.want)
			}
		})
	}
}

func zeros(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}
