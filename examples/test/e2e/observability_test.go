//go:build e2e

package e2e

import (
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

// The backends receive data asynchronously: Prometheus scrapes on an
// interval and the collector batches spans before Jaeger stores them, so
// every spec here polls instead of asserting once.
const (
	backendTimeout = 30 * time.Second
	backendPolling = time.Second

	evaluateSpan = "deploygate.evaluate"
)

// The subset of the Prometheus HTTP API response the specs read.
type promQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			// Value is [unix timestamp, "value as a string"].
			Value []any `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// The subset of the Jaeger query API response the specs read. Jaeger v2
// still serves the v1 JSON API on the UI port.
type jaegerTracesResponse struct {
	Data []struct {
		TraceID string `json:"traceID"`
		Spans   []struct {
			OperationName string `json:"operationName"`
			Tags          []struct {
				Key   string `json:"key"`
				Value any    `json:"value"`
			} `json:"tags"`
		} `json:"spans"`
	} `json:"data"`
}

var _ = Describe("Observability backends", func() {
	BeforeEach(func() {
		// Make sure there is at least one fresh decision to scrape and
		// trace, whichever order Ginkgo runs the containers in.
		resp, _ := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
		Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
	})

	Context("Prometheus", func() {
		It("scrapes deploygate", func() {
			Eventually(func(g Gomega) {
				res := promQuery(g, `up{job="deploygate"}`)
				g.Expect(res.Data.Result).NotTo(BeEmpty())
				g.Expect(res.Data.Result[0].Value).To(HaveLen(2))
				g.Expect(res.Data.Result[0].Value[1]).To(Equal("1"))
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})

		It("stores the decision counters", func() {
			Eventually(func(g Gomega) {
				res := promQuery(g, `sum(deploygate_decisions_total)`)
				g.Expect(res.Data.ResultType).To(Equal("vector"))
				g.Expect(res.Data.Result).NotTo(BeEmpty())
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})
	})

	Context("Jaeger", func() {
		It("stores the evaluate span with the decision it made", func() {
			Eventually(func(g Gomega) {
				q := url.Values{"service": {"deploygate"}, "limit": {"20"}}
				resp, body := jaeger.Get(g, "/api/traces?"+q.Encode())
				g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))

				g.Expect(evaluateSpanTags(fixture.Decode[jaegerTracesResponse](g, body))).To(ContainElement(
					And(HaveKey("sigil.decision"), HaveKey("sigil.policy"), HaveKey("sigil.team")),
				), "no %s span with sigil.decision in the last 20 traces", evaluateSpan)
			}).WithTimeout(backendTimeout).WithPolling(backendPolling).Should(Succeed())
		})
	})
})

func promQuery(g Gomega, query string) promQueryResponse {
	resp, body := prometheus.Get(g, "/api/v1/query?"+url.Values{"query": {query}}.Encode())
	g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))

	res := fixture.Decode[promQueryResponse](g, body)
	g.Expect(res.Status).To(Equal("success"))

	return res
}

// evaluateSpanTags returns the tags of every evaluate span in the traces,
// one map per span, so a spec can ask for a span that carries all of them.
func evaluateSpanTags(traces jaegerTracesResponse) []map[string]any {
	var out []map[string]any

	for _, t := range traces.Data {
		for _, s := range t.Spans {
			if s.OperationName != evaluateSpan {
				continue
			}

			tags := make(map[string]any, len(s.Tags))
			for _, tag := range s.Tags {
				tags[tag.Key] = tag.Value
			}

			out = append(out, tags)
		}
	}

	return out
}
