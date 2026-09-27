package ast

import (
	"fmt"
	"strconv"
	"strings"
)

// Sprint renders an expression with every operator application in
// parentheses, so the tree's shape is visible: `a or b and c` prints as
// `(a or (b and c))`. It's for tests and debugging; the formatter will
// produce canonical source instead.
func Sprint(n Node) string {
	var b strings.Builder
	sprint(&b, n)
	return b.String()
}

func sprint(b *strings.Builder, n Node) {
	if n == nil {
		b.WriteString("<nil>")
		return
	}

	if text, ok := leaf(n); ok {
		b.WriteString(text)
		return
	}

	switch n := n.(type) {
	case *ListLit:
		b.WriteByte('[')
		sprintList(b, n.Elems)
		b.WriteByte(']')

	case *MapLit:
		b.WriteByte('{')
		for i, e := range n.Entries {
			if i > 0 {
				b.WriteString(", ")
			}
			sprint(b, e.Key)
			b.WriteString(": ")
			sprint(b, e.Value)
		}
		b.WriteByte('}')

	case *ParenExpr:
		b.WriteByte('(')
		sprint(b, n.X)
		b.WriteByte(')')

	case *UnaryExpr:
		b.WriteByte('(')
		b.WriteString(n.Op.String())
		if n.Op == OpNot {
			b.WriteByte(' ')
		}
		sprint(b, n.X)
		b.WriteByte(')')

	case *BinaryExpr:
		b.WriteByte('(')
		sprint(b, n.X)
		b.WriteByte(' ')
		b.WriteString(n.Op.String())
		b.WriteByte(' ')
		sprint(b, n.Y)
		b.WriteByte(')')

	case *SelectorExpr, *IndexExpr, *CallExpr:
		sprintPostfix(b, n)

	case *QuantExpr:
		b.WriteByte('(')
		b.WriteString(n.Op.String())
		b.WriteByte(' ')
		b.WriteString(n.Var.Name)
		b.WriteString(" in ")
		sprint(b, n.Range)
		b.WriteString(": ")
		sprint(b, n.Body)
		b.WriteByte(')')

	default:
		fmt.Fprintf(b, "<%T>", n)
	}
}

// sprintPostfix renders the forms that bind tightest: field access,
// indexing and calls.
func sprintPostfix(b *strings.Builder, n Node) {
	if n == nil {
		return
	}
	switch n := n.(type) {
	case *SelectorExpr:
		sprint(b, n.X)
		b.WriteByte('.')
		b.WriteString(n.Sel.Name)

	case *IndexExpr:
		sprint(b, n.X)
		b.WriteByte('[')
		sprint(b, n.Index)
		b.WriteByte(']')

	case *CallExpr:
		sprint(b, n.Fun)
		b.WriteByte('(')
		sprintList(b, n.Args)
		b.WriteByte(')')
	}
}

// leaf returns the source text of a node without children.
func leaf(n Node) (string, bool) {
	if n == nil {
		return "", false
	}

	switch n := n.(type) {
	case *BadExpr:
		return "<error>", true
	case *Ident:
		return n.Name, true

	case *IntLit:
		return n.Text, true

	case *FloatLit:
		return n.Text, true

	case *DurationLit:
		return n.Text, true

	case *StringLit:
		return n.Text, true

	case *BoolLit:
		return strconv.FormatBool(n.Value), true

	case *Outcome:
		return "outcome", true

	default:
		return "", false
	}
}

func sprintList(b *strings.Builder, xs []Expr) {
	for i, x := range xs {
		if i > 0 {
			b.WriteString(", ")
		}
		sprint(b, x)
	}
}
