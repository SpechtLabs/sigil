package kind

import (
	"strings"

	"github.com/spechtlabs/sigil/internal/types"
)

// Func is a host function signature. Parameter names are documentation;
// policies pass arguments positionally.
type Func struct {
	Result types.Type
	Name   string
	Params []*Param
}

// Param is one parameter of a Func.
type Param struct {
	Type types.Type
	Name string
}

// Signature renders the declaration as a kind file writes it.
func (f *Func) Signature() string {
	params := make([]string, len(f.Params))
	for i, p := range f.Params {
		params[i] = p.Name + ": " + p.Type.String()
	}
	return "fn " + f.Name + "(" + strings.Join(params, ", ") + ") -> " + f.Result.String()
}
