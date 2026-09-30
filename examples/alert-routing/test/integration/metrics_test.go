package integration

import (
	"bytes"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// checkoutCritical is the label set of the decision a critical checkout
// alert gets.
var checkoutCritical = fixture.Labels{
	"team": fixture.TeamCheckout, "policy": "checkout.alerts",
	"decision": fixture.DecisionPage, "reason": fixture.ReasonCriticalAlert,
}

var _ = Describe("Metrics", func() {
	// Every spec gets an env with a registry of its own, so the values are
	// exact: the startup load and the spec's own requests, nothing else.
	var e *env

	BeforeEach(func() {
		e = newEnv()
	})

	It("counts the startup load and exposes what it loaded", func() {
		families := e.families()

		Expect(families.Value(fixture.MetricReloads, fixture.Labels{"result": "success"})).To(BeNumerically("==", 1))
		// The failure series exists before the first failure, so an alert
		// on the failure ratio has something to divide by.
		Expect(families.Find(fixture.MetricReloads, fixture.Labels{"result": "failure"})).NotTo(BeNil())
		Expect(families.Value(fixture.MetricReloads, fixture.Labels{"result": "failure"})).To(BeNumerically("==", 0))
		Expect(families.Value(fixture.MetricLastReload, nil)).To(BeNumerically("~", float64(clockStart.Unix()), 1e-3))
		Expect(families.Value(fixture.MetricReloadOK, nil)).To(BeNumerically("==", 1))

		fingerprint := e.served().Fingerprint
		Expect(families.Count(fixture.MetricPolicyInfo, nil)).To(Equal(2))
		for _, team := range []string{fixture.TeamCheckout, fixture.TeamPayments} {
			labels := fixture.Labels{"team": team, "policy": team + fixture.RootSuffix, "fingerprint": fingerprint, "source": e.dir}
			Expect(families.Value(fixture.MetricPolicyInfo, labels)).To(BeNumerically("==", 1), "%v", labels)
		}
	})

	It("counts each decision by team, policy, decision and reason, and times each evaluation", func() {
		for range 3 {
			resp, _ := e.client.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		}
		resp, _ := e.client.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutMuted, fixture.SeverityWarning))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		families := e.families()
		Expect(families.Value(fixture.MetricDecisions, checkoutCritical)).To(BeNumerically("==", 3))
		Expect(families.Value(fixture.MetricDecisions, fixture.Labels{
			"team": fixture.TeamCheckout, "decision": fixture.DecisionDrop, "reason": fixture.ReasonMuted,
		})).To(BeNumerically("==", 1))
		Expect(families.Count(fixture.MetricDecisions, nil)).To(Equal(2))

		m := families.Find(fixture.MetricEvalDuration, fixture.Labels{"team": fixture.TeamCheckout})
		Expect(m).NotTo(BeNil())
		Expect(families[fixture.MetricEvalDuration].GetType()).To(Equal(dto.MetricType_HISTOGRAM))
		Expect(m.GetHistogram().GetSampleCount()).To(BeNumerically("==", 4))

		By("counting a notification per alert by decision and destination")
		Expect(families.Value(fixture.MetricNotifications, fixture.Labels{
			"decision": fixture.DecisionPage, "destination": fixture.CheckoutOncall,
		})).To(BeNumerically("==", 3))
		Expect(families.Value(fixture.MetricNotifications, fixture.Labels{
			"decision": fixture.DecisionDrop, "destination": "-",
		})).To(BeNumerically("==", 1))
		Expect(families.Value(fixture.MetricAlertsReceived, fixture.Labels{"status": "firing"})).To(BeNumerically("==", 4))
		Expect(families.Value(fixture.MetricAlertsRouted, fixture.Labels{
			"team": fixture.TeamCheckout, "outcome": fixture.StatusRouted,
		})).To(BeNumerically("==", 4))
	})

	It("counts a webhook's alerts by status and outcome, its notifications and its size", func() {
		batch := fixture.MixedBatch(time.Now())
		resp, out := e.client.Webhook(Default, batch.Webhook)
		fixture.ExpectBatch(Default, batch, resp, out)

		families := e.families()
		Expect(families.Value(fixture.MetricAlertsReceived, fixture.Labels{"status": "firing"})).To(BeNumerically("==", batch.Firing()))
		Expect(families.Value(fixture.MetricAlertsReceived, fixture.Labels{"status": "resolved"})).To(BeNumerically("==", 1))

		routed := func(team, outcome string) float64 {
			return families.Value(fixture.MetricAlertsRouted, fixture.Labels{"team": team, "outcome": outcome})
		}
		Expect(routed(fixture.TeamCheckout, fixture.StatusRouted)).To(BeNumerically("==", 2))
		Expect(routed(fixture.TeamPayments, fixture.StatusRouted)).To(BeNumerically("==", 2))
		Expect(routed(fixture.TeamCheckout, fixture.StatusInvalid)).To(BeNumerically("==", 1))
		Expect(routed(fixture.TeamPayments, fixture.StatusInvalid)).To(BeNumerically("==", 1))
		// The team label of an unowned alert is whatever its rule says, so
		// it never becomes a label value: every one would be a new series.
		Expect(routed("-", fixture.StatusUnowned)).To(BeNumerically("==", 2))
		Expect(families.Count(fixture.MetricAlertsRouted, fixture.Labels{"team": "marketing"})).To(BeZero())
		Expect(families.Sum(fixture.MetricAlertsRouted, nil)).To(BeNumerically("==", batch.Firing()))

		// Every firing alert ends in exactly one notification.
		Expect(families.Sum(fixture.MetricNotifications, nil)).To(BeNumerically("==", batch.Firing()))
		Expect(families.Value(fixture.MetricNotifications, fixture.Labels{
			"decision": fixture.DecisionNotify, "destination": fixture.DefaultChannel,
		})).To(BeNumerically("==", 4))

		// Only the policies' own decisions are decisions; the fallback of an
		// unowned or invalid alert isn't.
		Expect(families.Sum(fixture.MetricDecisions, nil)).To(BeNumerically("==", 4))

		size := families.Find(fixture.MetricBatchSize, nil).GetHistogram()
		Expect(size.GetSampleCount()).To(BeNumerically("==", 1))
		Expect(size.GetSampleSum()).To(BeNumerically("==", len(batch.Webhook.Alerts)))
	})

	DescribeTable("counts a failed evaluation by team and kind, and not as a decision",
		func(rules, kind string, status int) {
			f := newEnv(withCopiedTeams())
			f.editCheckout(checkoutRules, rules)
			f.reloadOK()

			resp, out := f.client.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning))
			fixture.ExpectFallback(Default, fixture.TeamCheckout, status, resp, out)

			families := f.families()
			Expect(families.Value(fixture.MetricEvalErrors, fixture.Labels{"team": fixture.TeamCheckout, "kind": kind})).To(BeNumerically("==", 1))
			Expect(families.Count(fixture.MetricEvalErrors, nil)).To(Equal(1))
			Expect(families.Count(fixture.MetricDecisions, nil)).To(BeZero())
			Expect(families.Value(fixture.MetricAlertsRouted, fixture.Labels{"team": fixture.TeamCheckout, "outcome": fixture.StatusFailed})).To(BeNumerically("==", 1))
			Expect(families.Value(fixture.MetricNotifications, fixture.Labels{
				"decision": fixture.DecisionNotify, "destination": fixture.DefaultChannel,
			})).To(BeNumerically("==", 1))
		},
		Entry("two notifications of one reason to different channels", conflictingRule, "conflict", http.StatusInternalServerError),
		Entry("a list read past its end", failingRule, "runtime", http.StatusInternalServerError),
		// A failed input assert is the alert's fault, not the policy's, so
		// it answers 422, but it still routes the fallback and counts.
		Entry("an alert that fails an input assert", assertingRule, "assertion", http.StatusUnprocessableEntity),
	)

	It("doesn't count requests it refused before evaluating", func() {
		resp, _ := e.client.PostRaw(Default, fixture.RoutePath(fixture.TeamCheckout), `{`)
		Expect(resp).To(HaveHTTPStatus(http.StatusBadRequest))
		resp, _ = e.client.PostJSON(Default, fixture.RoutePath("marketing"), fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))
		Expect(resp).To(HaveHTTPStatus(http.StatusNotFound))
		resp, _ = e.client.PostJSON(Default, fixture.RoutePath(fixture.TeamCheckout), fixture.FiringAlert(fixture.CheckoutErrorRate, "urgent"))
		Expect(resp).To(HaveHTTPStatus(http.StatusUnprocessableEntity))

		families := e.families()
		Expect(families.Count(fixture.MetricDecisions, nil)).To(BeZero())
		Expect(families.Count(fixture.MetricEvalDuration, nil)).To(BeZero())
		Expect(families.Count(fixture.MetricAlertsReceived, nil)).To(BeZero())
		Expect(families.Count(fixture.MetricNotifications, nil)).To(BeZero())
	})

	It("serves its metrics and the process metrics on /metrics", func() {
		resp, _ := e.client.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		resp, body := e.client.Get(Default, fixture.PathMetrics)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		families, err := fixture.ParseMetrics(bytes.NewReader(body))
		Expect(err).NotTo(HaveOccurred())
		Expect(families.Value(fixture.MetricDecisions, checkoutCritical)).To(BeNumerically("==", 1))
		// The Go runtime and process collectors sit on the same registry,
		// so one scrape shows the service and the process it runs in.
		Expect(families).To(HaveKey("go_goroutines"))
		Expect(families).To(HaveKey("process_cpu_seconds_total"))
	})

	It("counts HTTP requests by status, method and route template", func() {
		resp, _ := e.client.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		resp, _ = e.client.Route(Default, fixture.TeamPayments, fixture.FiringAlert("PaymentsErrorRate", fixture.SeverityCritical))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		resp, _ = e.client.Webhook(Default, fixture.MixedBatch(time.Now()).Webhook)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		resp, _ = e.client.Get(Default, "/api/v2/nothing")
		Expect(resp).To(HaveHTTPStatus(http.StatusNotFound))
		resp, _ = e.client.Get(Default, fixture.PathMetrics)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		// The middleware records a request after the handler returns, which
		// can be a moment after the client holds the answer.
		Eventually(func(g Gomega) {
			families := e.families()
			g.Expect(families.Value(fixture.MetricRequests, fixture.Labels{
				"code": "200", "method": http.MethodPost, "route": routeRoute,
			})).To(BeNumerically("==", 2))
			g.Expect(families.Value(fixture.MetricRequests, fixture.Labels{
				"code": "200", "method": http.MethodPost, "route": fixture.PathAlerts,
			})).To(BeNumerically("==", 1))
			// A path without a route shares one series, so a scanner can't
			// create series without bound.
			g.Expect(families.Value(fixture.MetricRequests, fixture.Labels{
				"code": "404", "method": http.MethodGet, "route": "unmatched",
			})).To(BeNumerically("==", 1))
			g.Expect(families.Find(fixture.MetricRequestDuration, fixture.Labels{
				"method": http.MethodPost, "route": routeRoute,
			}).GetHistogram().GetSampleCount()).To(BeNumerically("==", 2))
		}).Should(Succeed())

		// Every team's routes share the route template's series, and the
		// scrape itself isn't counted, or it would dominate the rate.
		families := e.families()
		Expect(families.Count(fixture.MetricRequests, fixture.Labels{"route": fixture.RoutePath(fixture.TeamCheckout)})).To(BeZero())
		Expect(families.Count(fixture.MetricRequests, fixture.Labels{"route": fixture.PathMetrics})).To(BeZero())
	})
})
