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

// ExpectNamedActorFailure asserts the 422 checkout answers a request without
// an actor name with: the failed assert, and the kind's default as the
// fallback the host acts on, because a host that fails closed must not act
// on whatever the rules would have said.
func ExpectNamedActorFailure(g gomega.Gomega, resp *http.Response, out DecisionResponse) {
	g.Expect(resp).To(gomega.HaveHTTPStatus(http.StatusUnprocessableEntity))
	g.Expect(out.Team).To(gomega.Equal(TeamCheckout))
	g.Expect(out.Policy).To(gomega.Equal("checkout.production"))
	g.Expect(out.Decision).To(gomega.Equal(DecisionDeny))
	g.Expect(out.Reason).To(gomega.Equal("no_rule_matched"))
	g.Expect(out.Payload).To(gomega.MatchJSON(`{}`))
	g.Expect(out.Asserts).To(gomega.ConsistOf(AssertEntry{
		Reason:   "named_actor",
		Policy:   "checkout.production",
		Location: "checkout/production.sigil:6:1",
	}))
	g.Expect(out.Error).NotTo(gomega.BeNil())
	g.Expect(out.Error.Message).To(gomega.ContainSubstring("named_actor"))
}
