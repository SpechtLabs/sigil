package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/server"
	"github.com/spechtlabs/sigil/examples/internal/store"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		check      func(t *testing.T, resp server.DecisionResponse)
		name       string
		team       string
		body       string
		wantStatus int
		wantDec    string
		wantReason string
	}{
		{
			name:       "service owner of a pci service goes to review",
			team:       "payments",
			body:       request(nil),
			wantStatus: http.StatusAccepted,
			wantDec:    "review",
			wantReason: "service_owner",
			check: func(t *testing.T, resp server.DecisionResponse) {
				assertJSON(t, resp.Payload, `{"approvers":["payments-leads","security-leads"]}`)
				if len(resp.Trace) == 0 || !resp.Trace[0].Winner {
					t.Fatalf("trace = %+v, want the winner first", resp.Trace)
				}
				w := resp.Trace[0]
				if w.Policy != "deploy.production" || !strings.Contains(w.Location, "→") || len(w.Conditions) == 0 {
					t.Errorf("winner = %+v, want deploy.production reached through an invocation, with conditions", w)
				}
			},
		},
		{
			name: "payments sre gets an approval with a bake",
			team: "payments",
			body: request(func(r map[string]any) {
				r["actor"].(map[string]any)["teams"] = []string{"payments-sre"}
			}),
			wantStatus: http.StatusOK,
			wantDec:    "approve",
			wantReason: "payments_sre",
			check: func(t *testing.T, resp server.DecisionResponse) {
				assertJSON(t, resp.Payload, `{"bake":"15m"}`)
				assertJSON(t, resp.Trace[0].Payload, `{"bake":"15m"}`)
			},
		},
		{
			name: "a short soak is denied",
			team: "payments",
			body: request(func(r map[string]any) {
				r["release"] = map[string]any{"soak": "2h", "hotfix": false}
			}),
			wantStatus: http.StatusForbidden,
			wantDec:    "deny",
			wantReason: "soak_too_short",
			check: func(t *testing.T, resp server.DecisionResponse) {
				assertJSON(t, resp.Payload, `{}`)
			},
		},
		{
			name: "nothing matching falls back to the default deny",
			team: "payments",
			body: request(func(r map[string]any) {
				r["actor"].(map[string]any)["teams"] = []string{"marketing"}
			}),
			wantStatus: http.StatusForbidden,
			wantDec:    "deny",
			wantReason: "no_rule_matched",
			check: func(t *testing.T, resp server.DecisionResponse) {
				for _, c := range resp.Trace {
					if c.Winner {
						t.Errorf("candidate %+v marked as winner of a default decision", c)
					}
				}
			},
		},
		{
			name: "an unnamed actor fails checkout's assert",
			team: "checkout",
			body: request(func(r map[string]any) {
				r["actor"].(map[string]any)["name"] = ""
				r["service"].(map[string]any)["owners"] = []string{"checkout"}
				r["actor"].(map[string]any)["teams"] = []string{"checkout"}
			}),
			wantStatus: http.StatusUnprocessableEntity,
			wantDec:    "deny",
			wantReason: "no_rule_matched",
			check: func(t *testing.T, resp server.DecisionResponse) {
				if len(resp.Asserts) != 1 || resp.Asserts[0].Reason != "named_actor" || resp.Asserts[0].Location == "" {
					t.Errorf("asserts = %+v, want named_actor with a location", resp.Asserts)
				}
				if resp.Error == nil || !strings.Contains(resp.Error.Message, "named_actor") || len(resp.Error.Advice) == 0 {
					t.Errorf("error = %+v, want a message naming named_actor, with advice", resp.Error)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newServer(t).srv.Handler()
			rec := do(h, http.MethodPost, "/api/v1/teams/"+tt.team+"/deployments", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			var resp server.DecisionResponse
			decode(t, rec, &resp)
			if resp.Team != tt.team || resp.Policy != tt.team+".production" {
				t.Errorf("team, policy = %s, %s", resp.Team, resp.Policy)
			}
			if resp.Decision != tt.wantDec || resp.Reason != tt.wantReason {
				t.Errorf("decision = %s(%s), want %s(%s)", resp.Decision, resp.Reason, tt.wantDec, tt.wantReason)
			}
			tt.check(t, resp)
		})
	}
}

func TestEvaluateRejects(t *testing.T) {
	tests := []struct {
		name       string
		team       string
		body       string
		wantStatus int
		wantMsg    string
	}{
		{name: "unknown team", team: "billing", body: request(nil), wantStatus: http.StatusNotFound, wantMsg: `team "billing" isn't served`},
		{name: "malformed json", team: "payments", body: `{"release":`, wantStatus: http.StatusBadRequest, wantMsg: "isn't a valid deploy request"},
		{name: "empty body", team: "payments", body: ``, wantStatus: http.StatusBadRequest, wantMsg: "empty"},
		{name: "unknown field", team: "payments", body: request(func(r map[string]any) { r["cluster"] = "eu-1" }), wantStatus: http.StatusBadRequest, wantMsg: "cluster"},
		{name: "numeric duration", team: "payments", body: request(func(r map[string]any) {
			r["release"] = map[string]any{"soak": 21600}
		}), wantStatus: http.StatusBadRequest, wantMsg: "isn't a valid deploy request"},
		{name: "unparsable duration", team: "payments", body: request(func(r map[string]any) {
			r["release"] = map[string]any{"soak": "a while"}
		}), wantStatus: http.StatusBadRequest, wantMsg: "isn't a valid deploy request"},
		{name: "negative soak", team: "payments", body: request(func(r map[string]any) {
			r["release"] = map[string]any{"soak": "-1h"}
		}), wantStatus: http.StatusBadRequest, wantMsg: "release.soak is negative"},
		{name: "unknown field tier at the top level", team: "payments", body: request(func(r map[string]any) { r["tier"] = "standard" }), wantStatus: http.StatusBadRequest, wantMsg: "tier"},
		{name: "two json values", team: "payments", body: request(nil) + request(nil), wantStatus: http.StatusBadRequest, wantMsg: "more than one JSON value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(newServer(t).srv.Handler(), http.MethodPost, "/api/v1/teams/"+tt.team+"/deployments", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			var env server.ErrorEnvelope
			decode(t, rec, &env)
			if !strings.Contains(errorText(env.Error), tt.wantMsg) {
				t.Errorf("body %s doesn't mention %q", rec.Body, tt.wantMsg)
			}
			if env.Error != nil && len(env.Error.Advice) == 0 {
				t.Errorf("error %+v has no advice", env.Error)
			}
		})
	}
}

func TestEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		loaded     bool
		wantStatus int
		wantBody   []string
	}{
		{name: "healthz", method: http.MethodGet, path: "/healthz", loaded: true, wantStatus: http.StatusOK, wantBody: []string{`"status":"ok"`}},
		{name: "healthz before a load", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK, wantBody: []string{`"status":"ok"`}},
		{name: "readyz", method: http.MethodGet, path: "/readyz", loaded: true, wantStatus: http.StatusOK, wantBody: []string{`"status":"ready"`, `"loaded_at"`}},
		{name: "readyz before a load", method: http.MethodGet, path: "/readyz", wantStatus: http.StatusServiceUnavailable, wantBody: []string{`"status":"not ready"`}},
		{name: "policies", method: http.MethodGet, path: "/api/v1/policies", loaded: true, wantStatus: http.StatusOK, wantBody: []string{
			`"kind":"DeployApproval"`, `"version":1`, `"source":"embedded"`,
			`{"team":"payments","policy":"payments.production"}`, `{"team":"checkout","policy":"checkout.production"}`,
		}},
		{name: "policies before a load", method: http.MethodGet, path: "/api/v1/policies", wantStatus: http.StatusServiceUnavailable, wantBody: []string{"no policy bundle is loaded yet"}},
		{name: "evaluate before a load", method: http.MethodPost, path: "/api/v1/teams/payments/deployments", wantStatus: http.StatusServiceUnavailable, wantBody: []string{"no policy bundle is loaded yet"}},
		{name: "reload", method: http.MethodPost, path: "/api/v1/policies/reload", loaded: true, wantStatus: http.StatusOK, wantBody: []string{`"policies":[`}},
		{name: "reload loads a store that never loaded", method: http.MethodPost, path: "/api/v1/policies/reload", wantStatus: http.StatusOK, wantBody: []string{`"kind":"DeployApproval"`}},
		{name: "unknown route", method: http.MethodGet, path: "/api/v2/nothing", loaded: true, wantStatus: http.StatusNotFound, wantBody: []string{"no route for GET /api/v2/nothing"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServerWith(t, tt.loaded)
			rec := do(env.srv.Handler(), tt.method, tt.path, request(nil))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			for _, want := range tt.wantBody {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body %s doesn't contain %s", rec.Body, want)
				}
			}
		})
	}
}

// TestReloadFailureKeepsServing breaks the policies directory and checks
// that the reload endpoint reports the compile diagnostics while the old
// bundle keeps answering.
func TestReloadFailureKeepsServing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "payments/production.sigil", readFile(t, "../../policies/teams/payments/production.sigil"))
	st := store.New(deploy.Kind, store.WithTeams("payments"), store.WithTeamsDir(dir))
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	srv, err := server.New(server.WithStore(st))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := srv.Handler()

	writeFile(t, dir, "payments/production.sigil", "policy payments.production: DeployApproval@1\n\nguardrails(\n")
	rec := do(h, http.MethodPost, "/api/v1/policies/reload", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reload status = %d, want 500; body %s", rec.Code, rec.Body)
	}
	var env server.ErrorEnvelope
	decode(t, rec, &env)
	text := errorText(env.Error)
	if !strings.Contains(text, "previous bundle keeps serving") || !strings.Contains(text, "payments/production.sigil:4:1") {
		t.Errorf("error %s, want the failure and the compile diagnostics with the file name", text)
	}
	if strings.Count(text, "payments/production.sigil:4:1") != 1 {
		t.Errorf("the diagnostics appear more than once in %s", text)
	}

	if rec := do(h, http.MethodPost, "/api/v1/teams/payments/deployments", request(nil)); rec.Code != http.StatusAccepted {
		t.Errorf("evaluation after a failed reload = %d, want 202 from the old bundle", rec.Code)
	}
}

func TestMetrics(t *testing.T) {
	env := newServer(t)
	h := env.srv.Handler()
	do(h, http.MethodPost, "/api/v1/teams/payments/deployments", request(nil))
	do(h, http.MethodPost, "/api/v1/teams/checkout/deployments", request(func(r map[string]any) {
		r["actor"].(map[string]any)["name"] = ""
	}))

	rec := do(h, http.MethodGet, "/metrics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{
		`deploygate_decisions_total{decision="review",policy="payments.production",reason="service_owner",team="payments"} 1`,
		`deploygate_decisions_total{decision="deny",policy="checkout.production",reason="no_rule_matched",team="checkout"} 1`,
		`deploygate_evaluation_errors_total{kind="assertion",team="checkout"} 1`,
		`deploygate_evaluation_duration_seconds_count{team="payments"} 1`,
		`deploygate_policy_reloads_total{result="success"} 1`,
		`deploygate_policy_reloads_total{result="failure"} 0`,
		`deploygate_policy_loaded_info{policy="payments.production",source="embedded",team="payments"} 1`,
		`deploygate_policy_last_reload_timestamp_seconds`,
		`go_goroutines`,
		`process_cpu_seconds_total`,
		`deploygate_requests_total{code="202",method="POST",url="/api/v1/teams/:team/deployments"} 1`,
		`deploygate_requests_total{code="422",method="POST",url="/api/v1/teams/:team/deployments"} 1`,
		`deploygate_request_duration_seconds_count{code="202",method="POST",url="/api/v1/teams/:team/deployments"} 1`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("/metrics doesn't contain %s", want)
		}
	}
}

// TestRequestMetricLabels checks that no request label takes a value the
// client picks freely, so a client can't create series without bound.
func TestRequestMetricLabels(t *testing.T) {
	h := newServer(t).srv.Handler()
	do(h, "BREW", "/api/v1/teams/payments/deployments", "")
	do(h, http.MethodGet, "/wp-admin/setup.php", "")
	do(h, http.MethodGet, "/metrics", "")

	rec := do(h, http.MethodGet, "/metrics", "")
	body := rec.Body.String()
	for _, want := range []string{
		`deploygate_requests_total{code="404",method="other",url="unmatched"} 1`,
		`deploygate_requests_total{code="404",method="GET",url="unmatched"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics doesn't contain %s", want)
		}
	}
	for _, unwanted := range []string{"BREW", "wp-admin", `url="/metrics"`, "host="} {
		if strings.Contains(body, unwanted) {
			t.Errorf("/metrics contains %s", unwanted)
		}
	}
}

func TestEvaluateSpan(t *testing.T) {
	tests := []struct {
		name       string
		team       string
		body       string
		wantDec    string
		wantReason string
		wantError  bool
	}{
		{name: "review", team: "payments", body: request(nil), wantDec: "review", wantReason: "service_owner"},
		{name: "failed assert", team: "checkout", body: request(func(r map[string]any) {
			r["actor"].(map[string]any)["name"] = ""
		}), wantDec: "deny", wantReason: "no_rule_matched", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServer(t)
			do(env.srv.Handler(), http.MethodPost, "/api/v1/teams/"+tt.team+"/deployments", tt.body)

			span, ok := findSpan(env.spans.GetSpans(), "deploygate.evaluate")
			if !ok {
				t.Fatalf("no deploygate.evaluate span among %d spans", len(env.spans.GetSpans()))
			}
			attrs := map[attribute.Key]attribute.Value{}
			for _, kv := range span.Attributes {
				attrs[kv.Key] = kv.Value
			}
			for key, want := range map[attribute.Key]string{
				"sigil.kind":     "DeployApproval",
				"sigil.policy":   tt.team + ".production",
				"sigil.team":     tt.team,
				"sigil.decision": tt.wantDec,
				"sigil.reason":   tt.wantReason,
			} {
				if got := attrs[key].AsString(); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			if got := attrs["sigil.candidates"].AsInt64(); got != int64(countEvents(span, "sigil.candidate")) {
				t.Errorf("sigil.candidates = %d, but %d candidate events", got, countEvents(span, "sigil.candidate"))
			}
			if (span.Status.Code.String() == "Error") != tt.wantError {
				t.Errorf("span status = %v, want error %v", span.Status, tt.wantError)
			}
			if span.Parent.SpanID() != findParent(t, env.spans.GetSpans(), span).SpanContext.SpanID() {
				t.Error("the evaluate span isn't a child of the HTTP server span")
			}
		})
	}
}

func TestServeShutsDownGracefully(t *testing.T) {
	env := newServer(t)
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- env.srv.ServeListener(ctx, ln) }()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+ln.Addr().String()+"/healthz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ServeListener returned %v after a clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeListener didn't return after its context ended")
	}
}

func TestNewRequiresStore(t *testing.T) {
	if _, err := server.New(); err == nil {
		t.Fatal("New without a store succeeded")
	}
}

// testEnv is a server over the embedded policies with its spans recorded.
type testEnv struct {
	srv   *server.Server
	spans *tracetest.InMemoryExporter
}

func newServer(t *testing.T) testEnv {
	t.Helper()
	return newServerWith(t, true)
}

func newServerWith(t *testing.T, loaded bool) testEnv {
	t.Helper()
	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	m := telemetry.NewMetrics()
	st := store.New(deploy.Kind, store.WithTeams("payments", "checkout"), store.WithMetrics(m))
	if loaded {
		if err := st.Load(context.Background()); err != nil {
			t.Fatalf("Load: %v", err)
		}
	}
	srv, err := server.New(server.WithStore(st), server.WithMetrics(m), server.WithTracerProvider(tp))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return testEnv{srv: srv, spans: spans}
}

// request renders the documented example request, the payments owner of a
// PCI service, after edit changes it.
func request(edit func(map[string]any)) string {
	r := map[string]any{
		"release": map[string]any{"soak": "6h", "hotfix": false},
		"service": map[string]any{
			"name": "ledger", "tier": "standard", "owners": []string{"payments"},
			"labels": map[string]string{
				"app.kubernetes.io/managed-by":   "argocd",
				"platform.example.com/lifecycle": "ga",
				"regions":                        "eu,us",
				"compliance":                     "pci",
			},
		},
		"actor":       map[string]any{"name": "ada", "teams": []string{"payments"}, "roles": []string{"deployer"}, "regions": []string{"eu", "us"}},
		"environment": "production",
	}
	if edit != nil {
		edit(r)
	}
	out, _ := json.Marshal(r)
	return string(out)
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body, err)
	}
}

func assertJSON(t *testing.T, v any, want string) {
	t.Helper()
	got, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func findSpan(spans tracetest.SpanStubs, name string) (tracetest.SpanStub, bool) {
	for _, s := range spans {
		if s.Name == name {
			return s, true
		}
	}
	return tracetest.SpanStub{}, false
}

func findParent(t *testing.T, spans tracetest.SpanStubs, child tracetest.SpanStub) tracetest.SpanStub {
	t.Helper()
	for _, s := range spans {
		if s.SpanContext.SpanID() == child.Parent.SpanID() {
			return s
		}
	}
	t.Fatalf("span %s has no recorded parent", child.Name)
	return tracetest.SpanStub{}
}

func countEvents(span tracetest.SpanStub, name string) int {
	n := 0
	for _, e := range span.Events {
		if e.Name == name {
			n++
		}
	}
	return n
}

// errorText joins the messages of an error and its causes.
func errorText(e *server.ErrorResponse) string {
	var parts []string
	for ; e != nil; e = e.Cause {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, ": ")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
