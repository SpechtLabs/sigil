package types

// Map is `map<K, V>`. K must be a scalar or an enum; see [IsKey]. As with
// [List], the checker types an empty `{}` with nil parameters until the
// context supplies them.
type Map struct {
	Key   Type // K
	Value Type // V
}

// String implements [Type]. It returns `map<K, V>` with K and V spelled
// out.
func (m *Map) String() string { return "map<" + m.Key.String() + ", " + m.Value.String() + ">" }

func (*Map) isType() {}
