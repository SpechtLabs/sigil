package types

// Map is `map<K, V>`.
type Map struct {
	Key   Type
	Value Type
}

// String returns `map<K, V>`.
func (m *Map) String() string { return "map<" + m.Key.String() + ", " + m.Value.String() + ">" }

func (*Map) isType() {}
