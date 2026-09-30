package engine

import "runtime/debug"

// Option configures an [Engine].
type Option func(*Engine)

// WithHost sets how the engine runs the host functions compile's
// functions names. Without it, a policy compiled with functions fails
// each such call with a runtime error that says no host is attached.
func WithHost(h Host) Option {
	return func(e *Engine) { e.host = h }
}

// WithVersion sets the release version the version op reports. When
// empty, the main module's version from the build info is reported.
func WithVersion(version string) Option {
	return func(e *Engine) { e.tag = version }
}

// WithBuildInfo sets the build info the version op reports, in place of
// the one embedded in the running binary. A nil pointer keeps it.
func WithBuildInfo(bi *debug.BuildInfo) Option {
	return func(e *Engine) {
		if bi != nil {
			e.buildInfo = bi
		}
	}
}
