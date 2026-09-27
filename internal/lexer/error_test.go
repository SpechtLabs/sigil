package lexer

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

func TestErrorString(t *testing.T) {
	e := &diag.Error{Msg: "unterminated string literal", Pos: token.Pos{Offset: 40, Line: 12, Column: 21}}
	if got, want := e.Error(), "12:21: unterminated string literal"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
