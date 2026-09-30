---
title: Open questions
icon: mdi:help-circle-outline
createTime: 2026/09/24 22:30:00
permalink: /project/open-questions/
---

The language, the Go API and the CLI are implemented. This page is for contributors: it lists the design questions the implementation hasn't answered yet, what the code does today in each case, and what an answer would unblock.

The [roadmap](/project/roadmap/) tracks the planned work. Most questions here block nothing on it; they're refinements someone will run into once real policies push on the edges.

::: tip Weigh in
If you have an opinion on any of these, [open an issue](https://github.com/SpechtLabs/sigil/issues) and reference the heading.
:::

## Assertions

**Blocks:** nothing.

`assert("<reason>", <condition>)` fails the evaluation when its condition is false, and it's the guardrail mechanism for [collecting kinds](/reference/kind-files/#collecting-kinds), which have no deny that outranks a grant. The syntax and semantics are in [Policy files](/reference/policy-files/#assert), [Expressions](/reference/expressions/#decision-values-and-outcome) and [Evaluation semantics](/reference/evaluation/#assertions). Still open:

- **Dynamic text.** A reason is a literal. A failing assert may still want to say which value was wrong, the way a decision's `detail` field does. A named argument, `assert("reason", cond, detail: expr)`, would do it the way constructors do, with the expression evaluated only on failure. Today the parser rejects anything after the condition.
- **Asserts in kind files.** A host protects an assert by putting it in a policy it requires with `policy.Require`, the same way it protects denies. An assert declared in the kind would need no `Require`, but it brings expressions into kind files, which are pure declarations now, and the Go side would have to carry Sigil source in a string to define one. The one invariant that needs no expression, mutual exclusion of outcomes, is already a kind declaration, [`exclusive`](/reference/kind-files/#exclusive). Anything richer waits until `Require` proves too clumsy.
- **Static checks.** One block that constructs two outcomes an `exclusive` set, or an `exclusive in outcome` assert, forbids together always conflicts when the block fires. The checker doesn't reject it today; the conflict only shows up at evaluation. Rejecting that one obvious case is cheap. Anything beyond it would need the solver-style analysis the language otherwise avoids.

## Collecting kinds

**Blocks:** nothing.

A kind that declares `collect all` returns every candidate that fired, not one winner (see [Kind files](/reference/kind-files/#collect) and [Evaluation semantics](/reference/evaluation/#collecting-kinds)). Still open:

- **The result type.** One `Result` with an `Outcome` list serves both modes. `Decision[T].Match` on the result of a `collect all` kind without `precedence` panics at runtime. A separate result type for collecting kinds would make that a compile error in the host.
- **Precedence tiers.** `precedence suspended > {read, write, admin}` would rank groups of decisions. With `collect all`, it would return every candidate in the highest tier that has any, so a `suspended` decision could wipe all grants as a decision rather than an assert failure, and when nothing is suspended every grant comes back. Plain `collect all` without `precedence` is then a single tier. Tiers can be added later without breaking existing kinds.

## Pinned params on required policies

**Blocks:** nothing.

Params with an order, such as durations and numbers, are covered by [bounds](/reference/policy-files/#bounds). Params without one, such as `approvers: list<string>`, aren't: a team can bind an empty list. Options are a `pinned` modifier that forbids overriding entirely, a non-empty requirement, or leaving it to review and CI.

## String literals for host-ordered types

**Blocks:** nothing until [host-ordered types](/project/planned/#host-ordered-types) exist; they're planned, not implemented.

Host-ordered types are designed to decode from JSON through `encoding.TextUnmarshaler` when the Go type implements it. Should a string literal in a policy be parsed into the type the same way, at load time? That would allow `param min_version: Version = "1.4.0"`.

[Enums](/reference/types/#enums) answered the same question the other way: a string literal never converts to an enum, and a policy writes the bare value name. That works because an enum's values are names. A version has no names to write, so the enum answer doesn't carry over.

## Vacuous `all in`

**Blocks:** a lint for it.

The evaluator keeps the math: an empty left side makes `all in` and `exclusive in` true, `any in` and `one in` false, and `all x in []: ...` true. What's open is whether that should stay, and whether the linter should warn on a left side that can be empty.

A Go detail makes this sharper. The example deploy gate computes:

```sigil
let cleared = split(service.labels["regions"], ",") all in actor.regions
```

When the `regions` label is missing, `service.labels["regions"]` is `""`, and Go's `strings.Split("", ",")` returns `[""]`, not an empty list. So a missing label makes `cleared` false, which fails closed. An explicit empty list from some other source would make it vacuously true, which fails open. The same expression shape gives opposite safety properties depending on where the empty value came from. That argues for defining empty-left as false, or at least for a lint on every `all in` whose left side can be empty.

Whatever the answer, it has to cover `exclusive in` and `one in` too, which follow `all in` and `any in`. It also has to cover the quantifier: `all r in actor.roles: r != "admin"` is vacuously true for an empty `roles` list, for the same reason. Defining one as false and not the other would make the two spellings of "every element satisfies" disagree.

## Static cost analysis

**Blocks:** the static cost analysis deliverable on the [roadmap](/project/roadmap/).

A single quantifier costs time linear in its list, but a quantifier nested in another's body costs the product of both list sizes, so `all a in xs: any b in ys: a == b` is quadratic. A static estimate has to account for these products, list membership, repeated invocations and host-function costs. How a kind declares collection sizes, how a host function declares its cost, and what the budget API looks like are all undecided. Neither the compiler nor `sigil check` computes or enforces a cost today. The proposed analyzer is in [Planned designs](/project/planned/#static-cost-analysis), and [Halting by construction](/understanding/halting/) has the background.

## Reasons declared in the kind

**Blocks:** nothing.

The shape is settled. A decision declares its reasons as an inline enum field, `decision deny { reason: not_eligible | soak_too_short }`, every constructor labels one, `deny(reason: soak_too_short)`, and the reason can't name a declared enum type (see [Kind files](/reference/kind-files/#decision), [Decisions](/reference/decisions/#the-reason) and [Why the reason is the decision's own enum](/understanding/decisions/#why-the-reason-is-the-decision-s-own-enum)). What's open:

- **Assert reasons.** They're string literals, because an assert belongs to its policy and the kind has no say in it. Whether they should be declared too, so a typo can't create a new metric series, is open; nothing needs it yet. An enum in the kind would give a declared set an obvious form, but that hands the kind a say over every policy's asserts, which is the coupling string reasons avoid.
- **A lint for unranked reasons.** In a `collect one` kind, two reasons of one decision that the kind doesn't rank are a conflict when both fire. A lint could flag unranked reasons whose branches can overlap, but that's the solver-style analysis the language avoids, so it would only catch identical conditions.

## Invoking the same policy twice

**Blocks:** a lint for it.

A policy may invoke the same policy more than once with different arguments, for example `deploy.regional` once for `eu-1` and once for `us-1`. Each call is a separate instantiation, and each candidate records its call chain, so the trace tells the instances apart. The [`duplicate-invocation`](/reference/lints/) lint catches two calls with identical arguments.

One problem remains, because composition is a union: any rule the invoked policy doesn't scope to its params fires for every call. If `deploy.regional` had `when not in_scope { deny(reason: out_of_region) }`, the `eu-1` call would deny every deploy the `us-1` call was meant to review. Policies meant to be invoked more than once have to scope every rule to their own params, or callers have to gate each call, and nothing enforces either. A lint for unscoped denies in a policy that's invoked twice could.

## Host-layered bases

**Blocks:** nothing.

A host could layer a required policy itself, with something like `policy.Base("deploy.guardrails", params)`, so team files never mention it. [`policy.From`](/reference/bundles/#trusted-sources) already gives the host a trusted source for required policies; a base would add the invocation too. That suits platforms where teams shouldn't see or bind the guardrails' params, and it sidesteps pinned params entirely. It's deferred rather than rejected, because the team file then no longer shows the whole picture, and `sigil explain` would need the host's configuration to print it. When is it worth adding?

## Reasons reserved for required policies

**Blocks:** nothing.

A team can outrank a required policy's candidate by constructing the same decision with a higher-ranked reason, and when the payload carries the action, such as a page's target, that changes what happens without a conflict; see [What the guarantee doesn't cover](/understanding/composition/#what-the-guarantee-doesn-t-cover). The kind could mark reasons that only required, trusted policies may construct, so a team's `page(reason: critical_alert, ...)` becomes a compile error rather than a quiet redirect. It would put trust into the kind file, which today only describes types, and a reserved reason would need a way for the host to say which policies count as trusted when the kind is exported. Is that worth it, or is comparing with the required policy's own evaluation in the host the better answer?

## Non-Go evaluators

**Blocks:** nothing.

Other languages run the Go engine compiled to WebAssembly: `cmd/sigil-wasm` builds the module, and `@spechtlabs/sigil` wraps it for TypeScript (see [WebAssembly module](/reference/wasm/) and [One engine for every host](/understanding/one-engine/)). Host functions still carry their host's semantics: a `split` written in JavaScript is JavaScript's, as the `split` example above is Go's. Still open:

- **A Component Model wrapper.** A WIT interface over the same ops would give Rust, Python and .NET hosts typed bindings from their own tooling instead of JSON over linear memory. It waits for Go to target WASI preview 2; the ABI underneath doesn't change.
- **A Rust crate.** The obvious second binding, on Wasmtime, which can meter fuel and so bound an evaluation's work, not only its time, including a host function that never returns.
- **A binary encoding.** A request that doesn't start with `{` is reserved for one, such as CBOR. Natively, JSON is most of an evaluation's cost through the engine, but the WebAssembly runtime costs more; see [what JSON and WebAssembly cost](/understanding/one-engine/#what-json-and-webassembly-cost). It's worth adding once a host shows the JSON share matters.
- **Versioning the ABI.** The package ships with every release at the release's version, and releases attach the module as the archive `sigil_<version>_wasip1_wasm`. A host that loads a newer module than its binding was built for only learns about it from `sigil_abi_version`. Should the ABI version be part of the package's semver contract, and should a binding refuse a module whose ABI it doesn't know?

## Conflicts in test files

**Blocks:** nothing.

A `*_test.yaml` case expects a decision, an outcome or failing asserts, and a conflict fails all three, so a conflict the kind is meant to catch can only be tested from Go, with `Eval` and `errors.As` on a `*policy.ConflictError` (see [Test a conflict](/guides/test-policies/#test-a-conflict)). A fourth form listing the conflicting candidates would let a policy repository test it without a Go test. What's open is who should own that test: the policy repository, or the host, which declares the `exclusive` sets and decides what a conflict means to its callers.
