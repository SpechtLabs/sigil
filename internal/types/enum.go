package types

import (
	"slices"
	"strings"
)

// Enum is a named set of values a kind declares, like
// `enum Tier: critical | standard | internal`. Enums are nominal: two
// enums with the same values are still different types, and neither is a
// string. Values holds the names in declaration order, which is the order
// the kind prints them in; enums aren't ordered, so it means nothing else.
type Enum struct {
	Name   string
	Values []string
}

// Has reports whether the enum declares the value.
func (e *Enum) Has(v string) bool { return slices.Contains(e.Values, v) }

// String implements [Type]. It returns the enum's name.
func (e *Enum) String() string { return e.Name }

// ValueNames returns the values in declaration order, joined with commas,
// for messages like "Tier declares: critical, standard, internal".
func (e *Enum) ValueNames() string { return strings.Join(e.Values, ", ") }

func (*Enum) isType() {}
