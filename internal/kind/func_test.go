package kind_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

func TestFuncSignature(t *testing.T) {
	k := deploy()
	tests := []struct {
		got, want string
	}{
		{k.Func("split").Signature(), "fn split(s: string, sep: string) -> list<string>"},
		{(&kind.Func{Name: "now", Result: types.Timestamp}).Signature(), "fn now() -> timestamp"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("Signature() = %q, want %q", tt.got, tt.want)
		}
	}
}
