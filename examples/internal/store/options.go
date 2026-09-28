package store

import (
	"io/fs"
	"os"
	"slices"

	"go.opentelemetry.io/otel/trace"

	"github.com/spechtlabs/sigil/examples/internal/telemetry"
)

// Option configures a Store. Options are applied in order by New.
type Option func(*Store)

// WithTeams sets the teams to serve. Team t evaluates the policy
// t.production of the team bundle.
func WithTeams(teams ...string) Option {
	return func(s *Store) {
		s.teams = slices.Clone(teams)
	}
}

// WithTeamsFS sets the team bundle: the untrusted documents, such as a
// mounted ConfigMap. source names it in snapshots, logs and metrics, usually
// the directory it was read from.
func WithTeamsFS(fsys fs.FS, source string) Option {
	return func(s *Store) {
		s.teamsFS = fsys
		s.source = source
	}
}

// WithTeamsDir reads the team bundle from dir on disk and names the source
// after it.
func WithTeamsDir(dir string) Option {
	return WithTeamsFS(os.DirFS(dir), dir)
}

// WithPlatformFS replaces the platform documents the guardrails are read
// from. The service never does; tests use it to load a different platform.
func WithPlatformFS(fsys fs.FS) Option {
	return func(s *Store) {
		s.platformFS = fsys
	}
}

// WithMetrics reports loads on m instead of a private set of metrics.
func WithMetrics(m *telemetry.Metrics) Option {
	return func(s *Store) {
		s.metrics = m
	}
}

// WithTracer starts the reload spans on t instead of the global tracer.
func WithTracer(t trace.Tracer) Option {
	return func(s *Store) {
		s.tracer = t
	}
}

// WithClock replaces the wall clock, so a test can pin the loaded-at time
// and drive polling without waiting.
func WithClock(c Clock) Option {
	return func(s *Store) {
		s.clock = c
	}
}
