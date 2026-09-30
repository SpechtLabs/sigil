package integration

import (
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// What the failure specs add to their copy of checkout's policy, after the
// line that invokes the platform's routing.
const (
	checkoutPolicy = "checkout/alerts.sigil"
	checkoutRules  = `routing(muted: ["CheckoutCanaryLatency"])`

	// conflictingRule posts a production warning to a second channel for
	// the same reason the platform posts it to the team's, and the kind
	// can't pick one: a conflict, the policy's defect.
	conflictingRule = checkoutRules + "\n\nwhen alert.severity == warning {\n  notify(reason: routine, channel: \"#checkout-oncall\")\n}\n"

	// failingRule reads past the end of a list for every alert, a runtime
	// error.
	failingRule = checkoutRules + "\n\nlet names = [\"only\"]\n\nwhen names[1] == alert.name {\n  drop(reason: muted)\n}\n"

	// assertingRule asserts that every alert names its service, which an
	// alert without a service label fails before any rule runs: the
	// alert's fault, an input assert.
	assertingRule = checkoutRules + "\n\nassert(\"names_service\", alert.labels[\"service\"] != \"\")\n"
)

var _ = Describe("Receiving an Alertmanager webhook", func() {
	It("routes every firing alert, acknowledges the resolved one and loses none", func() {
		e := newEnv()
		batch := fixture.MixedBatch(time.Now())

		resp, out := e.client.Webhook(Default, batch.Webhook)
		fixture.ExpectBatch(Default, batch, resp, out)
		unknown, ok := out.Result("unknown-team")
		Expect(ok).To(BeTrue())
		Expect(unknown.Error).To(ContainSubstring(`"marketing"`))

		By("handing exactly one notification per firing alert to the dispatcher, in order")
		got := e.notifier.notifications()
		Expect(got).To(HaveLen(batch.Firing()))
		i := 0
		for _, want := range batch.Results {
			if want.Status == fixture.StatusResolved {
				continue
			}
			Expect(got[i]).To(And(
				HaveField("Fingerprint", want.Fingerprint),
				HaveField("Decision", want.Want.Decision),
				HaveField("Reason", want.Want.Reason),
				HaveField("Target", want.Want.Target),
				HaveField("Channel", want.Want.Channel),
			), want.Fingerprint)
			i++
		}
	})

	// Alertmanager adds fields to its payload over time, and the router
	// must keep routing through such an upgrade, so the webhook ignores
	// fields it doesn't read, unlike the route endpoint.
	It("routes a payload with fields it doesn't know", func() {
		body := `{"version": "4", "status": "firing", "newField": {"x": 1}, "alerts": [{"status": "firing", "fingerprint": "f1", "extra": true,
			"labels": {"alertname": "CheckoutErrorRate", "severity": "critical", "team": "checkout", "env": "production"},
			"startsAt": "2026-01-01T00:00:00Z"}]}`
		resp, out := shared.client.WebhookRaw(Default, body)

		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		Expect(out.Results).To(ConsistOf(And(
			HaveField("Status", fixture.StatusRouted),
			HaveField("Decision", fixture.DecisionPage),
			HaveField("Target", fixture.CheckoutOncall),
		)))
	})

	It("answers an empty batch with no results", func() {
		resp, out := shared.client.Webhook(Default, fixture.NewWebhook())

		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		Expect(out.Received).To(BeZero())
		Expect(out.Routed).To(BeZero())
		Expect(out.Results).To(BeEmpty())
	})

	Context("when the server's clock says how long an alert has fired", func() {
		var e *env
		var now time.Time

		BeforeEach(func() {
			now = time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)
			e = newEnv(withServerClock(newFakeClock(now)))
		})

		DescribeTable("derives firing_for from startsAt, so checkout's ten minutes decide the page",
			func(startedAgo time.Duration, want fixture.Route) {
				alert := fixture.Firing("latency", fixture.AlertLabels(fixture.TeamCheckout, fixture.CheckoutLatency, fixture.SeverityWarning), now.Add(-startedAgo))

				resp, out := e.client.Webhook(Default, fixture.NewWebhook(alert))
				fixture.ExpectBatch(Default, fixture.Batch{
					Webhook: fixture.NewWebhook(alert),
					Results: []fixture.ResultCase{{Fingerprint: "latency", Status: fixture.StatusRouted, Team: fixture.TeamCheckout, Want: want}},
				}, resp, out)
			},
			Entry("nine minutes is a fresh warning",
				9*time.Minute, fixture.Route{Decision: fixture.DecisionNotify, Reason: fixture.ReasonRoutine, Channel: fixture.CheckoutChannel}),
			Entry("ten minutes is sustained",
				10*time.Minute, fixture.Route{Decision: fixture.DecisionPage, Reason: fixture.ReasonSustained, Target: fixture.CheckoutOncall}),
			// Alertmanager's clock may run ahead of the router's; that
			// alert has fired for no time at all, not a negative one.
			Entry("a start in the future is a fresh warning",
				-time.Minute, fixture.Route{Decision: fixture.DecisionNotify, Reason: fixture.ReasonRoutine, Channel: fixture.CheckoutChannel}),
		)
	})

	Context("when an alert's evaluation fails", func() {
		var e *env

		BeforeEach(func() {
			e = newEnv(withCopiedTeams())
		})

		DescribeTable("fails that alert alone, routes it with the fallback and routes the rest",
			func(rules string, check func(fixture.AlertResult)) {
				e.editCheckout(checkoutRules, rules)
				e.reloadOK()

				batch := fixture.Batch{
					Webhook: fixture.NewWebhook(
						fixture.Firing("checkout-warning", fixture.AlertLabels(fixture.TeamCheckout, fixture.CheckoutLatency, fixture.SeverityWarning), time.Now().Add(-time.Minute)),
						fixture.Firing("payments-critical", fixture.AlertLabels(fixture.TeamPayments, "PaymentsErrorRate", fixture.SeverityCritical), time.Now().Add(-time.Minute)),
					),
					Results: []fixture.ResultCase{
						{Fingerprint: "checkout-warning", Status: fixture.StatusFailed, Team: fixture.TeamCheckout, Want: fixture.Unrouted},
						{
							Fingerprint: "payments-critical", Status: fixture.StatusRouted, Team: fixture.TeamPayments,
							Want: fixture.Route{Decision: fixture.DecisionPage, Reason: fixture.ReasonCriticalAlert, Target: fixture.PaymentsOncall},
						},
					},
				}
				resp, out := e.client.Webhook(Default, batch.Webhook)
				fixture.ExpectBatch(Default, batch, resp, out)
				check(out.Results[0])

				By("still dispatching the fallback, so the failed alert reaches #alerts")
				Expect(e.notifier.notifications()).To(ConsistOf(
					HaveField("Channel", fixture.DefaultChannel),
					HaveField("Target", fixture.PaymentsOncall),
				))
			},
			Entry("a conflict names both sides", conflictingRule, func(r fixture.AlertResult) {
				Expect(r.Conflict).NotTo(BeNil(), "the result has no conflict block")
				Expect(r.Conflict.Candidates).To(ConsistOf(
					HaveField("Payload", MatchJSON(`{"channel": "#checkout-alerts"}`)),
					HaveField("Payload", MatchJSON(`{"channel": "#checkout-oncall"}`)),
				))
				Expect(r.Error).To(ContainSubstring("checkout.alerts"))
			}),
			Entry("a runtime error says where", failingRule, func(r fixture.AlertResult) {
				Expect(r.Error).To(ContainSubstring("out of range"))
			}),
		)
	})

	DescribeTable("answers 400 for a payload it can't read, and routes none of it",
		func(c fixture.BadRequestCase) {
			e := newEnv()
			resp, raw := e.client.PostRaw(Default, fixture.PathAlerts, c.Body)

			Expect(resp).To(HaveHTTPStatus(c.Status))
			herr := fixture.Decode[fixture.ErrorResponse](Default, raw).Error
			Expect(herr).NotTo(BeNil())
			Expect(herr.Advice).NotTo(BeEmpty(), "a client error should say how to fix the request")

			Expect(e.notifier.notifications()).To(BeEmpty())
			Expect(e.families().Count(fixture.MetricAlertsReceived, nil)).To(BeZero())
		},
		fixture.Entries(fixture.WebhookBadRequestCases()),
	)

	It("answers 413 for a body over 1 MiB", func() {
		alert := fixture.Firing("big", fixture.AlertLabels(fixture.TeamCheckout, fixture.CheckoutLatency, fixture.SeverityWarning), time.Now().Add(-time.Minute))
		alert.Annotations["description"] = strings.Repeat("x", 1<<20)
		resp, body := shared.client.PostJSON(Default, fixture.PathAlerts, fixture.NewWebhook(alert))

		Expect(resp).To(HaveHTTPStatus(http.StatusRequestEntityTooLarge))
		Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error).NotTo(BeNil())
	})
})
