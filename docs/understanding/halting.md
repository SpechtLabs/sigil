---
title: Halting by construction
icon: mdi:timer-sand-complete
createTime: 2026/09/24 22:30:00
permalink: /understanding/halting/
---

Every Sigil policy terminates, and the compiler can tell you roughly how long it'll take before it ever runs. That isn't enforced with a timeout or a step counter. The language just doesn't contain the constructs you'd need to write something that loops forever.

A policy engine runs on a request path, often on every request. A policy author who accidentally writes something slow shouldn't be able to take the host down, and a host author shouldn't need to isolate policies in a separate process to protect themselves. So the guarantee has to come from the language, not from runtime defences.

## What's missing, on purpose

**No loops.** There's no `for`, no `while`, no recursion through data. The only iteration is the quantifiers, `any x in xs: ...` and `all x in xs: ...`, plus the list operators `all in` and `any in`. All of them range over a list that came from the input, a param or a literal, and all of those are finite.

**No recursion.** A `let` can refer to other `let`s, a file can import from other files, and a policy can invoke other policies, but all three graphs must be acyclic. The compiler builds each dependency graph and rejects a cycle with an error pointing at the edge that closes it. Without cycles, every `let` has a finite expansion and every chain of invocations bottoms out, so flattening a policy the way `sigil explain` does always terminates.

**No user-defined functions.** Functions are how most expression languages sneak recursion back in. In Sigil, the only callable things are host functions declared in the kind, like `fn split(s: string, sep: string) -> list<string>`. The host implements them in Go, and they must be pure: same arguments, same result, no side effects. If a host function hangs, that's a Go bug in the host, reviewed and tested like any other Go code.

**No backtracking regexes.** `matches` uses Go's RE2 engine, which runs in time linear in the input. The pattern must be a literal, so it compiles once when the policy loads, and a catastrophic pattern like `(a+)+$` can't blow up at evaluation time the way it would under a backtracking engine.

## Cost is bounded, and computable

With those pieces gone, each expression node runs at most once per `when` block evaluation, and the only multipliers are quantifiers over input lists. A policy without nested quantifiers costs policy size times input size. Nested quantifiers multiply: `any a in xs: any b in ys: a == b` costs `len(xs) * len(ys)`, and a third level would multiply again. The nesting depth is fixed in the source, so the cost is always a polynomial the compiler can read off the policy, never something that depends on the data's shape beyond list lengths. (An earlier draft of this design called the cost "linear"; that's too strong, and the [open questions](/project/open-questions/) track it.)

That makes static cost estimation possible, the same trick CEL uses. If the host declares a maximum size for each collection, the compiler can walk the policy and compute a worst-case cost. The host can then set a budget and reject policies that exceed it at load time. Suppose the payments team appended a rule to `payments/production.sigil` that spells out the ownership check by hand:

```text
payments/production.sigil:21:6: error: estimated worst-case cost 1048576 exceeds budget 100000
   |
21 | when any a in actor.teams: any b in service.owners: a == b {
   |      ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
   = help: nested quantifiers multiply; actor.teams (max 1024) x service.owners (max 1024)
```

The collection limits in that example are made up to show the shape; `DeployApproval` doesn't declare any. `sigil check` reports the estimated cost for every policy, so authors see it creep up in review before a budget ever trips.

## What this costs authors

Some things are awkward. There's no way to write a helper that walks a nested structure, compute a running total, or define a small reusable function in the policy itself. When those needs come up, the answer is a host function, which means a Go change and a new kind version.

That's a deliberate trade. A host function is reviewed by the people who own the host, tested in Go, visible in the kind file, and declared pure. A user-defined function in a policy file is reviewed by whoever owns that policy and can do whatever the language allows. For a language whose job is to gate access and approvals, the first is the one we want.

## Where the guarantee has limits

The halting guarantee covers the language. It can't cover host functions, since those are arbitrary Go. The kind's contract says they must be pure and should be cheap, but the compiler can't verify either. A host that binds a function doing a network call has stepped outside the model, and the cost estimate won't know about it.

Static cost also depends on the host declaring collection limits. Without them, the compiler can still prove termination but can only report cost in terms of input sizes, not as a number it can compare against a budget.
