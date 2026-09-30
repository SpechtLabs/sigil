//go:build e2e

package e2e

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/deploy-gates/test/internal/fixture"
)

var _ = Describe("An evaluation that doesn't finish", func() {
	// The route template the request metrics label a deployment with.
	const route = "/api/v1/teams/:team/deployments"

	It("answers 503 with the fallback once the evaluation timeout passes, and counts a timeout", func() {
		timeouts := fixture.Labels{"team": fixture.TeamPayments, "kind": "timeout", "stage": "deploy"}
		before := scrapeMetrics(Default)

		// The stack runs with the default evaluation timeout, one second.
		start := time.Now()
		resp, out := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.SlowToDecide()))
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		fixture.ExpectTimedOut(Default, resp, out, fixture.TeamPayments)

		after := scrapeMetrics(Default)
		Expect(after.Value(fixture.MetricEvalErrors, timeouts) - before.Value(fixture.MetricEvalErrors, timeouts)).
			To(BeNumerically("==", 1))
		Expect(after.Sum(fixture.MetricDecisions, nil)).To(Equal(before.Sum(fixture.MetricDecisions, nil)))
	})

	It("answers 499 and counts no error when the client leaves during the evaluation", func() {
		closed := fixture.Labels{"code": "499", "method": http.MethodPost, "url": route}
		before := scrapeMetrics(Default)

		deploygate.Abandon(Default, fixture.DeploymentsPath(fixture.TeamPayments),
			fixture.OwnerRequest(fixture.SlowToDecide()), 200*time.Millisecond)

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
