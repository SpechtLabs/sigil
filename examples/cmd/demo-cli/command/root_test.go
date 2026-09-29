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

	clioutput "github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/output"
	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/scenario"
	"github.com/spechtlabs/sigil/examples/internal/server"
	"github.com/spechtlabs/sigil/examples/internal/store"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
	request "github.com/spechtlabs/sigil/examples/requests"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestWalkthrough(t *testing.T) {
	url := testService(t)
	// The CLI must find every scenario even when invoked outside examples/.
	t.Chdir(t.TempDir())
	tests := []struct {
		args []string
		code int
		want []string
	}{
		{[]string{"deploy"}, 0, []string{"HTTP 202", "REVIEW: service_owner", "payments-leads", "security-leads", "deployer: team_member, expires in 8h"}},
		{[]string{"deploy", "owner", "--explain"}, 0, []string{"[winner] review: service_owner", "when service.labels", "payments/production.sigil"}},
		{[]string{"deploy", "short-soak", "--explain"}, 2, []string{"HTTP 403", "DENY: soak_too_short", "[winner] deny", "[candidate] review"}},
		{[]string{"deploy", "sre"}, 0, []string{"APPROVE: payments_sre", "Bake: 15m", "deployer: oncall, expires in 2h"}},
		{[]string{"deploy", "unnamed-actor"}, 2, []string{"HTTP 422", "DENY: no_rule_matched", "Team: checkout", "Failed assert: named_actor", "No roles granted.", "the deploy policy didn't run"}},
		{[]string{"access"}, 0, []string{"reader: team_member", "deployer: team_member, expires in 8h"}},
		{[]string{"access", "outsider"}, 2, []string{"HTTP 403", "No roles granted."}},
		{[]string{"access", "break-glass-platform"}, 2, []string{"HTTP 500", "No roles granted.", "Conflicting candidates:", "admin: break_glass", "release_manager: platform_member"}},
		{[]string{"access", "compliance-member", "--explain"}, 2, []string{"HTTP 500", "Failed assert: sod_auditor_deployer", "auditor", "deployer"}},
		{[]string{"policies"}, 0, []string{"DeployApproval@1", "AccessGrant@1", "payments.production", "access.main", "Source: embedded", "Loaded:"}},
		{[]string{"policies", "list"}, 0, []string{"payments.production"}},
		{[]string{"policies", "reload"}, 0, []string{"HTTP 200", "DeployApproval@1", "AccessGrant@1"}},
		{[]string{"status"}, 0, []string{"HTTP 200", "deploygate: ready"}},
		{[]string{"metrics"}, 0, []string{"deploygate_policy_reloads_total"}},
		{[]string{"deploy", "owner", "--team", "missing"}, 1, []string{"HTTP 404", `team "missing" isn't served`, "served teams:"}},
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
	for _, s := range scenario.List() {
		t.Run(s.Command+" "+s.Name+" JSON", func(t *testing.T) {
			output, err := execute(t, "", "--url", url, s.Command, s.Name, "--json")
			var fields map[string]json.RawMessage
			if decodeErr := json.Unmarshal([]byte(output), &fields); decodeErr != nil {
				t.Fatalf("JSON output is polluted or invalid: %v\n%s", decodeErr, output)
			}
			if _, ok := fields["policy"]; !ok {
				t.Fatalf("response lacks policy: %s", output)
			}
			if s.Name == "short-soak" && testExitCode(err) != 2 {
				t.Fatalf("--json hid refusal: %v", err)
			}
		})
	}
}

func TestCustomRequestsAndURL(t *testing.T) {
	body, err := request.Files.ReadFile("owner.json")
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
		if r.Method != http.MethodPost || r.URL.Path != "/platform/api/v1/teams/checkout/deployments" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing JSON content type")
		}
		got, readErr := io.ReadAll(r.Body)
		if readErr != nil || !bytes.Equal(got, body) {
			t.Errorf("request changed: %s, error: %v", got, readErr)
		}
		_, _ = io.WriteString(w, `{"team":"checkout","policy":"checkout.production","decision":"approve","reason":"staging","payload":{"bake":"0s"}}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DEPLOYGATE_URL", srv.URL+"/platform/")
	for _, source := range []string{file, "-"} {
		output, callErr := execute(t, string(body), "deploy", "--file", source, "--team", "checkout")
		if callErr != nil || !strings.Contains(output, "APPROVE: staging") {
			t.Fatalf("file %s: %v\n%s", source, callErr, output)
		}
	}
	// An explicit URL takes precedence over the environment.
	t.Setenv("DEPLOYGATE_URL", "http://invalid.invalid")
	if _, err := execute(t, string(body), "--url", srv.URL+"/platform", "deploy", "-f", "-", "--team", "checkout"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestInputErrors(t *testing.T) {
	tests := []struct {
		args []string
		in   string
		want string
	}{
		{[]string{"deploy", "typo"}, "", "unknown deploy scenario"},
		{[]string{"deploy", "owner", "--file", "-"}, "{}", "cannot be used together"},
		{[]string{"deploy", "--file", "-"}, "{}", "--team is required"},
		{[]string{"access", "--file", "-"}, "not JSON", "not valid JSON"},
		{[]string{"access", "--file", "-"}, strings.Repeat(" ", (1<<20)+1), "exceeds"},
		{[]string{"access", "--file", "does-not-exist.json"}, "", "cannot open request file"},
		{[]string{"status", "--url", "localhost:8080"}, "", "invalid deploygate URL"},
		{[]string{"status", "--url", "http://localhost?x=1"}, "", "invalid deploygate URL"},
		{[]string{"status", "--timeout", "0s"}, "", "timeout must be positive"},
		{[]string{"metrics", "--json"}, "", "Prometheus text format"},
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
		{"reload diagnostics", 500, `{"error":{"message":"previous bundle keeps serving","advice":["fix the policy"],"cause":{"message":"production.sigil:4:1: syntax error"}}}`, "production.sigil:4:1: syntax error"},
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
			output, err := execute(t, "", "--url", srv.URL, "policies", "reload")
			if testExitCode(err) != 1 || !strings.Contains(output+err.Error(), tt.want) {
				t.Fatalf("err = %v, output = %s; want exit 1 and %q", err, output, tt.want)
			}
			if calls != 1 {
				t.Fatalf("request repeated %d times", calls)
			}
		})
	}
}

// TestFailedEvaluationExitCodes checks which 5xx answers to a deployment
// are the policy's refusal, exit 2, and which aren't, exit 1: a failed
// evaluation names its policy and carries the fallback, whether the policy
// failed (500) or ran out of time (503), and any other 5xx doesn't.
func TestFailedEvaluationExitCodes(t *testing.T) {
	fallback := `{"team":"payments","policy":"payments.production","decision":"deny","reason":"no_rule_matched","payload":{},"trace":[],"error":{"message":"payments.production wasn't decided within deploygate's evaluation timeout"}}`
	tests := []struct {
		name   string
		status int
		body   string
		code   int
		want   string
	}{
		{"a policy that failed", 500, fallback, 2, "DENY: no_rule_matched"},
		{"a policy that ran out of time", 503, fallback, 2, "evaluation timeout"},
		{"bundles not loaded yet", 503, `{"error":{"message":"no policy bundle is loaded yet"}}`, 1, "no policy bundle is loaded yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(srv.Close)
			output, err := execute(t, "", "--url", srv.URL, "deploy")
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
	if err == nil || !strings.Contains(err.Error(), "cannot reach deploygate") {
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
	for _, args := range [][]string{{"--help"}, {"deploy", "--help"}, {"scenarios"}, {"version"}} {
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

func testService(t *testing.T) string {
	t.Helper()
	metrics := telemetry.NewMetrics()
	deploy := store.NewDeploy(store.WithMetrics(metrics), store.WithTeams("payments", "checkout"))
	access := store.NewAccess(store.WithMetrics(metrics))
	if err := deploy.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := access.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	svc, err := server.New(server.WithStore(deploy), server.WithAccessStore(access), server.WithMetrics(metrics))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(svc.Handler())
	t.Cleanup(srv.Close)
	return srv.URL
}
