package eval

import (
	"reflect"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/token"
)

// Cond is one `when` condition on the path to a rule or assert.
type Cond struct {
	Text string // the condition's source, with whitespace collapsed
	Pos  token.Pos
	End  token.Pos
}

// Rule is one decision constructor as compiled: everything about a
// candidate that doesn't depend on the input.
type Rule struct {
	Decision *kind.Decision // the decision the constructor names
	payload  reflect.Type   // the Go payload struct, or nil without a binding
	Reason   string
	Policy   string  // the document the constructor is in
	File     string  // the file that document is in
	Conds    []*Cond // enclosing conditions, outermost first, the invoking blocks' included
	Chain    []Site  // the invocations the rule was reached through, outermost first
	Args     []Arg   // the payload arguments as written, for explain
	fields   []payloadField
	Pos      token.Pos // of the constructor
	End      token.Pos
	rank     int // the decision's position in precedence, or declaration order without one
	rrank    int // the reason's position in the decision's ranking, or 0
}

// Outcome returns the rule's decision and reason as a decision value,
// `approve.release_manager`, which is how `outcome` and a candidate's
// `reason` field read it.
func (r *Rule) Outcome() string { return r.Decision.Name + "." + r.Reason }

// Arg is one payload argument as written, with params replaced by their
// bound values.
type Arg struct {
	Name string // the payload field
	Text string // the argument's source, whitespace collapsed
}

// payloadField is how one payload field's value is produced when the
// rule fires: from its argument's expression, or from the kind's default.
type payloadField struct {
	expr  Expr // nil when the default applies
	value Value
	typ   reflect.Type
	name  string
	index []int
}

// Candidate is a rule that fired, with its payload evaluated. The kind's
// default is a Candidate too, one without a position.
type Candidate struct {
	*Rule
	Payload map[string]any //nolint:emptyinterface // the untyped view of the payload, as Go values
	Typed   Value          // the payload struct, when the kind has a binding; invalid otherwise
}

// fire builds the candidate for r in frame f, evaluating the payload.
// The typed payload is built when the kind has a binding, and the untyped
// view reads its fields back so both show the host's Go types.
func fire(r *Rule, f *Frame) *Candidate {
	c := &Candidate{Rule: r, Payload: make(map[string]any, len(r.fields))}
	if r.payload != nil {
		c.Typed = reflect.New(r.payload).Elem()
	}
	for _, fl := range r.fields {
		v := fl.value
		if fl.expr != nil {
			v = fl.expr(f)
		}
		if r.payload == nil {
			c.Payload[fl.name] = iface(v)
			continue
		}
		fv := c.Typed.FieldByIndex(fl.index)
		fv.Set(convert(v, fl.typ))
		c.Payload[fl.name] = fv.Interface()
	}
	return c
}
