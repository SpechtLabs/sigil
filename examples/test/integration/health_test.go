package integration

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

var _ = Describe("Health and readiness", func() {
	Context("before the first load", func() {
		var e *env

		BeforeEach(func() {
			e = newEnv(unloaded())
		})

		It("is healthy, so the orchestrator doesn't restart it, but not ready", func() {
			resp, body := e.client.Get(Default, fixture.PathHealthz)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))
			Expect(body).To(MatchJSON(`{"status": "ok"}`))

			resp, body = e.client.Get(Default, fixture.PathReadyz)
			Expect(resp).To(HaveHTTPStatus(http.StatusServiceUnavailable))
			Expect(body).To(MatchJSON(`{"status": "not ready"}`))
		})

		It("answers 503 instead of guessing a decision", func() {
			resp, body := e.client.PostJSON(Default, fixture.DeploymentsPath(fixture.TeamPayments), fixture.OwnerRequest())
			Expect(resp).To(HaveHTTPStatus(http.StatusServiceUnavailable))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error).NotTo(BeNil())

			resp, _ = e.client.Get(Default, fixture.PathPolicies)
			Expect(resp).To(HaveHTTPStatus(http.StatusServiceUnavailable))
		})

		It("becomes ready once a reload succeeds", func() {
			resp, _ := e.client.Reload(Default)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))

			resp, body := e.client.Get(Default, fixture.PathReadyz)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))
			Expect(body).To(MatchJSON(`{"status": "ready", "loaded_at": "2026-09-28T12:00:00Z"}`))

			resp, _ = e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
			Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		})
	})

	It("reports ready with the time the bundle loaded", func() {
		resp, body := shared.client.Get(Default, fixture.PathReadyz)

		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		Expect(body).To(MatchJSON(`{"status": "ready", "loaded_at": "2026-09-28T12:00:00Z"}`))
	})
})
