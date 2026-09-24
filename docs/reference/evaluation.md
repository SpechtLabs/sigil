---
title: Evaluation semantics
icon: mdi:cogs
createTime: 2026/09/24 22:30:00
permalink: /reference/evaluation/
---

::: info Draft specification
This page specifies the language as designed. Nothing is implemented yet; see [Open questions](/project/open-questions/).
:::

Evaluation takes a compiled policy and one input value and produces exactly one decision. The rules on this page are the whole algorithm. For why it works this way, read [Why rule order never matters](/understanding/order-independence/).

```mermaid
flowchart LR
  A[Input] --> B[Evaluate every<br/>when block]
  B --> C{Any candidates?}
  C -- yes --> D[Pick highest<br/>precedence]
  C -- no --> E[Kind default]
  D --> F[Result + trace]
  E --> F
```

## The rules

1. Every top-level `when` block is evaluated, independently and in no particular order. That includes the blocks brought in by [policy invocations](#invocation), each with its call's enclosing conditions added.
2. A nested `when` fires only if its own condition and every enclosing condition hold. Nesting is conjunction: `when a { when b { ... } }` behaves like `when a and b { ... }`.
3. Each decision constructor reached in a firing block becomes a candidate carrying its decision name, reason, payload, source position, policy and call chain.
4. The winner is the candidate whose decision ranks highest in the kind's `precedence`.
5. If several candidates share the winning decision, the one with the earliest source position wins. For a rule reached through an invocation, the position is its call site first, then its position in the invoked file.
6. If there are no candidates at all, the result is the kind's `default`.

There's no `else`, no early return and no fall-through. `when not x` expresses the negative case without implying an order between blocks.

Rule order in a file doesn't change which decision wins. It can only matter in rule 5, when two candidates of the same decision carry different payloads.

### Ordering for ties

Rule 5 needs a total order over source positions across files. A candidate's position is the list of positions along its call chain: the call site in the evaluated file, then the call site in the invoked file if that one invokes further, and finally the constructor itself. Two positions compare element by element, by line and then column, like strings compare character by character.

So a policy invoked on line 7 contributes candidates that all sort before a constructor on line 18 of the same file, and after one on line 3. Where a call sits in the file decides where its rules sort, which is what a reader scanning the file would expect.

::: tip Proposed
Comparing whole call chains element by element is proposed here; the design only fixes that the call site comes first and the callee position second. Replacing positional tie-breaking with merge functions declared in the kind, such as the minimum `bake` or the union of `approvers`, is an [open question](/project/open-questions/).
:::

## Invocation

An invocation inside `when` blocks adds the enclosing conditions to every rule of the invoked policy. It's rule 2 again: nesting is conjunction. So this call in `payments.production`:

```sigil
when service.labels["compliance"] != "pci" {
  production(approvers: ["payments-leads"])
}
```

behaves exactly as if `deploy.production`'s rules were pasted inside the block, with `approvers` and `tiers` replaced by their bound values:

```sigil
when service.labels["compliance"] != "pci" {
  when cleared {
    when service.tier == "critical"
      and "release_manager" in actor.roles {
      approve("release_manager")
    }

    when service.tier in ["standard", "internal"]
      and owns_service {
      review("service_owner", approvers: ["payments-leads"])
    }
  }
}
```

Everything else still holds: all candidates are collected, precedence picks the winner, and rule order doesn't matter. An invocation at the top level has no enclosing conditions, so its rules run exactly as they would in the invoked file. [`sigil explain`](/reference/cli/#sigil-explain) prints this flattened view for a whole policy.

A policy invoked twice is instantiated twice. Each instance has its own params, and each candidate records the call chain it came from, for example `payments/production.sigil:14:3 → deploy/production.sigil:16:5`, so the trace tells the instances apart.

## Example

Take `payments.production`, which invokes `deploy.guardrails` and `deploy.production` and adds one approval:

```sigil
policy payments.production: DeployApproval

use deploy.guardrails
use deploy.production
use deploy.common.{cleared}

guardrails(min_soak: 4h)

when service.labels["compliance"] == "pci" {
  production(approvers: ["payments-leads", "security-leads"])
}

when service.labels["compliance"] != "pci" {
  production(approvers: ["payments-leads"])
}

when cleared and "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```

Take an otherwise eligible deploy of a release that has soaked for `2h`, not marked as a hotfix, by a cleared member of `payments-sre`. Two blocks fire: `deploy.guardrails`'s `soak_too_short` deny, since `2h < 4h`, and the team's `payments_sre` approve. With `precedence deny > review > approve` the deny wins. The trace still lists both candidates.

For the same person deploying a release that has soaked for `6h`, to a `standard` service without a `compliance` label, owned by another team, only the team rule fires and the result is `approve("payments_sre", bake: 15m)`.

## Composition

An invocation adds the invoked policy's rules to the candidate pool and nothing else. No rule can remove, override or suppress another rule's candidate, and precedence decides between them. What an invocation's caller does control is when those rules apply: an invocation inside `when` only contributes while the enclosing conditions hold. `when false { guardrails() }` switches off every deny in `guardrails`, and a real condition does the same in subtler ways.

The kind's default isn't protected either: a team rule can turn a no-match default deny into an approve. See [Composition without templating](/understanding/composition/).

### Required policies

The host closes the gap that gating opens. It names the policies every root policy must invoke unconditionally, with `policy.Require` when it loads a policy (see the [Go API](/reference/go-api/#required-policies)). The compiler checks that each required policy is reachable from the root through top-level invocations only, with no `when` anywhere on the path. A gated call fails the build:

```text
payments/production.sigil:10:3: error: deploy.guardrails must be invoked unconditionally
   |
 9 | when service.labels["compliance"] == "pci" {
10 |   guardrails(min_soak: 4h)
   |   ^^^^^^^^^^^^^^^^^^^^^^^^
   = help: the host requires deploy.guardrails for every DeployApproval policy.
           Move the call to the top level.
```

Every candidate a required policy produces is then always in the candidate set, so a required policy's deny can never be outranked. Protection is explicit: the host decides which policies are guardrails, instead of every composed policy being protected implicitly.

Params are still outside the guarantee. A team binds `min_soak` when it invokes `guardrails`, so it can loosen it. Letting a required policy bound its params is an [open question](/project/open-questions/), and so is whether a requirement may be met through a chain of other policies rather than directly in the root file.

## Lets

A `let` has no side effects and host functions are pure, so when a let gets evaluated is unobservable except through runtime errors.

::: tip Proposed
Lets are evaluated lazily, at most once per evaluation, on first use. A let that no firing path reaches never runs, so it can't raise a runtime error.
:::

## Determinism

The same compiled policy and the same input always produce the same result, trace included. The language has no clock, no randomness and no access to the environment. If a rule needs the current time, the host passes it in as an input of type `timestamp`. Replay testing and audit reconstruction only need the input that was evaluated.

Host functions must be pure for this to hold. The language can't check that; it's the host's contract.

## Runtime errors

Static typing removes most failure modes. What's left:

| Error                   | Example                                             |
| ----------------------- | --------------------------------------------------- |
| List index out of range | `actor.roles[5]` on a list of three                 |
| Integer overflow        | `int` or `duration` arithmetic that leaves 64 bits  |
| Host function error     | a bound Go function returns a non-nil `error`       |

A missing map key isn't an error; it yields the zero value. An absent optional isn't an error either, because the compiler already forced a `??`.

A runtime error anywhere aborts the evaluation. `Eval` returns the error together with a result holding the kind's default decision, so a host that fails closed can use the result directly. Since every block is evaluated, the outcome doesn't depend on block order: an input that triggers a runtime error always does.

Work skipped by short-circuiting (`and`, `or`, `??`, quantifiers stopping early, a `when` whose condition is false) never runs and can't raise an error.

## Halting and cost

Evaluation always terminates:

- There are no loops. Quantifiers iterate over finite input lists.
- There's no recursion. `let` bindings, imports and policy invocations must each form a DAG, and cycles are compile errors.
- There are no user-defined functions. Host functions are declared in the kind and must be pure.
- `matches` uses Go's RE2 engine, which runs in linear time.

Evaluation cost grows with policy size times input size. Given a host-declared maximum collection size, the compiler computes a static worst-case cost per policy, as CEL does, and a host can reject policies over a budget. `sigil check` reports the figure; see [CLI & editor tooling](/reference/cli/).

A quantifier nested inside another quantifier multiplies the collection sizes, so the worst case for two nested quantifiers over lists of size `n` is `n²`. The static estimate accounts for that.

::: warning Unspecified
How the budget is expressed, where the maximum collection size gets declared, and what a host function costs are all open. See [Halting by construction](/understanding/halting/) for the reasoning and [Open questions](/project/open-questions/) for what's undecided.
:::

## Concurrency

A compiled policy is immutable. Any number of goroutines may evaluate it at the same time, and replacing a policy at runtime is a pointer swap. See the [Go API](/reference/go-api/).
