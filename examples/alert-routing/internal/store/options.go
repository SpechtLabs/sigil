package store

import (
	"io/fs"
	"os"

	"go.opentelemetry.io/otel/trace"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
)

// Option configures a Store of any kind. Options are applied in order by
// New, so a later option overrides an earlier one.
type Option func(*config)

// config is a store's configuration. It doesn't depend on the kind's input
// type, which keeps the options plain functions that need no type argument.
type config struct {
	roots   []Root
	bundle  fs.FS
	source  string
	metrics *telemetry.Metrics
	tracer  trace.Tracer
	clock   Clock

	// required is the policy every root must invoke, read from trusted.
	required string
	trusted  fs.FS
}

// WithTeams sets one root per team: team t evaluates the policy t.alerts.
// It replaces any roots set before. alertrouter passes the team directory's
// names, so every team an alert can name has a compiled policy, and a team
// without one fails the load instead of failing its alerts.
func WithTeams(teams ...string) Option {
	return func(c *config) {
		c.roots = make([]Root, 0, len(teams))
		for _, team := range teams {
			c.roots = append(c.roots, Root{Team: team, Policy: team + RootSuffix})
		}
	}
}

// WithBundle sets the bundle the roots are compiled from: the untrusted
// documents, such as a mounted ConfigMap. source names it in snapshots, logs
// and metrics, usually the directory it was read from.
func WithBundle(fsys fs.FS, source string) Option {
	return func(c *config) {
		c.bundle = fsys
		c.source = source
	}
}

// WithBundleDir reads the bundle from dir on disk and names the source after
// it.
func WithBundleDir(dir string) Option {
	return WithBundle(os.DirFS(dir), dir)
}

// WithRequired names the policy every root must invoke unconditionally, and
// the trusted source it and everything it uses are read from. fsys must hold
// documents of the store's kind only, and must be comparable, since the
// loader keys its sources by value: pass an os.DirFS, an embed.FS or an
// fs.Sub of one, not a map-based fs.FS.
func WithRequired(name string, fsys fs.FS) Option {
	return func(c *config) {
		c.required = name
		c.trusted = fsys
	}
}

// WithMetrics reports loads on m instead of a private set of metrics.
func WithMetrics(m *telemetry.Metrics) Option {
	return func(c *config) {
		c.metrics = m
	}
}

// WithTracer starts the load spans on t instead of the global tracer.
func WithTracer(t trace.Tracer) Option {
	return func(c *config) {
		c.tracer = t
	}
}

// WithClock replaces the wall clock, so a test can pin the loaded-at time
// and drive polling without waiting.
func WithClock(clock Clock) Option {
	return func(c *config) {
		c.clock = clock
	}
}
