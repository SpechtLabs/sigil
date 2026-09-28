package ast

import (
	"strings"

	"github.com/spechtlabs/sigil/internal/token"
)

// File is one parsed source file: zero or more documents. A file with a
// parse error still has a File, holding every document and statement that
// did parse, so tools can work on the rest.
type File struct {
	Name string
	Docs []Doc
}

// Doc is a document: a policy, a module or a kind.
type Doc interface {
	Node
	docNode()
}

// Stmt is a statement in a policy or module: use, param, let, when, assert
// or a call.
type Stmt interface {
	Node
	stmtNode()
}

// Decl is a declaration in a kind document.
type Decl interface {
	Node
	declNode()
}

// Type is a type expression: a name, `?T`, `list<T>` or `map<K, V>`.
type Type interface {
	Node
	typeNode()
}

// PolicyName is a dotted name such as `deploy.common`, as written after
// `policy`, `module` and `use`.
type PolicyName struct {
	Parts []*Ident
}

// String joins the parts with dots.
func (n *PolicyName) String() string {
	parts := make([]string, len(n.Parts))
	for i, p := range n.Parts {
		parts[i] = p.Name
	}
	return strings.Join(parts, ".")
}

// Pos returns the start of the first part.
func (n *PolicyName) Pos() token.Pos { return n.Parts[0].Pos() }

// End returns the end of the last part.
func (n *PolicyName) End() token.Pos { return n.Parts[len(n.Parts)-1].End() }

// PolicyDoc is `policy name: Kind` followed by imports and statements. The
// span runs from the header keyword to the end of the last statement.
type PolicyDoc struct {
	Name  *PolicyName
	Kind  *Ident
	Uses  []*UseStmt
	Stmts []Stmt
	Span
}

// ModuleDoc is `module name: Kind` followed by imports and lets.
type ModuleDoc struct {
	Name *PolicyName
	Kind *Ident
	Uses []*UseStmt
	Lets []*LetStmt
	Span
}

// KindDoc is `kind Name version N` followed by declarations.
type KindDoc struct {
	Name    *Ident
	Version *IntLit
	Decls   []Decl
	Span
}

// UseStmt is an import. Exactly one form applies: a whole import binds the
// last path segment or Alias; a selective import binds each Item.
type UseStmt struct {
	Path  *PolicyName
	Alias *Ident        // `use a.b as c`; nil otherwise
	Items []*ImportItem // `use a.b.{x, y as z}`; nil for a whole import
	Span
}

// ImportItem is one name in a selective import, with its alias if any.
type ImportItem struct {
	Name  *Ident
	Alias *Ident
}

// ParamStmt is `param name: type` with an optional `= default`, then
// optional `, min: value` and `, max: value` bounds.
type ParamStmt struct {
	Name    *Ident
	Type    Type
	Default Expr // nil when the param is required
	Min     Expr // nil without a lower bound
	Max     Expr // nil without an upper bound
	Span
}

// LetStmt is `let name = value`, or `pub let name = value` for a let
// other documents may import. The span starts at `pub` when it's there.
type LetStmt struct {
	Name  *Ident
	Value Expr
	Span
	Pub bool
}

// WhenStmt is `when cond { body }`. The body holds WhenStmt, LetStmt,
// AssertStmt and CallStmt nodes. The span runs from `when` to just after `}`.
type WhenStmt struct {
	Cond Expr
	Body []Stmt
	Span
}

// AssertStmt is `assert("reason", cond)`. The span runs from `assert` to
// just after `)`.
type AssertStmt struct {
	Cond   Expr
	Reason *StringLit
	Span
}

// CallStmt is a decision constructor or a policy invocation: a name, an
// optional positional first argument (the reason of a constructor) and
// named arguments. The parser doesn't know which of the two it is; the
// checker decides by the name. The span runs from the name to just after
// `)`.
type CallStmt struct {
	Name       *Ident
	Positional Expr // nil when every argument is named
	Args       []*NamedArg
	Span
}

// NamedArg is `name: value`. The name may be spelled like a keyword.
type NamedArg struct {
	Name  *Ident
	Value Expr
}

// NamedType is a type written as a name: a built-in such as `string` or a
// struct type declared in the kind.
type NamedType struct {
	Name *Ident
}

// OptionalType is `?T`.
type OptionalType struct {
	Elem Type
	QPos token.Pos
}

// ListType is `list<T>`. The span runs from `list` to just after `>`.
type ListType struct {
	Elem Type
	Span
}

// MapType is `map<K, V>`. The span runs from `map` to just after `>`.
type MapType struct {
	Key   Type
	Value Type
	Span
}

// TypeDecl is `type Name { fields }`.
type TypeDecl struct {
	Name   *Ident
	Fields []*Field
	Span
}

// Field is a typed name: a struct field, a function parameter or a decision
// payload field. Only decision fields can carry a Default.
type Field struct {
	Name    *Ident
	Type    Type
	Default Expr
}

// InputDecl is `input name: type`.
type InputDecl struct {
	Name *Ident
	Type Type
	Span
}

// FnDecl is `fn name(types) -> result`. Parameters have types only;
// policies pass arguments positionally.
type FnDecl struct {
	Result Type
	Name   *Ident
	Params []Type
	Span
}

// DecisionDecl is `decision name(reason: string, fields...)`.
type DecisionDecl struct {
	Name   *Ident
	Fields []*Field
	Span
}

// PrecedenceDecl is `precedence a > b > c`.
type PrecedenceDecl struct {
	Names []*Ident
	Span
}

// CollectDecl is `collect one` or `collect all`.
type CollectDecl struct {
	All bool // `collect all`; false is `collect one`
	Span
}

// DefaultDecl is `default deny("reason")`.
type DefaultDecl struct {
	Call *CallStmt
	Span
}

// Pos returns the start of the name.
func (t *NamedType) Pos() token.Pos { return t.Name.Pos() }

// End returns the end of the name.
func (t *NamedType) End() token.Pos { return t.Name.End() }

// Pos returns the position of `?`.
func (t *OptionalType) Pos() token.Pos { return t.QPos }

// End returns the end of the element type.
func (t *OptionalType) End() token.Pos { return t.Elem.End() }

func (*PolicyDoc) docNode() {}
func (*ModuleDoc) docNode() {}
func (*KindDoc) docNode()   {}

func (*UseStmt) stmtNode()    {}
func (*ParamStmt) stmtNode()  {}
func (*LetStmt) stmtNode()    {}
func (*WhenStmt) stmtNode()   {}
func (*AssertStmt) stmtNode() {}
func (*CallStmt) stmtNode()   {}

func (*NamedType) typeNode()    {}
func (*OptionalType) typeNode() {}
func (*ListType) typeNode()     {}
func (*MapType) typeNode()      {}

func (*TypeDecl) declNode()       {}
func (*InputDecl) declNode()      {}
func (*FnDecl) declNode()         {}
func (*DecisionDecl) declNode()   {}
func (*PrecedenceDecl) declNode() {}
func (*CollectDecl) declNode()    {}
func (*DefaultDecl) declNode()    {}
