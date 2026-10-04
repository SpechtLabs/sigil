package server

import (
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/access"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/freeze"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/store"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/telemetry"
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

// WithFreeze sets where the change freeze comes from. The deployments handler
// asks src once per request and puts its answer into the deploy input,
// overwriting anything decoded, so a client can never claim an unfrozen
// environment. Without it, or with a nil src, nothing is frozen.
func WithFreeze(src freeze.Source) Option {
	return func(s *Server) {
		if src != nil {
			s.freeze = src
		}
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

// WithEvaluationTimeout bounds each policy evaluation, the access stage and
// the deploy stage each on its own. An evaluation still running at the
// deadline stops and answers 503 with the fallback decision. The default is
// one second, thousands of times what the example policies take, and a
// duration that isn't positive keeps it.
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
