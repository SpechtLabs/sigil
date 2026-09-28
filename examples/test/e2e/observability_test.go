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

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

// Alloy scrapes and batches asynchronously, and the profiler uploads every
// 15s. Poll for stored data, not just healthy backend processes.
const (
	backendTimeout = 60 * time.Second
	backendPolling = time.Second

	evaluateSpan = "deploygate.evaluate"
	accessSpan   = "deploygate.access"
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
		resp, _ := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
	})

	Context("Mimir", func() {
		It("receives Alloy's deploygate scrape", func() {
			Eventually(func(g Gomega) {
				res := promQuery(g, `up{job="deploygate"}`)
				g.Expect(res.Data.Result).NotTo(BeEmpty())
				g.Expect(res.Data.Result[0].Value).To(HaveLen(2))
				g.Expect(res.Data.Result[0].Value[1]).To(Equal("1"))
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})
		DescribeTable("stores the counters of both stages", func(query string) {
			Eventually(func(g Gomega) {
				res := promQuery(g, query)
				g.Expect(res.Data.ResultType).To(Equal("vector"))
				g.Expect(res.Data.Result).NotTo(BeEmpty())
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		},
			Entry("the deploy decisions", `sum(deploygate_decisions_total)`),
			Entry("the access grants", `sum by (role) (deploygate_access_grants_total)`),
		)
	})

	Context("Tempo", func() {
		DescribeTable("stores the span of each stage with what it decided", func(name string, keys ...string) {
			matchers := make([]types.GomegaMatcher, 0, len(keys))
			for _, k := range keys {
				matchers = append(matchers, HaveKey(k))
			}
			Eventually(func(g Gomega) {
				q := url.Values{"q": {`{resource.service.name = "deploygate" && name = "` + name + `"}`}, "limit": {"10"}}
				resp, body := tempo.Get(g, "/api/search?"+q.Encode())
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				search := fixture.Decode[tempoSearchResponse](g, body)
				g.Expect(search.Traces).NotTo(BeEmpty())
				resp, body = tempo.Get(g, "/api/traces/"+search.Traces[0].TraceID)
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
				g.Expect(spanTags(fixture.Decode[tempoTraceResponse](g, body), name)).To(ContainElement(And(matchers...)))
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		},
			Entry("the access stage and its roles", accessSpan, "sigil.kind", "sigil.policy", "sigil.team", "sigil.environment", "sigil.grants"),
			Entry("the deploy stage and its decision", evaluateSpan, "sigil.decision", "sigil.policy", "sigil.team", "sigil.roles"),
		)
	})

	Context("Loki", func() {
		It("stores decision logs whose trace IDs resolve in Tempo", func() {
			var traceID string
			Eventually(func(g Gomega) {
				q := url.Values{"query": {`{service_name="deploygate"} | json | msg="deploy decision" | team="payments" | trace_id!=""`}, "limit": {"1"}}
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
				g.Expect(fields).To(HaveKeyWithValue("team", "payments"))
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
				g.Expect(spanTags(fixture.Decode[tempoTraceResponse](g, body), evaluateSpan)).NotTo(BeEmpty())
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})
	})

	Context("Pyroscope", func() {
		It("receives deploygate goroutine profiles", func() {
			Eventually(func(g Gomega) {
				resp, body := pyroscope.PostJSON(g, "/querier.v1.QuerierService/LabelValues", map[string]any{
					"name":     "__profile_type__",
					"matchers": []string{`{service_name="deploygate"}`},
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
	})
})

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
