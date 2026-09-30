//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// Alloy scrapes and batches asynchronously, and the profiler uploads every
// 15s. Poll for stored data, not just healthy backend processes.
const (
	backendTimeout = 60 * time.Second
	backendPolling = time.Second

	routeSpan = "alertrouter.route"

	// webhookServerSpan is the HTTP server span of a webhook, named after
	// its route.
	webhookServerSpan = "POST /api/v1/alerts"
)

type promQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type tempoSearchResponse struct {
	Traces []struct {
		TraceID string `json:"traceID"`
	} `json:"traces"`
}

// Tempo returns OTLP JSON, with resource batches and instrumentation scopes.
type tempoTraceResponse struct {
	Batches []struct {
		ScopeSpans []struct {
			Spans []struct {
				Name       string `json:"name"`
				Attributes []struct {
					Key   string         `json:"key"`
					Value map[string]any `json:"value"`
				} `json:"attributes"`
			} `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"batches"`
}

type lokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Values [][]string `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

var _ = Describe("Observability backends", func() {
	BeforeEach(func() {
		resp, _ := alertrouter.Route(Default, fixture.TeamCheckout, fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))
		Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	})

	Context("Mimir", func() {
		It("receives Alloy's alertrouter scrape", func() {
			Eventually(func(g Gomega) {
				res := promQuery(g, `up{job="alertrouter"}`)
				g.Expect(res.Data.Result).NotTo(BeEmpty())
				g.Expect(res.Data.Result[0].Value).To(HaveLen(2))
				g.Expect(res.Data.Result[0].Value[1]).To(Equal("1"))
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})

		DescribeTable("stores the routing counters", func(query string) {
			Eventually(func(g Gomega) {
				res := promQuery(g, query)
				g.Expect(res.Data.ResultType).To(Equal("vector"))
				g.Expect(res.Data.Result).NotTo(BeEmpty())
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		},
			Entry("the decisions", `sum by (team) (alertrouter_decisions_total)`),
			Entry("the notifications", `sum by (decision) (alertrouter_notifications_total)`),
			Entry("the loaded policies", `alertrouter_policy_loaded_info{team="checkout"}`),
		)
	})

	Context("Tempo", func() {
		It("stores the route span with what the policy decided", func() {
			matchers := make([]types.GomegaMatcher, 0, 8)
			for _, k := range []string{
				"alert.name", "alert.severity", "alertrouter.team",
				"sigil.policy", "sigil.decision", "sigil.reason", "sigil.candidates",
			} {
				matchers = append(matchers, HaveKey(k))
			}
			Eventually(func(g Gomega) {
				q := url.Values{"q": {`{resource.service.name = "alertrouter" && name = "` + routeSpan + `"}`}, "limit": {"10"}}
				resp, body := tempo.Get(g, "/api/search?"+q.Encode())
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				search := fixture.Decode[tempoSearchResponse](g, body)
				g.Expect(search.Traces).NotTo(BeEmpty())
				resp, body = tempo.Get(g, "/api/traces/"+search.Traces[0].TraceID)
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				g.Expect(spanTags(fixture.Decode[tempoTraceResponse](g, body), routeSpan)).To(ContainElement(And(matchers...)))
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})

		It("stores one route span per firing alert of a webhook, under the request's span", func() {
			// The request carries its own trace ID, so the spec reads that
			// request's trace and nothing else.
			client, traceID := alertrouter.Traced(Default)
			batch := fixture.MixedBatch(time.Now())
			resp, out := client.Webhook(Default, batch.Webhook)
			fixture.ExpectBatch(Default, batch, resp, out)

			// The server span ends last, after every route span, so once
			// it is stored the route spans are too.
			var trace tempoTraceResponse
			Eventually(func(g Gomega) {
				resp, body := tempo.Get(g, "/api/traces/"+traceID)
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				trace = fixture.Decode[tempoTraceResponse](g, body)
				g.Expect(spanTags(trace, webhookServerSpan)).To(HaveLen(1))
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())

			Expect(spanTags(trace, routeSpan)).To(HaveLen(batch.Firing()))
			Expect(spanTags(trace, routeSpan)).To(ContainElement(HaveKeyWithValue("alert.fingerprint", HaveKeyWithValue("stringValue", "checkout-critical"))))
		})
	})

	Context("Loki", func() {
		It("logs a routed alert at info and an unreadable one at warn, each with its notification in the alert's trace", func() {
			// The batch carries its own trace ID, and the fingerprints are
			// the trace's, so the spec reads its own lines and nothing else.
			client, traceID := alertrouter.Traced(Default)
			routedFP, invalidFP := "e2e-routed-"+traceID[:16], "e2e-invalid-"+traceID[:16]
			started := time.Now().Add(-time.Minute)
			resp, out := client.Webhook(Default, fixture.NewWebhook(
				fixture.Firing(routedFP, fixture.AlertLabels(fixture.TeamCheckout, fixture.CheckoutErrorRate, fixture.SeverityCritical), started),
				fixture.Firing(invalidFP, fixture.AlertLabels(fixture.TeamCheckout, fixture.CheckoutLatency, "urgent"), started),
			))
			Expect(resp).To(HaveHTTPStatus(http.StatusOK))
			Expect(out.Routed).To(Equal(1))

			Eventually(func(g Gomega) {
				lines := lokiLines(g, `{service_name="alertrouter"} |= "`+traceID+`"`)
				for fingerprint, level := range map[string]string{routedFP: "info", invalidFP: "warn"} {
					routed := findLine(lines, "alert routed", fingerprint)
					g.Expect(routed).NotTo(BeNil(), "no alert routed line for %s", fingerprint)
					g.Expect(routed).To(And(
						HaveKeyWithValue("level", level),
						HaveKeyWithValue("trace_id", traceID),
					), fingerprint)

					// The notification is dispatched inside the alert's route
					// span, so its line carries the same trace and span.
					dispatched := findLine(lines, "notification dispatched", fingerprint)
					g.Expect(dispatched).NotTo(BeNil(), "no notification dispatched line for %s", fingerprint)
					g.Expect(dispatched).To(And(
						HaveKeyWithValue("trace_id", traceID),
						HaveKeyWithValue("span_id", routed["span_id"]),
						HaveKeyWithValue("decision", routed["decision"]),
					), fingerprint)
				}
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})

		It("stores one routing log line per alert whose trace ID resolves in Tempo", func() {
			var traceID string
			Eventually(func(g Gomega) {
				q := url.Values{"query": {`{service_name="alertrouter"} | json | msg="alert routed" | team="checkout" | trace_id!=""`}, "limit": {"1"}}
				resp, body := loki.Get(g, "/loki/api/v1/query_range?"+q.Encode())
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				logs := fixture.Decode[lokiResponse](g, body)
				g.Expect(logs.Status).To(Equal("success"))
				g.Expect(logs.Data.Result).NotTo(BeEmpty())
				g.Expect(logs.Data.Result[0].Values).NotTo(BeEmpty())
				entry := logs.Data.Result[0].Values[0]
				g.Expect(entry).To(HaveLen(2))
				var fields map[string]any
				g.Expect(json.Unmarshal([]byte(entry[1]), &fields)).To(Succeed())
				g.Expect(fields).To(HaveKeyWithValue("team", "checkout"))
				g.Expect(fields).To(HaveKey("decision"))
				id, ok := fields["trace_id"].(string)
				g.Expect(ok).To(BeTrue())
				g.Expect(id).To(MatchRegexp("^[0-9a-f]{32}$"))
				traceID = id
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
			// Pin the log's trace while Tempo catches up. Repeatedly choosing
			// the newest log under load could keep outrunning trace ingestion.
			Eventually(func(g Gomega) {
				resp, body := tempo.Get(g, "/api/traces/"+traceID)
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				g.Expect(spanTags(fixture.Decode[tempoTraceResponse](g, body), routeSpan)).NotTo(BeEmpty())
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})
	})

	Context("Pyroscope", func() {
		It("receives alertrouter goroutine profiles", func() {
			Eventually(func(g Gomega) {
				resp, body := pyroscope.PostJSON(g, "/querier.v1.QuerierService/LabelValues", map[string]any{
					"name":     "__profile_type__",
					"matchers": []string{`{service_name="alertrouter"}`},
					"start":    time.Now().Add(-time.Minute).UnixMilli(),
					"end":      time.Now().UnixMilli(),
				})
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				res := fixture.Decode[struct {
					Names []string `json:"names"`
				}](g, body)
				g.Expect(res.Names).To(ContainElement("goroutines:goroutine:count:goroutine:count"))
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})
	})

	Context("Grafana", func() {
		DescribeTable("connects to each provisioned datasource", func(uid string) {
			Eventually(func(g Gomega) {
				resp, body := grafana.Get(g, "/api/datasources/uid/"+uid+"/health")
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				res := fixture.Decode[struct {
					Status string `json:"status"`
				}](g, body)
				g.Expect(res.Status).To(Equal("OK"))
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		}, Entry("Mimir", "mimir"), Entry("Tempo", "tempo"), Entry("Loki", "loki"), Entry("Pyroscope", "pyroscope"))

		It("provisions the alertrouter dashboard", func() {
			Eventually(func(g Gomega) {
				resp, body := grafana.Get(g, "/api/dashboards/uid/alertrouter")
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				res := fixture.Decode[struct {
					Dashboard struct {
						UID    string           `json:"uid"`
						Panels []map[string]any `json:"panels"`
					} `json:"dashboard"`
				}](g, body)
				g.Expect(res.Dashboard.UID).To(Equal("alertrouter"))
				g.Expect(res.Dashboard.Panels).NotTo(BeEmpty())
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})
	})
})

// lokiLines runs a LogQL query over the last hour and returns every line
// it found, each decoded from alertrouter's JSON.
func lokiLines(g Gomega, query string) []map[string]any {
	q := url.Values{"query": {query}, "limit": {"1000"}}
	resp, body := loki.Get(g, "/loki/api/v1/query_range?"+q.Encode())
	g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	logs := fixture.Decode[lokiResponse](g, body)
	g.Expect(logs.Status).To(Equal("success"))

	var out []map[string]any
	for _, stream := range logs.Data.Result {
		for _, entry := range stream.Values {
			g.Expect(entry).To(HaveLen(2))
			var fields map[string]any
			g.Expect(json.Unmarshal([]byte(entry[1]), &fields)).To(Succeed(), "log line %s", entry[1])
			out = append(out, fields)
		}
	}
	return out
}

// findLine returns the line logged with msg for the alert with
// fingerprint, or nil.
func findLine(lines []map[string]any, msg, fingerprint string) map[string]any {
	for _, l := range lines {
		if l["msg"] == msg && l["fingerprint"] == fingerprint {
			return l
		}
	}
	return nil
}

func promQuery(g Gomega, query string) promQueryResponse {
	resp, body := mimir.Get(g, "/prometheus/api/v1/query?"+url.Values{"query": {query}}.Encode())
	g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	res := fixture.Decode[promQueryResponse](g, body)
	g.Expect(res.Status).To(Equal("success"))
	return res
}

func spanTags(trace tempoTraceResponse, name string) []map[string]any {
	var out []map[string]any
	for _, batch := range trace.Batches {
		for _, scope := range batch.ScopeSpans {
			for _, span := range scope.Spans {
				if span.Name != name {
					continue
				}
				tags := make(map[string]any, len(span.Attributes))
				for _, attr := range span.Attributes {
					tags[attr.Key] = attr.Value
				}
				out = append(out, tags)
			}
		}
	}
	return out
}
