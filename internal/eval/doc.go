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
// Nothing is converted or copied on the way in, except that an enum value
// becomes its constant once its enum is checked to declare it: the host's
// Go string can hold anything. A [Scope] maps the names a document
// declares to how the compiled code reads them, and a Frame holds the
// values of one evaluation.
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
// A runtime error, such as an index out of range, integer overflow, a
// host function returning an error or a host value outside its enum,
// unwinds through a recovered panic and comes back from [Run] or
// [Policy.Eval] as a *diag.Error pointing at the expression that failed.
// The error a host function returned is its Cause. A panic raised inside
// a host function isn't recovered and reaches the caller, unless the
// binding sets RecoverHostPanics: then it becomes a runtime error too,
// caused by a [*HostPanic] that keeps the stack.
//
// # Cancellation
//
// [Policy.EvalContext] polls its context before every rule and assert,
// after every host function call, and every few hundred steps of a loop
// over a list or map, so nested quantifiers over a large input stop soon
// after the context is done. A cancellation unwinds through its own
// panic, which no runtime error handler catches: it never turns into an
// assert's failure or a memoized condition, and the evaluation returns
// the context's error with no outcome.
package eval
