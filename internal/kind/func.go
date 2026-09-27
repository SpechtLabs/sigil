package kind

import (
	"strings"

	"github.com/spechtlabs/sigil/internal/types"
)

// Func is a host function signature. Parameters are types only; policies
// pass arguments positionally.
type Func struct {
	Result types.Type
	Name   string
	Params []types.Type
}

// Signature renders the declaration as a kind file writes it.
func (f *Func) Signature() string {
	params := make([]string, len(f.Params))
	for i, p := range f.Params {
		params[i] = p.String()
	}
	return "fn " + f.Name + "(" + strings.Join(params, ", ") + ") -> " + f.Result.String()
}
