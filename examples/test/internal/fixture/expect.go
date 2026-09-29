package fixture

import (
	"net/http"

	"github.com/onsi/gomega"
)

// ExpectDecision asserts that resp and out answer c: the status, the
// decision, the reason and the exact payload, and a trace whose one winner
// agrees with them.
func ExpectDecision(g gomega.Gomega, c DecisionCase, resp *http.Response, out DecisionResponse) {
	g.Expect(resp).To(gomega.HaveHTTPStatus(c.Status))
	g.Expect(out.Team).To(gomega.Equal(c.Team))
	g.Expect(out.Policy).To(gomega.Equal(c.Team + ".production"))
	g.Expect(out.Decision).To(gomega.Equal(c.Decision))
	g.Expect(out.Reason).To(gomega.Equal(c.Reason))
	g.Expect(out.Payload).To(gomega.MatchJSON(c.Payload))
	g.Expect(out.Error).To(gomega.BeNil())

	g.Expect(out.Access).NotTo(gomega.BeNil(), "the response has no access block")
	g.Expect(out.Access.Policy).To(gomega.Equal(AccessPolicy))
	ExpectGrants(g, c.Grants, out.Access.Grants)

	// The kind's default wins when no rule fires, and it has no candidate
	// in the trace to mark.
	if len(out.Trace) == 0 {
		return
	}

	winners := out.Winners()
	g.Expect(winners).To(gomega.HaveLen(1), "trace %+v", out.Trace)
	g.Expect(winners[0].Decision).To(gomega.Equal(c.Decision))
	g.Expect(winners[0].Reason).To(gomega.Equal(c.Reason))
	g.Expect(winners[0].Payload).To(gomega.MatchJSON(c.Payload))
}

// ExpectOwnerTrace asserts the trace of OwnerRequest against payments: the
// review is written in deploy.production and reached through the team's
// invocation, so the winner names the platform policy, shows the call chain
// through both files and lists the conditions that held on the way.
func ExpectOwnerTrace(g gomega.Gomega, out DecisionResponse) {
	winners := out.Winners()
	g.Expect(winners).To(gomega.HaveLen(1))

	w := winners[0]
	g.Expect(w.Decision).To(gomega.Equal(DecisionReview))
	g.Expect(w.Reason).To(gomega.Equal(ReasonServiceOwner))
	g.Expect(w.Policy).To(gomega.Equal("deploy.production"))
	g.Expect(w.Location).To(gomega.Equal("payments/production.sigil:10:3 → deploy/production.sigil:16:5"))
	g.Expect(w.Conditions).To(gomega.Equal([]string{
		`service.labels["compliance"] == "pci"`,
		"cleared",
		`service.tier in ["standard", "internal"] and owns_service`,
	}))
	g.Expect(w.Payload).To(gomega.MatchJSON(out.Payload))
}

// ExpectServedKinds asserts the policies listing: both kinds, each with its
// contract version, where its bundle came from, when it loaded and the
// policies it serves.
func ExpectServedKinds(g gomega.Gomega, got PoliciesResponse) {
	g.Expect(got.Kinds).To(gomega.HaveLen(2))

	deployKind, ok := got.Kind("DeployApproval")
	g.Expect(ok).To(gomega.BeTrue(), "no DeployApproval in %+v", got.Kinds)
	g.Expect(deployKind.Version).To(gomega.Equal(1))
	g.Expect(deployKind.Source).NotTo(gomega.BeEmpty())
	g.Expect(deployKind.LoadedAt).NotTo(gomega.BeZero())
	g.Expect(deployKind.Policies).To(gomega.ConsistOf(
		PolicyRef{Team: TeamPayments, Policy: "payments.production"},
		PolicyRef{Team: TeamCheckout, Policy: "checkout.production"},
	))

	accessKind, ok := got.Kind("AccessGrant")
	g.Expect(ok).To(gomega.BeTrue(), "no AccessGrant in %+v", got.Kinds)
	g.Expect(accessKind.Version).To(gomega.Equal(1))
	g.Expect(accessKind.Source).NotTo(gomega.BeEmpty())
	g.Expect(accessKind.LoadedAt).NotTo(gomega.BeZero())
	g.Expect(accessKind.Policies).To(gomega.ConsistOf(PolicyRef{Policy: AccessPolicy}))
}

// ExpectGrants asserts that got holds exactly the grants in want, in the
// same order, each written in the access policy's bundle at a known
// position. The order is the kind's declaration order, then source
// position, so a change in it is a change a client would see.
func ExpectGrants(g gomega.Gomega, want []GrantRef, got []Grant) {
	g.Expect(got).NotTo(gomega.BeNil(), "grants must be [], not null, when nothing fired")

	refs := make([]GrantRef, 0, len(got))
	for _, gr := range got {
		refs = append(refs, GrantRef{Role: gr.Role, Reason: gr.Reason, TTL: gr.TTL})
		g.Expect(gr.Policy).To(gomega.HavePrefix("access."), "grant %+v", gr)
		g.Expect(gr.Location).NotTo(gomega.BeEmpty(), "grant %+v", gr)
	}
	g.Expect(refs).To(gomega.Equal(want))
}

// ExpectAccess asserts that resp and out answer c on the access endpoint.
// An empty outcome is a 403 with the same body, so a client reads the
// grants the same way whatever the status.
func ExpectAccess(g gomega.Gomega, c AccessCase, resp *http.Response, out AccessResponse) {
	g.Expect(resp).To(gomega.HaveHTTPStatus(c.Status))
	g.Expect(out.Policy).To(gomega.Equal(AccessPolicy))
	g.Expect(out.Team).To(gomega.Equal(c.Request.Team))
	g.Expect(out.Environment).To(gomega.Equal(c.Request.Environment))
	g.Expect(out.Error).To(gomega.BeNil())
	ExpectGrants(g, c.Grants, out.Grants)
	// Every grant is a candidate of the trace; a collecting kind drops none.
	g.Expect(out.Trace).To(gomega.HaveLen(len(c.Grants)))
}

// ExpectAsserts asserts the answer to failed asserts: the status, 422 for an
// input assert, which is the caller's to fix, and 500 for an outcome assert,
// which is the policy's; every reason named, each with its policy; and an
// error that says what failed.
func ExpectAsserts(g gomega.Gomega, resp *http.Response, status int, asserts []AssertEntry, herr *ErrorBody, want ...AssertEntry) {
	g.Expect(resp).To(gomega.HaveHTTPStatus(status))
	g.Expect(herr).NotTo(gomega.BeNil())
	if herr == nil {
		return
	}

	got := make([]AssertEntry, 0, len(asserts))
	for _, a := range asserts {
		g.Expect(a.Location).NotTo(gomega.BeEmpty(), "assert %+v", a)
		got = append(got, AssertEntry{Reason: a.Reason, Policy: a.Policy})
	}
	g.Expect(got).To(gomega.ConsistOf(want))

	for _, a := range want {
		g.Expect(herr.Message).To(gomega.ContainSubstring(a.Reason))
	}
}

// ExpectAccessFailure asserts that resp and out answer c: the status and the
// failed asserts or the conflict that explain the failure, and, through
// [ExpectNoDeployDecision], that the deploy policy never ran.
func ExpectAccessFailure(g gomega.Gomega, c AccessFailureCase, resp *http.Response, out DecisionResponse) {
	if c.Conflict {
		ExpectBreakGlassConflict(g, resp, out.Conflict, out.Error)
	} else {
		ExpectAsserts(g, resp, c.Status, out.Asserts, out.Error, c.Asserts...)
	}
	ExpectNoDeployDecision(g, out, c.Team)
}

// ExpectNoDeployDecision asserts that a deployment ended in the access stage,
// before the deploy policy ran, so no deploy decision was made on roles
// nobody granted. The decision fields hold the deploy kind's default, the
// fallback a host that fails closed acts on, with no payload; the trace is
// empty, because the deploy policy produced no candidate; and the access
// block shows that nothing was granted.
func ExpectNoDeployDecision(g gomega.Gomega, out DecisionResponse, team string) {
	g.Expect(out.Team).To(gomega.Equal(team))
	g.Expect(out.Policy).To(gomega.Equal(team + ".production"))
	g.Expect(out.Decision).To(gomega.Equal(DecisionDeny))
	g.Expect(out.Reason).To(gomega.Equal("no_rule_matched"))
	g.Expect(out.Payload).To(gomega.MatchJSON(noPayload))
	g.Expect(out.Trace).NotTo(gomega.BeNil(), "trace must be [], not null")
	g.Expect(out.Trace).To(gomega.BeEmpty())
	g.Expect(out.Access).NotTo(gomega.BeNil(), "the response has no access block")
	if out.Access == nil {
		return
	}
	g.Expect(out.Access.Policy).To(gomega.Equal(AccessPolicy))
	ExpectGrants(g, []GrantRef{}, out.Access.Grants)
}

// ExpectBreakGlassConflict asserts the 500 for a break-glass platform
// member: the kind declares admin and release_manager exclusive, both
// fired, which is a defect in the policy and not in the request, and the
// answer names each side so the policy's owners can find both rules.
func ExpectBreakGlassConflict(g gomega.Gomega, resp *http.Response, conflict *Conflict, herr *ErrorBody) {
	g.Expect(resp).To(gomega.HaveHTTPStatus(http.StatusInternalServerError))
	g.Expect(herr).NotTo(gomega.BeNil())
	g.Expect(conflict).NotTo(gomega.BeNil(), "the response has no conflict block")
	if conflict == nil {
		return
	}

	sides := make([]GrantRef, 0, len(conflict.Candidates))
	for _, c := range conflict.Candidates {
		g.Expect(c.Location).NotTo(gomega.BeEmpty(), "candidate %+v", c)
		sides = append(sides, GrantRef{Role: c.Decision, Reason: c.Reason})
	}
	g.Expect(sides).To(gomega.ConsistOf(
		GrantRef{Role: RoleAdmin, Reason: "break_glass"},
		GrantRef{Role: RoleReleaseManager, Reason: "platform_member"},
	))
}
