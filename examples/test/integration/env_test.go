package integration

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/server"
	"github.com/spechtlabs/sigil/examples/internal/store"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

// clockStart is when every env's clock starts. A fixed time makes loaded_at
// and the last-reload gauge exact instead of "roughly now".
var clockStart = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// env is one deploygate wired up the way cmd/deploygate wires it, served by
// httptest instead of a real listener, with everything it reports captured:
// spans in memory, metrics on a registry of its own, and time from a clock
// the spec controls.
type env struct {
	client  *fixture.Client
	store   *store.Store
	metrics *telemetry.Metrics
	spans   *tracetest.InMemoryExporter
	clock   *fakeClock
	// dir is the team policies directory the store reads.
	dir string
}

type envConfig struct {
	dir      string
	copyDir  bool
	skipLoad bool
}

type envOption func(*envConfig)

// withCopiedTeams serves a private copy of policies/teams, so a spec can
// edit and break the policies without touching the checkout or any other
// spec's env.
func withCopiedTeams() envOption {
	return func(c *envConfig) { c.copyDir = true }
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

	if cfg.copyDir {
		dir := GinkgoT().TempDir()
		Expect(os.CopyFS(dir, os.DirFS(cfg.dir))).To(Succeed())
		cfg.dir = dir
	}

	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	DeferCleanup(func() { Expect(tp.Shutdown(context.Background())).To(Succeed()) })

	e := &env{
		metrics: telemetry.NewMetrics(),
		spans:   spans,
		clock:   newFakeClock(clockStart),
		dir:     cfg.dir,
	}

	e.store = store.New(deploy.Kind,
		store.WithTeams(fixture.TeamPayments, fixture.TeamCheckout),
		store.WithTeamsDir(cfg.dir),
		store.WithMetrics(e.metrics),
		store.WithTracer(tp.Tracer(telemetry.TracerName)),
		store.WithClock(e.clock),
	)
	if !cfg.skipLoad {
		// InitialLoad, not Load, because that is what cmd/deploygate calls
		// before it serves.
		Expect(e.store.InitialLoad(context.Background())).To(Succeed())
	}

	srv, herr := server.New(
		server.WithStore(e.store),
		server.WithMetrics(e.metrics),
		server.WithTracerProvider(tp),
	)
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

// writeTeamFile replaces a file in the env's team directory.
func (e *env) writeTeamFile(rel, content string) {
	GinkgoHelper()

	Expect(os.WriteFile(filepath.Join(e.dir, rel), []byte(content), 0o644)).To(Succeed())
}

func (e *env) readTeamFile(rel string) string {
	GinkgoHelper()

	data, err := os.ReadFile(filepath.Join(e.dir, rel))
	Expect(err).NotTo(HaveOccurred())

	return string(data)
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
