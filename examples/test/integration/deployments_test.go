package integration

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

var _ = Describe("Evaluating a deployment", func() {
	It("explains the owner's review with the platform rule and its call chain", func() {
		resp, out := shared.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())

		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		fixture.ExpectOwnerTrace(Default, out)
	})

	DescribeTable("encodes the decision in the HTTP status",
		func(c fixture.DecisionCase) {
			resp, out := shared.client.Deploy(Default, c.Team, c.Request)
			fixture.ExpectDecision(Default, c, resp, out)
		},
		decisionEntries(),
	)

	It("answers a failed assert with 422, the assert and the kind's default", func() {
		resp, out := shared.client.Deploy(Default, fixture.TeamCheckout, fixture.OwnerRequest(fixture.ActorName("")))
		fixture.ExpectNamedActorFailure(Default, resp, out)
	})

	Context("when the request can't be evaluated", func() {
		It("answers 404 naming the teams it does serve", func() {
			resp, body := shared.client.PostJSON(Default, fixture.DeploymentsPath("marketing"), fixture.OwnerRequest())

			Expect(resp).To(HaveHTTPStatus(http.StatusNotFound))
			herr := fixture.Decode[fixture.ErrorResponse](Default, body).Error
			Expect(herr).NotTo(BeNil())
			Expect(herr.Message).To(ContainSubstring(`"marketing"`))
			Expect(herr.Advice).To(ContainElement(ContainSubstring("payments, checkout")))
		})

		It("names the field when the soak is negative", func() {
			resp, body := shared.client.PostJSON(Default, fixture.DeploymentsPath(fixture.TeamPayments),
				fixture.OwnerRequest(fixture.Soak("-1h")))

			Expect(resp).To(HaveHTTPStatus(http.StatusBadRequest))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error.Message).
				To(ContainSubstring("release.soak is negative"))
		})

		DescribeTable("answers 400 for a body it won't evaluate",
			func(body string) {
				resp, raw := shared.client.PostRaw(Default, fixture.DeploymentsPath(fixture.TeamPayments), body)

				Expect(resp).To(HaveHTTPStatus(http.StatusBadRequest))
				herr := fixture.Decode[fixture.ErrorResponse](Default, raw).Error
				Expect(herr).NotTo(BeNil())
				Expect(herr.Advice).NotTo(BeEmpty(), "a client error should say how to fix the request")
			},
			badRequestEntries(),
		)

		It("answers 404 with the error model for a route that doesn't exist", func() {
			resp, body := shared.client.Get(Default, "/api/v2/deployments")

			Expect(resp).To(HaveHTTPStatus(http.StatusNotFound))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error).NotTo(BeNil())
		})
	})
})

var _ = Describe("The policy bundle", func() {
	It("lists every served team's policy with the directory it came from", func() {
		got := shared.client.ListPolicies(Default)

		Expect(got.Kind).To(Equal("DeployApproval"))
		Expect(got.Version).To(Equal(1))
		Expect(got.Source).To(Equal(shared.dir))
		Expect(got.Policies).To(Equal([]fixture.PolicyRef{
			{Team: fixture.TeamPayments, Policy: "payments.production"},
			{Team: fixture.TeamCheckout, Policy: "checkout.production"},
		}))
	})
})

// decisionEntries turns the shared decision cases into table entries, so
// this suite and the end-to-end suite run the same table.
func decisionEntries() []TableEntry {
	cases := fixture.DecisionCases()
	entries := make([]TableEntry, 0, len(cases))
	for _, c := range cases {
		entries = append(entries, Entry(c.Name, c))
	}

	return entries
}

func badRequestEntries() []TableEntry {
	cases := fixture.BadRequestCases()
	entries := make([]TableEntry, 0, len(cases))
	for _, c := range cases {
		entries = append(entries, Entry(c.Name, c.Body))
	}

	return entries
}
