package ast

// Inspect walks the expression tree under x in depth-first order, calling
// fn for every node, x included, before its children. When fn returns false
// the node's children are skipped. The walk visits the [Ident] after a `.`
// and the variable a quantifier or filter binds, as well as operands. A nil
// x is not visited. Statements aren't walked; callers start from an
// expression.
func Inspect(x Expr, fn func(Expr) bool) {
	if x == nil || !fn(x) {
		return
	}
	switch x := x.(type) {
	case *ListLit:
		for _, e := range x.Elems {
			Inspect(e, fn)
		}
	case *MapLit:
		for _, e := range x.Entries {
			Inspect(e.Key, fn)
			Inspect(e.Value, fn)
		}
	case *ParenExpr:
		Inspect(x.X, fn)
	case *UnaryExpr:
		Inspect(x.X, fn)
	case *BinaryExpr:
		Inspect(x.X, fn)
		Inspect(x.Y, fn)
	case *SelectorExpr:
		Inspect(x.X, fn)
		Inspect(x.Sel, fn)
	case *IndexExpr:
		Inspect(x.X, fn)
		Inspect(x.Index, fn)
	case *CallExpr:
		Inspect(x.Fun, fn)
		for _, a := range x.Args {
			Inspect(a, fn)
		}
	case *QuantExpr:
		Inspect(x.Var, fn)
		Inspect(x.Range, fn)
		Inspect(x.Body, fn)
	case *FilterExpr:
		Inspect(x.Var, fn)
		Inspect(x.Range, fn)
		Inspect(x.Body, fn)
	}
}
