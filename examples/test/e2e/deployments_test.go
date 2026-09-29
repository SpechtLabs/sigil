//go:build e2e

package e2e

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

var _ = Describe("Evaluating a deployment", func() {
	It("explains the owner's review with the platform rule and its call chain", func() {
		resp, out := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())

		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		fixture.ExpectOwnerTrace(Default, out)
	})

	DescribeTable("grants roles, then encodes the decision in the HTTP status",
		func(c fixture.DecisionCase) {
			resp, out := deploygate.Deploy(Default, c.Team, c.Request)
			fixture.ExpectDecision(Default, c, resp, out)
		},
		decisionEntries(),
	)

	Context("when the access stage fails", func() {
		// An access failure ends the request before the deploy policy
		// runs, so no deploy decision is made on roles nobody granted.

		It("answers 422 for an actor without a name, a failed input assert the caller can fix", func() {
			resp, out := deploygate.Deploy(Default, fixture.TeamCheckout, fixture.OwnerRequest(fixture.ActorName("")))
			fixture.ExpectAsserts(Default, resp, http.StatusUnprocessableEntity, out.Asserts, out.Error,
				fixture.AssertEntry{Reason: "named_actor", Policy: "access.guardrails"})
		})

		It("answers 500 for an actor who would audit their own deploys, a failed outcome assert", func() {
			resp, out := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Groups(fixture.ComplianceMember...)))
			fixture.ExpectAsserts(Default, resp, http.StatusInternalServerError, out.Asserts, out.Error,
				fixture.AssertEntry{Reason: "sod_auditor_deployer", Policy: "access.guardrails"})
		})

		It("answers 500 naming both sides when admin and release manager collide", func() {
			resp, out := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Groups(fixture.BreakGlassPlatform...)))
			fixture.ExpectBreakGlassConflict(Default, resp, out.Conflict, out.Error)
		})
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

var _ = Describe("Asking for access", func() {
	DescribeTable("grants every role whose rule fires",
		func(c fixture.AccessCase) {
			resp, out := deploygate.Access(Default, c.Request)
			fixture.ExpectAccess(Default, c, resp, out)
		},
		accessEntries(),
	)

	It("answers 500 naming both sides when admin and release manager collide", func() {
		resp, out := deploygate.Access(Default, fixture.AccessFor(fixture.BreakGlassPlatform...))
		fixture.ExpectBreakGlassConflict(Default, resp, out.Conflict, out.Error)
	})

	It("answers 500 when separation of duties fails", func() {
		resp, out := deploygate.Access(Default, fixture.AccessFor(fixture.ComplianceMember...))
		fixture.ExpectAsserts(Default, resp, http.StatusInternalServerError, out.Asserts, out.Error,
			fixture.AssertEntry{Reason: "sod_auditor_deployer", Policy: "access.guardrails"})
	})

	It("answers 422 for an actor without a name", func() {
		resp, out := deploygate.Access(Default, fixture.AccessFor(fixture.TeamPayments).Named(""))
		fixture.ExpectAsserts(Default, resp, http.StatusUnprocessableEntity, out.Asserts, out.Error,
			fixture.AssertEntry{Reason: "named_actor", Policy: "access.guardrails"})
	})

	DescribeTable("answers 400 for a body it won't evaluate",
		func(body string) {
			resp, raw := deploygate.PostRaw(Default, fixture.PathAccessGrants, body)

			Expect(resp).To(HaveHTTPStatus(http.StatusBadRequest))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, raw).Error).NotTo(BeNil())
		},
		accessBadRequestEntries(),
	)
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

func accessEntries() []TableEntry {
	cases := fixture.AccessCases()
	entries := make([]TableEntry, 0, len(cases))
	for _, c := range cases {
		entries = append(entries, Entry(c.Name, c))
	}

	return entries
}

func accessBadRequestEntries() []TableEntry {
	cases := fixture.AccessBadRequestCases()
	entries := make([]TableEntry, 0, len(cases))
	for _, c := range cases {
		entries = append(entries, Entry(c.Name, c.Body))
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
