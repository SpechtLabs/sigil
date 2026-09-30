package integration

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spechtlabs/go-otel-utils/otelzap"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// The log messages the service promises. The Loki queries of the dashboard
// and of an operator chasing an alert depend on them.
const (
	logRouted     = "alert routed"
	logDispatched = "notification dispatched"
)

var _ = Describe("Logs", func() {
	// The service logs through otelzap's process logger, so each spec
	// swaps in an observer for its own lines and puts the old logger back
	// when it ends. The suite runs its specs one at a time, so no other
	// spec's lines land here.
	var logs *observer.ObservedLogs

	BeforeEach(func() {
		core, observed := observer.New(zapcore.DebugLevel)
		DeferCleanup(otelzap.ReplaceGlobals(otelzap.New(zap.New(core), otelzap.WithMinLevel(zapcore.DebugLevel))))
		logs = observed
	})

	It("writes one alert routed line per firing alert, at a level that says how it went", func() {
		e := newEnv(withLogNotifier())
		batch := fixture.MixedBatch(time.Now())
		resp, out := e.client.Webhook(Default, batch.Webhook)
		fixture.ExpectBatch(Default, batch, resp, out)

		routed := linesByFingerprint(logs, logRouted)
		Expect(routed).To(HaveLen(batch.Firing()))
		Expect(routed).NotTo(HaveKey("checkout-resolved"), "a resolved alert is acknowledged, not routed")

		// A policy's route and an unowned alert's default went where they
		// should, so they are info; an alert the router can't read is a
		// warning to whoever wrote its rule.
		for _, want := range batch.Results {
			if want.Status == fixture.StatusResolved {
				continue
			}
			line := routed[want.Fingerprint]
			level := zapcore.InfoLevel
			if want.Status == fixture.StatusInvalid {
				level = zapcore.WarnLevel
			}
			Expect(line.Level).To(Equal(level), want.Fingerprint)
			Expect(line.ContextMap()).To(And(
				HaveKeyWithValue("status", want.Status),
				HaveKeyWithValue("decision", want.Want.Decision),
				HaveKeyWithValue("reason", want.Want.Reason),
				HaveKeyWithValue("destination", destination(want.Want)),
			), want.Fingerprint)
		}
	})

	It("writes a failed alert's line as an error", func() {
		e := newEnv(withCopiedTeams(), withLogNotifier())
		e.editCheckout(checkoutRules, conflictingRule)
		e.reloadOK()

		alert := fixture.Firing("conflict", fixture.AlertLabels(fixture.TeamCheckout, fixture.CheckoutLatency, fixture.SeverityWarning), time.Now().Add(-time.Minute))
		resp, _ := e.client.Webhook(Default, fixture.NewWebhook(alert))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		line := linesByFingerprint(logs, logRouted)["conflict"]
		Expect(line.Level).To(Equal(zapcore.ErrorLevel))
		Expect(line.ContextMap()).To(And(
			HaveKeyWithValue("status", fixture.StatusFailed),
			HaveKeyWithValue("destination", fixture.DefaultChannel),
		))
	})

	It("writes one notification dispatched line per alert, in the alert's trace", func() {
		e := newEnv(withLogNotifier())
		batch := fixture.MixedBatch(time.Now())
		resp, out := e.client.Webhook(Default, batch.Webhook)
		fixture.ExpectBatch(Default, batch, resp, out)

		routed := linesByFingerprint(logs, logRouted)
		dispatched := linesByFingerprint(logs, logDispatched)
		Expect(dispatched).To(HaveLen(batch.Firing()))

		spans := map[string]tracetest.SpanStub{}
		Eventually(func() tracetest.SpanStubs { return e.spansNamed(spanRoute) }).Should(HaveLen(batch.Firing()))
		for _, s := range e.spansNamed(spanRoute) {
			spans[attrs(s.Attributes)["alert.fingerprint"].(string)] = s
		}

		for fingerprint, line := range dispatched {
			d := line.ContextMap()
			r := routed[fingerprint].ContextMap()
			span := spans[fingerprint]

			// Both lines are written inside the alert's route span, so
			// either one leads to the trace that explains the decision.
			Expect(d).To(HaveKeyWithValue("trace_id", span.SpanContext.TraceID().String()), fingerprint)
			Expect(d).To(HaveKeyWithValue("span_id", span.SpanContext.SpanID().String()), fingerprint)
			Expect(r).To(HaveKeyWithValue("trace_id", d["trace_id"]), fingerprint)
			Expect(r).To(HaveKeyWithValue("span_id", d["span_id"]), fingerprint)
			Expect(d).To(HaveKeyWithValue("decision", r["decision"]), fingerprint)
			Expect(d).To(HaveKeyWithValue("destination", r["destination"]), fingerprint)
		}
	})
})

// linesByFingerprint returns the lines logged with msg, keyed by the alert
// fingerprint they carry, and fails the spec when two carry the same one.
func linesByFingerprint(logs *observer.ObservedLogs, msg string) map[string]observer.LoggedEntry {
	GinkgoHelper()

	out := map[string]observer.LoggedEntry{}
	for _, line := range logs.FilterMessage(msg).All() {
		fingerprint, _ := line.ContextMap()["fingerprint"].(string)
		Expect(out).NotTo(HaveKey(fingerprint), "two %q lines for alert %q", msg, fingerprint)
		out[fingerprint] = line
	}
	return out
}

// destination is where a route goes, as the logs and the notification
// metric name it: the paged target, the channel, or "-" for a drop.
func destination(r fixture.Route) string {
	switch {
	case r.Target != "":
		return r.Target
	case r.Channel != "":
		return r.Channel
	}
	return "-"
}
