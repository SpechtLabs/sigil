package types

import "strings"

// Candidate is one candidate of a decision as an assert reads it through
// `outcome.<decision>`: the decision's payload fields and its reason.
// It has no equality, and a list of candidates can't be indexed, because
// the order of candidates isn't part of the outcome; the checker only
// lets a policy range over one and read the fields of its elements.
type Candidate struct {
	Decision string   // the decision's name
	Fields   []*Field // the payload fields in declaration order, then `reason`
}

// NewCandidate returns the candidate type of a decision with the given
// payload fields. It adds `reason`, a decision value, which a kind can't
// declare as a payload field. The payload slice is copied; the fields
// themselves are shared.
func NewCandidate(decision string, payload []*Field) *Candidate {
	fields := append(append([]*Field{}, payload...), &Field{Name: "reason", Type: Decision})
	return &Candidate{Decision: decision, Fields: fields}
}

// String implements [Type]. It returns `review candidate` for a candidate
// of `review`, the name type errors use.
func (c *Candidate) String() string { return c.Decision + " candidate" }

// Field returns the field called name, or nil.
func (c *Candidate) Field(name string) *Field {
	for _, f := range c.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// FieldNames returns the field names in declaration order, joined with
// commas, for messages
// like "a review candidate has: approvers, reason".
func (c *Candidate) FieldNames() string {
	names := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		names[i] = f.Name
	}
	return strings.Join(names, ", ")
}

func (*Candidate) isType() {}

// IsCandidates reports whether t is a candidate or holds one, which is
// what may only be ranged over or read field by field.
func IsCandidates(t Type) bool {
	switch t := t.(type) {
	case *Candidate:
		return true
	case *List:
		return IsCandidates(t.Elem)
	case *Map:
		return IsCandidates(t.Value)
	case *Optional:
		return IsCandidates(t.Elem)
	}
	return false
}
