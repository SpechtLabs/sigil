package explain

import (
	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// Option configures the explain command.
type Option func(*options)

type options struct {
	output *output.Format
	kinds  []project.Linked
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

// WithKinds sets the kinds linked into the binary.
func WithKinds(kinds []project.Linked) Option {
	return func(o *options) { o.kinds = kinds }
}
