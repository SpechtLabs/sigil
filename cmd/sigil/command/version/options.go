package version

import (
	"runtime/debug"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/payload"
)

// Option configures the version command.
type Option func(*options)

type options struct {
	version   string
	buildInfo *debug.BuildInfo
	output    *output.Format
	kinds     []project.Linked
	// payload is the bundle compiled into the binary, and name the
	// binary's; with a payload, the command describes the bundle too.
	payload *payload.Payload
	name    string
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

// WithKinds sets the kinds linked into the binary, which a compiled
// bundle's documents may be written against. Only a compiled binary's
// version uses them, to load the bundle and name its kinds.
func WithKinds(kinds []project.Linked) Option {
	return func(o *options) { o.kinds = kinds }
}

// WithPayload makes the command a compiled binary's version: it describes
// the bundle of p as well, which policies it holds and which sigil
// compiled them. name is the binary's name, as help shows it. A nil
// p keeps the stock command.
func WithPayload(p *payload.Payload, name string) Option {
	return func(o *options) { o.payload, o.name = p, name }
}
