package integration

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/codes"

	"github.com/spechtlabs/sigil/examples/deploy-gates/test/internal/fixture"
)

// deploymentsRoute is the deployments endpoint's route template, the url
// label of its request metrics.
const deploymentsRoute = "/api/v1/teams/:team/deployments"

var _ = Describe("An evaluation that doesn't finish", func() {
	It("answers 503 with the fallback when the deploy stage runs out of time, and counts a timeout", func() {
		e := newEnv(withEvaluationTimeout(100 * time.Millisecond))

		start := time.Now()
		resp, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.SlowToDecide()))
		// Unbounded, the region check takes many seconds.
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		fixture.ExpectTimedOut(Default, resp, out, fixture.TeamPayments)

		families := e.families()
		Expect(families.Value(fixture.MetricEvalErrors, fixture.Labels{
			"team": fixture.TeamPayments, "kind": "timeout", "stage": "deploy",
		})).To(BeNumerically("==", 1))
		Expect(families.Count(fixture.MetricEvalErrors, nil)).To(Equal(1))
		Expect(families.Count(fixture.MetricDecisions, nil)).To(BeZero())

		// A 503 is deploygate's failure, so both the evaluation span and
		// the request span say so.
		Expect(e.waitForSpan("deploygate.evaluate").Status.Code).To(Equal(codes.Error))
		Expect(e.serverSpan().Status.Code).To(Equal(codes.Error))
		Eventually(func(g Gomega) {
			g.Expect(e.families().Value(fixture.MetricRequests, fixture.Labels{
				"code": "503", "method": http.MethodPost, "url": deploymentsRoute,
			})).To(BeNumerically("==", 1))
		}).Should(Succeed())
	})

	It("answers 499 and counts no error when the client leaves during the evaluation", func() {
		// The server's own timeout is a second; the client gives up long
		// before, so it's the client's cancellation that stops the
		// evaluation.
		e := newEnv()
		e.client.Abandon(Default, fixture.DeploymentsPath(fixture.TeamPayments),
			fixture.OwnerRequest(fixture.SlowToDecide()), 100*time.Millisecond)

		Eventually(func(g Gomega) {
			g.Expect(e.families().Value(fixture.MetricRequests, fixture.Labels{
				"code": "499", "method": http.MethodPost, "url": deploymentsRoute,
			})).To(BeNumerically("==", 1))
		}).WithTimeout(5 * time.Second).Should(Succeed())

		// Nothing failed, in the policy or in deploygate: no evaluation
		// error, no decision, and the evaluation span isn't marked as an
		// error, though it records why it stopped. The request span is:
		// otelgin follows the HTTP conventions, which count a client that
		// left as an error on the server span.
		families := e.families()
		Expect(families.Count(fixture.MetricEvalErrors, nil)).To(BeZero())
		Expect(families.Count(fixture.MetricDecisions, nil)).To(BeZero())

		evalSpan := e.waitForSpan("deploygate.evaluate")
		Expect(evalSpan.Status.Code).To(Equal(codes.Unset))
		Expect(evalSpan.Events).To(ContainElement(HaveField("Name", "exception")))
		server := e.serverSpan()
		Expect(server.Status.Code).To(Equal(codes.Error))
		Expect(server.Status.Description).To(Equal("context canceled"))
	})
})
