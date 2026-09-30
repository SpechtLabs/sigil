package fixture

import (
	"io"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	humane "github.com/sierrasoftworks/humane-errors-go"
)

// The metric names the service promises. The Grafana dashboard, the k6 suite
// and the alerts an operator writes depend on them, so a rename should fail a
// suite.
const (
	MetricAlertsReceived = "alertrouter_alerts_received_total"
	MetricAlertsRouted   = "alertrouter_alerts_routed_total"
	MetricDecisions      = "alertrouter_decisions_total"
	MetricEvalDuration   = "alertrouter_evaluation_duration_seconds"
	MetricEvalErrors     = "alertrouter_evaluation_errors_total"
	MetricNotifications  = "alertrouter_notifications_total"
	MetricBatchSize      = "alertrouter_webhook_batch_size"

	MetricReloads    = "alertrouter_policy_reloads_total"
	MetricLastReload = "alertrouter_policy_last_reload_timestamp_seconds"
	MetricReloadOK   = "alertrouter_policy_last_reload_successful"
	MetricPolicyInfo = "alertrouter_policy_loaded_info"

	MetricRequests        = "alertrouter_requests_total"
	MetricRequestDuration = "alertrouter_request_duration_seconds"
)

// Families maps metric families by name, the shape a registry's Gather
// returns turned into the shape the text parser returns, so both suites read
// metrics the same way.
type Families map[string]*dto.MetricFamily

// Labels selects series by label. A series matches when it carries every
// pair; labels it has beyond them don't matter.
type Labels map[string]string

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
	return seriesValue(f.Find(name, labels))
}

// Sum adds up the counter or gauge series of the named family that match
// labels, zero when none does, so a spec can check that no series of a
// family moved without naming each one.
func (f Families) Sum(name string, labels Labels) float64 {
	family, ok := f[name]
	if !ok {
		return 0
	}

	sum := 0.0
	for _, m := range family.GetMetric() {
		if labels.match(m) {
			sum += seriesValue(m)
		}
	}
	return sum
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

// seriesValue returns the value of a counter, gauge or untyped series, and
// zero for none.
func seriesValue(m *dto.Metric) float64 {
	if m == nil {
		return 0
	}

	switch {
	case m.GetCounter() != nil:
		return m.GetCounter().GetValue()
	case m.GetGauge() != nil:
		return m.GetGauge().GetValue()
	default:
		return m.GetUntyped().GetValue()
	}
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
