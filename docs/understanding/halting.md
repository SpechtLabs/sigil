---
title: Halting by construction
icon: mdi:timer-sand-complete
createTime: 2026/09/24 22:30:00
permalink: /understanding/halting/
---

A policy engine often runs on every request, so a policy that never finishes holds a request forever. Sigil rules that out by construction: its language constructs terminate on finite inputs, provided host functions terminate too, and the [design goals](/understanding/design-goals/#when-goals-conflict) rank that guarantee above every other. Termination isn't the whole story, though. It doesn't protect a request path from a policy that finishes eventually but takes too long, and the compiler doesn't estimate execution cost or enforce a budget. The second half of this page covers how a host closes that gap, and why a static budget isn't part of the answer.

## What's missing, on purpose

**No loops.** There's no `for`, no `while`, no recursion through data. The only iteration is the quantifiers, `any x in xs: ...` and `all x in xs: ...`, the filter, `filter x in xs: ...`, and the operators that scan a collection: `in`, `has`, and the list operators `all in`, `any in`, `one in` and `exclusive in`. Each of them walks a list or map that already exists when it runs: part of the input, a param, a literal, a `let` or a host function's return value. None of them can grow the collection it walks, and a filter's result is never longer than the list it filters.

**No recursion.** A `let` can refer to other `let`s, a file can import from other files, and a policy can invoke other policies, but all three graphs must be acyclic: a `let`, import or invocation cycle is a compile error (see [Policy files](/reference/policy-files/)). The compiler builds each dependency graph and rejects a cycle with an error pointing at the edge that closes it:

```text
deploy/fresh.sigil:4:37: error: let `fresh` depends on itself
  |
4 | let settled = release.hotfix or not fresh
  |                                     ^^^^^
  = help: lets form a directed acyclic graph; a let can't depend on itself, even through other lets
```

Without cycles, every `let` has a finite expansion and every chain of invocations bottoms out, so flattening a policy the way `sigil explain` does always terminates. The same reasoning keeps recursive types out of kinds: a type that contains itself could only be read to a fixed depth by a language that can't loop; see [Kinds as contracts](/understanding/kinds/#why-some-types-can-t-be-declared).

**No user-defined functions.** Functions are how most expression languages sneak recursion back in. In Sigil, the only callable things are host functions declared in the kind, like `fn split(string, string) -> list<string>`. The host implements them in Go, and they must be pure: same arguments, same result, no side effects. If a host function hangs, that's a Go bug in the host, reviewed and tested like any other Go code.

**No backtracking regexes.** `matches` uses Go's RE2 engine, which runs in time linear in the input. The pattern must be a literal, so it compiles once when the policy loads, and a catastrophic pattern like `(a+)+$` can't blow up at evaluation time the way it would under a backtracking engine.

## What this costs authors

Some things are awkward. There's no way to write a helper that walks a nested structure, compute a running total, or define a small reusable function in the policy itself. When those needs come up, the answer is a host function, which means a Go change and a new kind version.

The trade is about who reviews the code. A host function is reviewed by the people who own the host, tested in Go, visible in the kind file, and declared pure. A user-defined function in a policy file is reviewed by whoever owns that policy and can do whatever the language allows. For a language whose job is to gate access and approvals, the escape hatch belongs with the host.

## Terminating isn't the same as cheap

Nested quantifiers multiply work. `any a in xs: any b in ys: a == b` may compare every pair, costing `len(xs) * len(ys)`, so two nested quantifiers over lists of size `n` can take `n²` comparisons. List membership and distinct-element operators also compare elements across collections, and policy invocations repeat work for each instantiation. Strings, patterns and host functions have costs of their own. A general promise of linear evaluation cost would be wrong.

So there are two limits, one at run time and one on the host.

**A deadline bounds the time.** `Eval` takes a context, and the loops check it as they go ([Context checks](/reference/evaluation/#context-checks) says where). An input that makes nested quantifiers slow then ends with `context.DeadlineExceeded` and the kind's default, instead of holding the caller. That limits how long one evaluation takes, not the work an input asks for: a slow input still uses the CPU until the deadline. [Bound evaluation time](/guides/handle-errors/#bound-evaluation-time) shows how to set one, and [Strict schema, forgiving data](/understanding/strictness/#every-failure-fails-closed) explains why the result after a deadline doesn't depend on how far the evaluation got.

**The host bounds the inputs.** Work grows only with the sizes of the collections a policy walks, and the host decides those: it decodes the input, binds the params and implements the host functions. A host that caps the lists it accepts and the work its host functions do has bounded every evaluation. Fuzzing doesn't establish a resource bound: it exercises correctness and catches crashes; [What the properties check](/project/contributing/#what-the-properties-check) lists what the fuzzers check.

## Why there's no cost budget

[CEL](https://github.com/google/cel-go) estimates an expression's cost before running it and rejects one over a budget. Sigil doesn't, and doesn't plan to.

A static estimate needs a size for every collection, so every kind would have to declare a maximum length for every list and map, and every host function a cost. Those declarations would be part of the contract, versioned and reviewed like the rest of it, for a number the analyzer then multiplies through nested quantifiers. The estimate is a worst case: two nested quantifiers over lists declared with up to 1,000 elements cost a million comparisons on paper, whatever the input. A budget set below that rejects a reasonable policy; one set above it bounds nothing a deadline doesn't.

A deadline measures the actual evaluation of the actual input, and a realistic policy evaluates in microseconds (see [Performance](/reference/performance/)). Between a host that bounds its inputs and an evaluation that stops at its deadline, a budget would add contract surface without closing a gap.

## Where the guarantee has limits

The halting guarantee covers the language. It can't cover host functions, since those are arbitrary Go. The kind's contract says they must be pure, terminate and not panic, but the compiler can't verify any of that. `Eval` can't interrupt a host function that never returns; it only stops once the function returns after the context is done. A host function that panics crashes the calling goroutine, unless the kind asks for panics to be recovered with `policy.WithRecoverHostPanics()`, in which case the panic becomes a runtime error and the evaluation fails closed. [Recover host panics](/guides/handle-errors/#recover-host-panics) covers when to turn that on.

## Related

- [Strict schema, forgiving data](/understanding/strictness/) covers what a failed evaluation returns.
- [Prior art](/understanding/prior-art/#cel) compares Sigil with CEL, which estimates cost before running.
