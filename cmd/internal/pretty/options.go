package pretty

import "os"

// Option configures a Printer.
type Option func(*options)

type options struct {
	environ        []string
	darkBackground func() bool
}

func defaultOptions() *options {
	return &options{
		environ:        os.Environ(),
		darkBackground: hasDarkBackground,
	}
}

// WithEnviron sets the environment used to detect color support, such as
// NO_COLOR, CLICOLOR_FORCE and TERM. It defaults to os.Environ().
func WithEnviron(environ []string) Option {
	return func(o *options) { o.environ = environ }
}

// WithDarkBackground skips querying the terminal and picks the light or dark
// palette explicitly.
func WithDarkBackground(dark bool) Option {
	return func(o *options) { o.darkBackground = func() bool { return dark } }
}
