---
title: Open questions
icon: mdi:help-circle-outline
createTime: 2026/09/24 22:30:00
permalink: /project/open-questions/
---

These are the design decisions that aren't settled yet. They're ordered roughly by how much they block implementation: questions that change the grammar come first, because the parser (M2) can't start until they're answered, followed by type-checker questions, evaluation, composition, tooling, and finally things parked until much later.

Each question notes which [roadmap](/project/roadmap/) milestone it blocks. Where the reference pages already state a rule marked "proposed", the question here is whether to confirm it.

::: tip Weigh in
If you have an opinion on any of these, [open an issue](https://github.com/SpechtLabs/sigil/issues) and reference the heading.
:::

## Boolean operators

**Blocks: M2**

The docs use `and`/`or`/`not` because they read better across multi-line conditions:

```sigil
when service.tier in tiers
  and owns_service {
  review("service_owner", approvers: approvers)
}
```

`&&`/`||`/`!` would match Go and filt-rs, which is what host authors write every day. The word forms also pair naturally with `not in`, which would otherwise be `!in` or stay a word while `!` is a symbol, a mix that reads badly.

Options: keep the words; switch to symbols; accept both and have `sigil fmt` canonicalize to one. Accepting both is the worst of the three for a language that wants one canonical style, so the real choice is between the first two. Current lean: words.

## Quantifier body extent

**Blocks: M2**

A quantifier like `any r in actor.roles: r like "sre-*"` needs a rule for where its body ends. The proposed rule in [Expressions](/reference/expressions/) says the body extends as far right as possible, so

```sigil
any r in actor.roles: r like "sre-*" and release.soak < 1h
```

parses as `any r in actor.roles: (r like "sre-*" and release.soak < 1h)`. To end the body earlier you parenthesize the quantifier.

That's how lambdas work in most languages, and it's easy to implement in a Pratt parser. The risk is that authors read the line above as two conditions joined by `and` and get a subtly different result. An alternative is to require parentheses around every quantifier body, which is noisier but impossible to misread. A middle ground is the proposed rule plus a formatter that always adds parentheses when the body contains `and` or `or`.

## Chained comparisons

**Blocks: M2**

The proposed rule makes all precedence-level-4 operators (`==`, `<`, `in`, `has`, `like` and the rest) non-associative, so `a < b < c` and `x in xs == true` are compile errors instead of parsing as `(a < b) < c`.

Confirming this costs nothing at parse time and prevents a class of bug where a comparison chain type-checks by accident (a `bool` compared to a `bool`). The only open part is whether the error message should suggest `a < b and b < c` for the numeric case.

## Keywords as field names

**Blocks: M2**

Hosts that model Kubernetes objects, or anything else with a `type` or `kind` field, will declare struct fields whose names are Sigil keywords. A policy that reads a field named `kind` or `type` is natural, but `kind` also opens a kind file and `type` declares a struct. [Lexical structure](/reference/lexical/) proposes allowing any keyword wherever only a field or payload name can appear: after `.`, inside `type` bodies, in decision fields and in named arguments. Go struct tags can produce any name, so the alternative (reject keyword field names in `NewKind`) would push renames onto hosts for no benefit to readers.

A related lexer corner: in type position, `map<string, int>= {}` has to split `>=` into `>` and `=`. The reference proposes doing that only where a type is being parsed.

## Glob syntax for `like`

**Blocks: M2, M3**

`like` is described as a glob, but no page pins down which glob. The reference proposes only `*` (any run of characters) and `?` (one character), and doesn't yet say whether `*` crosses `/` or `.` the way it does in shell globs versus path globs. Labels such as `platform.example.com/env` make that choice visible: `"platform.*"` matches it only if `*` crosses both. Proposal: `*` matches anything, including separators, because Sigil matches strings, not paths.

## Multiple decisions per block

**Blocks: M2, M4**

Should a `when` body be allowed to contain several decision constructors?

```sigil
when not eligible {
  deny("not_eligible")
  deny("audit_flag")
}
```

Allowing it is more general and costs nothing in the evaluator, since each constructor just becomes another candidate. Requiring exactly one decision per block keeps traces simpler, because every block maps to at most one candidate, and it's hard to think of a case two separate `when` blocks can't express. Nested `when` blocks are unaffected either way.

## Scoped `let`

**Blocks: M2, M5**

Only top-level `let` exists for now. Block-scoped bindings would help deeply nested rules that compute the same sub-expression in several inner blocks:

```sigil
when active {
  let sre = any r in actor.roles: r like "sre-*"
  when sre and release.hotfix { approve("sre_hotfix") }
  when sre and not release.hotfix { review("sre_change", approvers: approvers) }
}
```

The cost is scoping rules: shadowing (which the flat namespace currently forbids), whether a scoped binding can be imported, and how the trace names it. Top-level `let` plus nesting covers every example written so far, so this stays closed unless real policies need it.

## Pinned params on required policies

**Blocks: M5** (and the grammar of `param`, so it touches M2)

A team invoking `guardrails(min_soak: 0s)` switches the `soak_too_short` deny off, even though the host requires `deploy.guardrails`. `policy.Require` guarantees the guardrails' candidates are always in the set; it says nothing about the values they compare against. [Composition without templating](/understanding/composition/) explains why the union-of-candidates guarantee doesn't cover params.

Two places could hold the bound:

```sigil
param min_soak: duration = 24h min 1h
```

```go
policy.Require("deploy.guardrails", policy.Min("min_soak", time.Hour))
```

Bounds in the policy keep the contract visible in the file, next to the rule that reads the param. Bounds set by the host keep it with whoever owns the guardrail, and don't need grammar. Either way, bounds are checked at compile time, since invocation arguments are constants; for params bound from Go with `policy.Params`, `Load` checks them when binding. Other options: a `pinned` modifier that forbids overriding entirely, or leaving it to review and CI. Bounds only make sense for ordered types (`int`, `float`, `duration`), so lists like `approvers` would need a different mechanism, if any.

## Optional structs

**Blocks: M3**

A Go pointer field becomes `?T` in the kind, and a `?T` has to be unwrapped with `??` before use. That works for scalars: a `?string` field unwraps with `field ?? ""`. It doesn't work for structs, because Sigil has no struct literal to put on the right of `??`. A Go host with a `*Release` field produces an input policies can't read at all.

Options:

- **Optional chaining**: `release?.soak` yields `?duration`, which then unwraps normally with `??`. Familiar from TypeScript and Kotlin, and composes cleanly.
- **Presence test with narrowing**: `when release != none { ... }` (or `present(release)`) and inside that block the compiler treats `release` as `Release`. More powerful, but flow-sensitive typing is a big step up in type-checker complexity.
- **Forbid pointer-to-struct** in `NewKind`, so hosts model absence with a flag field instead. Simplest, but it pushes awkwardness onto every host that already has pointer structs.

Optional chaining looks like the smallest change that works.

## Composite values: equality, ordering and recursion

**Blocks: M3**

The current design says comparisons are strictly typed but doesn't say which types support which comparisons. Unsettled:

- Does `==` work on lists, maps and structs? Structural equality is easy to define, but it's rarely what a policy wants and it hides cost in a single operator.
- Can strings be ordered with `<`? Byte-wise ordering is well defined in Go, but `"v10" < "v9"` is true, which is the kind of result that makes a version rule wrong.
- May kind types be recursive? Go allows `type Node struct { Parent *Node }`. Since policies can't loop or recurse, a recursive type is readable only to a fixed depth, which suggests `NewKind` should reject it.

## What the kind version means

**Blocks: M3, M8**

A kind carries `version 1`, but a policy or module header names only the kind (`policy deploy.production: DeployApproval`). Nothing says what happens when a policy written against version 1 compiles against version 2. The number feeds `sigil breaking`, and beyond that its role is undefined. Options: policies pin a version (`: DeployApproval@1`) and the loader rejects mismatches; or the version is informational and compatibility is decided by type-checking alone.

Two neighbouring gaps in the versioning table belong here too. Adding an input can collide with an existing policy name (see [Namespaces and imports](#namespaces-and-imports)). Adding a `fn` doesn't break any policy, but it does break every service that evaluates through `LoadKind`, because `Eval` refuses to run until all declared functions are bound. "Compatible" needs to say compatible for whom.

## `in` versus `has` for map keys

**Blocks: M3, M6**

Two operators test for a map key: `"env" in service.labels` and `service.labels has "env"`. They mean the same thing. `has` exists because it also takes a map of pairs (`labels has {"env": "dev"}`), and the single-key form fell out of that.

Options: keep both as synonyms; drop the single-key form of `has`; or keep both in the grammar and have `sigil fmt` rewrite one into the other. Two spellings for one check is exactly the kind of drift a canonical formatter should prevent, so the formatter option is the likely answer. Which form wins is still open.

## Namespaces and imports

**Blocks: M3, M5**

The proposed rule gives each policy one flat top-level namespace containing inputs, host functions, params, lets and every name bound by `use`. Any collision is a compile error, and nothing shadows anything, including quantifier variables.

Confirming it closes several questions at once: `use deploy.common.{cleared as actor}` is an error because `actor` is an input, a `let` can't be named `split`, and `any release in ...` can't shadow the `release` input. It also needs one addition: an imported policy is called in the same position as a decision constructor, so importing a policy under a name the kind uses for a decision has to be an error too. The cost is that adding an input to a kind can break an existing policy that already had a `let` or an import of that name, which makes "add an input" less compatible than the [versioning table](/reference/kind-files/) claims. Either the table needs a footnote, or kind-declared names need to live in a namespace policies can't collide with.

## Vacuous `all in`

**Blocks: M3**

An empty list on the left is a subset of anything, so `[] all in actor.regions` is true. Either keep the math and have the linter warn, or define an empty left side as false and document the exception.

A Go detail makes this sharper. The `deploy.production` policy computes:

```sigil
let cleared =
  split(service.labels["regions"], ",") all in actor.regions
```

When the `regions` label is missing, `service.labels["regions"]` is `""`, and Go's `strings.Split("", ",")` returns `[""]`, not an empty list. So a missing label makes `cleared` false, which fails closed. An explicit empty list from some other source would make it vacuously true, which fails open. The same expression shape gives opposite safety properties depending on where the empty value came from. That argues for defining empty-left as false, or at least for a linter warning on every `all in` whose left side can be empty.

Whatever the answer, it has to cover the quantifier too: `all r in actor.roles: r != "admin"` is vacuously true for an empty `roles` list, for the same reason. Defining one as false and not the other would make the two spellings of "every element satisfies" disagree.

## Ties within one decision

**Blocks: M4**

The MVP picks the earliest source position when several candidates share the winning decision. A candidate reached through an invocation takes its call site's position first, then its position in the invoked file. See [Why rule order never matters](/understanding/order-independence/).

That has a surprising consequence in the canonical example. Take a critical service and an actor who is a release manager and also in the `payments-sre` team. `deploy.production`'s `approve("release_manager")` (bake 1h, the kind's default) and the team's `approve("payments_sre", bake: 15m)` both fire. The team file calls `production(...)` above its own rule, so the release manager's approval wins: the team asked for a 15-minute bake and the deploy gets an hour. Moving the team rule above the call would flip the result, which is exactly the kind of order dependence the rest of the language avoids.

The alternative is a merge function declared in the kind per payload field, such as the minimum `bake` or the union of `approvers`. That removes the last trace of order dependence, but it raises its own questions: what the merged result's reason is, and which policy the result names.

If earliest-position stays, [Evaluation semantics](/reference/evaluation/) proposes comparing whole call chains element by element, which orders nested invocations too.

## What the result's `Policy` field names

**Blocks: M4**

The current design describes the `Policy` field of `Result` as "the name of the policy that produced it", and that phrase has two readings. When the host evaluates `payments.production` and the `service_owner` review wins, is `Policy` the evaluated policy (`payments.production`) or the policy whose rule won (`deploy.production`, reached through an invocation)? Those are two different fields. The illustrative `sigil eval` output in these docs shows the evaluated policy on top and each candidate's call chain in the trace, which carries both. The open part is which one the top-level field should hold, and whether the other deserves its own field.

## Cost of nested quantifiers

**Blocks: M7**

An earlier draft of this design called evaluation cost linear in policy size times input size. That holds for a single quantifier, but a quantifier nested in another's body costs the product of both list sizes, so `all a in xs: any b in ys: a == b` is quadratic. The static cost estimate still works (it multiplies the declared maximum sizes), but the budget has to be expressed in those terms, and the docs shouldn't promise "linear".

## Reason uniqueness across composed policies

**Blocks: M5, M6**

The linter warns when a policy uses the same reason twice. Two things aren't specified:

- Does the check include reasons from invoked policies? If `deploy.production` says `approve("release_manager")` and the team adds its own `approve("release_manager")`, metrics can't tell them apart. But warning means a team has to know every reason in every policy it invokes. A policy invoked twice produces the same reason twice by construction, so that case at least has to be exempt.
- What if base and team use the same reason for *different* decisions, `deny("release_manager")` in one and `approve("release_manager")` in the other? A metric keyed on reason alone would mix them. Decision plus reason is probably the right identity, which would make this case legal but still worth a warning.

## Checking a base policy on its own

**Blocks: M5, M6**

A missing required param is a compile error. So `deploy.production`, which declares `param approvers: list<string>` without a default, fails `sigil check` unless something binds `approvers`. That's right when a host loads it, and wrong for a policy repo that wants to lint its shared policies in CI before any team invokes them. Options: `sigil check` type-checks unbound params by their declared type and reports them as unbound rather than as errors; or a shared policy is only ever checked through the policies that invoke it. `sigil explain` has the same problem: without bound values it can only print param names.

## Input-dependent invocation arguments

**Blocks: M5**

Invocation arguments may reference constants and the invoking policy's own params, but not inputs. That keeps every invocation a static instantiation: `sigil explain` can print concrete values, and bounds on params (see [Pinned params on required policies](#pinned-params-on-required-policies)) can be checked at compile time. `production(approvers: service.owners)` would turn a param into a per-evaluation value, which blurs the line between a param and a `let`.

The workaround is a `when` per case, as the PCI split in the canonical example does. Relax the rule only if a real policy needs it.

## Invoking the same policy twice

**Blocks: M5**

A policy may invoke the same policy more than once with different arguments, for example `deploy.regional` once for `eu-1` and once for `us-1`. Each call is a separate instantiation, and each candidate records its call chain, so the trace can tell the instances apart. One problem remains, because composition is a union: any rule the invoked policy doesn't scope fires for every call. If `deploy.regional` had `when not in_scope { deny("out_of_region") }`, the `eu-1` call would deny every deploy the `us-1` call was meant to review. Policies meant to be invoked more than once have to scope every rule to their own params, or callers have to gate each call, and nothing enforces either. A lint for unscoped denies in a policy that's invoked twice could.

## Direct or transitive requirement

**Blocks: M5**

`policy.Require("deploy.guardrails")` makes the compiler check that the root policy reaches `deploy.guardrails` through top-level invocations only. Should the call have to sit in the root file itself, or is an ungated chain through other policies enough?

Direct is easier to read: open the team file and the guardrail call is there. Transitive allows shared "team baseline" policies, such as a `deploy.gate` that invokes the guardrails and the approvals, which a team then invokes in turn. The [Go API](/reference/go-api/#required-policies) currently describes the transitive reading.

## Host-layered bases

**Blocks: nothing yet**

A host could layer a required policy itself, with something like `policy.Base("deploy.guardrails", params)`, so team files never mention it. `policy.From` already gives the host a trusted source for required policies; a base would add the invocation too. That suits platforms where teams shouldn't see or bind the guardrails' params, and it sidesteps pinned params entirely. It's deferred rather than rejected, because the team file then no longer shows the whole picture, and `sigil explain` would need the host's configuration to print it. When is it worth adding?

## Module privacy

**Blocks: M5**

Every `let` in a module is exported. If modules turn out to need private helpers, a `let` other lets build on but importers shouldn't use, the options are a `pub` marker (as in Rust) or an underscore prefix. Neither is needed by any example written so far.

## Re-exports

**Blocks: M5**

Should a module be able to re-export names it imports, so a team gets one `use` line instead of several? Leaning no, because it hides where names come from, which is exactly what banning wildcard imports protects.

## Input encoding for the CLI

**Blocks: M6**

`sigil eval` and `sigil test` take JSON input, and the `Resolver` supplies values by path. Neither says how a `duration` or `timestamp` is encoded (Go duration strings like `"45m"` and RFC 3339 are the obvious choices), or whether a field missing from the JSON is an error or reads as the zero value. Reading it as zero matches how map keys behave, but it means a typo in a test fixture silently tests the wrong thing.

## Host functions in the CLI

**Blocks: M6**

`sigil eval` and `sigil test` evaluate policies, which means calling the host functions a kind declares. The exported kind file (`deploy_approval.sigil`) carries only their signatures. A standalone `sigil` binary has no implementations for them.

Options: hosts build their own `sigil` binary with their functions linked in (a small `main` package the library provides); a plugin mechanism; or a stub mode where the test file supplies return values for each function call. The `policytest` package for `go test` doesn't have this problem, since it runs inside the host.

## File extension

**Settled**

The project is Sigil, the CLI is `sigil`, and source files end in `.sigil`. The short form `.sgl` was the alternative considered; `.sigil` won because it matches the CLI and reads unambiguously in a file listing. Documents resolve by the names in their headers, not by path, so the extension only decides which files the loader reads. Kind documents in a bundle are never taken as the contract: one with the host kind's name must match the host's `Schema()` exactly, and others are ignored. See [Bundles and resolution](/reference/policy-files/#bundles-and-resolution).

## Document names in text output

**Blocks: M4, M6**

Every diagnostic and trace entry carries the document name alongside file, line and column. The proposed text format adds it in parentheses, `policies.sigil:42:5 (payments.production)`, and leaves it out when the file holds only that document and its path matches the name, so the common repository layout keeps short positions. The alternative is to always print it, which is uniform but doubles the length of every position in a one-document-per-file repository.

## Non-Go evaluators

**Blocks: nothing yet**

A WASM build of the evaluator would let other languages evaluate policies, not just type-check them against an exported kind. It's out of scope until the Go library is stable. The main design constraint it adds now is that nothing in the language semantics should depend on Go-specific behaviour that a WASM host couldn't reproduce, and the `split` example above shows that host functions already carry Go semantics with them.
