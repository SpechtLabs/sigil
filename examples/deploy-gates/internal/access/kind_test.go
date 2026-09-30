package access_test

import (
	"os"
	"testing"

	"github.com/spechtlabs/sigil/pkg/policy"
	"github.com/spechtlabs/sigil/pkg/policytest"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/access"
)

// TestKindFileIsCurrent fails when policies/access_grant.sigil is stale.
// Regenerate it with `go generate ./cmd/sigilc`.
func TestKindFileIsCurrent(t *testing.T) {
	policytest.Schema(t, access.Kind, "../../policies/access_grant.sigil")
}

// TestKindRecoversHostPanics fails when the kind stops recovering host
// function panics. It has no host function yet to make panic, so the
// option is checked where the kind records it rather than by a panic.
func TestKindRecoversHostPanics(t *testing.T) {
	if !access.Kind.Contract().Binding.RecoverHostPanics {
		t.Error("access.Kind doesn't set policy.WithRecoverHostPanics")
	}
}

// TestPolicies runs policies/access/main_test.yaml against the access bundle,
// loaded the way the service loads it: the platform's access documents are
// the trusted source of the required guardrails. The trusted source is
// platform/access, not platform: a bundle holding documents of another kind
// doesn't load, and platform/deploy holds the DeployApproval documents.
func TestPolicies(t *testing.T) {
	policytest.Run(t, access.Kind, os.DirFS("../../policies/access"),
		policy.Require("access.guardrails", policy.From(os.DirFS("../../policies/platform/access"))))
}
