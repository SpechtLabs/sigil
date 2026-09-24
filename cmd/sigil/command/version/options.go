package version

import (
	"runtime/debug"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
)

// Option configures the version command.
type Option func(*options)

type options struct {
	version   string
	buildInfo *debug.BuildInfo
	output    *output.Format
}

func defaultOptions() *options {
	format := output.Text
	o := &options{output: &format}
	if bi, ok := debug.ReadBuildInfo(); ok {
		o.buildInfo = bi
	}
	return o
}

// WithVersion sets the release version to report. When empty, the main
// module version from the build info is reported instead.
func WithVersion(version string) Option {
	return func(o *options) { o.version = version }
}

// WithBuildInfo sets the build info to report. It defaults to the build info
// embedded in the running binary. A nil pointer keeps the default.
func WithBuildInfo(buildInfo *debug.BuildInfo) Option {
	return func(o *options) {
		if buildInfo != nil {
			o.buildInfo = buildInfo
		}
	}
}

// WithOutput sets the output format. It takes a pointer so the command reads
// the value of the root --output flag after cobra has parsed it. A nil
// pointer keeps the default.
func WithOutput(format *output.Format) Option {
	return func(o *options) {
		if format != nil {
			o.output = format
		}
	}
}
