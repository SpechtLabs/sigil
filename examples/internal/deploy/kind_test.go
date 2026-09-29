package deploy_test

import (
	"os"
	"slices"
	"testing"

	"github.com/spechtlabs/sigil/pkg/policy"
	"github.com/spechtlabs/sigil/pkg/policytest"

	"github.com/spechtlabs/sigil/examples/internal/deploy"
)

// TestKindFileIsCurrent fails when policies/deploy_approval.sigil is stale.
// Regenerate it with `go generate ./cmd/sigilc`.
func TestKindFileIsCurrent(t *testing.T) {
	policytest.Schema(t, deploy.Kind, "../../policies/deploy_approval.sigil")
}

// TestKindRecoversHostPanics fails when the kind stops recovering host
// function panics. split can't be made to panic from a policy, so the
// option is checked where the kind records it rather than by a panic.
func TestKindRecoversHostPanics(t *testing.T) {
	if !deploy.Kind.Contract().Binding.RecoverHostPanics {
		t.Error("deploy.Kind doesn't set policy.WithRecoverHostPanics")
	}
}

// TestTiersMatchKind fails when [deploy.Tiers] and the kind's enum Tier
// drift apart, which would make deploygate refuse a tier a policy can read,
// or pass one through that fails the evaluation.
func TestTiersMatchKind(t *testing.T) {
	enum := deploy.Kind.Contract().Model.Enum("Tier")
	if enum == nil {
		t.Fatal("deploy.Kind declares no enum Tier")
	}
	got := make([]string, len(deploy.Tiers))
	for i, tier := range deploy.Tiers {
		got[i] = string(tier)
	}
	if !slices.Equal(got, enum.Values) {
		t.Errorf("deploy.Tiers = %v, the kind declares %v", got, enum.Values)
	}
}

func TestTierValid(t *testing.T) {
	tests := []struct {
		tier deploy.Tier
		want bool
	}{
		{tier: deploy.TierCritical, want: true},
		{tier: deploy.TierStandard, want: true},
		{tier: deploy.TierInternal, want: true},
		{tier: "", want: false},
		{tier: "critcal", want: false},
		{tier: "Critical", want: false},
	}
	for _, tt := range tests {
		t.Run(string(tt.tier), func(t *testing.T) {
			if got := tt.tier.Valid(); got != tt.want {
				t.Errorf("Tier(%q).Valid() = %v, want %v", tt.tier, got, tt.want)
			}
		})
	}
}

// TestPolicies runs every *_test.yaml under policies/teams against the team
// bundle, loaded the way the service loads it: the platform's deploy
// documents are the trusted source of the required guardrails. The trusted
// source is platform/deploy, not platform: a bundle holding documents of
// another kind doesn't load, and platform/access holds the AccessGrant ones.
func TestPolicies(t *testing.T) {
	policytest.Run(t, deploy.Kind, os.DirFS("../../policies/teams"),
		policy.Require("deploy.guardrails", policy.From(os.DirFS("../../policies/platform/deploy"))))
}
