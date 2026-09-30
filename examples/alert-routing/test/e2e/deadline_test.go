//go:build e2e

package e2e

import (
	"net/http"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// The shipped policies decide in microseconds whatever the alert, so these
// specs swap checkout's policy for one that runs for many seconds, far past
// the stack's evaluation timeout of one second. Serial, like the reload
// specs, because every other checkout spec would time out meanwhile.
var _ = Describe("An evaluation that doesn't finish", Serial, func() {
	// The route template the request metrics label a route with.
	const route = "/api/v1/teams/:team/route"

	BeforeEach(func() {
		dir := requireWritable(policiesDir)
		replaceFile(filepath.Join(dir, checkoutPolicy), fixture.SlowPolicy(30_000))
		expectReloadOK()
	})

	It("answers 503 with the fallback once the evaluation timeout passes, and counts a timeout", func() {
		timeouts := fixture.Labels{"team": fixture.TeamCheckout, "kind": "timeout"}
		before := scrapeMetrics(Default)

		start := time.Now()
		resp, out := alertrouter.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning))
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		fixture.ExpectFallback(Default, fixture.TeamCheckout, http.StatusServiceUnavailable, resp, out, "evaluation timeout")

		after := scrapeMetrics(Default)
		Expect(after.Value(fixture.MetricEvalErrors, timeouts) - before.Value(fixture.MetricEvalErrors, timeouts)).
			To(BeNumerically("==", 1))
		Expect(after.Sum(fixture.MetricDecisions, nil)).To(Equal(before.Sum(fixture.MetricDecisions, nil)))
	})

	It("marks the alert of a webhook as failed and still notifies #alerts", func() {
		batch := fixture.Batch{
			Webhook: fixture.NewWebhook(fixture.Firing("slow", fixture.AlertLabels(fixture.TeamCheckout, fixture.CheckoutLatency, fixture.SeverityWarning), time.Now().Add(-time.Minute))),
			Results: []fixture.ResultCase{{Fingerprint: "slow", Status: fixture.StatusFailed, Team: fixture.TeamCheckout, Want: fixture.Unrouted}},
		}
		failed := fixture.Labels{"team": fixture.TeamCheckout, "outcome": fixture.StatusFailed}
		before := scrapeMetrics(Default).Value(fixture.MetricAlertsRouted, failed)

		resp, out := alertrouter.Webhook(Default, batch.Webhook)
		fixture.ExpectBatch(Default, batch, resp, out)

		Expect(scrapeMetrics(Default).Value(fixture.MetricAlertsRouted, failed) - before).To(BeNumerically("==", 1))
	})

	It("answers 499 and counts no error when the client leaves during the evaluation", func() {
		closed := fixture.Labels{"code": "499", "method": http.MethodPost, "route": route}
		before := scrapeMetrics(Default)

		alertrouter.Abandon(Default, fixture.RoutePath(fixture.TeamCheckout),
			fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning), 200*time.Millisecond)

		// The server notices the closed connection and stops the
		// evaluation a moment after the client gave up.
		var after fixture.Families
		Eventually(func(g Gomega) {
			after = scrapeMetrics(g)
			g.Expect(after.Value(fixture.MetricRequests, closed) - before.Value(fixture.MetricRequests, closed)).
				To(BeNumerically("==", 1))
		}).WithTimeout(5 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())

		// Nothing failed: the client left on its own.
		Expect(after.Sum(fixture.MetricEvalErrors, nil)).To(Equal(before.Sum(fixture.MetricEvalErrors, nil)))
		Expect(after.Sum(fixture.MetricDecisions, nil)).To(Equal(before.Sum(fixture.MetricDecisions, nil)))
	})
})
