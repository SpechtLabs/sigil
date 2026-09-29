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

// The span and event names the service promises. Tempo searches and the
// Grafana trace panels depend on them.
const (
	spanAccess   = "deploygate.access"
	spanEvaluate = "deploygate.evaluate"
	spanReload   = "deploygate.policies.reload"
	eventCand    = "sigil.candidate"
	eventGrant   = "sigil.grant"
)

var _ = Describe("Traces", func() {
	// A fresh env per spec, so its exporter holds only the spec's spans.
	var e *env

	BeforeEach(func() {
		e = newEnv()
		e.spans.Reset() // drop the startup loads' spans
	})

	Context("when a deploy is evaluated", func() {
		var (
			out      fixture.DecisionResponse
			grant    tracetest.SpanStub
			evaluate tracetest.SpanStub
			server   tracetest.SpanStub
		)

		BeforeEach(func() {
			var resp *http.Response
			resp, out = e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Soak("2h")))
			Expect(resp).To(HaveHTTPStatus(http.StatusForbidden))

			grant = e.waitForSpan(spanAccess)
			evaluate = e.waitForSpan(spanEvaluate)
			server = e.serverSpan()
		})

		It("records the roles the access stage granted on a deploygate.access span", func() {
			Expect(attrs(grant.Attributes)).To(And(
				HaveKeyWithValue("sigil.kind", "AccessGrant"),
				HaveKeyWithValue("sigil.policy", fixture.AccessPolicy),
				HaveKeyWithValue("sigil.team", fixture.TeamPayments),
				HaveKeyWithValue("sigil.environment", fixture.EnvProduction),
				HaveKeyWithValue("sigil.grants", int64(len(out.Access.Grants))),
			))
			Expect(grant.Status.Code).NotTo(Equal(codes.Error))
		})

		It("adds one sigil.grant event per grant, in outcome order", func() {
			Expect(events(grant, eventGrant)).To(Equal([]map[string]any{
				{"role": fixture.RoleReader, "reason": "team_member", "ttl": ""},
				{"role": fixture.RoleDeployer, "reason": "team_member", "ttl": "8h"},
			}))
		})

		It("records the decision and the roles it was made with on a deploygate.evaluate span", func() {
			Expect(attrs(evaluate.Attributes)).To(And(
				HaveKeyWithValue("sigil.kind", "DeployApproval"),
				HaveKeyWithValue("sigil.policy", "payments.production"),
				HaveKeyWithValue("sigil.team", fixture.TeamPayments),
				HaveKeyWithValue("sigil.roles", []string{fixture.RoleDeployer}),
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
			Expect(events(evaluate, eventCand)).To(Equal(want))
		})

		It("runs both stages as siblings under the HTTP server span, access first", func() {
			Expect(server.Name).To(Equal("POST /api/v1/teams/:team/deployments"))
			for _, s := range []tracetest.SpanStub{grant, evaluate} {
				Expect(s.Parent.SpanID()).To(Equal(server.SpanContext.SpanID()), s.Name)
				Expect(s.SpanContext.TraceID()).To(Equal(server.SpanContext.TraceID()), s.Name)
			}
			Expect(grant.EndTime).NotTo(BeTemporally(">", evaluate.StartTime))
		})
	})

	It("records the grants of the access endpoint on the same span", func() {
		resp, _ := e.client.Access(Default, fixture.AccessFor("payments-sre"))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		span := e.waitForSpan(spanAccess)
		Expect(attrs(span.Attributes)).To(HaveKeyWithValue("sigil.grants", int64(1)))
		Expect(events(span, eventGrant)).To(Equal([]map[string]any{
			{"role": fixture.RoleDeployer, "reason": "oncall", "ttl": "2h"},
		}))
		Expect(e.serverSpan().Name).To(Equal("POST /api/v1/access/grants"))
	})

	DescribeTable("marks a failed access stage as an error and never runs the deploy policy",
		func(mutate fixture.Mutator, status int, serverStatus codes.Code) {
			resp, _ := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(mutate))
			Expect(resp).To(HaveHTTPStatus(status))

			span := e.waitForSpan(spanAccess)
			Expect(span.Status.Code).To(Equal(codes.Error))
			Expect(span.Events).To(ContainElement(HaveField("Name", "exception")))

			// Once the server span is in, the request is over, so a missing
			// evaluate span means it never started. The HTTP middleware
			// marks the server span as an error for a 5xx only, so a
			// policy's failure shows up as a failed request and the
			// caller's failed input assert doesn't.
			Expect(e.serverSpan().Status.Code).To(Equal(serverStatus))
			Expect(e.spansNamed(spanEvaluate)).To(BeEmpty())
		},
		Entry("a failed input assert", fixture.ActorName(""), http.StatusUnprocessableEntity, codes.Unset),
		Entry("a failed separation-of-duties assert", fixture.Groups(fixture.ComplianceMember...), http.StatusInternalServerError, codes.Error),
		Entry("admin and release manager in one outcome", fixture.Groups(fixture.BreakGlassPlatform...), http.StatusInternalServerError, codes.Error),
	)

	It("doesn't trace probes and scrapes", func() {
		for _, path := range []string{fixture.PathHealthz, fixture.PathReadyz, fixture.PathMetrics} {
			resp, _ := e.client.Get(Default, path)
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))
		}

		// A traced request afterwards gives the probes' spans, if there
		// were any, time to arrive: once its three spans are in, the
		// exporter holds nothing else.
		resp, _ := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
		e.waitForSpan(spanAccess)
		e.waitForSpan(spanEvaluate)
		e.serverSpan()
		Expect(e.spans.GetSpans()).To(HaveLen(3))
	})

	It("records the startup load with its own trigger", func() {
		cold := newEnv(unloaded())
		Expect(cold.store.InitialLoad(context.Background())).To(Succeed())

		span := cold.reloadSpan(kindDeploy)
		Expect(span.Status.Code).To(Equal(codes.Ok))
		Expect(attrs(span.Attributes)).To(HaveKeyWithValue("deploygate.reload.trigger", "startup"))
	})

	It("records a reload of each bundle in a deploygate.policies.reload span of its own", func() {
		resp, _ := e.client.Reload(Default)
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))

		deploySpan := e.reloadSpan(kindDeploy)
		Expect(deploySpan.Status.Code).To(Equal(codes.Ok))
		Expect(attrs(deploySpan.Attributes)).To(And(
			HaveKeyWithValue("sigil.source", e.dir),
			HaveKeyWithValue("deploygate.reload.trigger", "manual"),
			HaveKeyWithValue("sigil.policies", []string{"payments.production", "checkout.production"}),
		))

		accessSpan := e.reloadSpan(kindAccess)
		Expect(accessSpan.Status.Code).To(Equal(codes.Ok))
		Expect(attrs(accessSpan.Attributes)).To(And(
			HaveKeyWithValue("sigil.source", e.accessDir),
			HaveKeyWithValue("deploygate.reload.trigger", "manual"),
			HaveKeyWithValue("sigil.policies", []string{fixture.AccessPolicy}),
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
