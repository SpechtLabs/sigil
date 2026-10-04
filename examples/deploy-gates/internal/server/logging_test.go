package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/go-otel-utils/otelzap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/spechtlabs/sigil/cmd/sigil/command"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/access"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/freeze"
)

// TestFailureLogLines checks the line each failed evaluation logs at its
// classified level: once, with the request span's trace and span ids once
// each, so a log pipeline that parses the JSON sees each key once.
func TestFailureLogLines(t *testing.T) {
	platformBreakGlass := deployRequest(func(_, actor map[string]any) {
		actor["groups"] = []string{"platform", "break-glass"}
	})
	unnamedService := deployRequest(func(r, actor map[string]any) {
		actor["groups"] = []string{"checkout"}
		r["service"].(map[string]any)["name"] = ""
	})

	tests := []struct {
		name       string
		path, body string
		wantStatus int
		wantMsg    string
		wantLevel  string
	}{
		{
			name:       "grants whose access evaluation failed",
			path:       "/api/v1/access/grants",
			body:       accessRequest("bea", "", "payments", "production", "platform", "break-glass"),
			wantStatus: http.StatusInternalServerError,
			wantMsg:    "access evaluation failed, granting nothing",
			wantLevel:  "error",
		},
		{
			name:       "a deployment whose access stage failed",
			path:       "/api/v1/teams/payments/deployments",
			body:       platformBreakGlass,
			wantStatus: http.StatusInternalServerError,
			wantMsg:    "access evaluation failed, denying the deployment",
			wantLevel:  "error",
		},
		{
			name:       "a deployment whose deploy stage failed",
			path:       "/api/v1/teams/checkout/deployments",
			body:       unnamedService,
			wantStatus: http.StatusUnprocessableEntity,
			wantMsg:    "deploy evaluation failed, answering with the fallback decision",
			wantLevel:  "warn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, envOptions{deployLoaded: true, accessLoaded: true, teamOverrides: map[string]string{"checkout/production.sigil": assertedCheckout}})
			logs := captureLogs(t)
			if rec := do(env.srv.Handler(), http.MethodPost, tt.path, tt.body); rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}

			var line string
			for l := range strings.Lines(logs.String()) {
				if strings.Contains(l, `"msg":"`+tt.wantMsg+`"`) {
					line = strings.TrimSpace(l)
				}
			}
			if line == "" {
				t.Fatalf("no line logged %q; logs:\n%s", tt.wantMsg, logs)
			}
			for _, key := range []string{"trace_id", "span_id", "stage", "team", "policy", "error_kind"} {
				if got := strings.Count(line, `"`+key+`":`); got != 1 {
					t.Errorf("%q appears %d times, want once, in %s", key, got, line)
				}
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("the log line isn't JSON: %v\n%s", err, line)
			}
			if entry["level"] != tt.wantLevel {
				t.Errorf("level = %v, want %s", entry["level"], tt.wantLevel)
			}
			if id, _ := entry["trace_id"].(string); len(id) != 32 {
				t.Errorf("trace_id = %q, want the request span's", id)
			}
			if caller, _ := entry["caller"].(string); !strings.HasPrefix(caller, "server/") {
				t.Errorf("caller = %q, want the handler that logged the line", caller)
			}
		})
	}
}

// TestFreezeLogLines checks that the log line of a deploy decision, and of a
// failed deploy stage, carries the freeze the policy read, so the logged
// input replays.
func TestFreezeLogLines(t *testing.T) {
	tests := []struct {
		name, path, body, wantMsg string
	}{
		{name: "a decision", path: "/api/v1/teams/payments/deployments", body: deployRequest(nil), wantMsg: "deploy decision"},
		{
			name: "a failed deploy stage",
			path: "/api/v1/teams/checkout/deployments",
			body: deployRequest(func(r, actor map[string]any) {
				checkoutOwner(r, actor)
				r["service"].(map[string]any)["name"] = ""
			}),
			wantMsg: "deploy evaluation failed, answering with the fallback decision",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, envOptions{
				deployLoaded: true, accessLoaded: true, freeze: freeze.NewStatic("production"),
				teamOverrides: map[string]string{"checkout/production.sigil": assertedCheckout},
			})
			logs := captureLogs(t)
			do(env.srv.Handler(), http.MethodPost, tt.path, tt.body)

			var entry map[string]any
			for l := range strings.Lines(logs.String()) {
				if strings.Contains(l, `"msg":"`+tt.wantMsg+`"`) {
					if err := json.Unmarshal([]byte(l), &entry); err != nil {
						t.Fatalf("the log line isn't JSON: %v\n%s", err, l)
					}
				}
			}
			if entry == nil {
				t.Fatalf("no line logged %q; logs:\n%s", tt.wantMsg, logs)
			}
			envs, _ := entry["freeze_environments"].([]any)
			if len(envs) != 1 || envs[0] != "production" || entry["freeze_unknown"] != false {
				t.Errorf("freeze = %v, %v; want [production], false", entry["freeze_environments"], entry["freeze_unknown"])
			}
		})
	}
}

// captureLogs installs an otelzap logger that writes JSON at debug level to
// the returned buffer, the way Setup's logger writes to stderr, and puts the
// previous one back when the test ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&buf), zapcore.DebugLevel)
	t.Cleanup(otelzap.ReplaceGlobals(otelzap.New(zap.New(core, zap.AddCaller()), otelzap.WithMinLevel(zapcore.DebugLevel))))
	return &buf
}

// TestDecisionLogReplays takes the input field of each deploy decision's
// log line, saves it as a file and evaluates it with the sigilc command
// line, the deploy-gates kinds linked in, against the policies on disk: the
// line alone reproduces the decision, the freeze the policy read included.
func TestDecisionLogReplays(t *testing.T) {
	tests := []struct {
		name string
		src  freeze.Source
		edit func(r, actor map[string]any)
		want string
	}{
		{name: "a frozen production", src: freeze.NewStatic("production"), want: "payments.production: deny(reason: change_freeze)"},
		{name: "an unknown freeze", src: stubFreeze{Environments: []string{"staging"}, Unknown: true}, want: "payments.production: deny(reason: change_freeze)"},
		{name: "nothing frozen", want: "payments.production: review(reason: service_owner)"},
		{
			name: "a short soak",
			edit: func(r, _ map[string]any) { r["release"] = map[string]any{"soak": "1h30m", "hotfix": false} },
			want: "payments.production: deny(reason: soak_too_short)",
		},
		{
			name: "the on-call sre's approval",
			edit: func(_, actor map[string]any) { actor["groups"] = []string{"payments-sre"} },
			want: "payments.production: approve(reason: payments_sre)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, envOptions{deployLoaded: true, accessLoaded: true, freeze: tt.src})
			logs := captureLogs(t)
			do(env.srv.Handler(), http.MethodPost, "/api/v1/teams/payments/deployments", deployRequest(tt.edit))

			var entry struct {
				Input json.RawMessage `json:"input"`
			}
			for l := range strings.Lines(logs.String()) {
				if strings.Contains(l, `"msg":"deploy decision"`) {
					if err := json.Unmarshal([]byte(l), &entry); err != nil {
						t.Fatalf("the log line isn't JSON: %v\n%s", err, l)
					}
				}
			}
			if entry.Input == nil {
				t.Fatalf("no deploy decision logged with an input; logs:\n%s", logs)
			}

			var keys map[string]json.RawMessage
			if err := json.Unmarshal(entry.Input, &keys); err != nil {
				t.Fatal(err)
			}
			for _, in := range deploy.Kind.Contract().Model.Inputs {
				if _, ok := keys[in.Name]; !ok {
					t.Errorf("the logged input has no %s, which the kind declares", in.Name)
				}
			}

			file := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(file, entry.Input, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := command.NewCommand(command.WithKind(deploy.Kind), command.WithKind(access.Kind))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs([]string{"eval", "--config", "../../policies/sigil.yaml", "--input", file, "--policy", "payments.production", "../../policies"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("sigilc eval of the logged input: %v\n%s\ninput: %s", err, out.String(), entry.Input)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("sigilc eval of the logged input decided\n%s\nwant %s; input: %s", out.String(), tt.want, entry.Input)
			}
		})
	}
}
