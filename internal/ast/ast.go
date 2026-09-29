// Package ast declares the syntax tree the parser builds and the later
// stages walk.
//
// Every node knows where it came from: Pos is its first character and End
// the position just after its last, so a diagnostic can underline any node.
// Literal nodes keep both the source text, for the formatter, and the decoded
// value, for the checker and evaluator.
package ast

import (
	"time"

	"github.com/spechtlabs/sigil/internal/token"
)

// Node is anything with a position in the source.
type Node interface {
	Pos() token.Pos
	End() token.Pos
}

// Expr is an expression node.
type Expr interface {
	Node
	exprNode()
}

// Span is the source range of a node whose extent the parser records
// directly, such as a literal or a bracketed construct. Nodes built from
// children derive their range from them instead.
type Span struct {
	From token.Pos
	To   token.Pos
}

// Pos returns the start of the span.
func (s Span) Pos() token.Pos { return s.From }

// End returns the position just after the span.
func (s Span) End() token.Pos { return s.To }

// BadExpr stands in for an expression that failed to parse, so the nodes
// around it can still be built. A tree containing one always comes with a
// diagnostic.
type BadExpr struct {
	Span
}

// Ident is a name: an input, param, let, function, decision, quantifier
// variable, or a field after `.`. Fields may be spelled like keywords.
type Ident struct {
	Name string
	Span
}

// IntLit is an integer literal.
type IntLit struct {
	Text string
	Span
	Value int64
}

// FloatLit is a float literal.
type FloatLit struct {
	Text string
	Span
	Value float64
}

// DurationLit is a duration literal such as `1h30m`.
type DurationLit struct {
	Text string
	Span
	Value time.Duration
}

// StringLit is a double-quoted or raw string literal. Text is the source
// including the quotes; Value is the decoded string.
type StringLit struct {
	Text  string
	Value string
	Span
	Raw bool // backticks rather than double quotes
}

// BoolLit is `true` or `false`.
type BoolLit struct {
	Span
	Value bool
}

// Outcome is the `outcome` keyword used as a value.
type Outcome struct {
	Span
}

// ListLit is `[a, b, c]`. The span runs from `[` to just after `]`.
type ListLit struct {
	Elems []Expr
	Span
}

// MapLit is `{k: v, ...}`. The span runs from `{` to just after `}`.
type MapLit struct {
	Entries []MapEntry
	Span
}

// MapEntry is one `key: value` pair of a MapLit.
type MapEntry struct {
	Key   Expr
	Value Expr
}

// ParenExpr is a parenthesized expression. It's kept in the tree so the
// formatter can reproduce it and so the checker sees exactly what the author
// wrote.
type ParenExpr struct {
	X Expr
	Span
}

// UnaryExpr is a prefix operator applied to an operand: `not x` or `-x`.
type UnaryExpr struct {
	X     Expr
	OpPos token.Pos
	Op    Op
}

// BinaryExpr is an infix operator with two operands. OpPos is the position
// of the operator's first token.
type BinaryExpr struct {
	X     Expr
	Y     Expr
	OpPos token.Pos
	Op    Op
}

// SelectorExpr is a field access `x.name`, or a qualified name such as
// `common.cleared`. Optional marks `x?.name`, which reads the field of an
// optional struct and makes the rest of the chain absent when x is.
type SelectorExpr struct {
	X        Expr
	Sel      *Ident
	Optional bool
}

// IndexExpr is `x[i]`. Rbrack is the position just after `]`.
type IndexExpr struct {
	X      Expr
	Index  Expr
	Rbrack token.Pos
}

// CallExpr is a host function call `f(a, b)` inside an expression. Fun is
// the callee as written; the checker requires it to name a host function.
// Rparen is the position just after `)`.
type CallExpr struct {
	Fun    Expr
	Args   []Expr
	Rparen token.Pos
}

// QuantExpr is a quantifier `any x in xs: body` or `all x in xs: body`.
type QuantExpr struct {
	Var      *Ident
	Range    Expr
	Body     Expr
	QuantPos token.Pos
	Op       Op // OpAny or OpAll
}

// FilterExpr is a filter `filter x in xs: body`: the elements of xs for
// which body holds, in their order.
type FilterExpr struct {
	Var       *Ident
	Range     Expr
	Body      Expr
	FilterPos token.Pos
}

// Pos returns the start of the operator.
func (x *UnaryExpr) Pos() token.Pos { return x.OpPos }

// End returns the end of the operand.
func (x *UnaryExpr) End() token.Pos { return x.X.End() }

// Pos returns the start of the left operand.
func (x *BinaryExpr) Pos() token.Pos { return x.X.Pos() }

// End returns the end of the right operand.
func (x *BinaryExpr) End() token.Pos { return x.Y.End() }

// Pos returns the start of the operand.
func (x *SelectorExpr) Pos() token.Pos { return x.X.Pos() }

// End returns the end of the selected name.
func (x *SelectorExpr) End() token.Pos { return x.Sel.End() }

// Pos returns the start of the indexed operand.
func (x *IndexExpr) Pos() token.Pos { return x.X.Pos() }

// End returns the position just after `]`.
func (x *IndexExpr) End() token.Pos { return x.Rbrack }

// Pos returns the start of the callee.
func (x *CallExpr) Pos() token.Pos { return x.Fun.Pos() }

// End returns the position just after `)`.
func (x *CallExpr) End() token.Pos { return x.Rparen }

// Pos returns the start of the `any` or `all` keyword.
func (x *QuantExpr) Pos() token.Pos { return x.QuantPos }

// End returns the end of the body.
func (x *QuantExpr) End() token.Pos { return x.Body.End() }

// Pos returns the start of the `filter` keyword.
func (x *FilterExpr) Pos() token.Pos { return x.FilterPos }

// End returns the end of the body.
func (x *FilterExpr) End() token.Pos { return x.Body.End() }

func (*BadExpr) exprNode()      {}
func (*Ident) exprNode()        {}
func (*IntLit) exprNode()       {}
func (*FloatLit) exprNode()     {}
func (*DurationLit) exprNode()  {}
func (*StringLit) exprNode()    {}
func (*BoolLit) exprNode()      {}
func (*Outcome) exprNode()      {}
func (*ListLit) exprNode()      {}
func (*MapLit) exprNode()       {}
func (*ParenExpr) exprNode()    {}
func (*UnaryExpr) exprNode()    {}
func (*BinaryExpr) exprNode()   {}
func (*SelectorExpr) exprNode() {}
func (*IndexExpr) exprNode()    {}
func (*CallExpr) exprNode()     {}
func (*QuantExpr) exprNode()    {}
func (*FilterExpr) exprNode()   {}
