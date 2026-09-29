package telemetry

import (
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

func TestExporterOptionsDisabled(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want int
	}{
		{name: "no endpoint", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": ""}},
		{name: "exporter none", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317", "OTEL_TRACES_EXPORTER": "none"}},
		{name: "plaintext grpc", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317", "OTEL_TRACES_EXPORTER": ""}, want: 2},
		{name: "tls http", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector:4318", "OTEL_TRACES_EXPORTER": ""}, want: 1},
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
	for _, format := range []string{LogFormatJSON, LogFormatConsole, ""} {
		if _, err := newLogger(Config{LogFormat: format}); err != nil {
			t.Errorf("newLogger(%q): %v", format, err)
		}
	}
	if _, err := newLogger(Config{LogFormat: "xml"}); err == nil {
		t.Error("newLogger(xml) succeeded")
	}
}

func TestReloadSuccessReplacesLoadedInfo(t *testing.T) {
	m := NewMetrics()
	m.PrepareReloads("DeployApproval")
	m.PrepareReloads("AccessGrant")
	at := time.Unix(1_790_000_000, 0)

	m.ObserveReloadSuccess("DeployApproval", at, "embedded", []LoadedPolicy{
		{Team: "payments", Policy: "payments.production"},
		{Team: "checkout", Policy: "checkout.production"},
	})
	m.ObserveReloadSuccess("AccessGrant", at, "/etc/access", []LoadedPolicy{{Policy: "access.main"}})
	m.ObserveReloadSuccess("DeployApproval", at, "embedded", []LoadedPolicy{{Team: "payments", Policy: "payments.production"}})
	m.ObserveReloadFailure("AccessGrant")

	want := `
# HELP deploygate_policy_loaded_info One series per policy currently serving, always 1. team is empty for a policy that serves every team, such as access.main.
# TYPE deploygate_policy_loaded_info gauge
deploygate_policy_loaded_info{kind="AccessGrant",policy="access.main",source="/etc/access",team=""} 1
deploygate_policy_loaded_info{kind="DeployApproval",policy="payments.production",source="embedded",team="payments"} 1
`
	if err := testutil.CollectAndCompare(m.loadedInfo, strings.NewReader(want)); err != nil {
		t.Errorf("a reload of one kind must replace only that kind's series: %v", err)
	}
	want = `
# HELP deploygate_policy_reloads_total Attempts to load a policy bundle, by the kind it implements and result. A failure keeps the previous bundle serving.
# TYPE deploygate_policy_reloads_total counter
deploygate_policy_reloads_total{kind="AccessGrant",result="failure"} 1
deploygate_policy_reloads_total{kind="AccessGrant",result="success"} 1
deploygate_policy_reloads_total{kind="DeployApproval",result="failure"} 0
deploygate_policy_reloads_total{kind="DeployApproval",result="success"} 2
`
	if err := testutil.CollectAndCompare(m.reloads, strings.NewReader(want)); err != nil {
		t.Error(err)
	}
}

func TestAccessAndErrorMetrics(t *testing.T) {
	m := NewMetrics()
	m.ObserveGrant("payments", "deployer", "oncall")
	m.ObserveGrant("payments", "deployer", "oncall")
	m.ObserveEvaluationError(StageAccess, "payments", ErrorKindConflict)
	m.ObserveEvaluationError(StageDeploy, "checkout", ErrorKindAssertion)
	m.AccessTimer("payments").ObserveDuration()

	want := `
# HELP deploygate_access_grants_total Roles the access policy granted, by team, role and reason.
# TYPE deploygate_access_grants_total counter
deploygate_access_grants_total{reason="oncall",role="deployer",team="payments"} 2
`
	if err := testutil.CollectAndCompare(m.grants, strings.NewReader(want)); err != nil {
		t.Error(err)
	}
	want = `
# HELP deploygate_evaluation_errors_total Evaluations that failed, by team, kind of failure (assertion, runtime, conflict) and stage (access, deploy).
# TYPE deploygate_evaluation_errors_total counter
deploygate_evaluation_errors_total{kind="assertion",stage="deploy",team="checkout"} 1
deploygate_evaluation_errors_total{kind="conflict",stage="access",team="payments"} 1
`
	if err := testutil.CollectAndCompare(m.evaluationErrors, strings.NewReader(want)); err != nil {
		t.Error(err)
	}
	if got := testutil.CollectAndCount(m.accessEvaluationDuration); got != 1 {
		t.Errorf("access evaluation duration series = %d, want 1", got)
	}
}

// TestReloadHealth follows each kind's two reload gauges through loads that
// succeed and fail. The time moves only on a success, the health gauge on
// every attempt, and neither ever moves for the other kind.
func TestReloadHealth(t *testing.T) {
	const deploy, access = "DeployApproval", "AccessGrant"
	first := time.Unix(1_790_000_000, 0)
	later := first.Add(time.Hour)
	success := func(kind string, at time.Time) func(*Metrics) {
		return func(m *Metrics) { m.ObserveReloadSuccess(kind, at, "embedded", nil) }
	}
	failure := func(kind string) func(*Metrics) {
		return func(m *Metrics) { m.ObserveReloadFailure(kind) }
	}

	tests := []struct {
		name  string
		steps []func(*Metrics)
		// deploy's time, deploy's health, access's time, access's health.
		want [4]float64
	}{
		{name: "prepared, nothing loaded yet"},
		{
			name:  "a success stamps the time and marks the kind healthy",
			steps: []func(*Metrics){success(deploy, first)},
			want:  [4]float64{float64(first.Unix()), 1, 0, 0},
		},
		{
			name:  "a failure before any success",
			steps: []func(*Metrics){failure(access)},
		},
		{
			name:  "a failure keeps the time of the last good load",
			steps: []func(*Metrics){success(deploy, first), failure(deploy)},
			want:  [4]float64{float64(first.Unix()), 0, 0, 0},
		},
		{
			name:  "a success after a failure marks the kind healthy again",
			steps: []func(*Metrics){success(deploy, first), failure(deploy), success(deploy, later)},
			want:  [4]float64{float64(later.Unix()), 1, 0, 0},
		},
		{
			name:  "one kind's failure leaves the other kind's series alone",
			steps: []func(*Metrics){success(deploy, first), success(access, later), failure(access)},
			want:  [4]float64{float64(first.Unix()), 1, float64(later.Unix()), 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMetrics()
			m.PrepareReloads(deploy)
			m.PrepareReloads(access)
			for _, step := range tt.steps {
				step(m)
			}

			got := [4]float64{
				testutil.ToFloat64(m.lastReload.WithLabelValues(deploy)),
				testutil.ToFloat64(m.reloadSuccessful.WithLabelValues(deploy)),
				testutil.ToFloat64(m.lastReload.WithLabelValues(access)),
				testutil.ToFloat64(m.reloadSuccessful.WithLabelValues(access)),
			}
			if got != tt.want {
				t.Errorf("gauges = %v, want %v", got, tt.want)
			}
			// Both kinds have both series from the start, so an alert on
			// either kind has a series before that kind's first load ends.
			if n := testutil.CollectAndCount(m.lastReload); n != 2 {
				t.Errorf("last reload series = %d, want 2", n)
			}
			if n := testutil.CollectAndCount(m.reloadSuccessful); n != 2 {
				t.Errorf("reload health series = %d, want 2", n)
			}
		})
	}
}
