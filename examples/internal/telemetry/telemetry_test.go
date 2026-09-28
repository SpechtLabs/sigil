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
	if got := testutil.ToFloat64(m.lastReload); got != float64(at.Unix()) {
		t.Errorf("last reload = %v, want %v", got, at.Unix())
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
