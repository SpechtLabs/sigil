package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	humane "github.com/sierrasoftworks/humane-errors-go"

	clioutput "github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/output"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/scenario"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/dispatch"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/server"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/teams"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
	request "github.com/spechtlabs/sigil/examples/alert-routing/requests"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestWalkthrough(t *testing.T) {
	url := testService(t)
	// The CLI must find every scenario even when invoked outside
	// examples/alert-routing/.
	t.Chdir(t.TempDir())
	tests := []struct {
		args []string
		code int
		want []string
	}{
		{[]string{"route"}, 0, []string{"HTTP 200", "PAGE: critical_alert", "Team: checkout", "Policy: checkout.alerts", "Target: checkout-primary"}},
		{[]string{"route", "checkout-sustained", "--explain"}, 0, []string{"PAGE: sustained", "[winner] page: sustained", "Policy: platform.paging", "platform/paging.sigil", "when in_production", "[candidate] notify: routine", `with {"target":"checkout-primary"}`}},
		{[]string{"route", "checkout-warning"}, 0, []string{"NOTIFY: routine", "Channel: #checkout-alerts"}},
		{[]string{"route", "checkout-muted"}, 0, []string{"DROP: muted"}},
		{[]string{"route", "checkout-unrouted", "--explain"}, 0, []string{"NOTIFY: unrouted", "Channel: #alerts", "No candidates."}},
		{[]string{"route", "--team", "payments", "--name", "PaymentsLatencyHigh", "--severity", "warning", "--label", "env=production", "--firing-for", "7m"}, 0, []string{"PAGE: sustained", "Team: payments", "Target: payments-primary"}},
		{[]string{"route", "--team", "checkout", "--name", "CheckoutErrorRate", "--severity", "critical", "--label", "env=staging,service=api"}, 0, []string{"DROP: not_production"}},
		{[]string{"route", "invalid-severity"}, 1, []string{"HTTP 422", `"urgent" isn't a severity`, "critical, warning, info"}},
		{[]string{"route", "unknown-team"}, 1, []string{"HTTP 404", `team "search" isn't in the team directory`, "teams: checkout, payments"}},
		{[]string{"webhook"}, 0, []string{"HTTP 200", "Received 6, routed 2 by a team's policy", "STATUS", "LedgerReplicationLag", "#payments-ledger", "drop: not_production", "unowned", "invalid", "resolved", "Not routed by a policy:", "PaymentsWebhookBacklog (f8a2d61c0e7b4935)"}},
		{[]string{"webhook", "webhook-checkout"}, 0, []string{"Received 4, routed 3", "page: sustained", "checkout-primary"}},
		{[]string{"teams"}, 0, []string{"TEAM", "ON-CALL", "checkout", "checkout-primary", "#checkout-alerts", "payments-primary"}},
		{[]string{"policy"}, 0, []string{"AlertRouting@1", "Source: embedded", "Fingerprint: ", "Loaded:", "checkout.alerts  team=checkout", "payments.alerts  team=payments"}},
		{[]string{"policies", "list"}, 0, []string{"checkout.alerts"}},
		{[]string{"policy", "reload"}, 0, []string{"HTTP 200", "AlertRouting@1"}},
		{[]string{"status"}, 0, []string{"HTTP 200", "alertrouter: ready"}},
		{[]string{"metric"}, 0, []string{"alertrouter_policy_reloads_total"}},
		{[]string{"scenario"}, 0, []string{"COMMAND", "checkout-critical", "webhook-mixed"}},
		{[]string{"scenarios", "list"}, 0, []string{"route", "webhook"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			output, err := execute(t, "", append([]string{"--url", url}, tt.args...)...)
			if got := testExitCode(err); got != tt.code {
				t.Fatalf("exit = %d, want %d; err = %v; output:\n%s", got, tt.code, err, output)
			}
			for _, want := range tt.want {
				if !strings.Contains(output, want) {
					t.Errorf("output lacks %q:\n%s", want, output)
				}
			}
		})
	}

	scenarios, herr := scenario.List()
	if herr != nil {
		t.Fatal(herr)
	}
	for _, s := range scenarios {
		t.Run(s.Kind+" "+s.Name+" JSON", func(t *testing.T) {
			output, err := execute(t, "", "--url", url, s.Kind, s.Name, "--json")
			var fields map[string]json.RawMessage
			if decodeErr := json.Unmarshal([]byte(output), &fields); decodeErr != nil {
				t.Fatalf("JSON output is polluted or invalid: %v\n%s", decodeErr, output)
			}
			want := "policy"
			switch {
			case s.Kind == scenario.Webhook:
				want = "results"
			case testExitCode(err) != 0:
				want = "error"
			}
			if _, ok := fields[want]; !ok {
				t.Fatalf("response lacks %s: %s", want, output)
			}
		})
	}
}

func TestCustomRequestsAndURL(t *testing.T) {
	body, err := request.Files.ReadFile("checkout-critical.json")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/platform/api/v1/teams/payments/route" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing JSON content type")
		}
		got, readErr := io.ReadAll(r.Body)
		if readErr != nil || !bytes.Equal(got, body) {
			t.Errorf("request changed: %s, error: %v", got, readErr)
		}
		_, _ = io.WriteString(w, `{"team":"payments","policy":"payments.alerts","decision":"page","reason":"critical_alert","target":"payments-primary","trace":[]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ALERTROUTER_URL", srv.URL+"/platform/")
	for _, source := range []string{file, "-"} {
		output, callErr := execute(t, string(body), "route", "--file", source, "--team", "payments")
		if callErr != nil || !strings.Contains(output, "PAGE: critical_alert") {
			t.Fatalf("file %s: %v\n%s", source, callErr, output)
		}
	}
	// An explicit URL takes precedence over the environment.
	t.Setenv("ALERTROUTER_URL", "http://invalid.invalid")
	if _, err := execute(t, string(body), "--url", srv.URL+"/platform", "route", "-f", "-", "--team", "payments"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRouteFromFlags(t *testing.T) {
	var got map[string]map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/teams/checkout/route" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"team":"checkout","policy":"checkout.alerts","decision":"drop","reason":"muted","trace":[]}`)
	}))
	t.Cleanup(srv.Close)

	_, err := execute(t, "", "--url", srv.URL, "route", "--team", "checkout", "--name", "CheckoutCanaryLatency",
		"--severity", "warning", "--label", "env=production", "--label", "component=cart", "--firing-for", "4m")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name":       "CheckoutCanaryLatency",
		"severity":   "warning",
		"labels":     map[string]any{"env": "production", "component": "cart"},
		"firing_for": "4m",
	}
	gotJSON, _ := json.Marshal(got["alert"])
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("alert = %s, want %s", gotJSON, wantJSON)
	}
}

func TestInputErrors(t *testing.T) {
	tests := []struct {
		args []string
		in   string
		want string
	}{
		{[]string{"route", "typo"}, "", "unknown route scenario"},
		{[]string{"webhook", "checkout-critical"}, "", "unknown webhook scenario"},
		{[]string{"route", "checkout-critical", "--file", "-"}, "{}", "cannot be used together"},
		{[]string{"route", "--file", "-"}, "{}", "--team is required"},
		{[]string{"route", "--name", "X", "--severity", "info"}, "", "--team is required with --name"},
		{[]string{"route", "--name", "X", "--team", "checkout"}, "", "--severity is required"},
		{[]string{"route", "checkout-critical", "--name", "X"}, "", "--name cannot be used with a scenario"},
		{[]string{"route", "--name", "X", "--file", "-"}, "{}", "--name cannot be used with a scenario or --file"},
		{[]string{"webhook", "--file", "-"}, "not JSON", "not valid JSON"},
		{[]string{"webhook", "--file", "-"}, strings.Repeat(" ", (1<<20)+1), "exceeds"},
		{[]string{"webhook", "--file", "does-not-exist.json"}, "", "cannot open request file"},
		{[]string{"status", "--url", "localhost:8080"}, "", "invalid alertrouter URL"},
		{[]string{"status", "--url", "http://localhost?x=1"}, "", "invalid alertrouter URL"},
		{[]string{"status", "--timeout", "0s"}, "", "timeout must be positive"},
		{[]string{"metric", "--json"}, "", "Prometheus text format"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			_, err := execute(t, tt.in, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) || testExitCode(err) != 1 {
				t.Fatalf("error = %v, want %q and exit 1", err, tt.want)
			}
		})
	}
}

func TestHTTPFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"reload diagnostics", 500, `{"error":{"message":"the previous bundle keeps serving","advice":["fix the policy"],"cause":{"message":"checkout/alerts.sigil:4:1: syntax error"}}}`, "checkout/alerts.sigil:4:1: syntax error"},
		{"unavailable", 503, `{"error":{"message":"not loaded yet"}}`, "not loaded yet"},
		{"malformed response", 200, `<html>wrong service</html>`, "invalid JSON response"},
		{"redirect", 302, `{"error":{"message":"redirect"}}`, "HTTP 302"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Location", "/elsewhere")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(srv.Close)
			output, err := execute(t, "", "--url", srv.URL, "policy", "reload")
			if testExitCode(err) != 1 || !strings.Contains(output+err.Error(), tt.want) {
				t.Fatalf("err = %v, output = %s; want exit 1 and %q", err, output, tt.want)
			}
			if calls != 1 {
				t.Fatalf("request repeated %d times", calls)
			}
		})
	}
}

// TestFailedEvaluationExitCodes checks which error answers to a route are
// a failed evaluation that fell back to the kind's default, exit 2, and
// which aren't, exit 1: a failed evaluation names its policy and carries the
// fallback, whether the alert failed an input assert (422), the policy
// failed (500) or ran out of time (503), and any other error doesn't.
func TestFailedEvaluationExitCodes(t *testing.T) {
	fallback := `{"team":"checkout","policy":"checkout.alerts","decision":"notify","reason":"unrouted","channel":"#alerts","trace":[],"error":{"message":"checkout.alerts wasn't decided within alertrouter's evaluation timeout"}}`
	tests := []struct {
		name   string
		status int
		body   string
		code   int
		want   string
	}{
		{"an alert that failed an input assert", 422, fallback, 2, "NOTIFY: unrouted"},
		{"a policy that failed", 500, fallback, 2, "Channel: #alerts"},
		{"a policy that ran out of time", 503, fallback, 2, "evaluation timeout"},
		{"a severity the kind doesn't declare", 422, `{"error":{"message":"alert.severity \"urgent\" isn't a severity"}}`, 1, "isn't a severity"},
		{"a bundle not loaded yet", 503, `{"error":{"message":"no policy bundle is loaded yet"}}`, 1, "no policy bundle is loaded yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(srv.Close)
			output, err := execute(t, "", "--url", srv.URL, "route")
			if got := testExitCode(err); got != tt.code || !strings.Contains(output, tt.want) {
				t.Fatalf("exit = %d, want %d; output:\n%s", got, tt.code, output)
			}
		})
	}
}

func TestTimeoutAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	_, err := execute(t, "", "--url", srv.URL, "--timeout", "20ms", "status")
	if err == nil || !strings.Contains(err.Error(), "cannot reach alertrouter") {
		t.Fatalf("timeout error = %v", err)
	}
	cmd := NewCommand()
	cmd.SetArgs([]string{"--url", srv.URL, "status"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cmd.ExecuteContext(ctx); err == nil {
		t.Fatal("canceled command succeeded")
	}
}

func TestDiscoveryWithoutServer(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"route", "--help"}, {"webhook", "--help"}, {"scenario", "list"}, {"version"}} {
		output, err := execute(t, "", args...)
		if err != nil || output == "" {
			t.Fatalf("%v: %v, %q", args, err, output)
		}
	}
}

func execute(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(t.Context())
	return out.String(), err
}

func testExitCode(err error) int {
	if err == nil {
		return 0
	}
	if responseErr, ok := errors.AsType[*clioutput.ResponseError](err); ok {
		return responseErr.ExitCode()
	}
	return 1
}

// testService serves the embedded team policies and the default team
// directory in process, with a notifier that drops every notification, so
// the walkthrough runs against the real API without log noise.
func testService(t *testing.T) string {
	t.Helper()
	metrics := telemetry.NewMetrics()
	directory := teams.Default()
	st := store.NewRouting(store.WithMetrics(metrics), store.WithTeams(directory.Names()...))
	if err := st.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	svc, err := server.New(
		server.WithStore(st),
		server.WithDirectory(directory),
		server.WithMetrics(metrics),
		server.WithNotifier(dispatch.NotifierFunc(func(context.Context, dispatch.Notification) humane.Error { return nil })),
	)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(svc.Handler())
	t.Cleanup(srv.Close)
	return srv.URL
}
