package telemetry

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSplitEndpoint(t *testing.T) {
	tests := []struct {
		in, wantHost, wantScheme string
	}{
		{in: "http://otel-collector:4317", wantHost: "otel-collector:4317", wantScheme: "http"},
		{in: "https://collector.example.com:4318", wantHost: "collector.example.com:4318", wantScheme: "https"},
		{in: "otel-collector:4317", wantHost: "otel-collector:4317"},
		{in: "localhost:4318", wantHost: "localhost:4318"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			host, scheme := splitEndpoint(tt.in)
			if host != tt.wantHost || scheme != tt.wantScheme {
				t.Errorf("splitEndpoint(%q) = %q, %q, want %q, %q", tt.in, host, scheme, tt.wantHost, tt.wantScheme)
			}
		})
	}
}

func TestUseGRPC(t *testing.T) {
	tests := []struct {
		name, hostPort, protocol string
		want                     bool
	}{
		{name: "grpc port", hostPort: "collector:4317", want: true},
		{name: "http port", hostPort: "collector:4318"},
		{name: "protocol wins over port", hostPort: "collector:4317", protocol: "http/protobuf"},
		{name: "grpc on another port", hostPort: "collector:9000", protocol: "grpc", want: true},
		{name: "no port", hostPort: "collector"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", tt.protocol)
			if got := useGRPC(tt.hostPort); got != tt.want {
				t.Errorf("useGRPC(%q) with protocol %q = %v, want %v", tt.hostPort, tt.protocol, got, tt.want)
			}
		})
	}
}

func TestExporterOptions(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want int
	}{
		{name: "no endpoint", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": ""}},
		{name: "exporter none", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317", "OTEL_TRACES_EXPORTER": "none"}},
		{name: "plaintext grpc", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317", "OTEL_TRACES_EXPORTER": ""}, want: 2},
		{name: "tls http", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector:4318", "OTEL_TRACES_EXPORTER": ""}, want: 1},
		{name: "traces endpoint wins", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "collector:4318", "OTEL_TRACES_EXPORTER": ""}, want: 1},
		{name: "insecure by flag", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4317", "OTEL_EXPORTER_OTLP_INSECURE": "true", "OTEL_TRACES_EXPORTER": ""}, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "")
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if got := len(exporterOptions()); got != tt.want {
				t.Errorf("exporterOptions() gave %d options, want %d", got, tt.want)
			}
		})
	}
}

func TestNewLogger(t *testing.T) {
	tests := []struct {
		format  string
		debug   bool
		wantErr bool
	}{
		{format: LogFormatJSON},
		{format: LogFormatConsole},
		{format: ""},
		{format: LogFormatJSON, debug: true},
		{format: "xml", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			logger, err := newLogger(Config{LogFormat: tt.format, Debug: tt.debug})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("newLogger(%q) succeeded", tt.format)
				}
				return
			}
			if err != nil {
				t.Fatalf("newLogger(%q): %v", tt.format, err)
			}
			if got := logger.Core().Enabled(-1); got != tt.debug {
				t.Errorf("debug enabled = %v, want %v", got, tt.debug)
			}
		})
	}
}

func TestNewResource(t *testing.T) {
	tests := []struct {
		name, env, version    string
		wantName, wantVersion string
	}{
		{name: "defaults", wantName: DefaultServiceName, wantVersion: "dev"},
		{name: "from the environment", env: "router-canary", version: "v1.2.3", wantName: "router-canary", wantVersion: "v1.2.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_SERVICE_NAME", tt.env)
			attrs := map[string]string{}
			for _, kv := range newResource(tt.version).Attributes() {
				attrs[string(kv.Key)] = kv.Value.String()
			}
			if attrs["service.name"] != tt.wantName || attrs["service.version"] != tt.wantVersion {
				t.Errorf("service = %s@%s, want %s@%s", attrs["service.name"], attrs["service.version"], tt.wantName, tt.wantVersion)
			}
		})
	}
}

func TestSetupAndShutdown(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("PYROSCOPE_SERVER_ADDRESS", "")
	tel, err := Setup(Config{Version: "test", LogFormat: LogFormatJSON})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := tel.Shutdown(t.Context()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}

	if _, err := Setup(Config{LogFormat: "xml"}); err == nil {
		t.Error("Setup with an unknown log format succeeded")
	}
}

// TestReloadMetrics follows the reload series through loads that succeed and
// fail. The time moves only on a success, the health gauge on every attempt,
// and the loaded-policy series only on a success, which replaces them.
func TestReloadMetrics(t *testing.T) {
	first := time.Unix(1_790_000_000, 0)
	later := first.Add(time.Hour)
	both := []LoadedPolicy{{Team: "checkout", Policy: "checkout.alerts"}, {Team: "payments", Policy: "payments.alerts"}}
	success := func(at time.Time, fingerprint string, loaded []LoadedPolicy) func(*Metrics) {
		return func(m *Metrics) { m.ObserveReloadSuccess(at, "embedded", fingerprint, loaded) }
	}
	failure := func(m *Metrics) { m.ObserveReloadFailure() }

	tests := []struct {
		name       string
		steps      []func(*Metrics)
		wantAt     float64
		wantHealth float64
		wantLoaded string
		wantCount  string
	}{
		{
			name:      "prepared, nothing loaded yet",
			wantCount: "failure 0, success 0",
		},
		{
			name:       "a success stamps the time, marks the bundle healthy and lists its policies",
			steps:      []func(*Metrics){success(first, "aaa", both)},
			wantAt:     float64(first.Unix()),
			wantHealth: 1,
			wantLoaded: `alertrouter_policy_loaded_info{fingerprint="aaa",policy="checkout.alerts",source="embedded",team="checkout"} 1
alertrouter_policy_loaded_info{fingerprint="aaa",policy="payments.alerts",source="embedded",team="payments"} 1
`,
			wantCount: "failure 0, success 1",
		},
		{
			name:      "a failure before any success",
			steps:     []func(*Metrics){failure},
			wantCount: "failure 1, success 0",
		},
		{
			name:   "a failure keeps the time and the policies of the last good load",
			steps:  []func(*Metrics){success(first, "aaa", both), failure},
			wantAt: float64(first.Unix()),
			wantLoaded: `alertrouter_policy_loaded_info{fingerprint="aaa",policy="checkout.alerts",source="embedded",team="checkout"} 1
alertrouter_policy_loaded_info{fingerprint="aaa",policy="payments.alerts",source="embedded",team="payments"} 1
`,
			wantCount: "failure 1, success 1",
		},
		{
			name:       "a success replaces the policies, dropping a team and moving the fingerprint",
			steps:      []func(*Metrics){success(first, "aaa", both), failure, success(later, "bbb", both[:1])},
			wantAt:     float64(later.Unix()),
			wantHealth: 1,
			wantLoaded: `alertrouter_policy_loaded_info{fingerprint="bbb",policy="checkout.alerts",source="embedded",team="checkout"} 1
`,
			wantCount: "failure 1, success 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMetrics()
			m.PrepareReloads()
			for _, step := range tt.steps {
				step(m)
			}

			if got := testutil.ToFloat64(m.lastReload); got != tt.wantAt {
				t.Errorf("last reload = %v, want %v", got, tt.wantAt)
			}
			if got := testutil.ToFloat64(m.reloadSuccessful); got != tt.wantHealth {
				t.Errorf("last reload successful = %v, want %v", got, tt.wantHealth)
			}
			count := func(result string) float64 { return testutil.ToFloat64(m.reloads.WithLabelValues(result)) }
			if got := fmt.Sprintf("failure %g, success %g", count(reloadFailure), count(reloadSuccess)); got != tt.wantCount {
				t.Errorf("reloads = %s, want %s", got, tt.wantCount)
			}
			want := ""
			if tt.wantLoaded != "" {
				want = "# HELP alertrouter_policy_loaded_info One series per team policy currently serving, always 1, with the fingerprint and source of the bundle it was loaded from.\n# TYPE alertrouter_policy_loaded_info gauge\n" + tt.wantLoaded
			}
			if err := testutil.CollectAndCompare(m.loadedInfo, strings.NewReader(want)); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestRoutingMetrics checks each recorder lands on its own series with the
// labels the dashboard and the alerts query.
func TestRoutingMetrics(t *testing.T) {
	m := NewMetrics()
	m.ObserveBatch(3)
	m.ObserveReceived(AlertFiring)
	m.ObserveReceived(AlertFiring)
	m.ObserveReceived(AlertResolved)
	m.ObserveRouted("checkout", OutcomeRouted)
	m.ObserveRouted(NoTeam, OutcomeUnowned)
	m.ObserveDecision("checkout", "checkout.alerts", "page", "sustained")
	m.ObserveEvaluationError("payments", ErrorKindTimeout)
	m.ObserveNotification("page", "checkout-primary")
	m.ObserveNotification("drop", "-")
	m.EvaluationTimer("checkout").ObserveDuration()
	m.RequestTimer()("200", "POST", "/api/v1/alerts")

	tests := []struct {
		name string
		got  func() float64
		want float64
	}{
		{name: "firing received", got: func() float64 { return testutil.ToFloat64(m.received.WithLabelValues(AlertFiring)) }, want: 2},
		{name: "resolved received", got: func() float64 { return testutil.ToFloat64(m.received.WithLabelValues(AlertResolved)) }, want: 1},
		{name: "routed", got: func() float64 { return testutil.ToFloat64(m.routed.WithLabelValues("checkout", OutcomeRouted)) }, want: 1},
		{name: "unowned", got: func() float64 { return testutil.ToFloat64(m.routed.WithLabelValues(NoTeam, OutcomeUnowned)) }, want: 1},
		{name: "decision", got: func() float64 {
			return testutil.ToFloat64(m.decisions.WithLabelValues("checkout", "checkout.alerts", "page", "sustained"))
		}, want: 1},
		{name: "timeout", got: func() float64 {
			return testutil.ToFloat64(m.evaluationErrors.WithLabelValues("payments", ErrorKindTimeout))
		}, want: 1},
		{name: "page notification", got: func() float64 { return testutil.ToFloat64(m.notifications.WithLabelValues("page", "checkout-primary")) }, want: 1},
		{name: "drop notification", got: func() float64 { return testutil.ToFloat64(m.notifications.WithLabelValues("drop", "-")) }, want: 1},
		{name: "requests", got: func() float64 { return testutil.ToFloat64(m.requests.WithLabelValues("200", "POST", "/api/v1/alerts")) }, want: 1},
		{name: "batch series", got: func() float64 { return float64(testutil.CollectAndCount(m.batchSize)) }, want: 1},
		{name: "evaluation duration series", got: func() float64 { return float64(testutil.CollectAndCount(m.evaluationDuration)) }, want: 1},
		{name: "request duration series", got: func() float64 { return float64(testutil.CollectAndCount(m.requestDuration)) }, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got(); got != tt.want {
				t.Errorf("value = %v, want %v", got, tt.want)
			}
		})
	}
}
