package types

// List is `list<T>`, an ordered sequence of values of one type. The
// checker gives an empty `[]` a List with a nil Elem until the context
// supplies T; String panics on that placeholder.
type List struct {
	Elem Type // T
}

// String implements [Type]. It returns `list<T>` with T spelled out.
func (l *List) String() string { return "list<" + l.Elem.String() + ">" }

func (*List) isType() {}
