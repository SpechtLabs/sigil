package version

// Option configures the version command.
type Option func(*options)

type options struct{ version string }

// WithVersion sets the release version reported by the command.
func WithVersion(version string) Option {
	return func(o *options) { o.version = version }
}
