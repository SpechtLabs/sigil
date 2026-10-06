package lsp

import "github.com/spechtlabs/sigil/cmd/sigil/internal/project"

// Option configures the lsp command.
type Option func(*options)

type options struct {
	version string
	kinds   []project.Linked
}

// WithKinds sets the kinds linked into the binary. The server checks
// every document written against one with it, as check does, and a kind
// file of the same name must match it.
func WithKinds(kinds []project.Linked) Option {
	return func(o *options) { o.kinds = kinds }
}

// WithVersion sets the version the server reports to the editor.
func WithVersion(version string) Option {
	return func(o *options) { o.version = version }
}
