//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

var _ = Describe("Evaluating a deployment", func() {
	It("builds the same owner input the policy tests read from owner.json", func() {
		want, err := os.ReadFile(filepath.Join(fixture.ExamplesDir, fixture.OwnerFixture))
		Expect(err).NotTo(HaveOccurred())

		got, err := json.Marshal(fixture.OwnerRequest())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(MatchJSON(want))
	})

	It("explains the owner's review with the platform rule and its call chain", func() {
		resp, out := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())

		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		fixture.ExpectOwnerTrace(Default, out)
	})

	DescribeTable("encodes the decision in the HTTP status",
		func(c fixture.DecisionCase) {
			resp, out := deploygate.Deploy(Default, c.Team, c.Request)
			fixture.ExpectDecision(Default, c, resp, out)
		},
		decisionEntries(),
	)

	It("answers a failed assert with 422, the assert and the kind's default", func() {
		resp, out := deploygate.Deploy(Default, fixture.TeamCheckout, fixture.OwnerRequest(fixture.ActorName("")))
		fixture.ExpectNamedActorFailure(Default, resp, out)
	})

	Context("when the request can't be evaluated", func() {
		It("answers 404 for a team it doesn't serve", func() {
			resp, body := deploygate.PostJSON(Default, fixture.DeploymentsPath("marketing"), fixture.OwnerRequest())

			Expect(resp).To(HaveHTTPStatus(http.StatusNotFound))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error).NotTo(BeNil())
		})

		DescribeTable("answers 400 for a body it won't evaluate",
			func(body string) {
				resp, raw := deploygate.PostRaw(Default, fixture.DeploymentsPath(fixture.TeamPayments), body)

				Expect(resp).To(HaveHTTPStatus(http.StatusBadRequest))
				Expect(fixture.Decode[fixture.ErrorResponse](Default, raw).Error).NotTo(BeNil())
			},
			badRequestEntries(),
		)
	})
})

// decisionEntries turns the shared decision cases into table entries, so
// this suite and the integration suite run the same table.
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
