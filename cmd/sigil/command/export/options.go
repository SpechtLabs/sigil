package export

import "github.com/spechtlabs/sigil/cmd/sigil/internal/project"

// Option configures the export command.
type Option func(*options)

type options struct {
	kinds []project.Linked
}

// WithKinds sets the kinds linked into the binary.
func WithKinds(kinds []project.Linked) Option {
	return func(o *options) { o.kinds = kinds }
}
