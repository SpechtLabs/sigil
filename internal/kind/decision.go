package kind

import (
	"slices"
	"strings"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/types"
)

// Decision is a decision constructor: its payload schema and the reasons
// it can be constructed with. Every constructor names one of the reasons
// with `reason:`. Reasons are declared names, not strings, and they're
// scoped to their decision rather than the kind's namespace, so two
// decisions may declare the same reason.
type Decision struct {
	Name    string
	Fields  []*Field // the payload fields, in declaration order, the reason excluded
	Reasons []string // the declared reasons; a set, so their order is only the printing order
	// Ranked holds the reasons in precedence order when the kind ranks
	// them with a scoped `precedence`; nil when it doesn't.
	Ranked []string
}

// HasReason reports whether the decision declares the reason.
func (d *Decision) HasReason(name string) bool { return slices.Contains(d.Reasons, name) }

// ReasonRank returns the reason's position in the decision's ranking,
// from 0 for the highest, or 0 when the kind doesn't rank this decision's
// reasons, so every reason ties. A reason the ranking doesn't name also
// gets 0.
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

// String implements [fmt.Stringer]. It renders the outcome as a kind file
// writes it: `approve` or `approve.release_manager`.
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
// representation (see [constant.Conforms]) when HasDefault is set; a field
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

// Signature describes the declaration on one line, in prose, for a help
// like "approve takes reason: release_manager | payments_sre, and bake:
// duration = 1h", or "deny takes reason: not_eligible" without a payload.
// It's a whole sentence, not kind-file syntax, so nobody copies it into a
// kind file.
func (d *Decision) Signature() string {
	fields := d.body()
	if n := len(fields); n > 1 {
		fields[n-1] = "and " + fields[n-1]
	}
	return d.Name + " takes " + strings.Join(fields, ", ")
}

// Source renders the declaration as a kind file writes it: the reason
// first, then the payload fields in declaration order, one per line,
// ending in a newline.
func (d *Decision) Source() string {
	var b strings.Builder
	b.WriteString("decision " + d.Name + " {\n")
	for _, line := range d.body() {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// body renders the declaration's fields: `reason: a | b`, then each
// payload field as `bake: duration` or `bake: duration = 1h`.
func (d *Decision) body() []string {
	lines := make([]string, 0, 1+len(d.Fields))
	lines = append(lines, "reason: "+strings.Join(d.Reasons, " | "))
	for _, f := range d.Fields {
		line := f.Name + ": " + f.Type.String()
		if f.HasDefault {
			line += " = " + constant.Format(f.Default)
		}
		lines = append(lines, line)
	}
	return lines
}
