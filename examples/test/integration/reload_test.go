package integration

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/codes"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

// The kinds, as reload spans, the reload counter and the listing name them.
const (
	kindDeploy = "DeployApproval"
	kindAccess = "AccessGrant"
)

// What the reload specs write into their copies of the bundles.
const (
	paymentsPolicy = "payments/production.sigil"
	sreRule        = "approve(payments_sre, bake: 15m)"
	sreRuleEdited  = "approve(payments_sre, bake: 30m)"

	accessPolicy     = "main.sigil"
	oncallRule       = "deployer(oncall, ttl: 2h)"
	oncallRuleEdited = "deployer(oncall, ttl: 3h)"

	// brokenDocument has a valid header, so the loader indexes it, and an
	// unfinished condition, so it fails to parse and takes the whole
	// bundle down with it.
	brokenDocument = "policy broken.production: DeployApproval@1\n\nwhen service.tier == {\n  deny(not_eligible)\n}\n"

	// brokenAccessDocument is the same mistake in the access bundle.
	brokenAccessDocument = "policy access.broken: AccessGrant@1\n\nwhen team == {\n  reader(team_member)\n}\n"

	// unguarded is a payments policy that leaves the guardrails out, which
	// the host's Require rejects no matter what else the policy says.
	unguarded = "policy payments.production: DeployApproval@1\n\nuse deploy.production\n\nproduction(approvers: [\"payments-leads\"])\n"

	// shadowGuardrails claims the platform's policy name from the team
	// bundle, to turn the guardrails into an approval.
	shadowGuardrails = "policy deploy.guardrails: DeployApproval@1\n\nwhen release.hotfix {\n  approve(payments_sre)\n}\n"

	// shadowAccessGuardrails does the same to the access guardrails, to
	// drop the separation-of-duties assert.
	shadowAccessGuardrails = "policy access.guardrails: AccessGrant@1\n\nwhen team == \"payments\" {\n  reader(team_member)\n}\n"
)

var _ = Describe("Hot reload", func() {
	// Each spec edits private copies of the bundles, so nothing needs
	// restoring and no other spec sees the edits.
	var e *env

	BeforeEach(func() {
		e = newEnv(withCopiedTeams(), withCopiedAccess())
		e.spans.Reset()
	})

	It("serves an edited deploy rule after a reload", func() {
		before := loadedAt(e, kindDeploy)
		expectSREBake(e, "15m")

		editFile(e.dir, paymentsPolicy, sreRule, sreRuleEdited)
		resp, body := e.client.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		after, ok := fixture.Decode[fixture.PoliciesResponse](Default, body).Kind(kindDeploy)
		Expect(ok).To(BeTrue())
		Expect(after.LoadedAt).To(BeTemporally(">", before))
		expectSREBake(e, "30m")
	})

	It("grants an edited time to live after a reload", func() {
		before := loadedAt(e, kindAccess)
		expectOncallTTL(e, "2h")

		editFile(e.accessDir, accessPolicy, oncallRule, oncallRuleEdited)
		resp, body := e.client.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		after, ok := fixture.Decode[fixture.PoliciesResponse](Default, body).Kind(kindAccess)
		Expect(ok).To(BeTrue())
		Expect(after.LoadedAt).To(BeTemporally(">", before))
		expectOncallTTL(e, "3h")

		By("granting the same time to live in a deployment's access block")
		resp, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Groups("payments-sre")))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		Expect(out.Access.Grants).To(ConsistOf(HaveField("TTL", "3h")))
	})

	Context("when the new bundle doesn't load", func() {
		DescribeTable("rejects it and keeps serving the last good bundle",
			func(kind, file, content string, diagnostics ...string) {
				good := loadedAt(e, kind)

				dir := e.dir
				if kind == kindAccess {
					dir = e.accessDir
				}
				Expect(os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644)).To(Succeed())

				resp, body := e.client.Reload(Default)
				Expect(resp).To(HaveHTTPStatus(http.StatusInternalServerError))

				herr := fixture.Decode[fixture.ErrorResponse](Default, body).Error
				Expect(herr).NotTo(BeNil())
				Expect(herr.Message).To(ContainSubstring("the previous bundle keeps serving"))
				// The message says what happened; the compiler's
				// diagnostics sit in the cause below it.
				for _, d := range diagnostics {
					Expect(herr.Messages()).To(ContainElement(ContainSubstring(d)))
				}
				Expect(herr.Advice).NotTo(BeEmpty())

				By("still evaluating both stages with the last good bundles")
				resp, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
				Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
				Expect(out.Reason).To(Equal(fixture.ReasonServiceOwner))
				expectSREBake(e, "15m")
				expectOncallTTL(e, "2h")
				Expect(loadedAt(e, kind)).To(Equal(good))

				By("counting the failure against its kind and marking the reload span")
				families := e.families()
				Expect(families.Value(fixture.MetricReloads, fixture.Labels{"kind": kind, "result": "failure"})).To(BeNumerically("==", 1))

				By("marking only that kind's latest load as failed, and keeping the time of the load that serves")
				other := kindDeploy
				if kind == kindDeploy {
					other = kindAccess
				}
				Expect(families.Value(fixture.MetricReloadOK, fixture.Labels{"kind": kind})).To(BeNumerically("==", 0))
				Expect(families.Value(fixture.MetricLastReload, fixture.Labels{"kind": kind})).
					To(BeNumerically("~", float64(good.Unix()), 1e-3))
				// The same POST reloaded the other bundle, which loaded fine.
				Expect(families.Value(fixture.MetricReloadOK, fixture.Labels{"kind": other})).To(BeNumerically("==", 1))
				Expect(families.Value(fixture.MetricLastReload, fixture.Labels{"kind": other})).
					To(BeNumerically(">", float64(good.Unix())))

				span := e.reloadSpan(kind)
				Expect(span.Status.Code).To(Equal(codes.Error))
				Expect(span.Events).To(ContainElement(HaveField("Name", "exception")))
			},
			Entry("a team document that doesn't parse", kindDeploy, "broken.sigil", brokenDocument, "broken.sigil:3:"),
			Entry("a team policy that leaves out the guardrails", kindDeploy, paymentsPolicy, unguarded, "deploy.guardrails"),
			Entry("a team document that claims a platform policy's name", kindDeploy, "shadow.sigil", shadowGuardrails, "deploy.guardrails", "shadow.sigil"),
			Entry("an access document that doesn't parse", kindAccess, "broken.sigil", brokenAccessDocument, "broken.sigil:3:"),
			Entry("an access document that claims the access guardrails' name", kindAccess, "shadow.sigil", shadowAccessGuardrails, "access.guardrails", "shadow.sigil"),
		)

		It("loads again once the broken document is gone", func() {
			e.writeTeamFile("broken.sigil", brokenDocument)
			resp, _ := e.client.Reload(Default)
			Expect(resp).To(HaveHTTPStatus(http.StatusInternalServerError))

			Expect(e.families().Value(fixture.MetricReloadOK, fixture.Labels{"kind": kindDeploy})).To(BeNumerically("==", 0))

			Expect(os.Remove(filepath.Join(e.dir, "broken.sigil"))).To(Succeed())
			resp, body := e.client.Reload(Default)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))

			families := e.families()
			Expect(families.Value(fixture.MetricReloads, fixture.Labels{"kind": kindDeploy, "result": "success"})).
				To(BeNumerically("==", 2))
			Expect(families.Value(fixture.MetricReloadOK, fixture.Labels{"kind": kindDeploy})).To(BeNumerically("==", 1))
			reloaded, _ := fixture.Decode[fixture.PoliciesResponse](Default, body).Kind(kindDeploy)
			Expect(families.Value(fixture.MetricLastReload, fixture.Labels{"kind": kindDeploy})).
				To(BeNumerically("~", float64(reloaded.LoadedAt.Unix()), 1e-3))
		})
	})

	Context("when the deploy store watches its directory", func() {
		var sighup chan os.Signal

		BeforeEach(func() {
			sighup = make(chan os.Signal)
			watch(func(ctx context.Context) { e.store.Watch(ctx, time.Second, sighup) })
		})

		It("reloads on the next poll after the content changed", func() {
			editFile(e.dir, paymentsPolicy, sreRule, sreRuleEdited)
			e.clock.tick()

			Eventually(func(g Gomega) {
				resp, out := e.client.Deploy(g, fixture.TeamPayments, fixture.OwnerRequest(fixture.Groups("payments-sre")))
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				g.Expect(out.Payload).To(MatchJSON(`{"bake": "30m"}`))
			}).Should(Succeed())

			span := e.reloadSpan(kindDeploy)
			Expect(attrs(span.Attributes)).To(HaveKeyWithValue("deploygate.reload.trigger", "poll"))
		})

		It("leaves an unchanged directory alone", func() {
			// A tick is received only once the loop is back in its select,
			// so after the second one the first poll has finished.
			e.clock.tick()
			e.clock.tick()

			Expect(e.families().Value(fixture.MetricReloads, fixture.Labels{"kind": kindDeploy, "result": "success"})).
				To(BeNumerically("==", 1))
			Expect(e.spansNamed(spanReload)).To(BeEmpty())
		})

		It("reports a broken bundle once, not at every poll, while the health gauge stays down", func() {
			e.writeTeamFile("broken.sigil", brokenDocument)
			e.clock.tick()
			e.clock.tick()
			e.clock.tick()

			// The counter moved once and stays put, so an alert on its rate
			// would resolve while the stale bundle keeps serving; the gauge
			// stays 0 until a load succeeds.
			families := e.families()
			Expect(families.Value(fixture.MetricReloads, fixture.Labels{"kind": kindDeploy, "result": "failure"})).
				To(BeNumerically("==", 1))
			Expect(families.Value(fixture.MetricReloadOK, fixture.Labels{"kind": kindDeploy})).To(BeNumerically("==", 0))
			Expect(families.Value(fixture.MetricReloadOK, fixture.Labels{"kind": kindAccess})).To(BeNumerically("==", 1))
		})

		It("reloads on SIGHUP whether or not anything changed", func() {
			sighup <- syscall.SIGHUP

			span := e.reloadSpan(kindDeploy)
			Expect(attrs(span.Attributes)).To(HaveKeyWithValue("deploygate.reload.trigger", "sighup"))
			Expect(e.families().Value(fixture.MetricReloads, fixture.Labels{"kind": kindDeploy, "result": "success"})).
				To(BeNumerically("==", 2))
		})
	})

	Context("when the access store watches its directory", func() {
		BeforeEach(func() {
			watch(func(ctx context.Context) { e.access.Watch(ctx, time.Second, nil) })
		})

		It("reloads on the next poll after the content changed, and only the access bundle", func() {
			editFile(e.accessDir, accessPolicy, oncallRule, oncallRuleEdited)
			e.accessClock.tick()

			Eventually(func(g Gomega) {
				resp, out := e.client.Access(g, fixture.AccessFor("payments-sre"))
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				g.Expect(out.Grants).To(ConsistOf(HaveField("TTL", "3h")))
			}).Should(Succeed())

			span := e.reloadSpan(kindAccess)
			Expect(attrs(span.Attributes)).To(HaveKeyWithValue("deploygate.reload.trigger", "poll"))
			Expect(e.families().Value(fixture.MetricReloads, fixture.Labels{"kind": kindDeploy, "result": "success"})).
				To(BeNumerically("==", 1), "an access poll reloaded the deploy bundle too")
		})
	})
})

// watch runs a store's watch loop until the spec ends.
func watch(loop func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer GinkgoRecover()
		defer close(done)
		loop(ctx)
	}()

	DeferCleanup(func() {
		cancel()
		Eventually(done).Should(BeClosed())
	})
}

// loadedAt returns when the kind's bundle loaded, as the listing reports it.
func loadedAt(e *env, kind string) time.Time {
	GinkgoHelper()

	k, ok := e.client.ListPolicies(Default).Kind(kind)
	Expect(ok).To(BeTrue(), "the listing has no %s", kind)

	return k.LoadedAt
}

// expectSREBake asserts the bake the payments SRE approval carries, the one
// value the deploy policy edit changes.
func expectSREBake(e *env, bake string) {
	GinkgoHelper()

	resp, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Groups("payments-sre")))
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	Expect(out.Reason).To(Equal("payments_sre"))
	Expect(out.Payload).To(MatchJSON(`{"bake": "` + bake + `"}`))
}

// expectOncallTTL asserts the time to live of the on-call deployer grant,
// the one value the access policy edit changes.
func expectOncallTTL(e *env, ttl string) {
	GinkgoHelper()

	resp, out := e.client.Access(Default, fixture.AccessFor("payments-sre"))
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	Expect(out.Grants).To(ConsistOf(And(HaveField("Reason", "oncall"), HaveField("TTL", ttl))))
}
