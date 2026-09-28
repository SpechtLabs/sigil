//go:build e2e

package e2e

import (
	"bytes"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

var _ = Describe("Metrics", func() {
	// The specs compare values before and after their own requests instead
	// of absolute values, because the other specs, the reload poller and
	// earlier runs against the same stack all move the counters too.

	It("counts each decision by team, policy, decision and reason", func() {
		before := scrapeMetrics(Default).Value(fixture.MetricDecisions, fixture.OwnerReview)

		resp, _ := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))

		after := scrapeMetrics(Default).Value(fixture.MetricDecisions, fixture.OwnerReview)
		Expect(after).To(BeNumerically(">=", 1))
		Expect(after - before).To(BeNumerically("==", 1))
	})

	It("records the evaluation latency per team as a histogram", func() {
		resp, _ := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))

		families := scrapeMetrics(Default)
		Expect(families).To(HaveKey(fixture.MetricEvalDuration))
		Expect(families[fixture.MetricEvalDuration].GetType()).To(Equal(dto.MetricType_HISTOGRAM))

		m := families.Find(fixture.MetricEvalDuration, fixture.Labels{"team": fixture.TeamPayments})
		Expect(m).NotTo(BeNil(), "no %s series for team payments", fixture.MetricEvalDuration)
		Expect(m.GetHistogram().GetSampleCount()).To(BeNumerically(">=", 1))
		Expect(m.GetHistogram().GetBucket()).NotTo(BeEmpty())
	})

	It("counts a failed assert as an assertion error of that team", func() {
		labels := fixture.Labels{"team": fixture.TeamCheckout, "kind": "assertion"}
		before := scrapeMetrics(Default).Value(fixture.MetricEvalErrors, labels)

		resp, _ := deploygate.Deploy(Default, fixture.TeamCheckout, fixture.OwnerRequest(fixture.ActorName("")))
		Expect(resp).To(HaveHTTPStatus(http.StatusUnprocessableEntity))

		after := scrapeMetrics(Default).Value(fixture.MetricEvalErrors, labels)
		Expect(after - before).To(BeNumerically("==", 1))
	})

	It("counts successful reloads and stamps the time of the last one", func() {
		success := fixture.Labels{"result": "success"}
		before := scrapeMetrics(Default).Value(fixture.MetricReloads, success)

		resp, body := deploygate.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		loaded := fixture.Decode[fixture.PoliciesResponse](Default, body).LoadedAt

		families := scrapeMetrics(Default)
		after := families.Value(fixture.MetricReloads, success)
		Expect(after).To(BeNumerically(">=", 1))
		Expect(after - before).To(BeNumerically(">=", 1))

		// The poller may reload again between the POST and the scrape, so
		// the gauge is at least the loaded_at the POST reported.
		Expect(families.Value(fixture.MetricLastReload, nil)).
			To(BeNumerically(">=", float64(loaded.Unix())))
	})

	It("exposes every loaded policy as an info series", func() {
		families := scrapeMetrics(Default)

		for _, team := range []string{fixture.TeamPayments, fixture.TeamCheckout} {
			labels := fixture.Labels{"team": team, "policy": team + ".production"}
			Expect(families.Value(fixture.MetricPolicyInfo, labels)).
				To(BeNumerically("==", 1), "%s%v", fixture.MetricPolicyInfo, labels)
		}
	})
})

// scrapeMetrics fetches /metrics and parses it with the Prometheus text
// parser.
func scrapeMetrics(g Gomega) fixture.Families {
	resp, body := deploygate.Get(g, fixture.PathMetrics)
	g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))

	families, err := fixture.ParseMetrics(bytes.NewReader(body))
	g.Expect(err).NotTo(HaveOccurred())

	return families
}
