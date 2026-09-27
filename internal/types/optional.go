package types

// Optional is `?T`: a T or absent. Optionals don't nest.
type Optional struct {
	Elem Type
}

// String returns `?T`.
func (o *Optional) String() string { return "?" + o.Elem.String() }

func (*Optional) isType() {}
