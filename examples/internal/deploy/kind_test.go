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

// TestPolicies runs every *_test.yaml under policies/teams against the team
// bundle, loaded the way the service loads it: the platform's deploy
// documents are the trusted source of the required guardrails. The trusted
// source is platform/deploy, not platform: a bundle holding documents of
// another kind doesn't load, and platform/access holds the AccessGrant ones.
func TestPolicies(t *testing.T) {
	policytest.Run(t, deploy.Kind, os.DirFS("../../policies/teams"),
		policy.Require("deploy.guardrails", policy.From(os.DirFS("../../policies/platform/deploy"))))
}
