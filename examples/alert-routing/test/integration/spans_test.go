package integration

import (
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

var _ = Describe("Traces", func() {
	// A fresh env per spec, so its exporter holds only the spec's spans.
	var e *env

	BeforeEach(func() {
		e = newEnv()
		e.spans.Reset() // drop the startup load's span
	})

	Context("when one alert is routed", func() {
		var (
			out    fixture.RouteResponse
			route  tracetest.SpanStub
			server tracetest.SpanStub
		)

		BeforeEach(func() {
			var resp *http.Response
			resp, out = e.client.Route(Default, fixture.TeamCheckout,
				fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning, fixture.FiringFor("12m")))
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))

			route = e.waitForSpan(spanRoute)
			server = e.serverSpan()
		})

		It("records the alert and the decision on an alertrouter.route span", func() {
			Expect(attrs(route.Attributes)).To(And(
				HaveKeyWithValue("alert.name", fixture.CheckoutLatency),
				HaveKeyWithValue("alert.severity", fixture.SeverityWarning),
				HaveKeyWithValue("alert.fingerprint", ""),
				HaveKeyWithValue("alertrouter.team", fixture.TeamCheckout),
				HaveKeyWithValue("sigil.policy", "checkout.alerts"),
				HaveKeyWithValue("sigil.decision", fixture.DecisionPage),
				HaveKeyWithValue("sigil.reason", fixture.ReasonSustained),
				HaveKeyWithValue("sigil.candidates", int64(len(out.Trace))),
			))
			Expect(route.Status.Code).NotTo(Equal(codes.Error))
		})

		It("adds one sigil.candidate event per trace entry, in trace order", func() {
			Expect(out.Trace).To(HaveLen(2))

			want := make([]map[string]any, 0, len(out.Trace))
			for _, c := range out.Trace {
				want = append(want, map[string]any{
					"decision": c.Decision,
					"reason":   c.Reason,
					"policy":   c.Policy,
					"location": c.Location,
					"winner":   c.Winner,
				})
			}
			Expect(events(route, eventCand)).To(Equal(want))
		})

		It("runs under the HTTP server span, named after the route template", func() {
			Expect(server.Name).To(Equal("POST " + routeRoute))
			Expect(route.Parent.SpanID()).To(Equal(server.SpanContext.SpanID()))
			Expect(route.SpanContext.TraceID()).To(Equal(server.SpanContext.TraceID()))
		})
	})

	It("records one route span per firing alert of a webhook, under the request's span", func() {
		batch := fixture.MixedBatch(time.Now())
		resp, out := e.client.Webhook(Default, batch.Webhook)
		fixture.ExpectBatch(Default, batch, resp, out)

		server := e.serverSpan()
		Expect(server.Name).To(Equal("POST " + fixture.PathAlerts))

		var routes tracetest.SpanStubs
		Eventually(func() tracetest.SpanStubs {
			routes = e.spansNamed(spanRoute)
			return routes
		}).Should(HaveLen(batch.Firing()))

		byFingerprint := map[string]map[string]any{}
		for _, s := range routes {
			Expect(s.Parent.SpanID()).To(Equal(server.SpanContext.SpanID()), s.Name)
			a := attrs(s.Attributes)
			byFingerprint[a["alert.fingerprint"].(string)] = a
			// An unowned or invalid alert was routed as designed, so its
			// span isn't marked as failed.
			Expect(s.Status.Code).NotTo(Equal(codes.Error), "span of %v", a["alert.fingerprint"])
		}
		Expect(byFingerprint).NotTo(HaveKey("checkout-resolved"))
		Expect(byFingerprint["checkout-critical"]).To(And(
			HaveKeyWithValue("sigil.decision", fixture.DecisionPage),
			HaveKeyWithValue("alertrouter.team", fixture.TeamCheckout),
		))
		// No team owns the alert, whatever its label says, and the label is
		// the alert rule's choice, so the span names no team either: a
		// search by team finds only alerts that team owns.
		Expect(byFingerprint["unknown-team"]).To(And(
			HaveKeyWithValue("alertrouter.team", "-"),
			HaveKeyWithValue("sigil.policy", ""),
			HaveKeyWithValue("sigil.reason", fixture.ReasonUnrouted),
		))
		Expect(byFingerprint["bad-severity"]).To(HaveKeyWithValue("alert.severity", "urgent"))
	})

	It("marks the route span of a failed evaluation as an error", func() {
		f := newEnv(withCopiedTeams())
		f.editCheckout(checkoutRules, conflictingRule)
		f.reloadOK()
		f.spans.Reset()

		resp, _ := f.client.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning))
		Expect(resp).To(HaveHTTPStatus(http.StatusInternalServerError))

		span := f.waitForSpan(spanRoute)
		Expect(span.Status.Code).To(Equal(codes.Error))
		Expect(span.Events).To(ContainElement(HaveField("Name", "exception")))
		Expect(attrs(span.Attributes)).To(HaveKeyWithValue("sigil.reason", fixture.ReasonUnrouted))
		Expect(f.serverSpan().Status.Code).To(Equal(codes.Error))
	})

	It("doesn't trace probes and scrapes", func() {
		for _, path := range []string{fixture.PathHealthz, fixture.PathReadyz, fixture.PathMetrics} {
			resp, _ := e.client.Get(Default, path)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		}

		// A traced request afterwards gives the probes' spans, if there
		// were any, time to arrive: once its two spans are in, the exporter
		// holds nothing else.
		resp, _ := e.client.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		e.waitForSpan(spanRoute)
		e.serverSpan()
		Expect(e.spans.GetSpans()).To(HaveLen(2))
	})

	It("records the startup load with its own trigger", func() {
		cold := newEnv(unloaded())
		Expect(cold.store.InitialLoad(context.Background())).To(Succeed())

		span := cold.waitForSpan(spanLoad)
		Expect(span.Status.Code).To(Equal(codes.Ok))
		Expect(attrs(span.Attributes)).To(HaveKeyWithValue(triggerKey, "startup"))
	})

	It("records a reload in an alertrouter.policy.load span with what it loaded", func() {
		resp, _ := e.client.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		span := e.waitForSpan(spanLoad)
		Expect(span.Status.Code).To(Equal(codes.Ok))
		Expect(attrs(span.Attributes)).To(And(
			HaveKeyWithValue("sigil.source", e.dir),
			HaveKeyWithValue("sigil.kind", fixture.KindRouting),
			HaveKeyWithValue(triggerKey, "manual"),
			HaveKeyWithValue("sigil.policies", []string{"checkout.alerts", "payments.alerts"}),
			HaveKeyWithValue("sigil.fingerprint", e.served().Fingerprint),
		))
	})
})

// attrs flattens span or event attributes into plain Go values, so a spec
// can match them with the map matchers.
func attrs(kvs []attribute.KeyValue) map[string]any {
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		out[string(kv.Key)] = kv.Value.AsInterface()
	}

	return out
}

// events returns the attributes of span's events called name, in order.
func events(span tracetest.SpanStub, name string) []map[string]any {
	var out []map[string]any
	for _, ev := range span.Events {
		if ev.Name == name {
			out = append(out, attrs(ev.Attributes))
		}
	}

	return out
}
