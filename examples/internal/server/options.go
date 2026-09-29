package server

import (
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/spechtlabs/sigil/examples/internal/access"
	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/store"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
)

// Option configures a Server. Options are applied in order by New.
type Option func(*Server)

// WithStore sets the store of the team deploy policies. It is required.
func WithStore(st *store.Store[deploy.Input]) Option {
	return func(s *Server) {
		s.deploy = st
	}
}

// WithAccessStore sets the store of the access policy, whose single root
// grants the roles the deploy policies read. It is required.
func WithAccessStore(st *store.Store[access.Input]) Option {
	return func(s *Server) {
		s.access = st
	}
}

// WithMetrics reports decisions on m, the same set /metrics serves. Share it
// with the store so reloads show up on the same endpoint.
func WithMetrics(m *telemetry.Metrics) Option {
	return func(s *Server) {
		s.metrics = m
	}
}

// WithTracerProvider starts the HTTP server spans and the evaluation spans on
// tp instead of the global provider, which is how a test records them.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(s *Server) {
		s.tracerProvider = tp
	}
}

// WithAddr sets the listen address Serve uses. The default is ":8080".
func WithAddr(addr string) Option {
	return func(s *Server) {
		s.addr = addr
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
