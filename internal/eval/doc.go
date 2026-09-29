// Package eval compiles checked Sigil documents into closures over a Go
// host's data, and evaluates them.
//
// It is the last stage of the pipeline. The parser builds the tree, the
// checker types it and records what it learned in its Info, and eval
// compiles the result once, so an evaluation does no name lookup and no
// type dispatch. Package bundle drives it for a set of documents; tests
// and fuzzers compile single expressions with [Compile].
//
// # Expressions
//
// [Compile] turns an expression into an [Expr], a closure over a [Frame],
// using the types the checker recorded to pick each operator's
// implementation up front. Values are reflect.Values over the host's own
// Go data: an input's field is read through the index path the binding
// recorded, a list is indexed in place, a map is looked up in place.
// Nothing is converted or copied on the way in. A [Scope] maps the names
// a document declares to how the compiled code reads them, and a Frame
// holds the values of one evaluation.
//
// # Policies
//
// [CompilePolicy] compiles a root policy with every policy it invokes
// instantiated inside it, its params bound, and every document it imports
// linked through a [Linker]. The resulting [Policy] is immutable and safe
// for concurrent use. [Policy.Eval] walks it in three phases, input
// asserts, rules, outcome asserts, and resolves the candidates the rules
// produced into an [Outcome] by the rules at
// https://sigil.specht-labs.de/reference/evaluation/:
// equal candidates fold, exclusive sets are checked, and the top rank is
// what the host gets. Each instance gets a frame of its own per
// evaluation, in which a let is evaluated on first read and at most once,
// and a `when` condition the phases share is evaluated once.
//
// With [Options.Static] set, CompilePolicy compiles the structure only,
// for sigil explain: [Policy.Rules] and [Policy.Asserts] work without a
// binding or host functions, and [Policy.Eval] refuses to run.
//
// # Runtime errors
//
// A runtime error, such as an index out of range, integer overflow or a
// host function returning an error, unwinds through a recovered panic and
// comes back from [Run] or [Policy.Eval] as a *diag.Error pointing at the
// expression that failed. Any other panic, such as one raised inside a
// host function, isn't recovered and reaches the caller.
package eval
