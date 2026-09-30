package integration

import (
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/dispatch"
	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

var _ = Describe("Routing one alert", func() {
	DescribeTable("answers 200 with the route the team's policy chose",
		func(c fixture.RouteCase) {
			resp, out := shared.client.Route(Default, c.Team, c.Request)
			fixture.ExpectRouteCase(Default, c, resp, out)
		},
		fixture.Entries(fixture.RouteCases()),
	)

	It("explains a sustained page with the platform rule and the call chain through the team's policy", func() {
		resp, out := shared.client.Route(Default, fixture.TeamCheckout,
			fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning, fixture.FiringFor("12m")))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		winners := out.Winners()
		Expect(winners).To(HaveLen(1))
		w := winners[0]
		Expect(w.Policy).To(Equal("platform.paging"))
		Expect(w.Location).To(Equal("checkout/alerts.sigil:7:1 → platform/paging.sigil:12:3"))
		// The condition shows the threshold checkout passed, not the
		// parameter's name, so the trace explains this team's page.
		Expect(w.Conditions).To(Equal([]string{
			`in_production and alert.severity == warning and alert.firing_for >= 10m`,
		}))
		Expect(w.Payload).To(MatchJSON(`{"target": "checkout-primary"}`))

		// The warning's notification fired too and lost to the page, which
		// is what a trace is for: it shows what else the policy said.
		Expect(out.Trace).To(ContainElement(And(
			HaveField("Decision", fixture.DecisionNotify),
			HaveField("Reason", fixture.ReasonRoutine),
			HaveField("Winner", BeFalse()),
		)))
	})

	It("hands exactly one notification per alert to the dispatcher", func() {
		e := newEnv()
		for _, c := range fixture.RouteCases() {
			resp, _ := e.client.Route(Default, c.Team, c.Request)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK), c.Name)
		}

		got := e.notifier.notifications()
		Expect(got).To(HaveLen(len(fixture.RouteCases())))
		for i, c := range fixture.RouteCases() {
			Expect(got[i]).To(Equal(dispatch.Notification{
				Team:      c.Team,
				AlertName: c.Request.Alert.Name,
				Decision:  c.Want.Decision,
				Reason:    c.Want.Reason,
				Target:    c.Want.Target,
				Channel:   c.Want.Channel,
			}), c.Name)
		}
	})

	Context("when the request can't be evaluated", func() {
		It("answers 404 naming the teams the directory lists", func() {
			resp, body := shared.client.PostJSON(Default, fixture.RoutePath("marketing"),
				fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))

			Expect(resp).To(HaveHTTPStatus(http.StatusNotFound))
			herr := fixture.Decode[fixture.ErrorResponse](Default, body).Error
			Expect(herr).NotTo(BeNil())
			Expect(herr.Message).To(ContainSubstring(`"marketing"`))
			Expect(herr.Advice).To(ContainElement(ContainSubstring("checkout, payments")))
		})

		DescribeTable("refuses a body it won't evaluate, saying how to fix it",
			func(c fixture.BadRequestCase) {
				e := newEnv()
				resp, raw := e.client.PostRaw(Default, fixture.RoutePath(fixture.TeamCheckout), c.Body)

				Expect(resp).To(HaveHTTPStatus(c.Status))
				herr := fixture.Decode[fixture.ErrorResponse](Default, raw).Error
				Expect(herr).NotTo(BeNil())
				Expect(herr.Advice).NotTo(BeEmpty(), "a client error should say how to fix the request")

				// A refused request routes nothing, so nothing is dispatched.
				Expect(e.notifier.notifications()).To(BeEmpty())
			},
			fixture.Entries(fixture.RouteBadRequestCases()),
		)

		It("answers 413 for a body over 1 MiB", func() {
			big := fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning,
				fixture.Label("padding", strings.Repeat("x", 1<<20)))
			resp, body := shared.client.PostJSON(Default, fixture.RoutePath(fixture.TeamCheckout), big)

			Expect(resp).To(HaveHTTPStatus(http.StatusRequestEntityTooLarge))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error).NotTo(BeNil())
		})

		It("answers 404 with the error model for a route that doesn't exist", func() {
			resp, body := shared.client.Get(Default, "/api/v2/alerts")

			Expect(resp).To(HaveHTTPStatus(http.StatusNotFound))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error).NotTo(BeNil())
		})
	})
})

var _ = Describe("The team directory", func() {
	It("lists every team with its on-call target and channel", func() {
		fixture.ExpectDefaultTeams(Default, shared.client.ListTeams(Default))
	})
})

var _ = Describe("The policy bundle", func() {
	It("lists one root per team with the directory it came from and its fingerprint", func() {
		k := fixture.ExpectServedPolicies(Default, shared.client.ListPolicies(Default))

		Expect(k.Source).To(Equal(shared.dir))
		Expect(k.LoadedAt).To(BeTemporally("==", clockStart))
		Expect(k.Fingerprint).To(MatchRegexp(`^[0-9a-f]{16,}$`))
	})
})

// The files under requests/ are what demo-cli, the README's curl examples
// and k6 send, and requests/cases.json says what each must answer. Running
// them here keeps all three honest: a change that alters an answer fails
// this table before it can make the walkthrough or the load test wrong.
var _ = Describe("The sample requests", func() {
	DescribeTable("answer what requests/cases.json expects",
		func(c fixture.ManifestCase) {
			fixture.ExpectManifestCase(Default, shared.client, c)
		},
		fixture.Entries(fixture.ManifestCases()),
	)
})
