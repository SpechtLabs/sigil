package lsp

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/lexer"
	"github.com/spechtlabs/sigil/internal/token"
)

// TestTypeOfTokens checks the types a param's type tokens spell.
func TestTypeOfTokens(t *testing.T) {
	v, _ := viewOf(t, "production.sigil", head)
	k := v.kindNamed("DeployApproval").Model
	tests := []struct {
		src  string
		want string // "" for none
	}{
		{"duration", "duration"},
		{"Tier", "Tier"},
		{"Ticket", "Ticket"},
		{"?Ticket", "?Ticket"},
		{"list<Tier>", "list<Tier>"},
		{"list<list<string> >", "list<list<string>>"},
		{"map<string, list<Tier>>", "map<string, list<Tier>>"},
		{"?map<string, int>", "?map<string, int>"},
		{"nope", ""},
		{"list<nope>", ""},
		{"list<string", ""},
		{"map<string>", ""},
		{"map<string, >", ""},
		{"?", ""},
		{"1", ""},
		{"", ""},
	}
	for _, tt := range tests {
		var toks []token.Token
		l := lexer.New([]byte(tt.src))
		for tok := l.Next(); tok.Kind != token.EOF; tok = l.Next() {
			toks = append(toks, tok)
		}
		typ, rest := typeOfTokens(toks, k)
		got := ""
		if typ != nil && len(rest) == 0 {
			got = typ.String()
		}
		if got != tt.want {
			t.Errorf("typeOfTokens(%q) = %q, want %q", tt.src, got, tt.want)
		}
	}
}
