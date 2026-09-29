// Package ast declares the syntax tree the parser builds and the later
// stages walk.
//
// The parser in internal/parser produces one [File] per source file. The
// type checker resolves names and types over it, the evaluator compiles
// the checked tree, the linter inspects it, and the formatter in
// internal/format prints it back as canonical source. The tree records
// syntax only: it holds no types and no resolved references, which the
// checker keeps in its own tables keyed by node.
//
// # Node kinds
//
// Every node with a place of its own in the tree implements [Node]; parts
// of a node such as a [MapEntry] or a [Field] don't. The syntax categories
// are separate interfaces, each closed by an unexported marker method so
// that only this package's types satisfy them:
//
//   - [Doc] is a document: [PolicyDoc], [ModuleDoc] or [KindDoc].
//   - [Stmt] is a statement in a policy or module, such as [LetStmt] or
//     [WhenStmt].
//   - [Decl] is a declaration in a kind document, such as [DecisionDecl].
//   - [Type] is a type expression, such as [ListType].
//   - [Expr] is an expression, from an [Ident] or literal up to a
//     [QuantExpr].
//
// A statement or declaration that failed to parse is left out of its
// parent. A `when` condition that failed to parse is replaced by a
// [BadExpr], so the rule and its body are kept. Either way the parser
// reports a diagnostic for it.
//
// # Positions
//
// Every node knows where it came from: Pos is its first character and End
// the position just after its last, so a diagnostic can underline any node.
// Nodes whose extent the parser records directly embed a [Span]; the rest
// derive their range from their children. Literal nodes keep both the
// source text, for the formatter, and the decoded value, for the checker
// and evaluator.
//
// # Operators
//
// [Op] names every prefix, infix and quantifier operator. The constants
// follow the precedence table at https://sigil.specht-labs.de/reference/expressions/.
//
// # Walking and printing
//
// [Inspect] walks an expression tree. [Sprint] renders an expression fully
// parenthesized, [TypeString] renders a type, and [Dump] renders a whole
// file for the parser's golden tests.
package ast
