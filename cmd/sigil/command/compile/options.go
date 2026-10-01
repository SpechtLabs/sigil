package compile

import (
	"os"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// Option configures the compile command.
type Option func(*options)

type options struct {
	output     *output.Format
	kinds      []project.Linked
	version    string
	executable func() (string, error)
}

func defaultOptions() *options {
	format := output.Text
	return &options{output: &format, executable: os.Executable}
}

// WithOutput sets the output format. It takes a pointer so the command
// reads the root --output flag after cobra has parsed it. A nil pointer
// keeps text.
func WithOutput(format *output.Format) Option {
	return func(o *options) {
		if format != nil {
			o.output = format
		}
	}
}

// WithKinds sets the kinds linked into the binary. The command uses a
// linked kind for every document written against it, with its Go types
// and host functions, and a kind file of the same name must match it.
// The compiled binary has the same kinds linked in, so a bundle may use
// their host functions.
func WithKinds(kinds []project.Linked) Option {
	return func(o *options) { o.kinds = kinds }
}

// WithVersion sets the release version the compiled binary records as the
// sigil that compiled it. When empty, the main module version from the
// build info is recorded instead.
func WithVersion(version string) Option {
	return func(o *options) { o.version = version }
}

// WithExecutable sets how the command finds the binary it copies. It
// defaults to [os.Executable], the running binary; tests pass a prebuilt
// one. A nil function keeps the default.
func WithExecutable(executable func() (string, error)) Option {
	return func(o *options) {
		if executable != nil {
			o.executable = executable
		}
	}
}
