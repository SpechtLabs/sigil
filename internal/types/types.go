// Package types defines Sigil's types, as docs/reference/types.md
// specifies them: the scalars, `list<T>`, `map<K, V>`, `?T`, the struct
// types a kind declares, and `decision`.
//
// Types are values, not names. A kind file or a Go struct is turned into
// these once, and the checker compares them with Identical. Structs are
// nominal, so two struct types with the same fields are still different.
package types

// Type is a Sigil type. The implementations are Basic, *List, *Map,
// *Optional and *Struct; nothing outside this package can add one.
type Type interface {
	// String returns the type as written in a kind file.
	String() string
	isType()
}

// Identical reports whether a and b are the same type. Lists, maps and
// optionals compare by their parameters; structs compare by name, since
// a kind declares each name once.
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
	}
	return false
}

// IsKey reports whether t can be a map key. Go's rule applies: the
// comparable types, which in Sigil are the scalars. Structs have no
// equality, and decision can't be data at all.
func IsKey(t Type) bool {
	switch t {
	case Bool, Int, Float, String, Duration, Timestamp:
		return true
	}
	return false
}

// IsOrdered reports whether t supports `<`, `<=`, `>` and `>=`.
func IsOrdered(t Type) bool {
	switch t {
	case Int, Float, Duration, Timestamp:
		return true
	}
	return false
}

// IsEquatable reports whether t supports `==` and `!=`: the scalars.
// Lists, maps and structs don't, because a policy rarely means "these are
// identical", and one operator would hide a walk over a nested value.
func IsEquatable(t Type) bool {
	b, ok := t.(Basic)
	return ok && b != Invalid
}

// IsComparable reports whether values of t can be compared as elements by
// `in`, the list operators and `has`: the scalars, and lists, maps and
// optionals built from them, which compare structurally. A struct, or
// anything holding one, can't, because structs have no equality.
func IsComparable(t Type) bool {
	switch t := t.(type) {
	case Basic:
		return t != Invalid
	case *List:
		return IsComparable(t.Elem)
	case *Map:
		return IsComparable(t.Value)
	case *Optional:
		return IsComparable(t.Elem)
	}
	return false
}
