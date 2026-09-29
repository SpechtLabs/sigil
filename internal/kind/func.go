package kind

import (
	"strings"

	"github.com/spechtlabs/sigil/internal/types"
)

// Func is a host function signature. Parameters are types only; policies
// pass arguments positionally. The implementation isn't part of the
// model; a binding built by package gokind holds it.
type Func struct {
	Result types.Type // never optional in a valid kind
	Name   string
	Params []types.Type
}

// Signature renders the declaration as a kind file writes it, like
// `fn split(string, string) -> list<string>`.
func (f *Func) Signature() string {
	params := make([]string, len(f.Params))
	for i, p := range f.Params {
		params[i] = p.String()
	}
	return "fn " + f.Name + "(" + strings.Join(params, ", ") + ") -> " + f.Result.String()
}
