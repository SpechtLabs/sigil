package kind

import "github.com/spechtlabs/sigil/internal/types"

// Input is a top-level name policies can read.
type Input struct {
	Type types.Type
	Name string
}
