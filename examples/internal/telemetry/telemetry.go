// Package telemetry sets up what deploygate reports about itself: traces
// through OpenTelemetry, logs through otelzap so they land on the active span,
// Prometheus metrics, and optional continuous Go runtime profiles.
package telemetry

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spechtlabs/go-otel-utils/otelprovider"
	"github.com/spechtlabs/go-otel-utils/otelzap"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TracerName is the instrumentation scope of the spans deploygate starts
// itself, as opposed to the HTTP server spans otelgin starts.
const TracerName = "github.com/spechtlabs/sigil/examples/deploygate"

// DefaultServiceName is the service.name spans carry when OTEL_SERVICE_NAME
// is unset. Without it the name would be the binary's file name, which is a
// temporary path under `go run`.
const DefaultServiceName = "deploygate"

// The log formats Setup accepts.
const (
	LogFormatJSON    = "json"
	LogFormatConsole = "console"
)

// Config selects how the process reports. Tracing itself is configured
// through the standard OTEL_* environment variables, so it works the same way
// as in every other OpenTelemetry service.
type Config struct {
	// Version is recorded as service.version on every span.
	Version string
	// LogFormat is LogFormatJSON or LogFormatConsole.
	LogFormat string
	// Debug lowers the log level to debug.
	Debug bool
}

// Telemetry owns the process-wide tracer provider, logger and optional
// profiler, so Shutdown can flush them and put the previous globals back.
type Telemetry struct {
	tracerProvider *sdktrace.TracerProvider
	logger         *zap.Logger
	profiler       *continuousProfiler
	restore        []func()
}

// Setup builds the logger and the tracer provider and installs them as the
// process globals, which is what otelgin, ginzap and otelzap.L() read. Traces
// are exported over OTLP when OTEL_EXPORTER_OTLP_ENDPOINT is set and
// OTEL_TRACES_EXPORTER isn't "none"; otherwise spans are still created, so
// trace ids show up in the logs, but go nowhere. PYROSCOPE_SERVER_ADDRESS
// enables all supported Go profiles independently of tracing.
func Setup(cfg Config) (*Telemetry, humane.Error) {
	logger, err := newLogger(cfg)
	if err != nil {
		return nil, err
	}

	profiler, err := startProfiler(cfg.Version, logger)
	if err != nil {
		_ = logger.Sync()
		return nil, err
	}

	// The same wiring as the other SpechtLabs services: zap's globals and
	// the standard library's log package go to the same logger, and otelzap
	// annotates the active span with warnings and marks it failed on errors.
	undoZap := zap.ReplaceGlobals(logger)
	undoStdLog := zap.RedirectStdLog(logger)
	undoOtelZap := otelzap.ReplaceGlobals(otelzap.New(logger,
		otelzap.WithCaller(true),
		otelzap.WithMinLevel(logger.Level()),
		otelzap.WithAnnotateLevel(zap.WarnLevel),
		otelzap.WithErrorStatusLevel(zap.ErrorLevel),
		otelzap.WithStackTrace(false),
	))

	tp := otelprovider.NewTracer(append(exporterOptions(), otelprovider.WithTraceResources(newResource(cfg.Version)))...)

	// The default global propagator drops incoming trace context, which would
	// start a fresh trace for every request a traced client makes.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Telemetry{
		tracerProvider: tp,
		logger:         logger,
		profiler:       profiler,
		restore:        []func(){undoOtelZap, undoStdLog, undoZap},
	}, nil
}

// Shutdown stops profiling, flushes buffered spans and syncs the logger. Call it
// last, after the server stopped, so the shutdown itself is still traced.
func (t *Telemetry) Shutdown(ctx context.Context) humane.Error {
	var errs []error
	if t.profiler != nil {
		if err := t.profiler.Stop(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := t.tracerProvider.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}

	// Syncing stderr fails on some platforms with ENOTTY or EINVAL, which
	// isn't a lost log line, so only a real write error counts.
	if err := t.logger.Sync(); err != nil && !isStderrSyncError(err) {
		errs = append(errs, err)
	}
	for _, undo := range t.restore {
		undo()
	}

	if err := errors.Join(errs...); err != nil {
		return humane.Wrap(err, "flushing telemetry on shutdown failed",
			"buffered telemetry may be lost; check that the configured collectors are reachable")
	}
	return nil
}

// newLogger builds the zap logger for the configured format and level.
func newLogger(cfg Config) (*zap.Logger, humane.Error) {
	var zc zap.Config
	switch cfg.LogFormat {
	case LogFormatJSON, "":
		zc = zap.NewProductionConfig()
		zc.EncoderConfig.TimeKey = "time"
		zc.EncoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	case LogFormatConsole:
		zc = zap.NewDevelopmentConfig()
		zc.Development = false
	default:
		return nil, humane.New("unknown log format "+cfg.LogFormat,
			"use --log-format json or --log-format console")
	}

	// "30s" reads better in a log line than zap's default of 30.
	zc.EncoderConfig.EncodeDuration = zapcore.StringDurationEncoder
	// Errors here are humane errors that say what failed and what to do; a
	// Go stack trace under each would bury that.
	zc.DisableStacktrace = true

	zc.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	if cfg.Debug {
		zc.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}

	logger, err := zc.Build()
	if err != nil {
		return nil, humane.Wrap(err, "building the logger failed", "check the log configuration")
	}
	return logger, nil
}

// newResource names the service on every span.
func newResource(version string) *resource.Resource {
	name := os.Getenv("OTEL_SERVICE_NAME")
	if name == "" {
		name = DefaultServiceName
	}
	if version == "" {
		version = "dev"
	}

	// Schemaless so the merge can't fail on a schema URL mismatch between the
	// semconv version here and the SDK's default resource.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(name),
		semconv.ServiceVersion(version),
	))
	if err != nil {
		return resource.Default()
	}
	return res
}

// exporterOptions picks the OTLP exporter from the standard environment.
// otelprovider's WithTraceAutomaticEnv passes OTEL_EXPORTER_OTLP_ENDPOINT to
// the exporter as is, but the exporters want host:port, while the variable is
// conventionally a URL such as http://otel-collector:4317. So the endpoint is
// taken apart here: an http scheme means plaintext, and the protocol comes
// from OTEL_EXPORTER_OTLP_PROTOCOL, or from the well-known port when unset.
func exporterOptions() []otelprovider.TracerOption {
	if os.Getenv("OTEL_TRACES_EXPORTER") == "none" {
		return nil
	}

	raw := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if raw == "" {
		raw = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if raw == "" {
		return nil
	}

	hostPort, scheme := splitEndpoint(raw)
	insecure := scheme == "http" || os.Getenv("OTEL_EXPORTER_OTLP_INSECURE") == "true"

	var opts []otelprovider.TracerOption
	// The endpoint options read the insecure flag when they are applied, so
	// it has to come first.
	if insecure {
		opts = append(opts, otelprovider.WithTraceInsecure())
	}

	if useGRPC(hostPort) {
		return append(opts, otelprovider.WithGrpcTraceEndpoint(hostPort))
	}
	return append(opts, otelprovider.WithHttpTraceEndpoint(hostPort))
}

// splitEndpoint returns the host:port of an OTLP endpoint, given as a URL or
// as a bare host:port, and its scheme, empty for a bare host:port.
func splitEndpoint(raw string) (hostPort, scheme string) {
	if !strings.Contains(raw, "://") {
		return raw, ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw, ""
	}
	return u.Host, u.Scheme
}

// useGRPC reports whether the exporter should speak gRPC: when the protocol
// says so, or, without one, when the endpoint is on the OTLP gRPC port.
func useGRPC(hostPort string) bool {
	switch os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL") {
	case "grpc":
		return true
	case "http/protobuf", "http/json":
		return false
	}
	_, port, err := net.SplitHostPort(hostPort)
	return err == nil && port == "4317"
}

// isStderrSyncError reports whether err is the harmless error syncing a
// terminal or pipe returns.
func isStderrSyncError(err error) bool {
	var pathErr *os.PathError
	return errors.As(err, &pathErr) && pathErr.Path == "/dev/stderr"
}
