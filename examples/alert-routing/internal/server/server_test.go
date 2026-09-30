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
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spechtlabs/go-otel-utils/otelzap"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/dispatch"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/server"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/teams"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
	"github.com/spechtlabs/sigil/examples/alert-routing/policies"
)

// now is the time the test server's clock reads, which a webhook alert's
// firing time is measured against.
var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

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
		{name: "policies", method: http.MethodGet, path: "/api/v1/policies", env: loaded, wantStatus: http.StatusOK, wantBody: []string{
			`{"kinds":[{"kind":"AlertRouting","version":1,`, `"source":"embedded"`, `"fingerprint":"`,
			`{"team":"checkout","policy":"checkout.alerts"}`, `{"team":"payments","policy":"payments.alerts"}`,
		}},
		{name: "policies before a load", method: http.MethodGet, path: "/api/v1/policies", wantStatus: http.StatusServiceUnavailable, wantBody: []string{"no policy bundle is loaded yet"}},
		{name: "teams", method: http.MethodGet, path: "/api/v1/teams", env: loaded, wantStatus: http.StatusOK, wantBody: []string{
			`{"teams":[{"name":"checkout","oncall":"checkout-primary","channel":"#checkout-alerts"},{"name":"payments","oncall":"payments-primary","channel":"#payments-alerts"}]}`,
		}},
		{name: "teams before a load", method: http.MethodGet, path: "/api/v1/teams", wantStatus: http.StatusOK, wantBody: []string{`"name":"checkout"`}},
		{name: "route before a load", method: http.MethodPost, path: "/api/v1/teams/checkout/route", wantStatus: http.StatusServiceUnavailable, wantBody: []string{"no policy bundle is loaded yet"}},
		{name: "webhook before a load", method: http.MethodPost, path: "/api/v1/alerts", wantStatus: http.StatusServiceUnavailable, wantBody: []string{"wait until GET /readyz reports ready"}},
		{name: "reload", method: http.MethodPost, path: "/api/v1/policies/reload", env: loaded, wantStatus: http.StatusOK, wantBody: []string{`"kinds":[`, `"AlertRouting"`}},
		{name: "reload loads a store that never loaded", method: http.MethodPost, path: "/api/v1/policies/reload", wantStatus: http.StatusOK, wantBody: []string{`"checkout.alerts"`}},
		{name: "unknown route", method: http.MethodGet, path: "/api/v2/nothing", env: loaded, wantStatus: http.StatusNotFound, wantBody: []string{"no route for GET /api/v2/nothing"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, tt.env)
			rec := do(env.srv.Handler(), tt.method, tt.path, "")
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

// TestReloadFailureKeepsServing breaks the mounted bundle and checks that the
// reload endpoint reports the compile diagnostics while the old bundle keeps
// routing, and that fixing it moves the fingerprint.
func TestReloadFailureKeepsServing(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		wantText []string
	}{
		{
			name:     "a syntax error",
			file:     "policy checkout.alerts: AlertRouting@1\n\npaging(\n",
			wantText: []string{"AlertRouting policies", "previous bundle keeps serving", "checkout/alerts.sigil:4:1"},
		},
		{
			name:     "a team that drops the platform's paging",
			file:     "policy checkout.alerts: AlertRouting@1\n\nuse platform.routing\n\nrouting()\n",
			wantText: []string{"checkout.alerts failed to compile", "platform.paging"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, envOptions{loaded: true, dirs: true})
			h := env.srv.Handler()
			var before server.PoliciesResponse
			decode(t, do(h, http.MethodGet, "/api/v1/policies", ""), &before)

			writeFile(t, env.teamsDir, "checkout/alerts.sigil", tt.file)
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

			// The old bundle still pages for a critical production alert.
			var resp server.RouteResponse
			decode(t, do(h, http.MethodPost, "/api/v1/teams/checkout/route", routeRequest("critical", "production", "1m")), &resp)
			if resp.Decision != "page" || resp.Target != "checkout-primary" {
				t.Errorf("route after a failed reload = %+v, want the old bundle's page", resp)
			}
			var after server.PoliciesResponse
			decode(t, do(h, http.MethodGet, "/api/v1/policies", ""), &after)
			if after.Kinds[0].Fingerprint != before.Kinds[0].Fingerprint {
				t.Error("a failed reload changed the serving fingerprint")
			}

			writeFile(t, env.teamsDir, "checkout/alerts.sigil", "// fixed\n"+readEmbedded(t, "checkout/alerts.sigil"))
			if rec := do(h, http.MethodPost, "/api/v1/policies/reload", ""); rec.Code != http.StatusOK {
				t.Fatalf("reload after the fix = %d; body %s", rec.Code, rec.Body)
			}
			decode(t, do(h, http.MethodGet, "/api/v1/policies", ""), &after)
			if after.Kinds[0].Fingerprint == before.Kinds[0].Fingerprint {
				t.Error("a successful reload of changed content kept the fingerprint")
			}
		})
	}
}

// TestRequestMetricLabels checks that no request label takes a value the
// client picks freely, so a client can't create series without bound.
func TestRequestMetricLabels(t *testing.T) {
	h := newEnv(t, loaded).srv.Handler()
	do(h, "BREW", "/api/v1/teams/checkout/route", "")
	do(h, http.MethodGet, "/wp-admin/setup.php", "")
	do(h, http.MethodPost, "/api/v1/teams/billing/route", routeRequest("critical", "production", "1m"))
	do(h, http.MethodGet, "/metrics", "")

	body := do(h, http.MethodGet, "/metrics", "").Body.String()
	for _, want := range []string{
		`alertrouter_requests_total{code="404",method="other",route="unmatched"} 1`,
		`alertrouter_requests_total{code="404",method="GET",route="unmatched"} 1`,
		`alertrouter_requests_total{code="404",method="POST",route="/api/v1/teams/:team/route"} 1`,
		`alertrouter_request_duration_seconds_count{method="POST",route="/api/v1/teams/:team/route"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics doesn't contain %s", want)
		}
	}
	for _, unwanted := range []string{"BREW", "wp-admin", "billing", `route="/metrics"`, "host="} {
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

func TestServeRefusesABusyAddress(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	srv, herr := server.New(server.WithStore(store.NewRouting()), server.WithDirectory(teams.Default()), server.WithAddr(ln.Addr().String()))
	if herr != nil {
		t.Fatal(herr)
	}
	if err := srv.Serve(context.Background()); err == nil || !strings.Contains(err.Error(), "can't listen on") {
		t.Errorf("Serve on a busy address = %v, want it refused", err)
	}
}

func TestNewRequires(t *testing.T) {
	tests := []struct {
		name string
		opts []server.Option
		want string
	}{
		{name: "nothing", want: "no store for the team policies"},
		{name: "no directory", opts: []server.Option{server.WithStore(store.NewRouting())}, want: "no team directory"},
		{name: "no store", opts: []server.Option{server.WithDirectory(teams.Default())}, want: "no store for the team policies"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.New(tt.opts...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New error = %v, want one about %s", err, tt.want)
			}
		})
	}
}

// envOptions picks what a test server starts with.
type envOptions struct {
	loaded bool
	// dirs copies the embedded team bundle into a temporary directory the
	// test can edit.
	dirs bool
	// teamOverrides replace files in the copied team bundle; they imply dirs.
	teamOverrides map[string]string
	// evaluationTimeout replaces the server's default, which is too long
	// for a test to wait out.
	evaluationTimeout time.Duration
	// notifyErr is what the recording notifier returns.
	notifyErr humane.Error
}

// loaded is a server with the embedded bundle loaded.
var loaded = envOptions{loaded: true}

// testEnv is a server with its spans, metrics and notifications recorded.
type testEnv struct {
	srv      *server.Server
	spans    *tracetest.InMemoryExporter
	metrics  *telemetry.Metrics
	notes    *notifications
	teamsDir string
}

// notifications records what the server dispatched.
type notifications struct {
	mu  sync.Mutex
	all []dispatch.Notification
}

func (n *notifications) record(_ context.Context, note dispatch.Notification) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.all = append(n.all, note)
}

func (n *notifications) list() []dispatch.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]dispatch.Notification(nil), n.all...)
}

// fixedClock is a store.Clock that always reads now and never ticks.
type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

func (fixedClock) Tick(time.Duration) (<-chan time.Time, func()) { return nil, func() {} }

func newEnv(t *testing.T, o envOptions) testEnv {
	t.Helper()
	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	dir := teams.Default()
	env := testEnv{spans: spans, metrics: telemetry.NewMetrics(), notes: &notifications{}}
	opts := []store.Option{store.WithTeams(dir.Names()...), store.WithMetrics(env.metrics)}
	if o.dirs || o.teamOverrides != nil {
		env.teamsDir = copyTree(t, policies.Teams, o.teamOverrides)
		opts = append(opts, store.WithBundleDir(env.teamsDir))
	}
	st := store.NewRouting(opts...)
	if o.loaded {
		if err := st.Load(context.Background()); err != nil {
			t.Fatalf("loading the team policies: %s", errorText(server.NewErrorResponse(err)))
		}
	}

	srv, err := server.New(
		server.WithStore(st),
		server.WithDirectory(dir),
		server.WithNotifier(dispatch.NotifierFunc(func(ctx context.Context, n dispatch.Notification) humane.Error {
			env.notes.record(ctx, n)
			return o.notifyErr
		})),
		server.WithMetrics(env.metrics),
		server.WithTracerProvider(tp),
		server.WithClock(fixedClock{}),
		server.WithEvaluationTimeout(o.evaluationTimeout),
		server.WithShutdownTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	env.srv = srv
	return env
}

// routeRequest renders a route request for an alert of severity in env that
// has fired for firingFor.
func routeRequest(severity, env, firingFor string) string {
	return routeRequestWith("CheckoutLatencyHigh", severity, firingFor, map[string]string{"env": env})
}

// routeRequestWith renders a route request with every field given.
func routeRequestWith(name, severity, firingFor string, labels map[string]string) string {
	out, _ := json.Marshal(map[string]any{"alert": map[string]any{
		"name": name, "severity": severity, "labels": labels, "firing_for": firingFor,
	}})
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

func findSpans(spans tracetest.SpanStubs, name string) []tracetest.SpanStub {
	var out []tracetest.SpanStub
	for _, s := range spans {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
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

func attributes(kvs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	out := map[attribute.Key]attribute.Value{}
	for _, kv := range kvs {
		out[kv.Key] = kv.Value
	}
	return out
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

func readEmbedded(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(policies.Teams, name)
	if err != nil {
		t.Fatalf("reading embedded %s: %v", name, err)
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

// observeLogs installs a logger that records every line as the process
// logger, and puts the previous one back when the test ends.
func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	t.Cleanup(otelzap.ReplaceGlobals(otelzap.New(zap.New(core), otelzap.WithMinLevel(zapcore.DebugLevel))))
	return logs
}
