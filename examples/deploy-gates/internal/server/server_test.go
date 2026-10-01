package server_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/freeze"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/server"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/store"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/telemetry"
	"github.com/spechtlabs/sigil/examples/deploy-gates/policies"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		env        envOptions
		wantStatus int
		wantBody   []string
	}{
		{name: "healthz", method: http.MethodGet, path: "/healthz", env: loaded, wantStatus: http.StatusOK, wantBody: []string{`"status":"ok"`}},
		{name: "healthz before a load", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK, wantBody: []string{`"status":"ok"`}},
		{name: "readyz", method: http.MethodGet, path: "/readyz", env: loaded, wantStatus: http.StatusOK, wantBody: []string{`"status":"ready"`, `"loaded_at"`}},
		{name: "readyz before a load", method: http.MethodGet, path: "/readyz", wantStatus: http.StatusServiceUnavailable, wantBody: []string{`"status":"not ready"`}},
		{name: "readyz with only the team policies", method: http.MethodGet, path: "/readyz", env: envOptions{deployLoaded: true}, wantStatus: http.StatusServiceUnavailable},
		{name: "readyz with only the access policies", method: http.MethodGet, path: "/readyz", env: envOptions{accessLoaded: true}, wantStatus: http.StatusServiceUnavailable},
		{name: "policies lists both kinds", method: http.MethodGet, path: "/api/v1/policies", env: loaded, wantStatus: http.StatusOK, wantBody: []string{
			`{"kind":"DeployApproval","version":2,`, `"source":"embedded"`,
			`{"team":"payments","policy":"payments.production"}`, `{"team":"checkout","policy":"checkout.production"}`,
			`{"kind":"AccessGrant","version":1,`, `"policies":[{"policy":"access.main"}]`,
		}},
		{name: "policies before a load", method: http.MethodGet, path: "/api/v1/policies", wantStatus: http.StatusServiceUnavailable, wantBody: []string{"no policy bundle is loaded yet"}},
		{name: "policies with one kind loaded", method: http.MethodGet, path: "/api/v1/policies", env: envOptions{deployLoaded: true}, wantStatus: http.StatusServiceUnavailable},
		{name: "evaluate before a load", method: http.MethodPost, path: "/api/v1/teams/payments/deployments", wantStatus: http.StatusServiceUnavailable, wantBody: []string{"no policy bundle is loaded yet"}},
		{name: "evaluate without the access policies", method: http.MethodPost, path: "/api/v1/teams/payments/deployments", env: envOptions{deployLoaded: true}, wantStatus: http.StatusServiceUnavailable},
		{name: "grants before a load", method: http.MethodPost, path: "/api/v1/access/grants", wantStatus: http.StatusServiceUnavailable},
		{name: "reload", method: http.MethodPost, path: "/api/v1/policies/reload", env: loaded, wantStatus: http.StatusOK, wantBody: []string{`"kinds":[`, `"AccessGrant"`}},
		{name: "reload loads stores that never loaded", method: http.MethodPost, path: "/api/v1/policies/reload", wantStatus: http.StatusOK, wantBody: []string{`"DeployApproval"`, `"AccessGrant"`}},
		{name: "unknown route", method: http.MethodGet, path: "/api/v2/nothing", env: loaded, wantStatus: http.StatusNotFound, wantBody: []string{"no route for GET /api/v2/nothing"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, tt.env)
			rec := do(env.srv.Handler(), tt.method, tt.path, deployRequest(nil))
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

// TestReloadFailureKeepsServing breaks each bundle in turn and checks that
// the reload endpoint reports the compile diagnostics while both old bundles
// keep answering.
func TestReloadFailureKeepsServing(t *testing.T) {
	tests := []struct {
		name     string
		breakDir func(t *testing.T, env testEnv)
		wantText []string
	}{
		{
			name: "team policies",
			breakDir: func(t *testing.T, env testEnv) {
				writeFile(t, env.teamsDir, "payments/production.sigil", "policy payments.production: DeployApproval@1\n\nguardrails(\n")
			},
			wantText: []string{"DeployApproval policies", "previous bundle keeps serving", "payments/production.sigil:4:1"},
		},
		{
			name: "access policies",
			breakDir: func(t *testing.T, env testEnv) {
				writeFile(t, env.accessDir, "main.sigil", "policy access.main: AccessGrant@1\n\nguardrails(\n")
			},
			wantText: []string{"AccessGrant policies", "previous bundle keeps serving", "main.sigil:4:1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, envOptions{deployLoaded: true, accessLoaded: true, dirs: true})
			h := env.srv.Handler()
			before := do(h, http.MethodGet, "/api/v1/policies", "").Body.String()

			tt.breakDir(t, env)
			rec := do(h, http.MethodPost, "/api/v1/policies/reload", "")
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("reload status = %d, want 500; body %s", rec.Code, rec.Body)
			}
			var body server.ErrorEnvelope
			decode(t, rec, &body)
			text := errorText(body.Error)
			for _, want := range tt.wantText {
				if !strings.Contains(text, want) {
					t.Errorf("error %s doesn't contain %q", text, want)
				}
			}

			if rec := do(h, http.MethodPost, "/api/v1/teams/payments/deployments", deployRequest(nil)); rec.Code != http.StatusAccepted {
				t.Errorf("evaluation after a failed reload = %d, want 202 from the old bundles; body %s", rec.Code, rec.Body)
			}
			if tt.name == "team policies" {
				return
			}
			// The team policies reloaded fine, so only their loaded_at moved.
			after := do(h, http.MethodGet, "/api/v1/policies", "").Body.String()
			if after == before {
				t.Error("the team policies didn't reload although only the access bundle was broken")
			}
		})
	}
}

// TestRequestMetricLabels checks that no request label takes a value the
// client picks freely, so a client can't create series without bound.
func TestRequestMetricLabels(t *testing.T) {
	h := newEnv(t, loaded).srv.Handler()
	do(h, "BREW", "/api/v1/teams/payments/deployments", "")
	do(h, http.MethodGet, "/wp-admin/setup.php", "")
	do(h, http.MethodGet, "/metrics", "")

	body := do(h, http.MethodGet, "/metrics", "").Body.String()
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

func TestServeShutsDownGracefully(t *testing.T) {
	env := newEnv(t, loaded)
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

func TestNewRequiresStores(t *testing.T) {
	tests := []struct {
		name string
		opts []server.Option
		want string
	}{
		{name: "no store", want: "team policies"},
		{name: "no access store", opts: []server.Option{server.WithStore(store.NewDeploy())}, want: "access policies"},
		{name: "no team store", opts: []server.Option{server.WithAccessStore(store.NewAccess())}, want: "team policies"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.New(tt.opts...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New error = %v, want one about the %s", err, tt.want)
			}
		})
	}
}

// envOptions picks what a test server starts with.
type envOptions struct {
	deployLoaded bool
	accessLoaded bool
	// dirs copies the embedded bundles into temporary directories the test
	// can edit.
	dirs bool
	// teamOverrides replace files in the copied team bundle; they imply dirs.
	teamOverrides map[string]string
	// evaluationTimeout replaces the server's default, which is too long
	// for a test to wait out.
	evaluationTimeout time.Duration
	// onSpanStart is called with the name of every span that starts, so a
	// test can act at a known point inside a request.
	onSpanStart spanStartHook
	// freeze is the server's freeze source; nil leaves the default, nothing
	// frozen.
	freeze freeze.Source
}

// spanStartHook is a span processor that only reports span starts.
type spanStartHook func(name string)

func (h spanStartHook) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) { h(s.Name()) }
func (spanStartHook) OnEnd(sdktrace.ReadOnlySpan)                           {}
func (spanStartHook) Shutdown(context.Context) error                        { return nil }
func (spanStartHook) ForceFlush(context.Context) error                      { return nil }

// loaded is a server with both embedded bundles loaded.
var loaded = envOptions{deployLoaded: true, accessLoaded: true}

// testEnv is a server with its spans and metrics recorded.
type testEnv struct {
	srv       *server.Server
	spans     *tracetest.InMemoryExporter
	metrics   *telemetry.Metrics
	teamsDir  string
	accessDir string
}

func newEnv(t *testing.T, o envOptions) testEnv {
	t.Helper()
	spans := tracetest.NewInMemoryExporter()
	tpOpts := []sdktrace.TracerProviderOption{sdktrace.WithSyncer(spans)}
	if o.onSpanStart != nil {
		tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(o.onSpanStart))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	env := testEnv{spans: spans, metrics: telemetry.NewMetrics()}
	deployOpts := []store.Option{store.WithTeams("payments", "checkout"), store.WithMetrics(env.metrics)}
	accessOpts := []store.Option{store.WithMetrics(env.metrics)}
	if o.dirs || o.teamOverrides != nil {
		env.teamsDir = copyTree(t, policies.Teams, o.teamOverrides)
		env.accessDir = copyTree(t, policies.Access, nil)
		deployOpts = append(deployOpts, store.WithBundleDir(env.teamsDir))
		accessOpts = append(accessOpts, store.WithBundleDir(env.accessDir))
	}

	deploySt, accessSt := store.NewDeploy(deployOpts...), store.NewAccess(accessOpts...)
	if o.deployLoaded {
		if err := deploySt.Load(context.Background()); err != nil {
			t.Fatalf("loading the team policies: %v", err)
		}
	}
	if o.accessLoaded {
		if err := accessSt.Load(context.Background()); err != nil {
			t.Fatalf("loading the access policies: %v", err)
		}
	}

	srv, err := server.New(
		server.WithStore(deploySt),
		server.WithAccessStore(accessSt),
		server.WithMetrics(env.metrics),
		server.WithTracerProvider(tp),
		server.WithEvaluationTimeout(o.evaluationTimeout),
		server.WithFreeze(o.freeze),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env.srv = srv
	return env
}

// deployRequest renders the documented example request, a payments owner
// of a PCI service, after edit changes it.
func deployRequest(edit func(r, actor map[string]any)) string {
	actor := map[string]any{"name": "ada", "groups": []string{"payments"}, "clearance": "", "regions": []string{"eu", "us"}}
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
		"actor":       actor,
		"environment": "production",
	}
	if edit != nil {
		edit(r, actor)
	}
	out, _ := json.Marshal(r)
	return string(out)
}

// accessRequest renders a grants request for an actor in groups.
func accessRequest(name, clearance, team, environment string, groups ...string) string {
	out, _ := json.Marshal(map[string]any{
		"actor":       map[string]any{"name": name, "groups": groups, "clearance": clearance},
		"team":        team,
		"environment": environment,
	})
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

// copyTree copies the `.sigil` files of fsys into a temporary directory and
// applies overrides, so a test can edit a bundle while the store reads it.
func copyTree(t *testing.T, fsys fs.FS, overrides map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(name) != ".sigil" {
			return err
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		writeFile(t, dir, name, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("copying a bundle: %v", err)
	}
	for name, content := range overrides {
		writeFile(t, dir, name, content)
	}
	return dir
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
