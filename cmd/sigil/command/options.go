package command

import (
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// Option configures the root command [NewCommand] builds.
// [github.com/spechtlabs/sigil/pkg/cli.Option] is an alias of it.
type Option func(*options)

type options struct {
	version string
	kinds   []project.Linked
}

// WithVersion sets the release version `sigil version` reports. The
// commit, its time, the dirty state, the Go version and the platform come
// from the Go build info at run time. When version is empty, the main
// module's version from the build info is reported instead.
func WithVersion(version string) Option {
	return func(o *options) { o.version = version }
}

// WithKind links k, a host's kind, into the binary. eval and test then
// decode inputs into the host's own Go types and call its host functions,
// and every command uses the kind without --kind. A --kind file for a
// linked kind, matched by name, must match it exactly, which catches a
// stale export. A --kind file for any other kind is loaded on its own,
// with host functions that fail when called. A nil k is ignored.
//
// Repeat it for a host with several kinds. Commands then need --kind to
// pick one, and `sigil export` a KIND argument.
func WithKind[In any](k *policy.Kind[In]) Option {
	return func(o *options) {
		if k != nil {
			c := k.Contract()
			o.kinds = append(o.kinds, project.Linked{Model: c.Model, Binding: c.Binding})
		}
	}
}
