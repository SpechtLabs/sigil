package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spechtlabs/go-otel-utils/otelzap"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"github.com/spechtlabs/sigil/examples/internal/access"
	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/store"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
)

// The routes. Health and metrics sit at the root, where probes and scrapers
// expect them, and outside the versioned API.
const (
	RouteDeployments = "/api/v1/teams/:team/deployments"
	RouteGrants      = "/api/v1/access/grants"
	RoutePolicies    = "/api/v1/policies"
	RouteReload      = "/api/v1/policies/reload"
	RouteHealthz     = "/healthz"
	RouteReadyz      = "/readyz"
	RouteMetrics     = "/metrics"
)

// Defaults for the options.
const (
	DefaultAddr            = ":8080"
	DefaultShutdownTimeout = 15 * time.Second
)

// maxBodyBytes caps a request body. A deploy request is a few hundred bytes;
// the cap keeps a runaway client from making the server buffer megabytes.
const maxBodyBytes = 1 << 20

// standardMethods are the methods the request metrics name. Any other method
// counts as "other": the method is chosen by the client, and every distinct
// value would be a new series.
var standardMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
	http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace,
}

// quietPaths are hit every few seconds by probes and scrapers. Logging or
// tracing each hit would bury the requests worth reading.
var quietPaths = []string{RouteHealthz, RouteReadyz, RouteMetrics}

// Server is the deploygate HTTP server. Each request takes the stores'
// current snapshots, so a reload takes effect at the next request without
// touching the server.
type Server struct {
	deploy  *store.Store[deploy.Input]
	access  *store.Store[access.Input]
	router  *gin.Engine
	metrics *telemetry.Metrics

	tracerProvider trace.TracerProvider
	tracer         trace.Tracer

	addr            string
	shutdownTimeout time.Duration
}

// New builds the server and its routes. [WithStore] and [WithAccessStore]
// are required, and New returns an error without either; without
// [WithMetrics] the server reports on a private set of metrics, and without
// [WithTracerProvider] it uses the global tracer provider.
func New(opts ...Option) (*Server, humane.Error) {
	s := &Server{
		addr:            DefaultAddr,
		shutdownTimeout: DefaultShutdownTimeout,
	}
	for _, opt := range opts {
		opt(s)
	}

	if s.deploy == nil {
		return nil, humane.New("the server has no store for the team policies", "pass server.WithStore(store.NewDeploy(...)) to server.New")
	}
	if s.access == nil {
		return nil, humane.New("the server has no store for the access policies", "pass server.WithAccessStore(store.NewAccess(...)) to server.New")
	}
	if s.metrics == nil {
		s.metrics = telemetry.NewMetrics()
	}
	if s.tracerProvider == nil {
		s.tracerProvider = otel.GetTracerProvider()
	}
	s.tracer = s.tracerProvider.Tracer(telemetry.TracerName)

	s.router = s.newRouter()
	s.routes()
	return s, nil
}

// Handler returns the server's routes as an http.Handler, for tests and for
// embedding in another server.
func (s *Server) Handler() http.Handler {
	return s.router
}

// Serve listens on the configured address and serves until ctx ends; see
// [Server.ServeListener]. It returns an error at once when it can't listen.
//
//nolint:lifecycle // the context is the stop mechanism; a Stop method would be a second way to do the same
func (s *Server) Serve(ctx context.Context) humane.Error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.addr)
	if err != nil {
		return humane.Wrap(err, "deploygate can't listen on "+s.addr,
			"check that the address is valid and no other process uses it, or pick another with --addr")
	}
	return s.ServeListener(ctx, ln)
}

// ServeListener serves on ln until ctx ends, then shuts down gracefully: it
// stops accepting connections and waits up to the shutdown timeout for
// in-flight requests. It returns nil after a clean shutdown, and an error
// when serving fails or the shutdown timeout runs out. It closes ln. Tests
// pass a listener on port 0 to get a free port.
func (s *Server) ServeListener(ctx context.Context, ln net.Listener) humane.Error {
	srv := &http.Server{
		Handler:           s.router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// One goroutine serves, the other waits for the end of ctx, or for the
	// first one failing, and shuts the server down. The shutdown gets its own
	// budget, since ctx is done by then, but keeps ctx's values.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			return humane.Wrap(err, "the HTTP server on "+ln.Addr().String()+" failed",
				"check the logs above for the cause; restarting deploygate is safe")
		}
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		return s.shutdown(context.WithoutCancel(ctx), srv)
	})

	if err := g.Wait(); err != nil {
		if herr, ok := errors.AsType[humane.Error](err); ok {
			return herr
		}
		return humane.Wrap(err, "the HTTP server stopped unexpectedly", "check the logs above for the cause")
	}
	return nil
}

// shutdown stops srv within the shutdown timeout, in a span of its own, so a
// slow shutdown shows up in the traces with what it waited for.
func (s *Server) shutdown(ctx context.Context, srv *http.Server) humane.Error {
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()

	ctx, span := s.tracer.Start(ctx, "server.shutdown", trace.WithAttributes(
		attribute.String("deploygate.shutdown.timeout", s.shutdownTimeout.String()),
	))
	defer span.End()

	if err := srv.Shutdown(ctx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "graceful shutdown timed out")
		return humane.Wrap(err, "in-flight requests didn't finish within "+s.shutdownTimeout.String(),
			"raise --shutdown-timeout if requests legitimately take longer")
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// newRouter builds the gin engine with the observability middleware, in the
// same order as the other SpechtLabs services: recovery, tracing, access
// logs carrying the trace ids, and request metrics.
func (s *Server) newRouter() *gin.Engine {
	router := gin.New()
	router.ContextWithFallback = true
	router.Use(ginzap.RecoveryWithZap(otelzap.L(), true))
	router.Use(otelgin.Middleware(telemetry.DefaultServiceName,
		otelgin.WithTracerProvider(s.tracerProvider),
		otelgin.WithGinFilter(func(c *gin.Context) bool { return !isQuiet(c.Request.URL.Path) }),
	))
	// No TimeFormat: the logger's encoder stamps every line already, and a
	// second "time" field would make the JSON key ambiguous.
	router.Use(ginzap.GinzapWithConfig(otelzap.L(), &ginzap.Config{
		SkipPaths: quietPaths,
		Context:   traceFields,
	}))

	router.Use(s.requestMetrics)

	router.NoRoute(func(c *gin.Context) {
		writeError(c, http.StatusNotFound, humane.New("no route for "+c.Request.Method+" "+c.Request.URL.Path,
			"the API lives under /api/v1; see the README for the routes"))
	})

	return router
}

// routes registers the endpoints.
func (s *Server) routes() {
	s.router.POST(RouteDeployments, s.evaluate)
	s.router.POST(RouteGrants, s.grants)
	s.router.GET(RoutePolicies, s.listPolicies)
	s.router.POST(RouteReload, s.reload)
	s.router.GET(RouteHealthz, s.healthz)
	s.router.GET(RouteReadyz, s.readyz)
	s.router.GET(RouteMetrics, gin.WrapH(s.metrics.Handler()))
}

// requestMetrics counts every request but the scrapes of /metrics, which
// would otherwise dominate the request rate, and times it. Every label is
// bounded: the status code, the method from a fixed set, and the route
// template.
func (s *Server) requestMetrics(c *gin.Context) {
	if c.Request.URL.Path == RouteMetrics {
		c.Next()
		return
	}
	done := s.metrics.RequestTimer()
	c.Next()
	done(strconv.Itoa(c.Writer.Status()), methodLabel(c.Request.Method), routeLabel(c))
}

// traceFields adds the request's trace and span ids to its access log line,
// so a log line leads straight to its trace.
func traceFields(c *gin.Context) []zapcore.Field {
	sc := trace.SpanContextFromContext(c.Request.Context())
	if !sc.IsValid() {
		return nil
	}
	return []zapcore.Field{
		zap.String("trace_id", sc.TraceID().String()),
		zap.String("span_id", sc.SpanID().String()),
	}
}

// routeLabel is the request metrics' url label: the route template, so
// /api/v1/teams/payments/deployments and .../checkout/... share a series.
// A path without a route gets a fixed value instead of the raw path, which
// a scanner could otherwise use to create series without bound.
func routeLabel(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return "unmatched"
}

// methodLabel is the request metrics' method label: the method when it is a
// standard one, "other" otherwise.
func methodLabel(method string) string {
	if slices.Contains(standardMethods, method) {
		return method
	}
	return "other"
}

// isQuiet reports whether path is a probe or scrape path.
func isQuiet(path string) bool {
	return slices.Contains(quietPaths, path)
}
