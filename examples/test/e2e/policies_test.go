//go:build e2e

package e2e

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

var _ = Describe("Health", func() {
	It("reports healthy once the process serves", func() {
		resp, body := deploygate.Get(Default, fixture.PathHealthz)

		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		Expect(body).To(MatchJSON(`{"status": "ok"}`))
	})

	It("reports ready with the time the bundle loaded", func() {
		resp, body := deploygate.Get(Default, fixture.PathReadyz)

		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		Expect(fixture.Decode[map[string]any](Default, body)).To(And(
			HaveKeyWithValue("status", "ready"),
			HaveKey("loaded_at"),
		))
	})
})

var _ = Describe("The policy bundle", func() {
	It("lists every served team's policy under the DeployApproval kind", func() {
		got := deploygate.ListPolicies(Default)

		Expect(got.Kind).To(Equal("DeployApproval"))
		Expect(got.Version).To(Equal(1))
		Expect(got.Source).NotTo(BeEmpty())
		Expect(got.LoadedAt).NotTo(BeZero())
		Expect(got.Policies).To(ConsistOf(
			fixture.PolicyRef{Team: fixture.TeamPayments, Policy: "payments.production"},
			fixture.PolicyRef{Team: fixture.TeamCheckout, Policy: "checkout.production"},
		))
	})

	It("reloads on request and moves loaded_at forward", func() {
		before := deploygate.ListPolicies(Default)

		resp, body := deploygate.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		// The reload answers with the same body as GET /api/v1/policies,
		// so a client learns what is now loaded without a second call.
		after := fixture.Decode[fixture.PoliciesResponse](Default, body)
		Expect(after.Kind).To(Equal("DeployApproval"))
		Expect(after.Policies).To(ConsistOf(before.Policies))
		Expect(after.LoadedAt).NotTo(BeTemporally("<", before.LoadedAt))
		Expect(deploygate.ListPolicies(Default).LoadedAt).To(BeTemporally(">=", after.LoadedAt))
	})
})
