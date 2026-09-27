package lexer

import "testing"

func TestParseInt(t *testing.T) {
	tests := []struct {
		text string
		want int64
		msg  string
	}{
		{text: "0", want: 0},
		{text: "42", want: 42},
		{text: "007", want: 7},
		{text: "9223372036854775807", want: 9223372036854775807},
		{text: "9223372036854775808", msg: "integer literal `9223372036854775808` is out of range"},
		{text: "99999999999999999999", msg: "integer literal `99999999999999999999` is out of range"},
		{text: "", msg: "invalid integer literal ``"},
		{text: "1a", msg: "invalid integer literal `1a`"},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got, err := ParseInt(tt.text)
			checkLiteralErr(t, err, tt.msg)
			if got != tt.want {
				t.Errorf("ParseInt(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}
