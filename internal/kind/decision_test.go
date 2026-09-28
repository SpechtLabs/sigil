package kind_test

import "testing"

func TestDecisionSignature(t *testing.T) {
	k := deploy()
	tests := []struct {
		got, want string
	}{
		{k.Decision("deny").Signature(), "decision deny { not_eligible, soak_too_short, no_rule_matched }"},
		{k.Decision("review").Signature(), "decision review(approvers: list<string>) { service_owner, everyone }"},
		{k.Decision("approve").Signature(), "decision approve(bake: duration = 1h) { release_manager, payments_sre, open }"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("Signature() = %q, want %q", tt.got, tt.want)
		}
	}
}
