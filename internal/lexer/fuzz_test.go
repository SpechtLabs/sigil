package lexer_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/lexer"
	"github.com/spechtlabs/sigil/internal/token"
)

// Every token must consume input, preserve its byte span, and eventually
// reach a stable EOF, even for invalid UTF-8 and incomplete literals.
func FuzzLexer(f *testing.F) {
	for _, src := range []string{"", "policy p: K@1\nwhen true { allow(ok) }", "1h30m 1.25 9223372036854775808", "\"\\u1234\" `raw\ntext` // comment", "---\n?. ?? >= !=", "\x00\xff\r\n"} {
		f.Add([]byte(src))
	}
	f.Add([]byte(strings.Repeat("9", 400) + ".0"))
	f.Fuzz(func(t *testing.T, src []byte) {
		l := lexer.New(src)
		end := 0
		for range len(src) + 1 {
			tok := l.Next()
			if tok.Pos.Offset < end || tok.End.Offset < tok.Pos.Offset || tok.End.Offset > len(src) || tok.Pos.Line < 1 || tok.Pos.Column < 1 {
				t.Fatalf("invalid token span: %+v", tok)
			}
			if tok.Kind == token.EOF {
				if tok.Pos.Offset != len(src) || l.Next() != tok {
					t.Fatal("EOF is not stable at the end of the source")
				}
				return
			}
			if tok.End.Offset <= tok.Pos.Offset || tok.Text != string(src[tok.Pos.Offset:tok.End.Offset]) {
				t.Fatalf("token did not consume its text: %+v", tok)
			}
			end = tok.End.Offset
			switch tok.Kind {
			case token.Int:
				if _, err := lexer.ParseInt(tok.Text); err != nil {
					t.Fatalf("accepted invalid int: %v", err)
				}
			case token.Float:
				if _, err := lexer.ParseFloat(tok.Text); err != nil {
					t.Fatalf("accepted invalid float: %v", err)
				}
			case token.Duration:
				if _, err := lexer.ParseDuration(tok.Text); err != nil {
					t.Fatalf("accepted invalid duration: %v", err)
				}
			case token.String:
				if _, err := lexer.Unquote(tok.Text); err != nil {
					t.Fatalf("accepted invalid string: %v", err)
				}
			}
		}
		t.Fatal("lexer did not terminate")
	})
}

func FuzzStringRoundTrip(f *testing.F) {
	for _, s := range []string{"", "hello", "\n\t\"\\", "世界", "\x00\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := lexer.Unquote(strconv.Quote(s))
		if err != nil || got != s {
			t.Fatalf("Unquote(Quote(%q)) = %q, %v", s, got, err)
		}
	})
}
