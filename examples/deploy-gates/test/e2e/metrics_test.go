//go:build e2e

package e2e

import (
	"bytes"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"

	"github.com/spechtlabs/sigil/examples/deploy-gates/test/internal/fixture"
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

	It("counts every role the access stage grants, by team, role and reason", func() {
		labels := fixture.Labels{"team": fixture.TeamPayments, "role": fixture.RoleDeployer, "reason": "team_member"}
		before := scrapeMetrics(Default).Value(fixture.MetricAccessGrants, labels)

		resp, _ := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		resp, _ = deploygate.Access(Default, fixture.AccessFor(fixture.TeamPayments))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		families := scrapeMetrics(Default)
		Expect(families.Value(fixture.MetricAccessGrants, labels) - before).To(BeNumerically("==", 2))
		Expect(families.Find(fixture.MetricAccessDuration, fixture.Labels{"team": fixture.TeamPayments})).NotTo(BeNil())
	})

	DescribeTable("counts a failed access evaluation by team, kind and stage, and no decision",
		func(kind string, groups []string, status int) {
			labels := fixture.Labels{"team": fixture.TeamPayments, "kind": kind, "stage": "access"}
			before := scrapeMetrics(Default)

			resp, _ := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Groups(groups...)))
			Expect(resp).To(HaveHTTPStatus(status))

			after := scrapeMetrics(Default)
			Expect(after.Value(fixture.MetricEvalErrors, labels) - before.Value(fixture.MetricEvalErrors, labels)).
				To(BeNumerically("==", 1))
			// The deploy policy never ran, so no decision series moved, the
			// fallback's deny(reason: no_rule_matched) included.
			Expect(after.Sum(fixture.MetricDecisions, nil)).To(Equal(before.Sum(fixture.MetricDecisions, nil)))
		},
		Entry("a failed separation-of-duties assert", "assertion", fixture.ComplianceMember, http.StatusInternalServerError),
		Entry("admin and release manager in one outcome", "conflict", fixture.BreakGlassPlatform, http.StatusInternalServerError),
	)

	It("counts successful reloads and stamps each kind's time and health", func() {
		before := scrapeMetrics(Default)

		resp, body := deploygate.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		kinds := fixture.Decode[fixture.PoliciesResponse](Default, body).Kinds
		Expect(kinds).To(HaveLen(2))

		families := scrapeMetrics(Default)
		for _, k := range kinds {
			kind := fixture.Labels{"kind": k.Kind}
			success := fixture.Labels{"kind": k.Kind, "result": "success"}
			Expect(families.Value(fixture.MetricReloads, success)-before.Value(fixture.MetricReloads, success)).
				To(BeNumerically(">=", 1), k.Kind)

			// The poller may reload again between the POST and the scrape,
			// so each kind's gauge is at least the loaded_at the POST
			// reported for it.
			Expect(families.Value(fixture.MetricLastReload, kind)).
				To(BeNumerically(">=", float64(k.LoadedAt.Unix())), k.Kind)
			Expect(families.Value(fixture.MetricReloadOK, kind)).To(BeNumerically("==", 1), k.Kind)
		}
	})

	It("exposes every loaded policy as an info series", func() {
		families := scrapeMetrics(Default)

		for _, labels := range []fixture.Labels{
			{"kind": "DeployApproval", "team": fixture.TeamPayments, "policy": "payments.production"},
			{"kind": "DeployApproval", "team": fixture.TeamCheckout, "policy": "checkout.production"},
			{"kind": "AccessGrant", "policy": fixture.AccessPolicy},
		} {
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
