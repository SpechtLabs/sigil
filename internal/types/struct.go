package types

import "strings"

// Struct is a named struct type declared in a kind, with its fields in
// declaration order.
type Struct struct {
	Name   string
	Fields []*Field
}

// Field is one field of a Struct.
type Field struct {
	Type Type
	Name string
}

// String returns the struct's name.
func (s *Struct) String() string { return s.Name }

// Field returns the field called name, or nil.
func (s *Struct) Field(name string) *Field {
	for _, f := range s.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// FieldNames returns the field names in declaration order, for messages
// like "Service declares: name, tier, owners, labels".
func (s *Struct) FieldNames() string {
	names := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		names[i] = f.Name
	}
	return strings.Join(names, ", ")
}

func (*Struct) isType() {}
