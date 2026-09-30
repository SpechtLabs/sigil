//go:build e2e

package e2e

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/deploy-gates/test/internal/fixture"
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
	It("lists both kinds with the policies each serves", func() {
		fixture.ExpectServedKinds(Default, deploygate.ListPolicies(Default))
	})

	It("reloads both bundles on request and moves each loaded_at forward", func() {
		before := deploygate.ListPolicies(Default)

		resp, body := deploygate.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		// The reload answers with the same body as GET /api/v1/policies,
		// so a client learns what is now loaded without a second call.
		after := fixture.Decode[fixture.PoliciesResponse](Default, body)
		fixture.ExpectServedKinds(Default, after)
		for _, k := range before.Kinds {
			now, ok := after.Kind(k.Kind)
			Expect(ok).To(BeTrue())
			Expect(now.LoadedAt).NotTo(BeTemporally("<", k.LoadedAt), k.Kind)
		}
	})
})
