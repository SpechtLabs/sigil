package policy

import (
	"reflect"
	"strings"

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
	o := gokind.Options{Name: name, Input: reflect.TypeOf((*In)(nil)).Elem()}
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

// Compile compiles the policy called name from src, a one-file bundle
// that may hold several documents. Every document is checked against the
// kind, so a broken document fails the compile even when the root never
// uses it, and a kind document with the kind's name must match Schema().
// The error is a *CompileError listing every problem with a position and
// a fix hint.
func (k *Kind[In]) Compile(src, name string, opts ...LoadOption) (*Policy[In], error) {
	o := &loadOptions{params: Params{}}
	for _, opt := range opts {
		opt.apply(o)
	}
	b := newBundle(k)
	b.add("", []byte(src))
	return b.compile(name, o)
}
