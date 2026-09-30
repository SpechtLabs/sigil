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

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// What the reload specs write into their copies of the bundle.
const (
	// checkoutThreshold is where checkout sets when a warning pages, and
	// checkoutEdited doubles it.
	checkoutThreshold = `paging(page_after: 10m)`
	checkoutEdited    = `paging(page_after: 20m)`

	// brokenDocument has a valid header, so the loader indexes it, and an
	// unfinished condition, so it fails to parse and takes the whole
	// bundle down with it.
	brokenDocument = "policy broken.alerts: AlertRouting@1\n\nwhen alert.severity == {\n  drop(reason: muted)\n}\n"

	// unpaged is a checkout policy that leaves out the platform's paging,
	// which the host's Require rejects no matter what else it says.
	unpaged = "policy checkout.alerts: AlertRouting@1\n\nuse platform.routing\n\nrouting()\n"

	// conditionallyPaged invokes the paging, but only for alerts outside
	// the team's quiet hours label, which the Require rejects as well: the
	// platform's guarantee holds for every alert or it isn't one.
	conditionallyPaged = "policy checkout.alerts: AlertRouting@1\n\nuse platform.paging\nuse platform.routing\n\nwhen alert.labels[\"quiet\"] != \"true\" {\n  paging(page_after: 10m)\n}\n\nrouting()\n"

	// shadowPaging claims the platform policy's name from the team
	// bundle, to turn critical pages into drops.
	shadowPaging = "policy platform.paging: AlertRouting@1\n\nwhen alert.severity == critical {\n  drop(reason: muted)\n}\n"
)

var _ = Describe("Hot reload", func() {
	// Each spec edits a private copy of the bundle, so nothing needs
	// restoring and no other spec sees the edits.
	var e *env

	BeforeEach(func() {
		e = newEnv(withCopiedTeams())
		e.spans.Reset()
	})

	It("serves an edited threshold after a reload, with a new fingerprint", func() {
		before := e.served()
		expectSustainedAfter12m(e, true)

		e.editCheckout(checkoutThreshold, checkoutEdited)
		resp, body := e.client.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		after, ok := fixture.Decode[fixture.PoliciesResponse](Default, body).Routing()
		Expect(ok).To(BeTrue())
		Expect(after.LoadedAt).To(BeTemporally(">", before.LoadedAt))
		Expect(after.Fingerprint).NotTo(Equal(before.Fingerprint))
		expectSustainedAfter12m(e, false)

		By("labeling the loaded policies with the new fingerprint")
		families := e.families()
		Expect(families.Count(fixture.MetricPolicyInfo, fixture.Labels{"fingerprint": after.Fingerprint})).To(Equal(2))
		Expect(families.Count(fixture.MetricPolicyInfo, fixture.Labels{"fingerprint": before.Fingerprint})).To(BeZero())
	})

	Context("when the new bundle doesn't load", func() {
		DescribeTable("rejects it and keeps serving the last good bundle",
			func(write func(e *env), diagnostics ...string) {
				good := e.served()
				write(e)

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

				By("still routing with the last good bundle")
				expectSustainedAfter12m(e, true)
				resp, out := e.client.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))
				Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				Expect(out.Target).To(Equal(fixture.CheckoutOncall))
				Expect(e.served()).To(Equal(good))

				By("counting the failure, marking the latest load as failed and keeping the time and policies of the load that serves")
				families := e.families()
				Expect(families.Value(fixture.MetricReloads, fixture.Labels{"result": "failure"})).To(BeNumerically("==", 1))
				Expect(families.Value(fixture.MetricReloadOK, nil)).To(BeNumerically("==", 0))
				Expect(families.Value(fixture.MetricLastReload, nil)).To(BeNumerically("~", float64(good.LoadedAt.Unix()), 1e-3))
				Expect(families.Count(fixture.MetricPolicyInfo, fixture.Labels{"fingerprint": good.Fingerprint})).To(Equal(2))

				By("marking the load span failed")
				span := e.waitForSpan(spanLoad)
				Expect(span.Status.Code).To(Equal(codes.Error))
				Expect(span.Events).To(ContainElement(HaveField("Name", "exception")))
				Expect(attrs(span.Attributes)).To(HaveKeyWithValue(triggerKey, "manual"))
			},
			Entry("a team document that doesn't parse",
				func(e *env) { e.writeTeamFile("broken.sigil", brokenDocument) }, "broken.sigil:3:"),
			Entry("a team policy that leaves out the platform's paging",
				func(e *env) { e.writeTeamFile(checkoutPolicy, unpaged) }, "checkout.alerts doesn't invoke platform.paging"),
			Entry("a team policy that pages only under a condition",
				func(e *env) { e.writeTeamFile(checkoutPolicy, conditionallyPaged) }, "platform.paging must be invoked unconditionally"),
			// The platform bounds how long a team may let a warning fire
			// before it pages, so no team can page never or on every blip.
			Entry("a threshold above the platform's maximum",
				func(e *env) { e.editCheckout(checkoutThreshold, `paging(page_after: 2h)`) }, "page_after: 2h is above the maximum 1h"),
			Entry("a threshold below the platform's minimum",
				func(e *env) { e.editCheckout(checkoutThreshold, `paging(page_after: 1m)`) }, "page_after: 1m is below the minimum 5m"),
			Entry("a team document that claims the platform policy's name",
				func(e *env) { e.writeTeamFile("shadow.sigil", shadowPaging) }, "platform.paging", "shadow.sigil"),
			Entry("a team in the directory without a policy",
				func(e *env) { Expect(os.Remove(filepath.Join(e.dir, "payments", "alerts.sigil"))).To(Succeed()) }, "payments.alerts"),
		)

		It("loads again once the broken document is gone", func() {
			e.writeTeamFile("broken.sigil", brokenDocument)
			resp, _ := e.client.Reload(Default)
			Expect(resp).To(HaveHTTPStatus(http.StatusInternalServerError))
			Expect(e.families().Value(fixture.MetricReloadOK, nil)).To(BeNumerically("==", 0))

			Expect(os.Remove(filepath.Join(e.dir, "broken.sigil"))).To(Succeed())
			resp, body := e.client.Reload(Default)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))

			families := e.families()
			Expect(families.Value(fixture.MetricReloads, fixture.Labels{"result": "success"})).To(BeNumerically("==", 2))
			Expect(families.Value(fixture.MetricReloadOK, nil)).To(BeNumerically("==", 1))
			reloaded, _ := fixture.Decode[fixture.PoliciesResponse](Default, body).Routing()
			Expect(families.Value(fixture.MetricLastReload, nil)).To(BeNumerically("~", float64(reloaded.LoadedAt.Unix()), 1e-3))
		})
	})

	Context("when the store watches its directory", func() {
		var sighup chan os.Signal

		BeforeEach(func() {
			sighup = make(chan os.Signal)
			watch(func(ctx context.Context) { e.store.Watch(ctx, time.Second, sighup) })
		})

		It("reloads on the next poll after the content changed", func() {
			e.editCheckout(checkoutThreshold, checkoutEdited)
			e.clock.tick()

			Eventually(func(g Gomega) {
				resp, out := e.client.Route(g, fixture.TeamCheckout,
					fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning, fixture.FiringFor("12m")))
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				g.Expect(out.Decision).To(Equal(fixture.DecisionNotify))
			}).Should(Succeed())

			span := e.waitForSpan(spanLoad)
			Expect(attrs(span.Attributes)).To(HaveKeyWithValue(triggerKey, "poll"))
		})

		It("leaves an unchanged directory alone", func() {
			// A tick is received only once the loop is back in its select,
			// so after the second one the first poll has finished.
			e.clock.tick()
			e.clock.tick()

			Expect(e.families().Value(fixture.MetricReloads, fixture.Labels{"result": "success"})).To(BeNumerically("==", 1))
			Expect(e.spansNamed(spanLoad)).To(BeEmpty())
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
			Expect(families.Value(fixture.MetricReloads, fixture.Labels{"result": "failure"})).To(BeNumerically("==", 1))
			Expect(families.Value(fixture.MetricReloadOK, nil)).To(BeNumerically("==", 0))
		})

		It("reloads on SIGHUP whether or not anything changed", func() {
			sighup <- syscall.SIGHUP

			span := e.waitForSpan(spanLoad)
			Expect(attrs(span.Attributes)).To(HaveKeyWithValue(triggerKey, "sighup"))
			Expect(e.families().Value(fixture.MetricReloads, fixture.Labels{"result": "success"})).To(BeNumerically("==", 2))
		})
	})
})

var _ = Describe("Startup", func() {
	It("fails when a team policy leaves out the platform's paging, and never becomes ready", func() {
		e := newEnv(withCopiedTeams(), unloaded())
		e.writeTeamFile(checkoutPolicy, unpaged)

		herr := e.store.InitialLoad(context.Background())
		Expect(herr).To(HaveOccurred())
		Expect(herr.Error()).To(ContainSubstring("there is no earlier bundle to fall back to"))
		Expect(fixture.ErrorMessages(herr)).To(ContainElement(ContainSubstring("checkout.alerts doesn't invoke platform.paging")))

		resp, _ := e.client.Get(Default, fixture.PathReadyz)
		Expect(resp).To(HaveHTTPStatus(http.StatusServiceUnavailable))

		span := e.waitForSpan(spanLoad)
		Expect(span.Status.Code).To(Equal(codes.Error))
		Expect(attrs(span.Attributes)).To(HaveKeyWithValue(triggerKey, "startup"))
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

// expectSustainedAfter12m asserts whether a checkout warning that has fired
// for twelve minutes pages, the one outcome the threshold edit changes: past
// the shipped ten minutes it pages, short of the edited twenty it posts to
// the team's channel.
func expectSustainedAfter12m(e *env, pages bool) {
	GinkgoHelper()

	resp, out := e.client.Route(Default, fixture.TeamCheckout,
		fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning, fixture.FiringFor("12m")))
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	if pages {
		Expect(out.Decision).To(Equal(fixture.DecisionPage))
		Expect(out.Reason).To(Equal(fixture.ReasonSustained))
		return
	}
	Expect(out.Decision).To(Equal(fixture.DecisionNotify))
	Expect(out.Channel).To(Equal(fixture.CheckoutChannel))
}
