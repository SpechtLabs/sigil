package types

// List is `list<T>`.
type List struct {
	Elem Type
}

// String returns `list<T>`.
func (l *List) String() string { return "list<" + l.Elem.String() + ">" }

func (*List) isType() {}
