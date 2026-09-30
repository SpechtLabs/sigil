package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	humane "github.com/sierrasoftworks/humane-errors-go"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/dispatch"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/server"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/teams"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// clockStart is when every env's store clock starts. A fixed time makes
// loaded_at and the last-reload gauge exact instead of "roughly now".
var clockStart = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// The span names the service promises. Tempo searches and the Grafana trace
// panels depend on them.
const (
	spanRoute = "alertrouter.route"
	spanLoad  = "alertrouter.policy.load"
	eventCand = "sigil.candidate"

	// triggerKey is the load span's attribute that says what started it.
	triggerKey = "alertrouter.reload.trigger"
)

// env is one alertrouter wired up the way cmd/alertrouter wires it, served
// by httptest instead of a real listener, with everything it reports
// captured: spans in memory, metrics on a registry of its own, every
// notification the dispatcher got, and store time from a clock the spec
// controls.
type env struct {
	client    *fixture.Client
	store     *store.Store[routing.Input]
	metrics   *telemetry.Metrics
	spans     *tracetest.InMemoryExporter
	notifier  *recorder
	clock     *fakeClock
	directory *teams.Directory
	// dir is the team policies directory the store reads.
	dir string
}

type envConfig struct {
	dir               string
	serverClock       store.Clock
	copyTeams         bool
	skipLoad          bool
	logNotifier       bool
	evaluationTimeout time.Duration
}

type envOption func(*envConfig)

// withCopiedTeams serves a private copy of policies/teams, so a spec can
// edit and break the policies without touching the checkout or any other
// spec's env.
func withCopiedTeams() envOption {
	return func(c *envConfig) { c.copyTeams = true }
}

// withEvaluationTimeout bounds each evaluation by d instead of the server's
// default, so a spec can run a policy out of time without waiting a second.
func withEvaluationTimeout(d time.Duration) envOption {
	return func(c *envConfig) { c.evaluationTimeout = d }
}

// withLogNotifier dispatches through the service's own LogNotifier, which
// logs each notification, instead of the recorder, so a spec can read the
// "notification dispatched" lines the running service writes. The recorder
// stays empty.
func withLogNotifier() envOption {
	return func(c *envConfig) { c.logNotifier = true }
}

// withServerClock gives the server a clock of its own, the one it reads a
// webhook alert's firing time against. Without it the server reads the wall
// clock, which is what the webhook builders' startsAt is relative to.
func withServerClock(clock store.Clock) envOption {
	return func(c *envConfig) { c.serverClock = clock }
}

// unloaded builds the env without loading the bundle, the state a pod is in
// before its first load succeeds.
func unloaded() envOption {
	return func(c *envConfig) { c.skipLoad = true }
}

// newEnv builds an env and registers its teardown with Ginkgo, so it lives
// as long as the node that built it: the suite for BeforeSuite, one spec for
// BeforeEach or It.
func newEnv(opts ...envOption) *env {
	GinkgoHelper()

	cfg := envConfig{dir: filepath.Join(fixture.ExamplesDir, "policies", "teams")}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.copyTeams {
		cfg.dir = copyDir(cfg.dir)
	}

	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	DeferCleanup(func() { Expect(tp.Shutdown(context.Background())).To(Succeed()) })

	e := &env{
		metrics:   telemetry.NewMetrics(),
		spans:     spans,
		notifier:  &recorder{},
		clock:     newFakeClock(clockStart),
		directory: teams.Default(),
		dir:       cfg.dir,
	}

	e.store = store.NewRouting(
		store.WithTeams(e.directory.Names()...),
		store.WithBundleDir(cfg.dir),
		store.WithMetrics(e.metrics),
		store.WithTracer(tp.Tracer(telemetry.TracerName)),
		store.WithClock(e.clock),
	)
	if !cfg.skipLoad {
		// InitialLoad, not Load, because that is what cmd/alertrouter calls
		// before it serves.
		Expect(e.store.InitialLoad(context.Background())).To(Succeed())
	}

	srvOpts := []server.Option{
		server.WithStore(e.store),
		server.WithDirectory(e.directory),
		server.WithMetrics(e.metrics),
		server.WithTracerProvider(tp),
		server.WithEvaluationTimeout(cfg.evaluationTimeout),
	}
	if cfg.logNotifier {
		srvOpts = append(srvOpts, server.WithNotifier(dispatch.NewLogNotifier()))
	} else {
		srvOpts = append(srvOpts, server.WithNotifier(e.notifier))
	}
	if cfg.serverClock != nil {
		srvOpts = append(srvOpts, server.WithClock(cfg.serverClock))
	}
	srv, herr := server.New(srvOpts...)
	Expect(herr).To(Succeed())

	ts := httptest.NewServer(srv.Handler())
	DeferCleanup(ts.Close)
	e.client = fixture.NewClient(ts.URL)

	return e
}

// families gathers the env's registry, which holds everything its /metrics
// serves.
func (e *env) families() fixture.Families {
	GinkgoHelper()

	mfs, err := e.metrics.Registry().Gather()
	Expect(err).NotTo(HaveOccurred())

	return fixture.Gathered(mfs)
}

// spansNamed returns the finished spans called name, oldest first.
func (e *env) spansNamed(name string) tracetest.SpanStubs {
	var out tracetest.SpanStubs
	for _, s := range e.spans.GetSpans() {
		if s.Name == name {
			out = append(out, s)
		}
	}

	return out
}

// waitForSpan waits until exactly one span called name has finished and
// returns it. The handler writes the response before its deferred span.End
// runs, and the HTTP middleware ends its span after that, so a client can
// hold the answer a moment before the exporter holds the spans.
func (e *env) waitForSpan(name string) tracetest.SpanStub {
	GinkgoHelper()

	return e.waitFor(func(s tracetest.SpanStub) bool { return s.Name == name })
}

// serverSpan waits until exactly one HTTP server span has finished and
// returns it.
func (e *env) serverSpan() tracetest.SpanStub {
	GinkgoHelper()

	return e.waitFor(func(s tracetest.SpanStub) bool { return s.SpanKind == trace.SpanKindServer })
}

func (e *env) waitFor(match func(tracetest.SpanStub) bool) tracetest.SpanStub {
	GinkgoHelper()

	var spans tracetest.SpanStubs
	Eventually(func() tracetest.SpanStubs {
		spans = nil
		for _, s := range e.spans.GetSpans() {
			if match(s) {
				spans = append(spans, s)
			}
		}
		return spans
	}).WithTimeout(2 * time.Second).WithPolling(5 * time.Millisecond).Should(HaveLen(1))

	return spans[0]
}

// served returns the AlertRouting bundle the env's listing reports.
func (e *env) served() fixture.KindPolicies {
	GinkgoHelper()

	return e.client.Served(Default)
}

// writeTeamFile replaces a file in the env's team directory, creating it
// when it doesn't exist.
func (e *env) writeTeamFile(rel, content string) {
	GinkgoHelper()

	path := filepath.Join(e.dir, rel)
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}

// editCheckout replaces the one occurrence of from in the env's copy of
// checkout's policy with to.
func (e *env) editCheckout(from, to string) {
	GinkgoHelper()

	path := filepath.Join(e.dir, checkoutPolicy)
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	Expect(strings.Count(string(data), from)).To(Equal(1),
		"%s no longer holds %q once; update the spec with the policy", path, from)

	Expect(os.WriteFile(path, []byte(strings.Replace(string(data), from, to, 1)), 0o644)).To(Succeed())
}

// reloadOK reloads the env's bundle and expects it to load.
func (e *env) reloadOK() {
	GinkgoHelper()

	resp, _ := e.client.Reload(Default)
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
}

// copyDir copies a policies directory into a temporary one the spec owns.
func copyDir(src string) string {
	GinkgoHelper()

	dir := GinkgoT().TempDir()
	Expect(os.CopyFS(dir, os.DirFS(src))).To(Succeed())

	return dir
}

// recorder is a dispatch.Notifier that keeps every notification, so a spec
// can prove that each firing alert ended in exactly one.
type recorder struct {
	got []dispatch.Notification
	mu  sync.Mutex
}

// Notify records n.
func (r *recorder) Notify(_ context.Context, n dispatch.Notification) humane.Error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.got = append(r.got, n)
	return nil
}

// notifications returns what was recorded so far, oldest first.
func (r *recorder) notifications() []dispatch.Notification {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]dispatch.Notification(nil), r.got...)
}

// fakeClock is a store.Clock whose time moves one second per reading, so
// every load gets a later loaded_at than the one before, and whose ticks the
// spec sends by hand, so polling needs no waiting.
type fakeClock struct {
	now   time.Time
	ticks chan time.Time
	mu    sync.Mutex
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start, ticks: make(chan time.Time)}
}

// Now returns the current fake time and advances it.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now
	c.now = c.now.Add(time.Second)

	return now
}

// Tick hands out the one channel Tick sends on, whatever the interval.
func (c *fakeClock) Tick(time.Duration) (<-chan time.Time, func()) {
	return c.ticks, func() {}
}

// tick fires one poll. It blocks until the watch loop receives it, so when
// it returns the loop has started the poll.
func (c *fakeClock) tick() {
	c.ticks <- clockStart
}
