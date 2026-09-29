package kind

import "github.com/spechtlabs/sigil/internal/types"

// Input is a top-level name policies can read, and its type. Inputs and
// host functions share one namespace.
type Input struct {
	Type types.Type
	Name string
}
