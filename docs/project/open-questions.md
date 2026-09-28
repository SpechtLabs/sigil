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

**Settled**

The language uses the words `and`, `or`, `xor` and `not`, not `&&`, `||` and `!`. Words read better across multi-line conditions:

```sigil
when service.tier in tiers
  and owns_service {
  review("service_owner", approvers: approvers)
}
```

`&&`/`||`/`!` would have matched Go and filt-rs, which is what host authors write every day. Words won because they also pair with `not in`, `all in` and the other word operators; with symbols, `not in` would become `!in` or stay a word next to `!`, a mix that reads badly.

Accepting both spellings and having `sigil fmt` canonicalize was ruled out, because a language that wants one canonical style shouldn't parse two.

`xor` came with [assertions](#assertions) and follows the textbook truth table: exactly one of two operands is true. It can't be chained, because `a xor b xor c` computes parity, not "exactly one"; the list operators `one in` (exactly one) and `exclusive in` (at most one) cover the n-ary cases. See [Expressions](/reference/expressions/#boolean-operators).

## Quantifier body extent

**Settled**

The parser implements the rule below: the body extends as far right as possible, and parentheses end it early. Whether the formatter should add parentheses around a body that contains `and` or `or` is still open, for M6.

A quantifier like `any r in actor.roles: r like "sre-*"` needs a rule for where its body ends. The proposed rule in [Expressions](/reference/expressions/) says the body extends as far right as possible, so

```sigil
any r in actor.roles: r like "sre-*" and release.soak < 1h
```

parses as `any r in actor.roles: (r like "sre-*" and release.soak < 1h)`. To end the body earlier you parenthesize the quantifier.

That's how lambdas work in most languages, and it's easy to implement in a Pratt parser. The risk is that authors read the line above as two conditions joined by `and` and get a subtly different result. An alternative is to require parentheses around every quantifier body, which is noisier but impossible to misread. A middle ground is the proposed rule plus a formatter that always adds parentheses when the body contains `and` or `or`.

## Chained comparisons

**Settled**

Level-4 operators are non-associative. The parser reports "`<` can't follow `<`: comparisons don't chain" at the second operator and suggests parentheses or `and`.

The proposed rule makes all precedence-level-4 operators (`==`, `<`, `in`, `has`, `like` and the rest) non-associative, so `a < b < c` and `x in xs == true` are compile errors instead of parsing as `(a < b) < c`.

Confirming this costs nothing at parse time and prevents a class of bug where a comparison chain type-checks by accident (a `bool` compared to a `bool`). The only open part is whether the error message should suggest `a < b and b < c` for the numeric case.

## Keywords as field names

**Settled**

Any keyword is accepted after `.`, in `type` bodies, in decision fields and in named arguments; top-level names must be plain identifiers, and so must every segment of a policy name. The `>=` split happens only after a type argument list.

Hosts that model Kubernetes objects, or anything else with a `type` or `kind` field, will declare struct fields whose names are Sigil keywords. A policy that reads a field named `kind` or `type` is natural, but `kind` also opens a kind file and `type` declares a struct. [Lexical structure](/reference/lexical/) proposes allowing any keyword wherever only a field or payload name can appear: after `.`, inside `type` bodies, in decision fields and in named arguments. Go struct tags can produce any name, so the alternative (reject keyword field names in `NewKind`) would push renames onto hosts for no benefit to readers.

A related lexer corner: in type position, `map<string, int>= {}` has to split `>=` into `>` and `=`. The reference proposes doing that only where a type is being parsed.

## Glob syntax for `like`

**Settled**

`*` matches any run of characters, including `/` and `.`, and `?` matches one; there are no character classes or escapes. The rest of this entry is the reasoning. `like` is described as a glob, but no page pins down which glob. The reference proposes only `*` (any run of characters) and `?` (one character), and doesn't yet say whether `*` crosses `/` or `.` the way it does in shell globs versus path globs. Labels such as `platform.example.com/env` make that choice visible: `"platform.*"` matches it only if `*` crosses both. Proposal: `*` matches anything, including separators, because Sigil matches strings, not paths.

## Multiple decisions per block

**Blocks: M4**

The parser accepts several constructors in one body, so this is the checker's decision.

Should a `when` body be allowed to contain several decision constructors?

```sigil
when not eligible {
  deny("not_eligible")
  deny("audit_flag")
}
```

Allowing it is more general and costs nothing in the evaluator, since each constructor just becomes another candidate. Requiring exactly one decision per block keeps traces simpler, because every block maps to at most one candidate, and it's hard to think of a case two separate `when` blocks can't express. Nested `when` blocks are unaffected either way.

[Collecting kinds](#collecting-kinds) tilt this toward allowing it. Granting a platform member both `write` and `development_environment_writer` under one condition is the natural way to write a role policy, and repeating the condition in a second block is exactly the duplication `when` nesting exists to avoid.

## Assertions

**Blocks: M4**

The syntax is settled and parsed: `assert("reason", cond)`, with the reason first as in a decision constructor, and `)` ending the condition. An earlier draft used Python's `assert cond, "reason"`; the constructor shape won because an assert behaves like one (it only counts when reached, under its enclosing conditions, and its reason is what traces and metrics key on), and a long condition no longer pushes the reason to the end. Settled too: an assert that reads `outcome` is checked after the outcome exists, and every other assert is checked before any rule runs, so an input precondition reports its own reason instead of a rule's runtime error. The checker infers which from the condition; there's no keyword for it (a `defer assert` was considered and dropped, see [Evaluation semantics](/reference/evaluation/#assertions)). The rest below is open.

`assert("<reason>", <condition>)` fails the evaluation loudly when its condition is false, and it's the guardrail mechanism for [collecting kinds](#collecting-kinds), which have no deny that outranks a grant. The proposal is spread across [Policy files](/reference/policy-files/#assert), [Expressions](/reference/expressions/#decision-values-and-outcome) and [Evaluation semantics](/reference/evaluation/#assertions). Still open:

- **Dynamic text.** A reason is a literal. A failing assert may still want to say which value was wrong, the way a decision's `detail` field does. A named argument, `assert("reason", cond, detail: expr)`, would do it the way constructors do, and the expression would only be evaluated on failure. Today an assert takes the reason and the condition only.
- **Asserts in kind files.** Today a host protects an assert by putting it in a policy it requires with `policy.Require`, the same way it protects denies. An assert declared in the kind would need no `Require`, but it brings expressions into kind files, which are pure declarations now, and the Go side would have to carry Sigil source in a string to define one. Deferred until `Require` proves too clumsy.
- **Reason identity.** Whether assert reasons share the `duplicate-reason` lint and metric space with decision reasons, or live on their own. The proposal keeps them apart: `policy_assert_failures_total{reason="sod_customer_dev"}`.
- **Static checks.** The proposal only rejects the obvious case: one block constructing two decisions that an `exclusive in outcome` assert forbids together, since that assert always fails when the block fires. Anything more would need the solver-style analysis the language otherwise avoids.

## Decision values and `outcome`

**Blocks: M4**

Settled so far: a bare decision name is a value of type `decision` only inside an `assert` condition, like `outcome`; anywhere else it's a compile error pointing at the constructor form. The namespace question below is what's left.

An assert that checks what evaluation decided has to name decisions as values: `[customer_data_writer, development_environment_writer] exclusive in outcome`. The proposal makes a bare decision name a value of a closed `decision` type and `outcome` a `list<decision>` only asserts can read (see [Types](/reference/types/#decision)).

That puts decision names into the policy's flat namespace, which costs something. A `let` named `deny` is now a compile error, and adding a decision to a kind can break a policy that already used the name, just as adding an input can (see [Namespaces and imports](#namespaces-and-imports)). The alternatives:

- **String names**, `["customer_data_writer", "development_environment_writer"] exclusive in outcome` with `outcome: list<string>`, and the checker rejecting literals that aren't declared decisions. No namespace change, but the check only works on literals, and a computed string would slip past it.
- **Qualified names**, `decision.customer_data_writer`. No collision, but verbose where it's used most.

Two more gaps:

- **Payloads.** `outcome` holds decisions, not candidates, so an assert can't read a payload: "no `admin` grant with a `ttl` above 8h" isn't expressible. Something like `all g in outcome.admin: g.ttl <= 8h` would need a per-decision view of the candidates.
- **Kinds with `precedence`.** There `outcome` is the winner alone. An assert can't see the losing candidates, which is right for "what will the host do", but means an assert can't check, for example, that a guardrail's deny fired at all when a team's approve won.

## Collecting kinds

**Blocks: M4**

A kind that declares `collect all` returns every candidate that fired, not one winner: roles a user can hold at the same time, feature flags, labels to attach. See [Kind files](/reference/kind-files/#collect) and [Evaluation semantics](/reference/evaluation/#collecting-kinds).

Settled: the spelling. Every kind declares `collect one` or `collect all`, and `collect one` requires `precedence`. A missing line is an error instead of silently picking one mode, and the result's shape reads off one line. `collect one` without `precedence` is an error, because the only thing left to choose a winner by would be source position. Still open:

- **Duplicates.** The proposal returns every candidate and leaves it to the host to decide what two `admin` grants with different payloads mean. Deduplicating or merging would bring back the tie-breaking problem from [Ties within one decision](#ties-within-one-decision).
- **The result type.** The proposal keeps one `Result` with an `Outcome` list for both kinds. A separate type for collecting kinds would make `Match` on a collecting result a compile error instead of a runtime panic.
- **`collect all` with `precedence`.** Reserved: a compile error today. The intended meaning keeps the two lines independent: `precedence` ranks, and `collect` says how many candidates of the top rank come back. `collect all` with `precedence deny > review > approve` would return every candidate of the highest-ranked decision that fired, so two `review`s with different approvers both reach the host instead of one winning by source position (see [Ties within one decision](#ties-within-one-decision)).
- **Precedence tiers.** `precedence suspended > {read, write, admin}` would rank groups of decisions. With `collect all` it would return every candidate in the highest tier that has any, so a `suspended` decision could wipe all grants as a decision, not as an assert failure, and when nothing is suspended every grant comes back. Plain `collect all` without `precedence` is then a single tier. Tiers are what make the reserved combination useful for roles, which is why the two should be designed together. Neither is needed for the first collecting kinds, and both can be added later without breaking them.

## Scoped `let`

**Blocks: M5**

Settled and implemented in the parser and checker. A `let` inside a `when` body is visible in that body and the blocks nested in it. It can't shadow anything, it can't be `pub`, and its name is unique in the whole document, even against a `let` in an unrelated body, so a trace and `sigil explain` name every `let` the same way. See [Scoped lets](/reference/policy-files/#scoped-lets).

## Pinned params on required policies

**Blocks: M5**

Settled for ordered params: bounds in the declaration, written as named arguments, `param min_soak: duration = 24h, min: 1h, max: 48h`. Named arguments keep `min` and `max` out of the keywords, so a kind can still declare `fn max`. Because invocation arguments are constants, bounds are checked when the policy compiles, at the offending argument, not at evaluation time as an assert would be. Host-side bounds, `policy.Require("deploy.guardrails", policy.Min("min_soak", time.Hour))`, were dropped: with `policy.From`, the bounds in a trusted file are just as trustworthy and stay next to the rule that reads the param. The declaration checks are implemented; checking arguments and `policy.Params` comes with invocation. See [Bounds](/reference/policy-files/#bounds).

Still open: params without an order, such as `approvers: list<string>`. A team can bind an empty list. Options are a `pinned` modifier that forbids overriding entirely, a non-empty requirement, or leaving it to review and CI.

## Optional structs

**Blocks: M3**

Settled and implemented: optional chaining. `release?.soak` reads a field of a `?Release` and is a `?duration`, unwrapped with `??` as usual. As in TypeScript, a `?.` that finds its operand absent makes the rest of the chain absent, so `release.parent?.author.name` needs no second `?.` when `author` isn't optional. `?.` on a value that can't be absent is a compile error. A Go pointer to a slice or a map, and `?list<T>` or `?map<K, V>` in a kind file, are rejected, because an absent collection would read the same as an empty one. See [Optional chaining](/reference/expressions/#optional-chaining).

Settled too: the presence test. `present release` is `true` when the optional holds a value, so `when not present release { deny("no_release") }` tells an absent release from one with a zero soak. It's a prefix keyword that binds like unary minus and needs an optional operand. It doesn't narrow the type: fields are still read with `?.`. Flow typing, where `release` would become `Release` inside `when present release { ... }`, stays out until real policies need it. See [Presence](/reference/expressions/#presence-present).

## Composite values: equality, ordering and recursion

**Blocks: M4**

Settled:

- `==` and `!=` work on scalars only, not on lists, maps or structs. A policy rarely means "identical", and one operator would hide a walk over a nested value.
- `in`, the list operators and `has` keep comparing elements structurally, so `["eu-1"] in [["eu-1"], ["us-1"]]` works. Elements that are or contain structs are a compile error: structs have no equality, and the evaluator used to answer `false` for them without saying so.
- Strings aren't ordered. `"v10" < "v9"` would be true byte-wise, which is the kind of result that makes a version rule wrong.
- Versions and other domain orderings come from [host-ordered types](/reference/types/#host-ordered-types): `type Version ordered` in the kind, backed by a Go type with a mandatory `Compare(T) int` method, compared with the ordinary operators. The method is never exposed as an `fn` in the kind. A built-in `version` type was rejected, because semver's ordering rules aren't a parse layout and the language would own them forever. Until host-ordered types are implemented, a host can declare `fn semver_cmp(string, string) -> int` and policies write `semver_cmp(release.version, "1.4.0") >= 0`.
- Recursive kind types are rejected, naming the cycle, since a policy could only read one to a fixed depth. Map keys follow Go's rule: any scalar.

Settled for host-ordered types, not implemented yet:

- Registration is explicit: `policy.WithOrdered[*semver.Version]("Version")`. Recognising a `Compare` method automatically was rejected: adding a method in Go would change the policy contract without anything in the kind code showing it, a tagged struct that gained `Compare` would turn opaque, and most version libraries put `Compare` on a pointer, which collides with `*T` meaning `?T`. The registered Go type is exact, so a registered pointer type is the ordered value, one pointer deeper is optional, and a nil value in a comparison is a runtime error.
- Printing prefers `encoding.TextMarshaler`, falls back to `fmt.Stringer`, and a type with neither is rejected at registration. Go's `%v` is never used, because it can print a pointer's address and break determinism. JSON input decodes through `encoding/json`, so `encoding.TextUnmarshaler` is what lets `sigil eval` and `sigil test` read the type from a string; it's optional.
- Still open: parsing a string literal into the type at load time through `TextUnmarshaler`, which would allow `param min_version: Version = "1.4.0"`.

## What the kind version means

**Settled and implemented.** Every policy and module header pins the kind version it was written against, `policy deploy.production: DeployApproval@2`, and the kind declares the oldest pin it still accepts, `kind DeployApproval version 3, accepts: 2`. Every document compiles against the one kind the host has; the pin only decides whether the host accepts the document at all. A pin below `accepts` or above `version` is a compile error, and a missing pin is one that suggests the current version.

Requiring an exact match was rejected, because every kind bump would then break every policy and force hosts to keep old kinds around. With a floor, the host's cost is two numbers: every change bumps `version`, and a breaking change also raises `accepts`. `sigil breaking` checks both. See [Versioning](/reference/kind-files/#versioning).

Still open from the same gap: adding a `fn` breaks hosts that evaluate through `LoadKind`, because `Eval` needs every function bound. That's about whether a host can run, not whether policies compile, and it's covered by [Host functions in the CLI](#host-functions-in-the-cli) for the standalone tools.

## `in` versus `has` for map keys

**Settled.** A map key is tested with `has` only: `service.labels has "env"`. `in` means a list element or a substring, and `"env" in service.labels` is a compile error that suggests the `has` form. `has` stays because it also takes a map of pairs, and one spelling per check is what a canonical formatter would otherwise have to enforce. The cost is readers coming from CEL or Python, where `"env" in labels` is the idiom; the error message points them at `has`.

## Namespaces and imports

**Settled and implemented.** Each document has one flat namespace: the kind's inputs, host functions and decisions, and the document's params, lets, imports and quantifier variables. Any collision is a compile error, and nothing shadows anything, with one exception that version pins make safe.

A document pinned to `@N` compiled against version N, where any collision was an error. So a document pinned below the kind's current version that collides with an input, host function or decision must be colliding with a name the kind added since. The document keeps its own name, and the `shadowed-kind-name` lint reports it. A document pinned to the current version gets the collision error. That makes "add an input, function or decision" compatible without any footnote.

The alternatives were a footnote saying that adding a name can break policies, which leaves the host unable to tell, qualifying every kind name (`input.release`), which makes every expression longer, and letting any document name shadow a kind name, which would let new policies shadow by accident. See [Identifiers](/reference/policy-files/#identifiers).

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

## Ties within one decision

**Blocks: M4**

The MVP picks the earliest source position when several candidates share the winning decision. A candidate reached through an invocation takes its call site's position first, then its position in the invoked file. See [Why rule order never matters](/understanding/order-independence/).

That has a surprising consequence in the canonical example. Take a critical service and an actor who is a release manager and also in the `payments-sre` team. `deploy.production`'s `approve("release_manager")` (bake 1h, the kind's default) and the team's `approve("payments_sre", bake: 15m)` both fire. The team file calls `production(...)` above its own rule, so the release manager's approval wins: the team asked for a 15-minute bake and the deploy gets an hour. Moving the team rule above the call would flip the result, which is exactly the kind of order dependence the rest of the language avoids.

The alternative is a merge function declared in the kind per payload field, such as the minimum `bake` or the union of `approvers`. That removes the last trace of order dependence, but it raises its own questions: what the merged result's reason is, and which policy the result names.

If earliest-position stays, [Evaluation semantics](/reference/evaluation/) proposes comparing whole call chains element by element, which orders nested invocations too.

[Collecting kinds](#collecting-kinds) don't have this problem, because they return every candidate and pick none.

## What the result's `Policy` field names

**Blocks: M4**

The current design describes the `Policy` field of `Result` as "the name of the policy that produced it", and that phrase has two readings. When the host evaluates `payments.production` and the `service_owner` review wins, is `Policy` the evaluated policy (`payments.production`) or the policy whose rule won (`deploy.production`, reached through an invocation)? Those are two different fields. The illustrative `sigil eval` output in these docs shows the evaluated policy on top and each candidate's call chain in the trace, which carries both. The open part is which one the top-level field should hold, and whether the other deserves its own field.

## Cost of nested quantifiers

**Blocks: M7**

An earlier draft of this design called evaluation cost linear in policy size times input size. That holds for a single quantifier, but a quantifier nested in another's body costs the product of both list sizes, so `all a in xs: any b in ys: a == b` is quadratic. The static cost estimate still works (it multiplies the declared maximum sizes), but the budget has to be expressed in those terms, and the docs shouldn't promise "linear".

## One reason on different decisions

**Blocks: M5, M6**

Repeating a decision and reason across branches is settled: it's allowed, including across invoked policies and for a policy invoked twice, and the trace tells the branches apart by call chain. See [Decisions](/reference/decisions/).

What's left is the same reason on *different* decisions, `deny("release_manager")` in one place and `approve("release_manager")` in another. A metric keyed on reason alone would mix them, so the proposal treats decision plus reason as the identity and has the linter warn. Unsettled: whether that warning should also fire when the two sides sit in different policies, one invoking the other, which means a team has to know every reason in every policy it invokes to avoid it.

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

Settled: a `let` is private unless it's declared `pub let`, in modules and policies alike. A policy's `pub let` can't read a param, which the checker reports at the declaration. What's left for the composition milestone is the import side: a `use` that names a private `let` is a compile error. See [Exporting lets](/reference/policy-files/#exporting-lets).

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
