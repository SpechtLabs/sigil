package gokind

import (
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
)

// SetDefault does what Build does with a payload field's `default=src` tag
// option: it parses src as a constant of the field's type into field, and
// returns what that reports. FuzzGoKindRoundTrip sets its defaults through
// it rather than through a payload type per default: reflect.StructOf keeps
// every type it builds for the life of the process, so a fuzz worker
// building one per input grows until it runs out of memory.
func SetDefault(field *kind.Field, decision, src string) diag.ErrorList {
	b := &builder{}
	b.setDefault(field, decision, src)
	return b.errs
}
