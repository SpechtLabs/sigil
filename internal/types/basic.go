package types

// The basic types. Decision is the closed type of decision names used as
// values; it has no literal and no name in source.
const (
	Invalid Basic = iota
	Bool
	Int
	Float
	String
	Duration
	Timestamp
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

// Basic is a type without parameters: a scalar or `decision`.
type Basic uint8

// Lookup returns the built-in scalar type called name. `list` and `map`
// aren't here: they take parameters, so the parser builds them.
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

// String returns the type's name.
func (b Basic) String() string {
	if int(b) < len(basicNames) {
		return basicNames[b]
	}
	return "invalid"
}

func (Basic) isType() {}
