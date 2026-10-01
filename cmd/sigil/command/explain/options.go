package explain

import (
	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/payload"
)

// Option configures the explain command.
type Option func(*options)

type options struct {
	output *output.Format
	kinds  []project.Linked
	// payload is the bundle compiled into the binary, and name the
	// binary's; with a payload, the command is the compiled explain.
	payload *payload.Payload
	name    string
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
func WithKinds(kinds []project.Linked) Option {
	return func(o *options) { o.kinds = kinds }
}

// WithPayload makes the command a compiled binary's explain: it explains
// the policies of p's bundle, the root unless --policy names others, and
// reads no policy file. name is the binary's name, as help shows it.
// A nil p keeps the stock command.
func WithPayload(p *payload.Payload, name string) Option {
	return func(o *options) { o.payload, o.name = p, name }
}
