// Package contract is what sigil's own tools, the CLI and package
// policytest, need of a kind a host built with policy.NewKind: the kind
// model, and the binding back to the host's Go types and functions.
// Package policy hands it out through Kind.Contract; the package is
// internal, so nothing outside this module can use it.
//
// A Kind is a non-generic handle on a policy.Kind[In], so a tool can hold
// kinds of different input types side by side, and package policy doesn't
// export the model or the binding.
package contract

import (
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// Kind is a host's kind with its binding. Both come from the same
// [gokind.Build] run and are shared with the policy.Kind that returned
// them, so neither may be modified.
type Kind struct {
	Model   *kind.Kind      // the contract, as the checker and evaluator see it
	Binding *gokind.Binding // the host's Go types and host function implementations
}
