---
title: Halting by construction
icon: mdi:timer-sand-complete
createTime: 2026/09/24 22:30:00
permalink: /understanding/halting/
---

Sigil's language constructs terminate on finite inputs, provided host functions terminate too. The language has no unbounded loops or recursive policies. The compiler does not currently estimate execution cost or enforce a budget.

A policy engine often runs on every request. Termination alone does not protect that request path from expensive policies. Hosts still need to bound input sizes and review host functions while cost analysis remains on the roadmap.

## What's missing, on purpose

**No loops.** There's no `for`, no `while`, no recursion through data. The only iteration is the quantifiers, `any x in xs: ...` and `all x in xs: ...`, plus the list operators `all in` and `any in`. All of them range over a list that came from the input, a param or a literal, and all of those are finite.

**No recursion.** A `let` can refer to other `let`s, a file can import from other files, and a policy can invoke other policies, but all three graphs must be acyclic. The compiler builds each dependency graph and rejects a cycle with an error pointing at the edge that closes it. Without cycles, every `let` has a finite expansion and every chain of invocations bottoms out, so flattening a policy the way `sigil explain` does always terminates.

**No user-defined functions.** Functions are how most expression languages sneak recursion back in. In Sigil, the only callable things are host functions declared in the kind, like `fn split(string, string) -> list<string>`. The host implements them in Go, and they must be pure: same arguments, same result, no side effects. If a host function hangs, that's a Go bug in the host, reviewed and tested like any other Go code.

**No backtracking regexes.** `matches` uses Go's RE2 engine, which runs in time linear in the input. The pattern must be a literal, so it compiles once when the policy loads, and a catastrophic pattern like `(a+)+$` can't blow up at evaluation time the way it would under a backtracking engine.

## Static cost analysis is planned

Nested quantifiers multiply work. `any a in xs: any b in ys: a == b` may compare every pair, costing `len(xs) * len(ys)`. List membership and distinct-element operators also scan collections, and policy invocations repeat work for each instantiation. Strings, patterns and host functions have costs of their own. A general promise of linear evaluation cost would be wrong.

The proposed analyzer would combine declared collection limits with operator and host-function costs, then reject policies over a host's budget. The declarations and budget API are not designed yet. This is an illustration of a future diagnostic:

```text
payments/production.sigil:21:6: error: estimated worst-case cost 1048576 exceeds budget 100000
   |
21 | when any a in actor.teams: any b in service.owners: a == b {
   |      ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
   = help: nested quantifiers multiply; actor.teams (max 1024) x service.owners (max 1024)
```

The collection limits and diagnostic in that example are illustrative. `DeployApproval` declares no size limits, and `sigil check` does not report estimated costs today.

## What this costs authors

Some things are awkward. There's no way to write a helper that walks a nested structure, compute a running total, or define a small reusable function in the policy itself. When those needs come up, the answer is a host function, which means a Go change and a new kind version.

That's a deliberate trade. A host function is reviewed by the people who own the host, tested in Go, visible in the kind file, and declared pure. A user-defined function in a policy file is reviewed by whoever owns that policy and can do whatever the language allows. For a language whose job is to gate access and approvals, the first is the one we want.

## Where the guarantee has limits

The halting guarantee covers the language. It cannot cover host functions, since those are arbitrary Go. The kind's contract says they must be pure and should be cheap, but the compiler cannot verify either. Hosts are responsible for terminating their functions and controlling any external work.

Future static cost analysis will need explicit limits and function costs. Fuzzing exercises correctness and catches crashes; passing fuzz campaigns does not establish a resource bound. See [Testing and fuzzing](/guides/testing/) for the properties currently checked.
