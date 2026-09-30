package server

import (
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/dispatch"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/teams"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
)

// Option configures a Server. Options are applied in order by New.
type Option func(*Server)

// WithStore sets the store of the team policies. It is required.
func WithStore(st *store.Store[routing.Input]) Option {
	return func(s *Server) {
		s.store = st
	}
}

// WithDirectory sets the team directory an alert's team label is looked up
// in. It is required, and it must name the same teams the store serves, as
// it does when the store was built with store.WithTeams(dir.Names()...).
func WithDirectory(dir *teams.Directory) Option {
	return func(s *Server) {
		s.directory = dir
	}
}

// WithNotifier delivers each routed alert through n instead of the default
// [dispatch.LogNotifier].
func WithNotifier(n dispatch.Notifier) Option {
	return func(s *Server) {
		s.notifier = n
	}
}

// WithMetrics reports alerts and decisions on m, the same set /metrics
// serves. Share it with the store so reloads show up on the same endpoint.
func WithMetrics(m *telemetry.Metrics) Option {
	return func(s *Server) {
		s.metrics = m
	}
}

// WithTracerProvider starts the HTTP server spans and the route spans on tp
// instead of the global provider, which is how a test records them.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(s *Server) {
		s.tracerProvider = tp
	}
}

// WithClock replaces the wall clock the server reads a webhook alert's
// firing time against, so a test can pin how long an alert has fired.
func WithClock(clock store.Clock) Option {
	return func(s *Server) {
		s.clock = clock
	}
}

// WithAddr sets the listen address Serve uses. The default is ":8080".
func WithAddr(addr string) Option {
	return func(s *Server) {
		s.addr = addr
	}
}

// WithEvaluationTimeout bounds each alert's evaluation on its own. An
// evaluation still running at the deadline stops and the alert is routed
// with the fallback decision. The default is one second, thousands of times
// what the example policies take, and a duration that isn't positive keeps
// it.
func WithEvaluationTimeout(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.evaluationTimeout = d
		}
	}
}

// WithShutdownTimeout bounds how long Serve waits for in-flight requests
// when its context ends. The default is 15 seconds, and a duration that
// isn't positive keeps it.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.shutdownTimeout = d
		}
	}
}
