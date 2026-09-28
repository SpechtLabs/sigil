package integration

import (
	"bytes"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

var _ = Describe("Metrics", func() {
	// Every spec gets an env with a registry of its own, so the values are
	// exact: the startup loads and the spec's own requests, nothing else.
	var e *env

	BeforeEach(func() {
		e = newEnv()
	})

	It("counts each bundle's startup load and exposes what it loaded", func() {
		families := e.families()

		for _, kind := range []string{kindDeploy, kindAccess} {
			success := fixture.Labels{"kind": kind, "result": "success"}
			failure := fixture.Labels{"kind": kind, "result": "failure"}
			Expect(families.Value(fixture.MetricReloads, success)).To(BeNumerically("==", 1), kind)
			// The failure series exists before the first failure, so an
			// alert on the failure ratio has something to divide by.
			Expect(families.Find(fixture.MetricReloads, failure)).NotTo(BeNil(), kind)
			Expect(families.Value(fixture.MetricReloads, failure)).To(BeNumerically("==", 0), kind)
		}

		Expect(families.Value(fixture.MetricLastReload, nil)).
			To(BeNumerically("~", float64(clockStart.Unix()), 1e-3))

		Expect(families.Count(fixture.MetricPolicyInfo, nil)).To(Equal(3))
		for _, labels := range []fixture.Labels{
			{"kind": kindDeploy, "team": fixture.TeamPayments, "policy": "payments.production", "source": e.dir},
			{"kind": kindDeploy, "team": fixture.TeamCheckout, "policy": "checkout.production", "source": e.dir},
			{"kind": kindAccess, "policy": fixture.AccessPolicy, "source": e.accessDir},
		} {
			Expect(families.Value(fixture.MetricPolicyInfo, labels)).To(BeNumerically("==", 1), "%v", labels)
		}
	})

	It("counts each decision by team, policy, decision and reason", func() {
		for range 3 {
			resp, _ := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
			Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		}
		resp, _ := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Soak("2h")))
		Expect(resp).To(HaveHTTPStatus(http.StatusForbidden))

		families := e.families()
		Expect(families.Value(fixture.MetricDecisions, fixture.OwnerReview)).To(BeNumerically("==", 3))
		Expect(families.Value(fixture.MetricDecisions, fixture.Labels{
			"team": fixture.TeamPayments, "decision": fixture.DecisionDeny, "reason": "soak_too_short",
		})).To(BeNumerically("==", 1))
		Expect(families.Count(fixture.MetricDecisions, nil)).To(Equal(2))

		m := families.Find(fixture.MetricEvalDuration, fixture.Labels{"team": fixture.TeamPayments})
		Expect(m).NotTo(BeNil())
		Expect(families[fixture.MetricEvalDuration].GetType()).To(Equal(dto.MetricType_HISTOGRAM))
		Expect(m.GetHistogram().GetSampleCount()).To(BeNumerically("==", 4))
	})

	It("counts every grant by team, role and reason, and times each access evaluation", func() {
		for range 2 {
			resp, _ := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
			Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		}
		resp, _ := e.client.Access(Default, fixture.AccessFor("payments-sre"))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		resp, _ = e.client.Access(Default, fixture.AccessFor("marketing"))
		Expect(resp).To(HaveHTTPStatus(http.StatusForbidden))

		families := e.families()
		grants := func(role, reason string) float64 {
			return families.Value(fixture.MetricAccessGrants, fixture.Labels{
				"team": fixture.TeamPayments, "role": role, "reason": reason,
			})
		}
		Expect(grants(fixture.RoleReader, "team_member")).To(BeNumerically("==", 2))
		Expect(grants(fixture.RoleDeployer, "team_member")).To(BeNumerically("==", 2))
		Expect(grants(fixture.RoleDeployer, "oncall")).To(BeNumerically("==", 1))
		// An empty outcome grants nothing, so it adds no series.
		Expect(families.Count(fixture.MetricAccessGrants, nil)).To(Equal(3))

		m := families.Find(fixture.MetricAccessDuration, fixture.Labels{"team": fixture.TeamPayments})
		Expect(m).NotTo(BeNil())
		Expect(families[fixture.MetricAccessDuration].GetType()).To(Equal(dto.MetricType_HISTOGRAM))
		Expect(m.GetHistogram().GetSampleCount()).To(BeNumerically("==", 4))
	})

	DescribeTable("counts a failed access evaluation by team, kind and stage, and no decision",
		func(kind string, groups []string, status int) {
			resp, _ := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Groups(groups...)))
			Expect(resp).To(HaveHTTPStatus(status))
			resp, _ = e.client.Access(Default, fixture.AccessFor(groups...))
			Expect(resp).To(HaveHTTPStatus(status))

			families := e.families()
			Expect(families.Value(fixture.MetricEvalErrors, fixture.Labels{
				"team": fixture.TeamPayments, "kind": kind, "stage": "access",
			})).To(BeNumerically("==", 2))
			Expect(families.Count(fixture.MetricEvalErrors, fixture.Labels{"stage": "deploy"})).To(BeZero())
			// The deploy policy never ran, so there is no deploy decision to
			// count, and a failed evaluation grants nothing.
			Expect(families.Count(fixture.MetricDecisions, nil)).To(BeZero())
			Expect(families.Count(fixture.MetricAccessGrants, nil)).To(BeZero())
		},
		Entry("a failed separation-of-duties assert", "assertion", fixture.ComplianceMember, http.StatusUnprocessableEntity),
		Entry("admin and release manager in one outcome", "conflict", fixture.BreakGlassPlatform, http.StatusConflict),
	)

	It("doesn't count requests it refused before evaluating", func() {
		resp, _ := e.client.PostRaw(Default, fixture.DeploymentsPath(fixture.TeamPayments), `{`)
		Expect(resp).To(HaveHTTPStatus(http.StatusBadRequest))
		resp, _ = e.client.PostJSON(Default, fixture.DeploymentsPath("marketing"), fixture.OwnerRequest())
		Expect(resp).To(HaveHTTPStatus(http.StatusNotFound))

		families := e.families()
		Expect(families.Count(fixture.MetricDecisions, nil)).To(BeZero())
		Expect(families.Count(fixture.MetricEvalDuration, nil)).To(BeZero())
		Expect(families.Count(fixture.MetricAccessDuration, nil)).To(BeZero())
	})

	It("serves its metrics and the process metrics on /metrics", func() {
		resp, _ := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))

		resp, body := e.client.Get(Default, fixture.PathMetrics)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		families, err := fixture.ParseMetrics(bytes.NewReader(body))
		Expect(err).NotTo(HaveOccurred())
		Expect(families.Value(fixture.MetricDecisions, fixture.OwnerReview)).To(BeNumerically("==", 1))
		// The Go runtime and process collectors sit on the same registry,
		// so one scrape shows the service and the process it runs in.
		Expect(families).To(HaveKey("go_goroutines"))
		Expect(families).To(HaveKey("process_cpu_seconds_total"))
	})

	It("counts HTTP requests by status, method and route template", func() {
		const route = "/api/v1/teams/:team/deployments"

		resp, _ := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		resp, _ = e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Soak("2h")))
		Expect(resp).To(HaveHTTPStatus(http.StatusForbidden))
		resp, _ = e.client.Access(Default, fixture.AccessFor(fixture.TeamPayments))
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
				"code": "202", "method": http.MethodPost, "url": route,
			})).To(BeNumerically("==", 1))
			g.Expect(families.Value(fixture.MetricRequests, fixture.Labels{
				"code": "403", "method": http.MethodPost, "url": route,
			})).To(BeNumerically("==", 1))
			g.Expect(families.Value(fixture.MetricRequests, fixture.Labels{
				"code": "200", "method": http.MethodPost, "url": fixture.PathAccessGrants,
			})).To(BeNumerically("==", 1))
			// A path without a route shares one series, so a scanner can't
			// create series without bound.
			g.Expect(families.Value(fixture.MetricRequests, fixture.Labels{
				"code": "404", "method": http.MethodGet, "url": "unmatched",
			})).To(BeNumerically("==", 1))
			g.Expect(families.Find(fixture.MetricRequestDuration, fixture.Labels{
				"code": "202", "url": route,
			}).GetHistogram().GetSampleCount()).To(BeNumerically("==", 1))
		}).Should(Succeed())

		// Every team's deployments share the route template's series, and
		// the scrape itself isn't counted, or it would dominate the rate.
		families := e.families()
		Expect(families.Count(fixture.MetricRequests, fixture.Labels{"url": fixture.DeploymentsPath(fixture.TeamPayments)})).To(BeZero())
		Expect(families.Count(fixture.MetricRequests, fixture.Labels{"url": fixture.PathMetrics})).To(BeZero())
	})
})
