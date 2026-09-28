package integration

import (
	"context"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

// The span and event names the service promises. Jaeger searches and the
// Grafana trace panels depend on them.
const (
	spanEvaluate = "deploygate.evaluate"
	spanReload   = "deploygate.policies.reload"
	eventCand    = "sigil.candidate"
)

var _ = Describe("Traces", func() {
	// A fresh env per spec, so its exporter holds only the spec's spans.
	var e *env

	BeforeEach(func() {
		e = newEnv()
		e.spans.Reset() // drop the startup load's span
	})

	Context("when a deploy is evaluated", func() {
		var (
			out      fixture.DecisionResponse
			evaluate tracetest.SpanStub
		)

		BeforeEach(func() {
			var resp *http.Response
			resp, out = e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Soak("2h")))
			Expect(resp).To(HaveHTTPStatus(http.StatusForbidden))

			evaluate = e.waitForSpan(spanEvaluate)
		})

		It("records the decision on a deploygate.evaluate span", func() {
			Expect(attrs(evaluate.Attributes)).To(And(
				HaveKeyWithValue("sigil.kind", "DeployApproval"),
				HaveKeyWithValue("sigil.policy", "payments.production"),
				HaveKeyWithValue("sigil.team", fixture.TeamPayments),
				HaveKeyWithValue("sigil.decision", fixture.DecisionDeny),
				HaveKeyWithValue("sigil.reason", "soak_too_short"),
				HaveKeyWithValue("sigil.candidates", int64(len(out.Trace))),
			))
			Expect(evaluate.Status.Code).NotTo(Equal(codes.Error))
		})

		It("adds one sigil.candidate event per trace entry, in trace order", func() {
			// The short soak fires the guardrail's deny and the owner's
			// review, so the span shows the review that lost as well.
			Expect(out.Trace).To(HaveLen(2))

			var events []map[string]any
			for _, ev := range evaluate.Events {
				if ev.Name == eventCand {
					events = append(events, attrs(ev.Attributes))
				}
			}

			Expect(events).To(HaveLen(len(out.Trace)))
			for i, c := range out.Trace {
				Expect(events[i]).To(Equal(map[string]any{
					"decision": c.Decision,
					"reason":   c.Reason,
					"policy":   c.Policy,
					"location": c.Location,
					"winner":   c.Winner,
				}), "event %d", i)
			}
		})

		It("nests the evaluation under the HTTP server span", func() {
			server := e.serverSpan()
			Expect(server.Name).To(Equal("POST /api/v1/teams/:team/deployments"))
			Expect(evaluate.Parent.SpanID()).To(Equal(server.SpanContext.SpanID()))
			Expect(evaluate.SpanContext.TraceID()).To(Equal(server.SpanContext.TraceID()))
		})
	})

	It("marks a failed evaluation's span as an error", func() {
		resp, _ := e.client.Deploy(Default, fixture.TeamCheckout, fixture.OwnerRequest(fixture.ActorName("")))
		Expect(resp).To(HaveHTTPStatus(http.StatusUnprocessableEntity))

		span := e.waitForSpan(spanEvaluate)
		Expect(span.Status.Code).To(Equal(codes.Error))
		Expect(span.Status.Description).To(ContainSubstring("named_actor"))
		Expect(attrs(span.Attributes)).To(And(
			HaveKeyWithValue("sigil.decision", fixture.DecisionDeny),
			HaveKeyWithValue("sigil.reason", "no_rule_matched"),
			HaveKeyWithValue("sigil.candidates", int64(0)),
		))
	})

	It("doesn't trace probes and scrapes", func() {
		for _, path := range []string{fixture.PathHealthz, fixture.PathReadyz, fixture.PathMetrics} {
			resp, _ := e.client.Get(Default, path)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		}

		// A traced request afterwards gives the probes' spans, if there
		// were any, time to arrive: once its two spans are in, the
		// exporter holds nothing else.
		resp, _ := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		e.waitForSpan(spanEvaluate)
		e.serverSpan()
		Expect(e.spans.GetSpans()).To(HaveLen(2))
	})

	It("records the startup load with its own trigger", func() {
		cold := newEnv(unloaded())
		Expect(cold.store.InitialLoad(context.Background())).To(Succeed())

		span := cold.waitForSpan(spanReload)
		Expect(span.Status.Code).To(Equal(codes.Ok))
		Expect(attrs(span.Attributes)).To(HaveKeyWithValue("deploygate.reload.trigger", "startup"))
	})

	It("records a reload in a deploygate.policies.reload span", func() {
		resp, _ := e.client.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		span := e.waitForSpan(spanReload)
		Expect(span.Status.Code).To(Equal(codes.Ok))
		Expect(attrs(span.Attributes)).To(And(
			HaveKeyWithValue("sigil.source", e.dir),
			HaveKeyWithValue("sigil.kind", "DeployApproval"),
			HaveKeyWithValue("deploygate.reload.trigger", "manual"),
			HaveKeyWithValue("sigil.policies", []string{"payments.production", "checkout.production"}),
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
