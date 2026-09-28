package kind

import (
	"slices"
	"strings"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/types"
)

// Decision is a decision constructor: its payload schema and the reasons
// it can be constructed with. A constructor names one of the reasons
// first; they're declared names, not strings.
type Decision struct {
	Name    string
	Fields  []*Field
	Reasons []string
	// Ranked holds the reasons in precedence order when the kind ranks
	// them with a scoped `precedence`; nil when it doesn't.
	Ranked []string
}

// HasReason reports whether the decision declares the reason.
func (d *Decision) HasReason(name string) bool { return slices.Contains(d.Reasons, name) }

// ReasonRank returns the reason's position in the decision's ranking, or
// 0 when the kind doesn't rank this decision's reasons, so every reason
// ties.
func (d *Decision) ReasonRank(name string) int {
	for i, r := range d.Ranked {
		if r == name {
			return i
		}
	}
	return 0
}

// Outcome is a decision, or one reason of it, as `exclusive` names them.
type Outcome struct {
	Decision string
	Reason   string // empty for the whole decision
}

// String renders the outcome as a kind file writes it.
func (o Outcome) String() string {
	if o.Reason == "" {
		return o.Decision
	}
	return o.Decision + "." + o.Reason
}

// Matches reports whether a candidate of the decision and reason is one
// of the outcomes o names: any reason when o names the decision alone.
func (o Outcome) Matches(decision, reason string) bool {
	return o.Decision == decision && (o.Reason == "" || o.Reason == reason)
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

// Signature renders the declaration on one line, for messages like
// "approve is declared as: decision approve(bake: duration = 1h) {
// release_manager, payments_sre }".
func (d *Decision) Signature() string {
	return d.head() + " { " + strings.Join(d.Reasons, ", ") + " }"
}

// Source renders the declaration as a kind file writes it, one reason
// per line.
func (d *Decision) Source() string {
	var b strings.Builder
	b.WriteString(d.head() + " {\n")
	for _, r := range d.Reasons {
		b.WriteString("  " + r + "\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// head renders the name and payload fields: `decision approve(bake:
// duration = 1h)`, without parentheses when there are no fields.
func (d *Decision) head() string {
	s := "decision " + d.Name
	if len(d.Fields) == 0 {
		return s
	}
	parts := make([]string, len(d.Fields))
	for i, f := range d.Fields {
		parts[i] = f.Name + ": " + f.Type.String()
		if f.HasDefault {
			parts[i] += " = " + constant.Format(f.Default)
		}
	}
	return s + "(" + strings.Join(parts, ", ") + ")"
}
