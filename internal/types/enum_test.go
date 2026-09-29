package types_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/types"
)

func TestEnum(t *testing.T) {
	if got, want := tier.String(), "Tier"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := tier.ValueNames(), "critical, standard, internal"; got != want {
		t.Errorf("ValueNames() = %q, want %q", got, want)
	}
	if got := (&types.Enum{Name: "Empty"}).ValueNames(); got != "" {
		t.Errorf("ValueNames() of an enum without values = %q, want empty", got)
	}
	tests := []struct {
		value string
		want  bool
	}{
		{"critical", true},
		{"internal", true},
		{"critcal", false},
		{"Critical", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := tier.Has(tt.value); got != tt.want {
			t.Errorf("Has(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}
