package kind_test

import "testing"

func TestDecisionSignature(t *testing.T) {
	k := deploy()
	tests := []struct {
		got, want string
	}{
		{k.Decision("deny").Signature(), "decision deny(reason: string)"},
		{k.Decision("review").Signature(), "decision review(reason: string, approvers: list<string>)"},
		{k.Decision("approve").Signature(), "decision approve(reason: string, bake: duration = 1h)"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("Signature() = %q, want %q", tt.got, tt.want)
		}
	}
}
