package policy

import (
	"reflect"
	"strings"

	"github.com/spechtlabs/sigil/internal/contract"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// Kind is a contract built from Go types. Its type parameter is the input
// struct, whose tagged fields are the inputs.
type Kind[In any] struct {
	kind    *kind.Kind
	binding *gokind.Binding
}

// NewKind builds the kind called name from the input struct In and the
// options. It panics with every problem found when the contract can't be
// exported, so a bad kind fails at init rather than at the first Load.
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

// Name returns the kind's name.
func (k *Kind[In]) Name() string { return k.kind.Name }

// Schema returns the kind as a kind file, which a policy repository
// checks in so the CLI and other services can type-check against it
// without importing the host.
func (k *Kind[In]) Schema() string { return k.kind.Source() }

// Contract is for sigil's own tools, the CLI a host builds with package
// cli and package policytest: the kind model and its binding to the Go
// types and functions. Its type is internal to the sigil module, so
// nothing else can use it.
func (k *Kind[In]) Contract() *contract.Kind {
	return &contract.Kind{Model: k.kind, Binding: k.binding}
}
