package kind_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

func TestDecisionSignature(t *testing.T) {
	k := deploy()
	tests := []struct {
		got, want string
	}{
		{k.Decision("deny").Signature(), "deny takes reason: not_eligible | soak_too_short | no_rule_matched"},
		{k.Decision("review").Signature(), "review takes reason: service_owner | everyone, and approvers: list<string>"},
		{k.Decision("approve").Signature(), "approve takes reason: release_manager | payments_sre | open, and bake: duration = 1h"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("Signature() = %q, want %q", tt.got, tt.want)
		}
	}
}

func TestDecisionSignatureWithSeveralFields(t *testing.T) {
	d := deploy().Decision("review")
	d.Fields = append(d.Fields, &kind.Field{Name: "ticket", Type: types.String})
	want := "review takes reason: service_owner | everyone, approvers: list<string>, and ticket: string"
	if got := d.Signature(); got != want {
		t.Errorf("Signature() = %q, want %q", got, want)
	}
}
