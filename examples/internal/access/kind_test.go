package access_test

import (
	"os"
	"testing"

	"github.com/spechtlabs/sigil/pkg/policy"
	"github.com/spechtlabs/sigil/pkg/policytest"

	"github.com/spechtlabs/sigil/examples/internal/access"
)

// TestKindFileIsCurrent fails when policies/access_grant.sigil is stale.
// Regenerate it with `go generate ./cmd/sigilc`.
func TestKindFileIsCurrent(t *testing.T) {
	policytest.Schema(t, access.Kind, "../../policies/access_grant.sigil")
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
