package integration

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/codes"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// routeRoute is the route endpoint's route template, the route label of its
// request metrics.
const routeRoute = "/api/v1/teams/:team/route"

// slowNames is the size of the list the slow policy walks: a hundred million
// steps, well over a second unbounded.
const slowNames = 10_000

var _ = Describe("An evaluation that doesn't finish", func() {
	// The shipped policies decide in microseconds whatever the alert, so
	// every spec here swaps checkout's policy for one that doesn't.
	var e *env

	Context("when it runs past the evaluation timeout", func() {
		BeforeEach(func() {
			e = newEnv(withCopiedTeams(), withEvaluationTimeout(100*time.Millisecond))
			e.writeTeamFile(checkoutPolicy, fixture.SlowPolicy(slowNames))
			e.reloadOK()
			e.spans.Reset()
		})

		It("answers 503 with the fallback, dispatches it, and counts a timeout, not a decision", func() {
			start := time.Now()
			resp, out := e.client.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning))
			// Unbounded, the policy takes well over a second.
			Expect(time.Since(start)).To(BeNumerically("<", time.Second))
			fixture.ExpectFallback(Default, fixture.TeamCheckout, http.StatusServiceUnavailable, resp, out, "evaluation timeout")

			// The fallback still goes out, so the alert isn't lost.
			Expect(e.notifier.notifications()).To(ConsistOf(And(
				HaveField("Decision", fixture.DecisionNotify),
				HaveField("Reason", fixture.ReasonUnrouted),
				HaveField("Channel", fixture.DefaultChannel),
			)))

			families := e.families()
			Expect(families.Value(fixture.MetricEvalErrors, fixture.Labels{"team": fixture.TeamCheckout, "kind": "timeout"})).To(BeNumerically("==", 1))
			Expect(families.Count(fixture.MetricEvalErrors, nil)).To(Equal(1))
			Expect(families.Count(fixture.MetricDecisions, nil)).To(BeZero())
			Expect(families.Value(fixture.MetricAlertsRouted, fixture.Labels{"team": fixture.TeamCheckout, "outcome": fixture.StatusFailed})).To(BeNumerically("==", 1))

			// A 503 is alertrouter's failure, so both the route span and the
			// request span say so.
			Expect(e.waitForSpan(spanRoute).Status.Code).To(Equal(codes.Error))
			Expect(e.serverSpan().Status.Code).To(Equal(codes.Error))
			Eventually(func(g Gomega) {
				g.Expect(e.families().Value(fixture.MetricRequests, fixture.Labels{
					"code": "503", "method": http.MethodPost, "route": routeRoute,
				})).To(BeNumerically("==", 1))
			}).Should(Succeed())
		})

		It("fails the webhook's alert alone and still answers the batch with 200", func() {
			batch := fixture.Batch{
				Webhook: fixture.NewWebhook(
					fixture.Firing("slow", fixture.AlertLabels(fixture.TeamCheckout, fixture.CheckoutLatency, fixture.SeverityWarning), time.Now().Add(-time.Minute)),
					fixture.Firing("fast", fixture.AlertLabels(fixture.TeamPayments, "PaymentsErrorRate", fixture.SeverityCritical), time.Now().Add(-time.Minute)),
				),
				Results: []fixture.ResultCase{
					{Fingerprint: "slow", Status: fixture.StatusFailed, Team: fixture.TeamCheckout, Want: fixture.Unrouted},
					{
						Fingerprint: "fast", Status: fixture.StatusRouted, Team: fixture.TeamPayments,
						Want: fixture.Route{Decision: fixture.DecisionPage, Reason: fixture.ReasonCriticalAlert, Target: fixture.PaymentsOncall},
					},
				},
			}

			resp, out := e.client.Webhook(Default, batch.Webhook)
			fixture.ExpectBatch(Default, batch, resp, out)
			Expect(out.Results[0].Error).To(ContainSubstring("evaluation timeout"))
			Expect(e.notifier.notifications()).To(HaveLen(2))
		})
	})

	It("answers 499 and counts no error when the client leaves during the evaluation", func() {
		// The server's own timeout is a second; the client gives up long
		// before, so it's the client's cancellation that stops the
		// evaluation.
		e = newEnv(withCopiedTeams())
		e.writeTeamFile(checkoutPolicy, fixture.SlowPolicy(slowNames))
		e.reloadOK()
		e.spans.Reset()

		e.client.Abandon(Default, fixture.RoutePath(fixture.TeamCheckout),
			fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning), 100*time.Millisecond)

		Eventually(func(g Gomega) {
			g.Expect(e.families().Value(fixture.MetricRequests, fixture.Labels{
				"code": "499", "method": http.MethodPost, "route": routeRoute,
			})).To(BeNumerically("==", 1))
		}).WithTimeout(5 * time.Second).Should(Succeed())

		// Nothing failed, in the policy or in alertrouter, and no one is
		// left to tell: no evaluation error, no decision, no notification,
		// and no span marked as an error, though the route span records
		// why it stopped.
		families := e.families()
		Expect(families.Count(fixture.MetricEvalErrors, nil)).To(BeZero())
		Expect(families.Count(fixture.MetricDecisions, nil)).To(BeZero())
		Expect(e.notifier.notifications()).To(BeEmpty())

		routeSpan := e.waitForSpan(spanRoute)
		Expect(routeSpan.Status.Code).To(Equal(codes.Unset))
		Expect(routeSpan.Events).To(ContainElement(HaveField("Name", "exception")))
		Expect(e.serverSpan().Status.Code).To(Equal(codes.Unset))
	})
})
