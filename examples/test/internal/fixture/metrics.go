package fixture

import (
	"io"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	humane "github.com/sierrasoftworks/humane-errors-go"
)

// The metric names the service promises. The Grafana dashboard and the
// alerts an operator writes depend on them, so a rename should fail a suite.
const (
	MetricDecisions    = "deploygate_decisions_total"
	MetricEvalDuration = "deploygate_evaluation_duration_seconds"
	MetricEvalErrors   = "deploygate_evaluation_errors_total"
	MetricReloads      = "deploygate_policy_reloads_total"
	MetricLastReload   = "deploygate_policy_last_reload_timestamp_seconds"
	MetricPolicyInfo   = "deploygate_policy_loaded_info"

	MetricRequests        = "deploygate_requests_total"
	MetricRequestDuration = "deploygate_request_duration_seconds"
)

// Families maps metric families by name, the shape a registry's Gather
// returns turned into the shape the text parser returns, so both suites read
// metrics the same way.
type Families map[string]*dto.MetricFamily

// Labels selects series by label. A series matches when it carries every
// pair; labels it has beyond them don't matter.
type Labels map[string]string

// OwnerReview is the label set of the decision OwnerRequest produces.
var OwnerReview = Labels{
	"team":     TeamPayments,
	"policy":   TeamPayments + ".production",
	"decision": DecisionReview,
	"reason":   ReasonServiceOwner,
}

// ParseMetrics reads the Prometheus text format, as /metrics serves it. The
// suites compare label sets and values instead of matching strings that
// change with label order or float formatting.
func ParseMetrics(r io.Reader) (Families, humane.Error) {
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(r)
	if err != nil {
		return nil, humane.Wrap(err, "the metrics aren't in the Prometheus text format",
			"check what the /metrics handler serves for Accept: text/plain")
	}
	return families, nil
}

// Gathered indexes what a registry's Gather returned.
func Gathered(mfs []*dto.MetricFamily) Families {
	out := make(Families, len(mfs))
	for _, mf := range mfs {
		out[mf.GetName()] = mf
	}
	return out
}

// Find returns the first series of the named family that matches labels, or
// nil.
func (f Families) Find(name string, labels Labels) *dto.Metric {
	family, ok := f[name]
	if !ok {
		return nil
	}

	for _, m := range family.GetMetric() {
		if labels.match(m) {
			return m
		}
	}

	return nil
}

// Value returns the value of a counter or gauge series, and zero when the
// series doesn't exist yet: a counter with labels only appears after its
// first increment, which is the "before" of many specs.
func (f Families) Value(name string, labels Labels) float64 {
	m := f.Find(name, labels)

	switch {
	case m == nil:
		return 0
	case m.GetCounter() != nil:
		return m.GetCounter().GetValue()
	case m.GetGauge() != nil:
		return m.GetGauge().GetValue()
	default:
		return m.GetUntyped().GetValue()
	}
}

// Count returns how many series of the named family match labels.
func (f Families) Count(name string, labels Labels) int {
	family, ok := f[name]
	if !ok {
		return 0
	}

	n := 0
	for _, m := range family.GetMetric() {
		if labels.match(m) {
			n++
		}
	}
	return n
}

func (l Labels) match(m *dto.Metric) bool {
	if m == nil {
		return false
	}

	have := make(map[string]string, len(m.GetLabel()))
	for _, pair := range m.GetLabel() {
		have[pair.GetName()] = pair.GetValue()
	}

	for k, v := range l {
		if have[k] != v {
			return false
		}
	}

	return true
}
