package command

// Option configures the root command.
type Option func(*options)

type options struct {
	version string
}

// WithVersion sets the release version reported by the CLI.
func WithVersion(version string) Option {
	return func(o *options) { o.version = version }
}
