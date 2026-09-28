package gen

import "github.com/spechtlabs/sigil/cmd/internal/output"

// Option configures the gen command.
type Option func(*options)

type options struct {
	output *output.Format
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
