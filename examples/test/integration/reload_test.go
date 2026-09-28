package integration

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/codes"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

// What the reload specs write into their copy of the team policies.
const (
	paymentsPolicy = "payments/production.sigil"
	sreRule        = "approve(payments_sre, bake: 15m)"
	sreRuleEdited  = "approve(payments_sre, bake: 30m)"

	// brokenDocument has a valid header, so the loader indexes it, and an
	// unfinished condition, so it fails to parse and takes the whole
	// bundle down with it.
	brokenDocument = "policy broken.production: DeployApproval@1\n\nwhen service.tier == {\n  deny(not_eligible)\n}\n"

	// unguarded is a payments policy that leaves the guardrails out, which
	// the host's Require rejects no matter what else the policy says.
	unguarded = "policy payments.production: DeployApproval@1\n\nuse deploy.production\n\nproduction(approvers: [\"payments-leads\"])\n"

	// shadowGuardrails claims the platform's policy name from the team
	// bundle, to turn the guardrails into an approval.
	shadowGuardrails = "policy deploy.guardrails: DeployApproval@1\n\nwhen release.hotfix {\n  approve(payments_sre)\n}\n"
)

var _ = Describe("Hot reload", func() {
	// Each spec edits a private copy of the team policies, so nothing needs
	// restoring and no other spec sees the edits.
	var e *env

	BeforeEach(func() {
		e = newEnv(withCopiedTeams())
		e.spans.Reset()
	})

	It("serves an edited rule after a reload", func() {
		before := e.client.ListPolicies(Default)
		expectSREBake(e, "15m")

		e.writeTeamFile(paymentsPolicy, strings.Replace(e.readTeamFile(paymentsPolicy), sreRule, sreRuleEdited, 1))
		resp, body := e.client.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		after := fixture.Decode[fixture.PoliciesResponse](Default, body)
		Expect(after.LoadedAt).To(BeTemporally(">", before.LoadedAt))
		expectSREBake(e, "30m")
	})

	Context("when the new bundle doesn't load", func() {
		DescribeTable("rejects it and keeps serving the last good bundle",
			func(file, content string, diagnostics ...string) {
				good := e.client.ListPolicies(Default)

				e.writeTeamFile(file, content)
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

				By("still evaluating with the last good bundle")
				resp, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
				Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
				Expect(out.Reason).To(Equal(fixture.ReasonServiceOwner))
				expectSREBake(e, "15m")
				Expect(e.client.ListPolicies(Default).LoadedAt).To(Equal(good.LoadedAt))

				By("counting the failure and marking the reload span")
				families := e.families()
				Expect(families.Value(fixture.MetricReloads, fixture.Labels{"result": "failure"})).To(BeNumerically("==", 1))
				Expect(families.Value(fixture.MetricReloads, fixture.Labels{"result": "success"})).To(BeNumerically("==", 1))
				span := e.waitForSpan(spanReload)
				Expect(span.Status.Code).To(Equal(codes.Error))
				Expect(span.Events).To(ContainElement(HaveField("Name", "exception")))
			},
			Entry("a document that doesn't parse", "broken.sigil", brokenDocument, "broken.sigil:3:"),
			Entry("a team policy that leaves out the guardrails", paymentsPolicy, unguarded, "deploy.guardrails"),
			Entry("a team document that claims a platform policy's name", "shadow.sigil", shadowGuardrails, "deploy.guardrails", "shadow.sigil"),
		)

		It("loads again once the broken document is gone", func() {
			e.writeTeamFile("broken.sigil", brokenDocument)
			resp, _ := e.client.Reload(Default)
			Expect(resp).To(HaveHTTPStatus(http.StatusInternalServerError))

			Expect(os.Remove(filepath.Join(e.dir, "broken.sigil"))).To(Succeed())
			resp, _ = e.client.Reload(Default)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))

			Expect(e.families().Value(fixture.MetricReloads, fixture.Labels{"result": "success"})).To(BeNumerically("==", 2))
		})
	})

	Context("when the store watches the directory", func() {
		var sighup chan os.Signal

		BeforeEach(func() {
			sighup = make(chan os.Signal)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})

			go func() {
				defer GinkgoRecover()
				defer close(done)
				e.store.Watch(ctx, time.Second, sighup)
			}()

			DeferCleanup(func() {
				cancel()
				Eventually(done).Should(BeClosed())
			})
		})

		It("reloads on the next poll after the content changed", func() {
			e.writeTeamFile(paymentsPolicy, strings.Replace(e.readTeamFile(paymentsPolicy), sreRule, sreRuleEdited, 1))
			e.clock.tick()

			Eventually(func(g Gomega) {
				resp, out := e.client.Deploy(g, fixture.TeamPayments, fixture.OwnerRequest(fixture.Teams("payments-sre")))
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				g.Expect(out.Payload).To(MatchJSON(`{"bake": "30m"}`))
			}).Should(Succeed())

			span := e.waitForSpan(spanReload)
			Expect(attrs(span.Attributes)).To(HaveKeyWithValue("deploygate.reload.trigger", "poll"))
		})

		It("leaves an unchanged directory alone", func() {
			// A tick is received only once the loop is back in its select,
			// so after the second one the first poll has finished.
			e.clock.tick()
			e.clock.tick()

			Expect(e.families().Value(fixture.MetricReloads, fixture.Labels{"result": "success"})).To(BeNumerically("==", 1))
			Expect(e.spansNamed(spanReload)).To(BeEmpty())
		})

		It("reports a broken bundle once, not at every poll", func() {
			e.writeTeamFile("broken.sigil", brokenDocument)
			e.clock.tick()
			e.clock.tick()
			e.clock.tick()

			Expect(e.families().Value(fixture.MetricReloads, fixture.Labels{"result": "failure"})).To(BeNumerically("==", 1))
		})

		It("reloads on SIGHUP whether or not anything changed", func() {
			sighup <- syscall.SIGHUP

			span := e.waitForSpan(spanReload)
			Expect(attrs(span.Attributes)).To(HaveKeyWithValue("deploygate.reload.trigger", "sighup"))
			Expect(e.families().Value(fixture.MetricReloads, fixture.Labels{"result": "success"})).To(BeNumerically("==", 2))
		})
	})
})

// expectSREBake asserts the bake the payments SRE approval carries, the one
// value the edit changes.
func expectSREBake(e *env, bake string) {
	GinkgoHelper()

	resp, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Teams("payments-sre")))
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	Expect(out.Reason).To(Equal("payments_sre"))
	Expect(out.Payload).To(MatchJSON(`{"bake": "` + bake + `"}`))
}
