// Package types defines Sigil's types: the scalars, `list<T>`,
// `map<K, V>`, `?T`, the struct types and enums a kind declares,
// `decision`, and the candidates an assert reads through
// `outcome.<decision>`. The language reference specifies them at
// https://sigil.specht-labs.de/reference/types/.
//
// Types are values, not names. A kind file or a Go struct is turned into
// these once, and the checker compares them with [Identical]. Structs and
// enums are nominal, so two struct types with the same fields, or two
// enums with the same values, are still different.
//
// The predicates [IsKey], [IsOrdered], [IsEquatable], [IsComparable] and
// [IsCandidates] hold the type rules the operators share, so the checker
// and the kind validator agree on them.
//
// [Invalid] stands for a type that couldn't be worked out. Whoever
// produces it has already reported why, and code that meets it reports
// nothing more, so one mistake is reported once.
package types

// Type is a Sigil type. The implementations are [Basic], [*List], [*Map],
// [*Optional], [*Struct], [*Enum] and [*Candidate]; nothing outside this
// package can add one.
type Type interface {
	// String returns the type as written in a kind file.
	String() string
	isType()
}

// Identical reports whether a and b are the same type. Lists, maps and
// optionals compare by their parameters; structs and enums compare by
// name, since a kind declares each name once, and candidates by their
// decision. A nil type is identical to nothing, itself included.
func Identical(a, b Type) bool {
	if a == nil || b == nil {
		return false
	}
	switch x := a.(type) {
	case Basic:
		y, ok := b.(Basic)
		return ok && x == y
	case *List:
		y, ok := b.(*List)
		return ok && Identical(x.Elem, y.Elem)
	case *Map:
		y, ok := b.(*Map)
		return ok && Identical(x.Key, y.Key) && Identical(x.Value, y.Value)
	case *Optional:
		y, ok := b.(*Optional)
		return ok && Identical(x.Elem, y.Elem)
	case *Struct:
		y, ok := b.(*Struct)
		return ok && x.Name == y.Name
	case *Enum:
		y, ok := b.(*Enum)
		return ok && x.Name == y.Name
	case *Candidate:
		y, ok := b.(*Candidate)
		return ok && x.Decision == y.Decision
	}
	return false
}

// IsKey reports whether t can be a map key. Go's rule applies: the
// comparable types, which in Sigil are the scalars and enums. Structs
// have no equality, and decision can't be data at all, so neither is a
// key.
func IsKey(t Type) bool {
	switch t := t.(type) {
	case Basic:
		return t >= Bool && t <= Timestamp
	case *Enum:
		return true
	}
	return false
}

// IsOrdered reports whether t supports `<`, `<=`, `>` and `>=`. Enums
// don't: their declaration order is only the order they print in.
func IsOrdered(t Type) bool {
	switch t {
	case Int, Float, Duration, Timestamp:
		return true
	}
	return false
}

// IsEquatable reports whether t supports `==` and `!=`: the scalars,
// enums and decision. Lists, maps and structs don't, because a policy
// rarely means "these are identical", and one operator would hide a walk
// over a nested value.
func IsEquatable(t Type) bool {
	switch t := t.(type) {
	case Basic:
		return t != Invalid
	case *Enum:
		return true
	}
	return false
}

// IsComparable reports whether values of t can be compared as elements by
// `in`, the list operators and `has`: the scalars, enums and decision,
// and lists, maps and optionals built from them, which compare
// structurally. A struct, or anything holding one, can't, because structs
// have no equality.
func IsComparable(t Type) bool {
	switch t := t.(type) {
	case Basic:
		return t != Invalid
	case *Enum:
		return true
	case *List:
		return IsComparable(t.Elem)
	case *Map:
		return IsComparable(t.Value)
	case *Optional:
		return IsComparable(t.Elem)
	}
	return false
}
