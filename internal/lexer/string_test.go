package lexer

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/token"
)

func TestUnquote(t *testing.T) {
	tests := []struct {
		lit  string
		want string
		msg  string
		pos  token.Pos // relative to the literal, when msg is set
		end  token.Pos
	}{
		{lit: `""`, want: ""},
		{lit: `"deployer"`, want: "deployer"},
		{lit: `"line one\nline two"`, want: "line one\nline two"},
		{lit: `"\a\b\f\n\r\t\v\\\""`, want: "\a\b\f\n\r\t\v\\\""},
		{lit: `"\x41é\U0001F680\101"`, want: "Aé🚀A"},
		{lit: `"é 🚀"`, want: "é 🚀"},
		{lit: `"tab	inside"`, want: "tab\tinside"},

		{lit: `"a\qb"`, msg: "unknown escape sequence `\\q`", pos: token.Pos{Offset: 2, Column: 2}, end: token.Pos{Offset: 4, Column: 4}},
		{lit: `"\'"`, msg: "unknown escape sequence `\\'`", pos: token.Pos{Offset: 1, Column: 1}, end: token.Pos{Offset: 3, Column: 3}},
		{lit: `"é\é"`, msg: "unknown escape sequence `\\é`", pos: token.Pos{Offset: 3, Column: 2}, end: token.Pos{Offset: 6, Column: 4}},
		{lit: `"\x4"`, msg: "unknown escape sequence `\\x`", pos: token.Pos{Offset: 1, Column: 1}, end: token.Pos{Offset: 3, Column: 3}},
		{lit: `"\u12"`, msg: "unknown escape sequence `\\u`", pos: token.Pos{Offset: 1, Column: 1}, end: token.Pos{Offset: 3, Column: 3}},
		{lit: `"\12"`, msg: "unknown escape sequence `\\1`", pos: token.Pos{Offset: 1, Column: 1}, end: token.Pos{Offset: 3, Column: 3}},
		{lit: `"\"`, msg: "unknown escape sequence `\\`", pos: token.Pos{Offset: 1, Column: 1}, end: token.Pos{Offset: 2, Column: 2}},
		// A lone surrogate is a valid-looking escape that Go still rejects.
		{lit: `"\uD800"`, msg: "invalid string literal"},
		{lit: `"\777"`, msg: "invalid string literal"},
		{lit: `"`, msg: "invalid string literal"},
		{lit: `abc`, msg: "invalid string literal"},
		{lit: "`raw`", msg: "invalid string literal"},
	}

	for _, tt := range tests {
		t.Run(tt.lit, func(t *testing.T) {
			got, err := Unquote(tt.lit)
			checkLiteralErr(t, err, tt.msg)
			if err != nil && (err.Pos != tt.pos || err.End != tt.end) {
				t.Errorf("span = %+v-%+v, want %+v-%+v", err.Pos, err.End, tt.pos, tt.end)
			}
			if got != tt.want {
				t.Errorf("Unquote(%q) = %q, want %q", tt.lit, got, tt.want)
			}
		})
	}
}
