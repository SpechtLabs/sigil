package types

// Optional is `?T`: a T or absent. Optionals don't nest.
type Optional struct {
	Elem Type // T, never itself an Optional
}

// String implements [Type]. It returns `?T` with T spelled out.
func (o *Optional) String() string { return "?" + o.Elem.String() }

func (*Optional) isType() {}
