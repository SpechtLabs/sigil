package command

import (
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// Option configures the root command.
type Option func(*options)

type options struct {
	version string
	kinds   []project.Linked
}

// WithVersion sets the release version of the binary. Everything else about
// the build is read from the Go build info at run time.
func WithVersion(version string) Option {
	return func(o *options) { o.version = version }
}

// WithKind links a host's kind into the binary. eval and test then decode
// inputs into the host's own Go types and call its host functions, and
// every command uses the kind without --kind; a --kind file for the same
// kind must match it exactly. Repeat it for a host with several kinds.
func WithKind[In any](k *policy.Kind[In]) Option {
	return func(o *options) {
		if k != nil {
			c := k.Contract()
			o.kinds = append(o.kinds, project.Linked{Model: c.Model, Binding: c.Binding})
		}
	}
}
