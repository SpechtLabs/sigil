//go:build e2e

package e2e

import (
	"bytes"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

var _ = Describe("Metrics", func() {
	// The specs compare values before and after their own requests instead
	// of absolute values, because the other specs, the reload poller, k6 and
	// earlier runs against the same stack all move the counters too.

	It("counts each decision by team, policy, decision and reason", func() {
		labels := fixture.Labels{
			"team": fixture.TeamCheckout, "policy": "checkout.alerts",
			"decision": fixture.DecisionPage, "reason": fixture.ReasonCriticalAlert,
		}
		before := scrapeMetrics(Default).Value(fixture.MetricDecisions, labels)

		resp, _ := alertrouter.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		after := scrapeMetrics(Default).Value(fixture.MetricDecisions, labels)
		Expect(after - before).To(BeNumerically("==", 1))
	})

	It("records the evaluation latency per team as a histogram", func() {
		resp, _ := alertrouter.Route(Default, fixture.TeamPayments, fixture.FiringAlert("PaymentsErrorRate", fixture.SeverityCritical))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		families := scrapeMetrics(Default)
		Expect(families).To(HaveKey(fixture.MetricEvalDuration))
		Expect(families[fixture.MetricEvalDuration].GetType()).To(Equal(dto.MetricType_HISTOGRAM))

		m := families.Find(fixture.MetricEvalDuration, fixture.Labels{"team": fixture.TeamPayments})
		Expect(m).NotTo(BeNil(), "no %s series for team payments", fixture.MetricEvalDuration)
		Expect(m.GetHistogram().GetSampleCount()).To(BeNumerically(">=", 1))
		Expect(m.GetHistogram().GetBucket()).NotTo(BeEmpty())
	})

	It("counts a webhook's alerts by status, outcome and notification, and its size", func() {
		batch := fixture.MixedBatch(time.Now())
		before := scrapeMetrics(Default)

		resp, out := alertrouter.Webhook(Default, batch.Webhook)
		fixture.ExpectBatch(Default, batch, resp, out)

		after := scrapeMetrics(Default)
		delta := func(name string, labels fixture.Labels) float64 {
			return after.Sum(name, labels) - before.Sum(name, labels)
		}
		Expect(delta(fixture.MetricAlertsReceived, fixture.Labels{"status": "firing"})).To(BeNumerically("==", batch.Firing()))
		Expect(delta(fixture.MetricAlertsReceived, fixture.Labels{"status": "resolved"})).To(BeNumerically("==", 1))
		Expect(delta(fixture.MetricAlertsRouted, fixture.Labels{"outcome": fixture.StatusRouted})).To(BeNumerically("==", 4))
		Expect(delta(fixture.MetricAlertsRouted, fixture.Labels{"team": "-", "outcome": fixture.StatusUnowned})).To(BeNumerically("==", 2))
		Expect(delta(fixture.MetricAlertsRouted, fixture.Labels{"outcome": fixture.StatusInvalid})).To(BeNumerically("==", 2))
		// Every firing alert ends in exactly one notification.
		Expect(delta(fixture.MetricNotifications, nil)).To(BeNumerically("==", batch.Firing()))
		Expect(delta(fixture.MetricNotifications, fixture.Labels{
			"decision": fixture.DecisionNotify, "destination": fixture.DefaultChannel,
		})).To(BeNumerically("==", 4))

		size := func(f fixture.Families) uint64 {
			return f.Find(fixture.MetricBatchSize, nil).GetHistogram().GetSampleCount()
		}
		Expect(size(after) - size(before)).To(BeNumerically("==", 1))
	})

	It("counts successful reloads and stamps the time and health", func() {
		success := fixture.Labels{"result": "success"}
		before := scrapeMetrics(Default).Value(fixture.MetricReloads, success)

		resp, body := alertrouter.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		loaded, ok := fixture.Decode[fixture.PoliciesResponse](Default, body).Routing()
		Expect(ok).To(BeTrue())

		families := scrapeMetrics(Default)
		Expect(families.Value(fixture.MetricReloads, success) - before).To(BeNumerically(">=", 1))
		// The poller may reload again between the POST and the scrape, so
		// the gauge is at least the loaded_at the POST reported.
		Expect(families.Value(fixture.MetricLastReload, nil)).To(BeNumerically(">=", float64(loaded.LoadedAt.Unix())))
		Expect(families.Value(fixture.MetricReloadOK, nil)).To(BeNumerically("==", 1))
	})

	It("exposes every loaded team policy as an info series with the bundle's fingerprint", func() {
		fingerprint := alertrouter.Served(Default).Fingerprint
		families := scrapeMetrics(Default)

		for _, team := range []string{fixture.TeamCheckout, fixture.TeamPayments} {
			labels := fixture.Labels{"team": team, "policy": team + fixture.RootSuffix, "fingerprint": fingerprint}
			Expect(families.Value(fixture.MetricPolicyInfo, labels)).
				To(BeNumerically("==", 1), "%s%v", fixture.MetricPolicyInfo, labels)
		}
	})
})

// scrapeMetrics fetches /metrics and parses it with the Prometheus text
// parser.
func scrapeMetrics(g Gomega) fixture.Families {
	resp, body := alertrouter.Get(g, fixture.PathMetrics)
	g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))

	families, err := fixture.ParseMetrics(bytes.NewReader(body))
	g.Expect(err).NotTo(HaveOccurred())

	return families
}
