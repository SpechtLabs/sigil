package fixture

import (
	"net/http"

	"github.com/onsi/gomega"

	request "github.com/spechtlabs/sigil/examples/alert-routing/requests"
)

// KindRouting is the name of the kind every policy implements.
const KindRouting = "AlertRouting"

// RootSuffix makes a team's root policy name: team checkout is routed by
// checkout.alerts.
const RootSuffix = ".alerts"

// ExpectRouteCase asserts that resp and out answer c: 200, whatever the
// decision, and the route c wants.
func ExpectRouteCase(g gomega.Gomega, c RouteCase, resp *http.Response, out RouteResponse) {
	g.Expect(resp).To(gomega.HaveHTTPStatus(http.StatusOK))
	ExpectRoute(g, c.Team, c.Want, out)
	g.Expect(out.Error).To(gomega.BeNil())
}

// ExpectRoute asserts that out is want, decided by team's policy: the
// decision, the reason, and the target or channel, the other one empty, and
// a trace whose one winner agrees with them.
func ExpectRoute(g gomega.Gomega, team string, want Route, out RouteResponse) {
	g.Expect(out.Team).To(gomega.Equal(team))
	g.Expect(out.Policy).To(gomega.Equal(team + RootSuffix))
	g.Expect(Route{Decision: out.Decision, Reason: out.Reason, Target: out.Target, Channel: out.Channel}).
		To(gomega.Equal(want))
	g.Expect(out.Trace).NotTo(gomega.BeNil(), "trace must be [], not null")

	// The kind's default wins when no rule fires, and it has no candidate
	// in the trace to mark.
	if len(out.Trace) == 0 {
		g.Expect(want.Reason).To(gomega.Equal(ReasonUnrouted), "only the default decides without a candidate")
		return
	}

	w := out.Winners()
	g.Expect(w).To(gomega.HaveLen(1), "trace %+v", out.Trace)
	g.Expect(w[0].Decision).To(gomega.Equal(want.Decision))
	g.Expect(w[0].Reason).To(gomega.Equal(want.Reason))
	g.Expect(w[0].Location).NotTo(gomega.BeEmpty())
}

// ExpectFallback asserts the answer to an alert whose evaluation failed:
// status, which is 503 for a timeout, alertrouter's failure to decide in
// time, 500 for a policy that failed, and 422 for an alert that failed an
// input assert; the kind's default, which the host acts on so the alert
// still reaches #alerts; and an error whose messages contain each of want.
func ExpectFallback(g gomega.Gomega, team string, status int, resp *http.Response, out RouteResponse, want ...string) {
	g.Expect(resp).To(gomega.HaveHTTPStatus(status))
	g.Expect(out.Team).To(gomega.Equal(team))
	g.Expect(out.Policy).To(gomega.Equal(team + RootSuffix))
	g.Expect(Route{Decision: out.Decision, Reason: out.Reason, Target: out.Target, Channel: out.Channel}).
		To(gomega.Equal(Unrouted))
	g.Expect(out.Error).NotTo(gomega.BeNil())
	if out.Error == nil {
		return
	}
	for _, msg := range want {
		g.Expect(out.Error.Messages()).To(gomega.ContainElement(gomega.ContainSubstring(msg)))
	}
}

// ExpectBatch asserts that resp and out answer b: 200, because Alertmanager
// retries anything else; every alert counted and answered in the order it
// came; routed counting the alerts a policy decided; and each result as b
// wants it.
func ExpectBatch(g gomega.Gomega, b Batch, resp *http.Response, out WebhookResponse) {
	g.Expect(resp).To(gomega.HaveHTTPStatus(http.StatusOK))
	g.Expect(out.Received).To(gomega.Equal(len(b.Webhook.Alerts)))
	g.Expect(out.Results).To(gomega.HaveLen(len(b.Results)))

	routed := 0
	for i, want := range b.Results {
		if want.Status == StatusRouted {
			routed++
		}
		if i >= len(out.Results) {
			continue
		}
		g.Expect(out.Results[i].Fingerprint).To(gomega.Equal(want.Fingerprint), "result %d is out of order", i)
		ExpectResult(g, want, out.Results[i])
	}
	g.Expect(out.Routed).To(gomega.Equal(routed))
}

// ExpectResult asserts one alert's result: its status, its team when want
// names one, and the route it took. A resolved alert takes none. An unowned
// or invalid alert takes the kind's default without an evaluation, so it
// has no trace, and says why no policy decided it; an unowned one names no
// team and no policy either. A failed one carries the fallback and says what failed.
func ExpectResult(g gomega.Gomega, want ResultCase, got AlertResult) {
	g.Expect(got.Fingerprint).To(gomega.Equal(want.Fingerprint))
	if want.Team != "" {
		g.Expect(got.Team).To(gomega.Equal(want.Team), "result %+v", got)
	}
	g.Expect(got.Status).To(gomega.Equal(want.Status), "result %+v", got)
	g.Expect(Route{Decision: got.Decision, Reason: got.Reason, Target: got.Target, Channel: got.Channel}).
		To(gomega.Equal(want.Want), "result %+v", got)

	switch want.Status {
	case StatusRouted:
		ExpectRoute(g, want.Team, want.Want, got.Route())
		g.Expect(got.Error).To(gomega.BeEmpty())
	case StatusResolved:
		g.Expect(got.Error).To(gomega.BeEmpty())
		g.Expect(got.Trace).To(gomega.BeEmpty())
	case StatusUnowned:
		// No team owns it, whatever its label says; the error names the
		// label.
		g.Expect(got.Team).To(gomega.BeEmpty())
		g.Expect(got.Policy).To(gomega.BeEmpty())
		g.Expect(got.Trace).To(gomega.BeEmpty())
		g.Expect(got.Error).NotTo(gomega.BeEmpty(), "an unowned alert must say why no policy decided it")
	case StatusInvalid:
		g.Expect(got.Error).NotTo(gomega.BeEmpty(), "an invalid alert must say what is wrong with it")
		g.Expect(got.Trace).To(gomega.BeEmpty())
	case StatusFailed:
		// A conflict keeps the candidates that fired, so the trace may
		// hold them; the error says what failed.
		g.Expect(got.Error).NotTo(gomega.BeEmpty(), "a failed alert must say what went wrong")
	}
}

// ExpectServedPolicies asserts the policies listing and returns its one
// entry: the AlertRouting kind with its contract version, one root per team
// in the directory, where the bundle came from, its fingerprint and when it
// loaded.
func ExpectServedPolicies(g gomega.Gomega, list PoliciesResponse) KindPolicies {
	g.Expect(list.Kinds).To(gomega.HaveLen(1))
	got, ok := list.Routing()
	g.Expect(ok).To(gomega.BeTrue(), "no %s in %+v", KindRouting, list.Kinds)

	g.Expect(got.Version).To(gomega.Equal(1))
	g.Expect(got.Source).NotTo(gomega.BeEmpty())
	g.Expect(got.Fingerprint).NotTo(gomega.BeEmpty())
	g.Expect(got.LoadedAt).NotTo(gomega.BeZero())
	g.Expect(got.Policies).To(gomega.ConsistOf(
		PolicyRef{Team: TeamCheckout, Policy: TeamCheckout + RootSuffix},
		PolicyRef{Team: TeamPayments, Policy: TeamPayments + RootSuffix},
	))

	return got
}

// ExpectDefaultTeams asserts the default team directory, which the
// integration suite serves embedded and the compose stack serves unchanged.
func ExpectDefaultTeams(g gomega.Gomega, got TeamsResponse) {
	g.Expect(got.Teams).To(gomega.ConsistOf(
		Team{Name: TeamCheckout, Oncall: CheckoutOncall, Channel: CheckoutChannel},
		Team{Name: TeamPayments, Oncall: PaymentsOncall, Channel: PaymentsChannel},
	))
}

// ExpectManifestCase sends c's request body from requests/ through client
// byte for byte, and asserts the answer requests/cases.json expects: the
// route of a single alert, the error of one the service refuses, or the
// result of every alert of a webhook.
func ExpectManifestCase(g gomega.Gomega, client *Client, c ManifestCase) {
	g.Expect(client).NotTo(gomega.BeNil(), "no client to send %s with", c.Case.Name)
	if client == nil {
		return
	}
	g.Expect(c.Err).NotTo(gomega.HaveOccurred())
	if c.Err != nil {
		return
	}

	body, herr := c.Case.Body()
	g.Expect(herr).To(gomega.Succeed())
	if herr != nil {
		return
	}

	switch c.Case.Kind {
	case request.KindRoute:
		resp, raw := client.PostRaw(g, RoutePath(c.Case.Team), string(body))
		g.Expect(resp).To(gomega.HaveHTTPStatus(c.Case.StatusCode()))
		if c.Case.StatusCode() != http.StatusOK {
			// A refused request has no route, only the error that says why.
			g.Expect(Decode[ErrorResponse](g, raw).Error).NotTo(gomega.BeNil())
			return
		}
		ExpectRoute(g, c.Case.Team, outcome(c.Case.Expect.Outcome), Decode[RouteResponse](g, raw))
	case request.KindWebhook:
		batch := Batch{Webhook: Decode[Webhook](g, body)}
		g.Expect(c.Case.Expect.Received).To(gomega.Equal(len(batch.Webhook.Alerts)), "%s expects a different count than its file holds", c.Case.Name)
		for _, r := range c.Case.Expect.Results {
			batch.Results = append(batch.Results, ResultCase{
				Fingerprint: r.Fingerprint, Status: r.Status, Team: r.Team, Want: outcome(r.Outcome),
			})
		}
		resp, out := client.WebhookRaw(g, string(body))
		ExpectBatch(g, batch, resp, out)
		g.Expect(out.Routed).To(gomega.Equal(c.Case.Expect.Routed), "%s expects a different routed count", c.Case.Name)
	default:
		g.Expect(c.Case.Kind).To(gomega.BeElementOf(request.KindRoute, request.KindWebhook), "case %s", c.Case.Name)
	}
}

func outcome(o request.Outcome) Route {
	return Route{Decision: o.Decision, Reason: o.Reason, Target: o.Target, Channel: o.Channel}
}
