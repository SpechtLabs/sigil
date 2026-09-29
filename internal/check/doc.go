// Package check is Sigil's type checker. It loads kind documents into a
// [kind.Kind], checks policies and modules against one, gives every
// expression a type, and reports what doesn't fit with a hint. The typing
// rules are specified at https://sigil.specht-labs.de/reference/expressions/ and
// https://sigil.specht-labs.de/reference/types/.
//
// It sits between the parser and the evaluator. [LoadKind] or
// [Checker.KindFile] turns a kind file into the kind model. The bundle
// creates one [Checker] per document, sets its [Resolver], and calls
// [Checker.Policy] or [Checker.Module]. The checker records what it learns
// in an [Info], which is what the evaluator compiles from: by the time
// evaluation starts, every operator already knows the types of its
// operands. [Checker.Exported] is what the checked document offers the
// documents that import or invoke it.
//
// # Kinds
//
// [Checker.Kind] resolves type names, evaluates constant defaults and
// reports what only source can get wrong, such as a declaration given
// twice. Every other rule is [kind.Kind.Validate]'s, so a kind file and a
// kind built from Go types are held to the same ones. The checker records
// where each declaration is, so those findings point at the right line.
//
// # Names
//
// Every document has one flat namespace: the kind's inputs, host
// functions and decisions, and the document's imports, params and lets.
// Nothing shadows anything, and a name taken twice is an error. The one
// exception is a name the kind added after the version a document pins:
// the document keeps its own name, and [Info.Shadows] records it for the
// shadowed-kind-name lint. Let names are unique in a document even across
// `when` bodies, so a trace can name each let. Lets are typed in
// dependency order, whatever their order in source, and a let that reaches
// itself is a cycle.
//
// # Types
//
// Nothing converts implicitly. An empty `[]` or `{}` has no type of its
// own and takes one from its context, such as the other operand or the
// type [Checker.ExprAs] expects; without one it's an error. An optional
// must be unwrapped before anything else touches it. `outcome` and the
// candidates it holds can only be read where [Env.InAssert] is set.
//
// # Diagnostics
//
// The checker doesn't stop at the first error. It collects every
// diagnostic, most with a help line and many with a "did you mean" from
// [Nearest], and [Checker.Errors] returns them in source order. An
// expression that fails is typed [types.Invalid], and nothing that uses it
// reports again.
package check
