package types

// The basic types. Bool through Timestamp are the scalars, in the order
// [ScalarNames] lists them.
const (
	// Invalid is the type of something that couldn't be typed, after the
	// error saying why.
	Invalid Basic = iota
	Bool
	Int       // a signed 64-bit integer
	Float     // a 64-bit IEEE 754 float
	String    // a byte string, compared byte for byte
	Duration  // Go's time.Duration
	Timestamp // Go's time.Time; it has no literal and comes from input only
	// Decision is the closed type of the kind's decision names, and their
	// reasons, used as values. It has no name in source, so no param,
	// input or field can be declared with it.
	Decision
)

var basicNames = [...]string{
	Invalid:   "invalid",
	Bool:      "bool",
	Int:       "int",
	Float:     "float",
	String:    "string",
	Duration:  "duration",
	Timestamp: "timestamp",
	Decision:  "decision",
}

// Basic is a type without parameters: a scalar, `decision` or [Invalid].
// Basic values compare with ==.
type Basic uint8

// Lookup returns the built-in scalar type called name, and false with
// [Invalid] when there's none. `list` and `map` aren't here: they take
// parameters, so the parser builds them. Neither is `decision`, which has
// no name in source.
func Lookup(name string) (Basic, bool) {
	for b := Bool; b <= Timestamp; b++ {
		if basicNames[b] == name {
			return b, true
		}
	}
	return Invalid, false
}

// IsReserved reports whether name is a built-in type name, which a kind
// can't reuse for a struct type: the scalars plus `list` and `map`.
func IsReserved(name string) bool {
	if _, ok := Lookup(name); ok {
		return true
	}
	return name == "list" || name == "map"
}

// ScalarNames returns the names of the built-in scalar types in
// declaration order, the candidates for a "did you mean" on a type name.
// Each call returns a new slice.
func ScalarNames() []string {
	names := make([]string, 0, Timestamp)
	for b := Bool; b <= Timestamp; b++ {
		names = append(names, basicNames[b])
	}
	return names
}

// String implements [Type]. It returns the type's name, and "invalid" for
// a value outside the declared constants.
func (b Basic) String() string {
	if int(b) < len(basicNames) {
		return basicNames[b]
	}
	return "invalid"
}

func (Basic) isType() {}
