package policy

import (
	"reflect"
	"strings"

	"github.com/spechtlabs/sigil/internal/contract"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// Kind is a contract built from Go types: the inputs a policy reads, the
// decisions it may construct, and the host functions it may call. Its
// type parameter is the input struct, whose tagged fields are the inputs.
//
// Build one with [NewKind], once, at package level. A Kind is immutable
// and safe for concurrent use; [Kind.Load] and [Kind.Compile] turn policy
// source into a [Policy] of the same input type.
type Kind[In any] struct {
	kind    *kind.Kind
	binding *gokind.Binding
}

// NewKind builds the kind called name from the input struct In and the
// options. The name is how policies pin the kind in their headers, as in
// `policy payments.production: DeployApproval@1`.
//
// NewKind reflects over In once and records where every input lives, so
// [Policy.Eval] reads the host's values in place. The options must include
// [WithVersion], and either [WithDecisions] with [WithDefault], or
// [WithCollect].
//
// NewKind panics when the contract can't be exported: a Go type with no
// Sigil equivalent, a missing version or default, a decision whose
// reasons are ranked twice.
// The panic lists every problem found, so a bad kind fails at init rather
// than at the first [Kind.Load], and is fixed in one round.
func NewKind[In any](name string, opts ...Option) *Kind[In] {
	o := gokind.Options{Name: name, Input: reflect.TypeFor[In]()}
	for _, opt := range opts {
		opt(&o)
	}
	k, b, errs := gokind.Build(o)
	if errs != nil {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = "  " + e.Msg
			if e.Help != "" {
				msgs[i] += " (" + e.Help + ")"
			}
		}
		panic("policy.NewKind(" + name + "): invalid kind:\n" + strings.Join(msgs, "\n")) //nolint:nopanic // a kind that can't be exported is a programming error, caught at init like regexp.MustCompile
	}
	return &Kind[In]{kind: k, binding: b}
}

// Name returns the kind's name, as passed to [NewKind].
func (k *Kind[In]) Name() string { return k.kind.Name }

// Schema returns the kind as a kind file, which a policy repository
// checks in so the CLI and other services can type-check against it
// without importing the host. The text is in `sigil fmt`'s canonical
// style and parses back into the same contract.
//
// [Kind.Load] and [Kind.Compile] compare a kind document of the same name
// in the bundle against Schema and fail on any difference, which catches a
// stale export. [github.com/spechtlabs/sigil/pkg/policytest.Schema] checks
// the file on disk from go test.
func (k *Kind[In]) Schema() string { return k.kind.Source() }

// Contract is for sigil's own tools, the CLI a host builds with package
// [github.com/spechtlabs/sigil/pkg/cli] and package
// [github.com/spechtlabs/sigil/pkg/policytest]: the kind model and its
// binding to the Go types and functions. Its type is internal to the sigil
// module, so nothing else can use it.
func (k *Kind[In]) Contract() *contract.Kind {
	return &contract.Kind{Model: k.kind, Binding: k.binding}
}
