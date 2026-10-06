package lsp

import (
	"io"
	"time"
)

// defaultDelay is how long after the last change to a project the server
// loads it again and publishes its diagnostics: long enough that typing
// doesn't reload on every key, short enough to feel immediate.
const defaultDelay = 200 * time.Millisecond

// Option configures a [Server].
type Option func(*Server)

// WithLog sets where the server logs: never the client's stream. Without
// it the server logs nothing; the CLI passes stderr, which editors keep
// in their language server log. A nil writer keeps the default.
func WithLog(w io.Writer) Option {
	return func(s *Server) {
		if w != nil {
			s.log = w
		}
	}
}

// WithVersion sets the version the server reports in initialize.
func WithVersion(v string) Option {
	return func(s *Server) { s.version = v }
}

// WithDelay sets how long after the last change to a project the server
// loads it again and publishes its diagnostics. A request in the
// meantime loads it first anyway. A negative delay keeps the default.
func WithDelay(d time.Duration) Option {
	return func(s *Server) {
		if d >= 0 {
			s.delay = d
		}
	}
}
