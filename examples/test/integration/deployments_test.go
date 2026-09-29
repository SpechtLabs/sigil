package integration

import (
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

var _ = Describe("Evaluating a deployment", func() {
	It("sends the owner's deploy the policy tests read from owner.json, with the teams as groups", func() {
		input, err := os.ReadFile(filepath.Join(fixture.ExamplesDir, fixture.OwnerFixture))
		Expect(err).NotTo(HaveOccurred())

		req, herr := fixture.RequestFromInput(input)
		Expect(herr).To(Succeed())
		Expect(req.JSON()).To(MatchJSON(fixture.OwnerRequest().JSON()))
	})

	It("explains the owner's review with the platform rule and its call chain", func() {
		resp, out := shared.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())

		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		fixture.ExpectOwnerTrace(Default, out)
	})

	DescribeTable("grants roles, then encodes the decision in the HTTP status",
		func(c fixture.DecisionCase) {
			resp, out := shared.client.Deploy(Default, c.Team, c.Request)
			fixture.ExpectDecision(Default, c, resp, out)
		},
		fixture.Entries(fixture.DecisionCases()),
	)

	// An access failure ends the request before the deploy policy runs, so
	// no deploy decision is made on roles nobody granted.
	DescribeTable("when the access stage fails, never runs the deploy policy",
		func(c fixture.AccessFailureCase) {
			resp, out := shared.client.Deploy(Default, c.Team, c.Request)
			fixture.ExpectAccessFailure(Default, c, resp, out)
		},
		fixture.Entries(fixture.AccessFailureCases()),
	)

	// An outcome assert that fails when no rule fired leaves the trace as
	// empty as a failed input assert does, but it's still the policy that
	// failed, so it answers 500, not the caller's 422.
	It("answers 500 for an outcome assert that fails with nothing fired", func() {
		f := newEnv(withCopiedTeams())
		editFile(f.dir, checkoutPolicy, checkoutAssert,
			checkoutAssert+"\n"+`assert("decided_by_a_rule", deny.no_rule_matched not in outcome)`)
		resp, _ := f.client.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		// A critical service is outside checkout's tiers and the owner isn't
		// a release manager, so no rule fires and the default decides.
		resp, out := f.client.Deploy(Default, fixture.TeamCheckout, fixture.OwnerRequest(
			fixture.Owners(fixture.TeamCheckout), fixture.Groups(fixture.TeamCheckout), fixture.Tier("critical"), fixture.Hotfix()))

		fixture.ExpectAsserts(Default, resp, http.StatusInternalServerError, out.Asserts, out.Error,
			fixture.AssertEntry{Reason: "decided_by_a_rule", Policy: "checkout.production"})
		Expect(out.Trace).To(BeEmpty())
		Expect(f.families().Value(fixture.MetricEvalErrors, fixture.Labels{
			"team": fixture.TeamCheckout, "kind": "assertion", "stage": "deploy",
		})).To(BeNumerically("==", 1))
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
			func(c fixture.BadRequestCase) {
				resp, raw := shared.client.PostRaw(Default, fixture.DeploymentsPath(fixture.TeamPayments), c.Body)

				Expect(resp).To(HaveHTTPStatus(http.StatusBadRequest))
				herr := fixture.Decode[fixture.ErrorResponse](Default, raw).Error
				Expect(herr).NotTo(BeNil())
				Expect(herr.Advice).NotTo(BeEmpty(), "a client error should say how to fix the request")
			},
			fixture.Entries(fixture.BadRequestCases()),
		)

		It("answers 404 with the error model for a route that doesn't exist", func() {
			resp, body := shared.client.Get(Default, "/api/v2/deployments")

			Expect(resp).To(HaveHTTPStatus(http.StatusNotFound))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error).NotTo(BeNil())
		})
	})
})

var _ = Describe("Asking for access", func() {
	DescribeTable("grants every role whose rule fires",
		func(c fixture.AccessCase) {
			resp, out := shared.client.Access(Default, c.Request)
			fixture.ExpectAccess(Default, c, resp, out)
		},
		fixture.Entries(fixture.AccessCases()),
	)

	It("marks every grant as part of the outcome in the trace", func() {
		resp, out := shared.client.Access(Default, fixture.AccessFor(fixture.TeamPayments))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		// A collecting kind has no single winner: every candidate that
		// survives is the outcome, so every trace entry is marked.
		Expect(out.Trace).To(HaveEach(HaveField("Winner", BeTrue())))
		Expect(out.Grants[1].TTL).To(Equal("8h"))
		Expect(out.Trace[1].Payload).To(MatchJSON(`{"ttl": "8h"}`))
	})

	It("answers 500 naming both sides when admin and release manager collide", func() {
		resp, out := shared.client.Access(Default, fixture.AccessFor(fixture.BreakGlassPlatform...))

		fixture.ExpectBreakGlassConflict(Default, resp, out.Conflict, out.Error)
		Expect(out.Grants).To(BeEmpty())
		Expect(out.Error.Advice).To(ContainElement(ContainSubstring("no role is granted")))
	})

	It("answers 500 when separation of duties fails, and shows the roles that would have come together", func() {
		resp, out := shared.client.Access(Default, fixture.AccessFor(fixture.ComplianceMember...))

		fixture.ExpectAsserts(Default, resp, http.StatusInternalServerError, out.Asserts, out.Error,
			fixture.AssertEntry{Reason: "sod_auditor_deployer", Policy: "access.guardrails"})
		Expect(out.Grants).To(BeEmpty())
		Expect(out.Trace).To(ContainElements(
			HaveField("Decision", fixture.RoleDeployer),
			HaveField("Decision", fixture.RoleAuditor),
		))
	})

	It("answers 422 for an actor without a name", func() {
		resp, out := shared.client.Access(Default, fixture.AccessFor(fixture.TeamPayments).Named(""))

		fixture.ExpectAsserts(Default, resp, http.StatusUnprocessableEntity, out.Asserts, out.Error,
			fixture.AssertEntry{Reason: "named_actor", Policy: "access.guardrails"})
		// An input assert runs before any rule, so no candidate exists.
		Expect(out.Trace).To(BeEmpty())
	})

	DescribeTable("answers 400 for a body it won't evaluate",
		func(c fixture.BadRequestCase) {
			resp, raw := shared.client.PostRaw(Default, fixture.PathAccessGrants, c.Body)

			Expect(resp).To(HaveHTTPStatus(http.StatusBadRequest))
			herr := fixture.Decode[fixture.ErrorResponse](Default, raw).Error
			Expect(herr).NotTo(BeNil())
			Expect(herr.Advice).NotTo(BeEmpty(), "a client error should say how to fix the request")
		},
		fixture.Entries(fixture.AccessBadRequestCases()),
	)
})

// The README's curl examples post the files under requests/. Running them
// here keeps the walkthrough honest: a change that alters what they answer
// fails this table before it can make the README wrong.
var _ = Describe("The README's example requests", func() {
	DescribeTable("answer what the README shows",
		func(file, path string, status int) {
			body, err := os.ReadFile(filepath.Join(fixture.ExamplesDir, "requests", file))
			Expect(err).NotTo(HaveOccurred())

			resp, _ := shared.client.PostRaw(Default, path, string(body))
			Expect(resp).To(HaveHTTPStatus(status))
		},
		Entry("the owner's review", "owner.json", fixture.DeploymentsPath(fixture.TeamPayments), http.StatusAccepted),
		Entry("the short soak's deny", "short-soak.json", fixture.DeploymentsPath(fixture.TeamPayments), http.StatusForbidden),
		Entry("the SRE's approval", "sre.json", fixture.DeploymentsPath(fixture.TeamPayments), http.StatusOK),
		Entry("the unnamed actor's failed assert", "unnamed-actor.json", fixture.DeploymentsPath(fixture.TeamCheckout), http.StatusUnprocessableEntity),
		Entry("a member's grants", "access-member.json", fixture.PathAccessGrants, http.StatusOK),
		Entry("an outsider's empty outcome", "access-outsider.json", fixture.PathAccessGrants, http.StatusForbidden),
		Entry("the break-glass conflict", "access-break-glass-platform.json", fixture.PathAccessGrants, http.StatusInternalServerError),
		Entry("the compliance member's failed assert", "access-compliance-member.json", fixture.PathAccessGrants, http.StatusInternalServerError),
	)
})

var _ = Describe("The policy bundles", func() {
	It("lists both kinds with the directory each came from", func() {
		got := shared.client.ListPolicies(Default)
		fixture.ExpectServedKinds(Default, got)

		deployKind, _ := got.Kind("DeployApproval")
		Expect(deployKind.Source).To(Equal(shared.dir))
		accessKind, _ := got.Kind("AccessGrant")
		Expect(accessKind.Source).To(Equal(shared.accessDir))
	})
})
