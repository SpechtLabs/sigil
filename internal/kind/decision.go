package kind

import (
	"strings"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/types"
)

// Decision is a decision constructor and its payload schema. The reason
// isn't a field: every decision takes it first, so it's implied.
type Decision struct {
	Name   string
	Fields []*Field
}

// Field is a payload field. Default holds a constant in the evaluator's
// representation (see constant.Conforms) when HasDefault is set; a field
// without a default is required at every call site. A nil Default with
// HasDefault set means the kind's source couldn't produce the value and
// has reported why, so Validate doesn't report it again.
type Field struct {
	Type       types.Type
	Default    any
	Name       string
	HasDefault bool
}

// Field returns the payload field called name, or nil.
func (d *Decision) Field(name string) *Field {
	for _, f := range d.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// Signature renders the declaration as a kind file writes it, for
// messages like "approve is declared as: decision approve(reason: string,
// bake: duration = 1h)".
func (d *Decision) Signature() string {
	parts := make([]string, 0, 1+len(d.Fields))
	parts = append(parts, "reason: string")
	for _, f := range d.Fields {
		p := f.Name + ": " + f.Type.String()
		if f.HasDefault {
			p += " = " + constant.Format(f.Default)
		}
		parts = append(parts, p)
	}
	return "decision " + d.Name + "(" + strings.Join(parts, ", ") + ")"
}
