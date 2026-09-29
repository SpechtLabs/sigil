package deploy_test

import (
	"os"
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

// TestPolicies runs every *_test.yaml under policies/teams against the team
// bundle, loaded the way the service loads it: the platform's deploy
// documents are the trusted source of the required guardrails. The trusted
// source is platform/deploy, not platform: a bundle holding documents of
// another kind doesn't load, and platform/access holds the AccessGrant ones.
func TestPolicies(t *testing.T) {
	policytest.Run(t, deploy.Kind, os.DirFS("../../policies/teams"),
		policy.Require("deploy.guardrails", policy.From(os.DirFS("../../policies/platform/deploy"))))
}
