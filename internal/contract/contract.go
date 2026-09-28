// Package contract is what sigil's own tools, the CLI and package
// policytest, need of a kind a host built with policy.NewKind: the kind
// model, and the binding back to the host's Go types and functions.
// Package policy hands it out through Kind.Contract; the package is
// internal, so nothing outside this module can use it.
package contract

import (
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// Kind is a host's kind with its binding.
type Kind struct {
	Model   *kind.Kind
	Binding *gokind.Binding
}
