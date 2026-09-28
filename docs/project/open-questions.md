---
title: Open questions
icon: mdi:help-circle-outline
createTime: 2026/09/24 22:30:00
permalink: /project/open-questions/
---

These are the design decisions that aren't settled yet. They're ordered roughly by how much they block implementation: type-checker questions come first, followed by evaluation, composition, tooling, and finally things parked until much later. Settled decisions leave this page; their rules live in the [reference](/reference/grammar/).

Each question notes which [roadmap](/project/roadmap/) milestone it blocks. Where the reference pages already state a rule marked "proposed", the question here is whether to confirm it.

::: tip Weigh in
If you have an opinion on any of these, [open an issue](https://github.com/SpechtLabs/sigil/issues) and reference the heading.
:::

## Assertions

**Blocks: M5, M6** (the remaining points)

`assert("<reason>", <condition>)` fails the evaluation loudly when its condition is false, and it's the guardrail mechanism for [collecting kinds](#collecting-kinds), which have no deny that outranks a grant. The syntax and semantics are in [Policy files](/reference/policy-files/#assert), [Expressions](/reference/expressions/#decision-values-and-outcome) and [Evaluation semantics](/reference/evaluation/#assertions). Still open:

- **Dynamic text.** A reason is a literal. A failing assert may still want to say which value was wrong, the way a decision's `detail` field does. A named argument, `assert("reason", cond, detail: expr)`, would do it the way constructors do, and the expression would only be evaluated on failure. Today an assert takes the reason and the condition only.
- **Asserts in kind files.** Today a host protects an assert by putting it in a policy it requires with `policy.Require`, the same way it protects denies. An assert declared in the kind would need no `Require`, but it brings expressions into kind files, which are pure declarations now, and the Go side would have to carry Sigil source in a string to define one. The one invariant that needs no expression, mutual exclusion of outcomes, is now a kind declaration, [`exclusive`](/reference/kind-files/#exclusive). Anything richer stays deferred until `Require` proves too clumsy.
- **Reason identity.** Whether assert reasons share the metric space with decision reasons, or live on their own. The proposal keeps them apart: `policy_assert_failures_total{reason="sod_customer_dev"}`.
- **Static checks.** The proposal only rejects the obvious case: one block constructing two outcomes an `exclusive` set or an `exclusive in outcome` assert forbids together, since that always conflicts when the block fires. Anything more would need the solver-style analysis the language otherwise avoids.

## Decision values and `outcome`

**Blocks: M5** (the namespace question)

An assert that checks what evaluation decided has to name decisions as values: `[customer_data_writer, development_environment_writer] exclusive in outcome`. A bare decision name is a value of a closed `decision` type and `outcome` a `list<decision>`, and only assert conditions can read either (see [Types](/reference/types/#decision)).

That puts decision names into the policy's flat namespace, which costs something: a `let` named `deny` is a compile error. Adding a decision to a kind stays compatible, because a document pinned to an older version keeps its own name (see [Identifiers](/reference/policy-files/#identifiers)), but the names are still taken from every new policy. The alternatives:

- **String names**, `["customer_data_writer", "development_environment_writer"] exclusive in outcome` with `outcome: list<string>`, and the checker rejecting literals that aren't declared decisions. No namespace change, but the check only works on literals, and a computed string would slip past it.
- **Qualified names**, `decision.customer_data_writer`. No collision, but verbose where it's used most.

Two more gaps:

- **Payloads.** `outcome` holds decisions, not candidates, so an assert can't read a payload: "no `admin` grant with a `ttl` above 8h" isn't expressible. Something like `all g in outcome.admin: g.ttl <= 8h` would need a per-decision view of the candidates.
- **Kinds with `precedence`.** There `outcome` is the winner alone. An assert can't see the losing candidates, which is right for "what will the host do", but means an assert can't check, for example, that a guardrail's deny fired at all when a team's approve won. Contradictions between candidates are the kind's business now, through [`exclusive`](/reference/kind-files/#exclusive), which sees every candidate.

## Collecting kinds

**Blocks: M6** (the remaining points)

A kind that declares `collect all` returns every candidate that fired, not one winner: roles a user can hold at the same time, feature flags, labels to attach. See [Kind files](/reference/kind-files/#collect) and [Evaluation semantics](/reference/evaluation/#collecting-kinds). Still open:

- **The result type.** The proposal keeps one `Result` with an `Outcome` list for both kinds. A separate type for collecting kinds would make `Match` on a collecting result a compile error instead of a runtime panic.
- **Precedence tiers.** `precedence suspended > {read, write, admin}` would rank groups of decisions. With `collect all` it would return every candidate in the highest tier that has any, so a `suspended` decision could wipe all grants as a decision, not as an assert failure, and when nothing is suspended every grant comes back. Plain `collect all` without `precedence` is then a single tier. Tiers are what would make `collect all` with `precedence` useful for roles. The first collecting kinds don't need them, and they can be added later without breaking them.

## Pinned params on required policies

**Blocks: M5**

Params with an order, such as durations and numbers, are covered by [bounds](/reference/policy-files/#bounds). Params without one, such as `approvers: list<string>`, aren't: a team can bind an empty list. Options are a `pinned` modifier that forbids overriding entirely, a non-empty requirement, or leaving it to review and CI.

## String literals for host-ordered types

**Blocks: M4**

[Host-ordered types](/reference/types/#host-ordered-types) decode from JSON through `encoding.TextUnmarshaler` when the Go type implements it. Should a string literal in a policy be parsed into the type the same way, at load time? That would allow `param min_version: Version = "1.4.0"`.

## Vacuous `all in`

**Blocks: M6** (the linter)

The evaluator keeps the math: an empty left side makes `all in` and `exclusive in` true, `any in` and `one in` false, and `all x in []: ...` true. What's open is whether the linter should warn on a left side that can be empty.

An empty list on the left is a subset of anything, so `[] all in actor.regions` is true. Either keep the math and have the linter warn, or define an empty left side as false and document the exception.

A Go detail makes this sharper. The `deploy.production` policy computes:

```sigil
let cleared =
  split(service.labels["regions"], ",") all in actor.regions
```

When the `regions` label is missing, `service.labels["regions"]` is `""`, and Go's `strings.Split("", ",")` returns `[""]`, not an empty list. So a missing label makes `cleared` false, which fails closed. An explicit empty list from some other source would make it vacuously true, which fails open. The same expression shape gives opposite safety properties depending on where the empty value came from. That argues for defining empty-left as false, or at least for a linter warning on every `all in` whose left side can be empty.

Whatever the answer, it has to cover `exclusive in` and `one in` as well, which follow `all in` and `any in` today: an empty left side makes `exclusive in` true and `one in` false. It also has to cover the quantifier: `all r in actor.roles: r != "admin"` is vacuously true for an empty `roles` list, for the same reason. Defining one as false and not the other would make the two spellings of "every element satisfies" disagree.

## Cost of nested quantifiers

**Blocks: M7**

An earlier draft of this design called evaluation cost linear in policy size times input size. That holds for a single quantifier, but a quantifier nested in another's body costs the product of both list sizes, so `all a in xs: any b in ys: a == b` is quadratic. The static cost estimate still works (it multiplies the declared maximum sizes), but the budget has to be expressed in those terms, and the docs shouldn't promise "linear".

## Reasons declared in the kind

**Blocks: M6** (the remaining points)

Reasons are declared per decision in the kind, and constructors name one of them (see [Kind files](/reference/kind-files/#decision) and [Decisions](/reference/decisions/#the-reason)). What's open:

- **Assert reasons.** They stay string literals, because an assert belongs to its policy and the kind has no say in it. Whether they should be declared too, for the same metric reasons, is open; nothing in the design needs it.
- **Unranked reasons in `collect one`.** Allowed, and a conflict when two of them fire. A lint could flag unranked reasons whose branches can overlap, but that's the solver-style analysis the language avoids, so it would only catch identical conditions.

## Checking a base policy on its own

**Blocks: M5, M6**

A missing required param is a compile error. So `deploy.production`, which declares `param approvers: list<string>` without a default, fails `sigil check` unless something binds `approvers`. That's right when a host loads it, and wrong for a policy repo that wants to lint its shared policies in CI before any team invokes them. Options: `sigil check` type-checks unbound params by their declared type and reports them as unbound rather than as errors; or a shared policy is only ever checked through the policies that invoke it. `sigil explain` has the same problem: without bound values it can only print param names.

## Input-dependent invocation arguments

**Blocks: M5**

Invocation arguments may reference constants and the invoking policy's own params, but not inputs. That keeps every invocation a static instantiation: `sigil explain` can print concrete values, and bounds on params (see [Bounds](/reference/policy-files/#bounds)) can be checked at compile time. `production(approvers: service.owners)` would turn a param into a per-evaluation value, which blurs the line between a param and a `let`.

The workaround is a `when` per case, as the PCI split in the canonical example does. Relax the rule only if a real policy needs it.

## Invoking the same policy twice

**Blocks: M5**

A policy may invoke the same policy more than once with different arguments, for example `deploy.regional` once for `eu-1` and once for `us-1`. Each call is a separate instantiation, and each candidate records its call chain, so the trace can tell the instances apart. One problem remains, because composition is a union: any rule the invoked policy doesn't scope fires for every call. If `deploy.regional` had `when not in_scope { deny(out_of_region) }`, the `eu-1` call would deny every deploy the `us-1` call was meant to review. Policies meant to be invoked more than once have to scope every rule to their own params, or callers have to gate each call, and nothing enforces either. A lint for unscoped denies in a policy that's invoked twice could.

## Direct or transitive requirement

**Blocks: M5**

`policy.Require("deploy.guardrails")` makes the compiler check that the root policy reaches `deploy.guardrails` through top-level invocations only. Should the call have to sit in the root file itself, or is an ungated chain through other policies enough?

Direct is easier to read: open the team file and the guardrail call is there. Transitive allows shared "team baseline" policies, such as a `deploy.gate` that invokes the guardrails and the approvals, which a team then invokes in turn. The [Go API](/reference/go-api/#required-policies) currently describes the transitive reading.

## Host-layered bases

**Blocks: nothing yet**

A host could layer a required policy itself, with something like `policy.Base("deploy.guardrails", params)`, so team files never mention it. `policy.From` already gives the host a trusted source for required policies; a base would add the invocation too. That suits platforms where teams shouldn't see or bind the guardrails' params, and it sidesteps pinned params entirely. It's deferred rather than rejected, because the team file then no longer shows the whole picture, and `sigil explain` would need the host's configuration to print it. When is it worth adding?

## Re-exports

**Blocks: M5**

Should a module be able to re-export names it imports, so a team gets one `use` line instead of several? Leaning no, because it hides where names come from, which is exactly what banning wildcard imports protects.

## Parentheses around quantifier bodies

**Blocks: M6** (the formatter)

A quantifier body extends as far right as possible, so

```sigil
any r in actor.roles: r like "sre-*" and release.soak < 1h
```

parses as `any r in actor.roles: (r like "sre-*" and release.soak < 1h)`. That's settled and implemented; see [Quantifiers](/reference/expressions/#quantifiers). The risk is that authors read the line above as two conditions joined by `and` and get a subtly different result. Should `sigil fmt` always add parentheses around a body that contains `and` or `or`, so the extent is visible?

## Input encoding for the CLI

**Blocks: M6**

`sigil eval` and `sigil test` take JSON input, and the `Resolver` supplies values by path. Neither says how a `duration` or `timestamp` is encoded (Go duration strings like `"45m"` and RFC 3339 are the obvious choices), or whether a field missing from the JSON is an error or reads as the zero value. Reading it as zero matches how map keys behave, but it means a typo in a test fixture silently tests the wrong thing.

## Host functions in the CLI

**Blocks: M6**

`sigil eval` and `sigil test` evaluate policies, which means calling the host functions a kind declares. The exported kind file (`deploy_approval.sigil`) carries only their signatures. A standalone `sigil` binary has no implementations for them.

Options: hosts build their own `sigil` binary with their functions linked in (a small `main` package the library provides); a plugin mechanism; or a stub mode where the test file supplies return values for each function call. The `policytest` package for `go test` doesn't have this problem, since it runs inside the host.

## Document names in text output

**Blocks: M6**

Every diagnostic and trace entry carries the document name alongside file, line and column, as a `policy.Position`. Its text form adds the name in parentheses, `policies.sigil:42:5 (payments.production)`, and leaves it out when the file's path matches the name, so the common repository layout keeps short positions. What's left for the CLI is whether the "file holds only that document" half of the rule matters in practice, and whether the CLI should always print the name instead, which is uniform but doubles the length of every position in a one-document-per-file repository.

## Non-Go evaluators

**Blocks: nothing yet**

A WASM build of the evaluator would let other languages evaluate policies, not just type-check them against an exported kind. It's out of scope until the Go library is stable. The main design constraint it adds now is that nothing in the language semantics should depend on Go-specific behaviour that a WASM host couldn't reproduce, and the `split` example above shows that host functions already carry Go semantics with them.
