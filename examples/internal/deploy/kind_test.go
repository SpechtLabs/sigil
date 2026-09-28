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

// TestPolicies runs every *_test.yaml under policies/ against the bundle,
// loaded the way the service loads it: the platform's documents are the
// trusted source of the required guardrails.
func TestPolicies(t *testing.T) {
	policytest.Run(t, deploy.Kind, os.DirFS("../../policies/teams"),
		policy.Require("deploy.guardrails", policy.From(os.DirFS("../../policies/platform"))))
}
