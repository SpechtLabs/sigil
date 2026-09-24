package command

// Option configures the root command.
type Option func(*options)

type options struct {
	version string
}

// WithVersion sets the release version of the binary. Everything else about
// the build is read from the Go build info at run time.
func WithVersion(version string) Option {
	return func(o *options) { o.version = version }
}
