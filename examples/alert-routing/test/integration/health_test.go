package integration

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
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

		It("answers 503 instead of guessing a route, so Alertmanager retries the batch", func() {
			resp, body := e.client.PostJSON(Default, fixture.RoutePath(fixture.TeamCheckout),
				fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))
			Expect(resp).To(HaveHTTPStatus(http.StatusServiceUnavailable))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error).NotTo(BeNil())

			resp, body = e.client.PostJSON(Default, fixture.PathAlerts, fixture.MixedBatch(time.Now()).Webhook)
			Expect(resp).To(HaveHTTPStatus(http.StatusServiceUnavailable))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error).NotTo(BeNil())

			resp, _ = e.client.Get(Default, fixture.PathPolicies)
			Expect(resp).To(HaveHTTPStatus(http.StatusServiceUnavailable))

			// Nothing was routed, so nothing may have been dispatched.
			Expect(e.notifier.notifications()).To(BeEmpty())
		})

		It("still lists the team directory, which doesn't depend on the bundle", func() {
			fixture.ExpectDefaultTeams(Default, e.client.ListTeams(Default))
		})

		It("becomes ready once a reload succeeds", func() {
			e.reloadOK()

			resp, body := e.client.Get(Default, fixture.PathReadyz)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))
			Expect(body).To(MatchJSON(`{"status": "ready", "loaded_at": "2026-09-28T12:00:00Z"}`))

			resp, _ = e.client.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		})
	})

	It("reports ready with the time the bundle loaded", func() {
		resp, body := shared.client.Get(Default, fixture.PathReadyz)

		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		Expect(body).To(MatchJSON(`{"status": "ready", "loaded_at": "` + clockStart.Format(time.RFC3339) + `"}`))
	})
})
