---
title: Kind files
icon: mdi:file-certificate-outline
createTime: 2026/09/24 22:30:00
permalink: /reference/kind-files/
---

The kind file format, its validity rules and its versioning rules.

A kind file is what a host's `Schema()` exports from the kind it defines in Go with `policy.NewKind` ([Kinds](/reference/go-api/#kinds)), and what `sigil check`, `eval`, `explain` and `test` read, among their inputs or named with `--kind` ([Kinds](/reference/cli/#kinds)).

- Every policy names exactly one kind in its header and is type-checked against it.
- Kind files use the `.sigil` extension, like policies and modules. The `kind` header tells them apart.
- By convention the file is named after the kind: `deploy_approval.sigil`.

Why a kind is a contract generated from Go: [Kinds as contracts](/understanding/kinds/).

::: warning Planned
[Loading a kind at run time](/project/planned/#loading-a-kind-at-run-time) with `policy.LoadKind`, [`sigil gen go`](/project/planned/#sigil-gen-go) and the [language server](/project/planned/#sigil-lsp) don't exist yet.
:::

## A complete kind

```sigil
kind DeployApproval version 1

enum Tier: critical | standard | internal

type Release {
  soak: duration
  hotfix: bool
}

type Service {
  name: string
  tier: Tier
  owners: list<string>
  labels: map<string, string>
}

type Actor {
  name: string
  teams: list<string>
  roles: list<string>
  regions: list<string>
}

input release: Release
input service: Service
input actor: Actor
input environment: string

fn split(string, string) -> list<string>

decision deny {
  reason: not_eligible | soak_too_short | no_rule_matched
}

decision review {
  reason: service_owner
  approvers: list<string>
}

decision approve {
  reason: release_manager | payments_sre
  bake: duration = 1h
}

collect one
precedence deny > review > approve
precedence deny: not_eligible > soak_too_short > no_rule_matched
precedence approve: release_manager > payments_sre

default deny(reason: no_rule_matched)
```

## Declarations

| Declaration  | Example                                       | Purpose                                                               |
| ------------ | --------------------------------------------- | --------------------------------------------------------------------- |
| `kind`       | `kind DeployApproval version 3, accepts: 2`   | Name, contract version, and the oldest version policies may still pin |
| `enum`       | `enum Tier: critical \| standard \| internal` | Closed sets of named values                                           |
| `type`       | `type Release { soak: duration }`             | Struct types used by inputs, host functions and payloads              |
| `input`      | `input release: Release`                      | Top-level names policies can read                                     |
| `fn`         | `fn split(string, string) -> list<string>`    | Host function signatures                                              |
| `decision`   | `decision review { reason: service_owner }`   | Decision constructors: reasons and payload schema                     |
| `collect`    | `collect one` or `collect all`                | One winner, or every decision that fired                              |
| `precedence` | `precedence deny > review > approve`          | Ranks decisions, or the reasons of one decision                       |
| `exclusive`  | `exclusive grant_a, grant_b`                  | Outcomes that can't fire together                                     |
| `default`    | `default deny(reason: no_rule_matched)`       | Result when nothing fires; optional with `collect all`                |
| `conflict`   | `conflict deny(reason: conflicting_rules)`    | Result of a conflict; optional, and only with `collect one`           |

### `kind`

```sigil
kind DeployApproval version 3, accepts: 2
```

- The header must be the first statement. A file holds exactly one kind.
- The name is an identifier. Policies refer to it with the version they were written against: `policy deploy.production: DeployApproval@1`.
- The version is a positive integer that changes whenever the contract does.
- `accepts` names the oldest version a policy may still pin, from 1 up to the version. It's optional and defaults to 1, which accepts every version, so `kind DeployApproval version 1` is a complete header.
- In Go, the name is `NewKind`'s first argument, and the numbers come from `policy.WithVersion` and `policy.WithAccepts`. A kind without `WithVersion` doesn't build.

See [Versioning](#versioning).

### `enum`

```sigil
enum Tier: critical | standard | internal
```

Declares an enum type: a name and the closed set of values it has.

- The name is an identifier, not a built-in type name, and unique among enums and struct types.
- An enum declares at least one value. Values are identifiers, not keywords, and unique within the enum.
- A long list may break across lines, and a line may start with `|`.
- The declaration order is the printing order and means nothing else. Enums aren't ordered, so `<` doesn't apply.
- Two enums may declare the same value name, such as `standard` in `Tier` and in `Plan`. A policy then needs [context or the qualified form](/reference/expressions/#enum-values), `Tier.standard`, to tell them apart.
- The enum's name and every value join the kind's namespace, with the inputs, host functions and decisions, and can't share a name with any of them. A value can't share a struct type's name either. Reasons aren't in the namespace, so a reason may share a value's name.
- Where an enum may appear, and its operators: [Enums](/reference/types/#enums).
- Only a kind declares enums. A policy or module can't.
- In Go, it's a named string type with `policy.WithEnum(TierCritical, TierStandard, TierInternal)`; see [Enums](/reference/go-api/#enums).

```text
deploy_approval.sigil:3:34: error: enum Tier: value "standard" is declared twice
  |
3 | enum Tier: critical | standard | standard
  |                                  ^^^^^^^^
  = help: declare each value once
```

Why values are written bare: [Why enum values are bare names](/understanding/language-choices/#why-enum-values-are-bare-names).

### `type`

```sigil
type Service {
  name: string
  tier: Tier
  owners: list<string>
  labels: map<string, string>
}
```

Declares a struct type with named, typed fields.

- Fields are written `name: type` with no separator between them. The parser finds the next field by its `name:` prefix.
- Field names must be unique within a type and may be spelled like keywords.
- A field's type may be any [type](/reference/types/) except `decision`, including another struct type and an optional `?T`.
- A type's name can't be a built-in type name such as `string` or `list`.
- Types may refer to each other in any order.
- Struct types must not be recursive, directly or through other types. `NewKind` and the kind loader reject a recursive type, naming the cycle.

```text
deploy_approval.sigil:5:6: error: type Release is recursive: Release -> Commit -> Release
  |
5 | type Release {
  |      ^^^^^^^
  = help: policies can't loop, so a recursive type could only be read to a fixed depth; model the relation with an id instead

deploy_approval.sigil:11:6: error: type Commit is recursive: Commit -> Release -> Commit
   |
11 | type Commit {
   |      ^^^^^^
   = help: policies can't loop, so a recursive type could only be read to a fixed depth; model the relation with an id instead
```

::: warning Planned
`type Version ordered` would declare a [host-ordered type](/project/planned/#host-ordered-types).
:::

### `input`

```sigil
input service: Service
```

Declares a top-level name policies can read, and its type. Inputs are read-only. In Go, each input is a field of the host's input struct with a `policy:"..."` tag; see [Go type mapping](/reference/go-api/#go-type-mapping).

### `fn`

```sigil
fn split(string, string) -> list<string>
```

Declares a host function's signature: the parameter types and the result type.

- Parameters have no names. Policies pass arguments positionally.
- The return type is required and can't be optional.
- Host functions must be pure and deterministic.
- The Go implementation returns `T` or `(T, error)`. A non-nil error becomes a [runtime error](/reference/evaluation/#runtime-errors).
- Variadic Go functions are rejected.
- The stock `sigil` CLI type-checks calls against the signature alone and has no implementation; see [Host functions and host binaries](/reference/cli/#host-functions-and-host-binaries).

### `decision`

```sigil
decision review {
  reason: service_owner
  approvers: list<string>
}

decision approve {
  reason: release_manager | payments_sre
  bake: duration = 1h
}
```

Declares a decision constructor: the reasons it can be constructed with, and its payload fields.

| Field                  | Rule                                                                                  |
| ---------------------- | ------------------------------------------------------------------------------------- |
| `reason: a \| b \| c`  | Required, exactly once, no default. Lists the decision's reasons inline, at least one |
| `name: type`           | A payload field, required at every call site                                          |
| `name: type = default` | A payload field that may be left out. The default is a constant of the field's type   |

- Fields are separated by whitespace, like a [`type`](#type) body. `sigil fmt` writes one per line, `reason` first.
- A decision with one reason writes it alone: `reason: service_owner`.
- The reason can't name a type. With `enum Reason` declared, `reason: Reason` is an error; list the values inline.
- A constructor names one of the reasons with `reason:`, as in `approve(reason: payments_sre, bake: 15m)`. Any other name is a compile error with a did-you-mean hint.
- Reasons are scoped to their decision. `deny` and `approve` may both declare `release_manager`; they're two names, `deny.release_manager` and `approve.release_manager`.
- Reasons aren't in the namespace: an enum value, input or policy name may share a reason's name.
- The reason list is a set. Its order means nothing; reasons are ranked by a separate [scoped `precedence`](#precedence).
- Payload field names are unique within a decision.
- The old form, `decision approve(bake: duration = 1h) { release_manager payments_sre }`, still parses. The kind loader rejects it with the new form in the help, and [`sigil fmt --write`](/reference/cli/#sigil-fmt) rewrites it.

```text
deploy_approval.sigil:40:10: error: old decision syntax; write `decision approve { reason: … bake: … }`
   |
40 | decision approve(bake: duration = 1h) {
   |          ^^^^^^^
   = help: write `decision approve { reason: release_manager | payments_sre  bake: duration = 1h }`, or run `sigil fmt --write`
```

How policies construct decisions is on [Decisions](/reference/decisions/). Why the reason is an inline list: [Why the reason is the decision's own enum](/understanding/decisions/#why-the-reason-is-the-decision-s-own-enum).

### `collect`

```sigil
collect one
precedence deny > review > approve
```

```sigil
collect all
```

Declares how many candidates the host gets back. Every kind declares it.

|               | without `precedence`                        | with `precedence`                                              |
| ------------- | ------------------------------------------- | -------------------------------------------------------------- |
| `collect one` | Error: nothing picks the winner             | One winner: the top-ranked candidate, or a conflict error when several share the top rank |
| `collect all` | Every candidate that fired                  | Every candidate at the top rank                                |

- `precedence` ranks, and `collect` says how many candidates of the top rank come back.
- In Go, `policy.WithDecisions` makes a `collect one` kind and `policy.WithCollect` a `collect all` kind. A kind uses one of them, not both.

```text
deploy_approval.sigil:45:1: error: kind DeployApproval collects one decision but has no precedence
   |
45 | collect one
   | ^^^^^^^^^^^
   = help: `collect one` returns the highest-ranked decision; rank them with `precedence deny > review > approve`, highest first
```

How the top rank is formed, and what happens when it holds more than one candidate: [Resolution](/reference/evaluation/#resolution). Why `collect` is always spelled out and why `collect one` needs `precedence`: [Kinds as contracts](/understanding/kinds/).

### `precedence`

```sigil
precedence deny > review > approve
precedence approve: release_manager > payments_sre
```

The first form ranks decisions from highest to lowest.

- It must name every declared decision exactly once.
- It's required with `collect one` and optional with `collect all`. A kind declares it at most once.
- In Go, a `WithDecisions` kind derives it from the order of `policy.WithDecisions(...)`, which is always total. A `WithCollect` kind ranks its decisions with `policy.WithPrecedence(...)`.

The second form ranks the reasons of one decision, and is optional.

- It only matters when candidates of the same decision compete.
- A decision takes at most one, and it must name every reason of that decision exactly once.
- A decision without one has unranked reasons. Ties between them are a [conflict](/reference/evaluation/#resolution) under `collect one`.
- In a `collect all` kind with `precedence`, a decision with ranked reasons at the top rank returns only the candidates with the highest-ranked reason that fired.
- In Go, it's `policy.WithReasonPrecedence(reasons...)`, with a reason handle from `Decision[T].Reason` for each reason, all of one decision.

Reasons of different decisions never rank against each other: `deny > approve.release_manager > review` isn't a valid declaration.

```text
deploy_approval.sigil:48:1: error: precedence approve: doesn't name reason "payments_sre"
   |
48 | precedence approve: release_manager
   | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
   = help: list every reason exactly once, highest first
```

### `exclusive`

```sigil
exclusive grant_a, grant_b
exclusive approve.release_manager, approve.payments_sre
```

Declares that at most one of the listed outcomes may fire in one evaluation. Candidates from two of them together are a conflict; see [Failed evaluations](/reference/evaluation/#failed-evaluations).

- Each entry is a decision, matching any of its reasons, or a decision with one reason.
- A set names at least two entries. A kind may declare any number of sets, and one outcome may appear in several.
- The check happens before ranking, so an exclusive pair is a conflict even when a third decision outranks both.
- It works the same under `collect one` and `collect all`.
- In `collect one`, the relative rank of an exclusive pair is unobservable, since they never both survive to be ranked. `precedence` still has to list them.
- In Go, `policy.WithExclusive(GrantA, GrantB)` or `policy.WithExclusive(Approve.Reason("release_manager"), Approve.Reason("payments_sre"))`.

It's the relation that [`exclusive in`](/reference/expressions/#list-set-operators) tests over `outcome`. When to declare it here and when to assert it in a policy: [Asserts and decisions](/understanding/asserts/).

### Collecting kinds

A policy for a `collect all` kind grants each outcome in its own `when` block, and several can fire for one actor. What the host gets back is on [Collecting kinds](/reference/evaluation/#collecting-kinds).

```sigil
kind AccessGrant version 1

type Actor {
  name: string
  groups: list<string>
  clearance: string
}

input actor: Actor

decision read {
  reason: engineering_member
}

decision write {
  reason: platform_member
}

decision admin {
  reason: oncall
  ttl: duration = 8h
}

decision customer_data_writer {
  reason: data_engineer
}

decision development_environment_writer {
  reason: platform_member
}

collect all
```

How a collecting kind gets guardrails: [Asserts and decisions](/understanding/asserts/).

### `default`

```sigil
default deny(reason: no_rule_matched)
```

The result when no rule fires.

- It's a decision constructor, with `reason:` naming one of the decision's declared reasons, the same way a policy writes one; see [Decisions](/reference/decisions/#the-reason).
- Every payload value must be a constant. Fields with a default may be left out.
- A `collect one` kind must declare a default.
- A collecting kind may leave it out. Then an evaluation where nothing fires returns no decisions at all.
- A `collect one` kind also returns it from a [failed evaluation](/reference/evaluation/#failed-evaluations), except after a conflict when it declares a [`conflict`](#conflict) outcome.
- In Go, `policy.WithDefault(Deny.Reason("no_rule_matched"))` takes a [reason handle](/reference/go-api/#decisions-and-reasons) and no payload. Every field takes its default, so every payload field of the default decision needs a `default=` tag.

### `conflict`

```sigil
decision deny {
  reason: not_eligible | soak_too_short | no_rule_matched | conflicting_rules
}

default deny(reason: no_rule_matched)
conflict deny(reason: conflicting_rules)
```

The result when [resolution](/reference/evaluation/#resolution) ends in a conflict: two members of an `exclusive` set fired, or several candidates share the top rank. `Eval` still returns the `*ConflictError`, and the trace still lists every candidate; only the outcome that comes with the error changes. Without it, a conflict returns the default. Why a kind would name its own: [Every failure fails closed](/understanding/strictness/#every-failure-fails-closed).

- It's optional, and declared at most once.
- It's written like the default: a decision constructor whose `reason:` names one of the decision's declared reasons, where every payload value is a constant and fields with a default may be left out.
- Only conflicts return it. After a runtime error, a failed assert or a done context, the result still holds the default.
- Give it a reason no rule constructs, so a result carrying it can only mean a conflict, and a decision that fails closed, since the host acts on it.
- Only a `collect one` kind may declare one. A collecting kind returns an empty outcome on every failed evaluation, a conflict included, because granting anything on a defect in the policy would fail open.
- `conflict` is a [keyword](/reference/lexical/#keywords), like `default`, so no input, enum, enum value, param, let or reason can be called `conflict`. A field or payload field still can.
- In Go, `policy.WithConflict(Deny.Reason("conflicting_rules"))`. Like `WithDefault`, it takes a reason handle and no payload, so every payload field of its decision needs a `default=` tag.

A `conflict` line added to the [collecting kind](#collecting-kinds) above:

```text
access_grant.sigil:34:1: error: kind AccessGrant collects all decisions and can't declare a conflict outcome
   |
34 | conflict admin(reason: oncall)
   | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
   = help: a collecting kind returns an empty outcome on a conflict, because granting anything on a defect in the policy would fail open; remove `conflict`
```

## Validity rules

The rules in each declaration's section above apply. These hold across the whole file:

| Declaration           | Rule                                                                                                                                                                                                                                                                               |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `kind`                | The file holds no other document                                                                                                                                                                                                                                                   |
| `enum`, `type`        | Enum and struct type names are unique together, and none is a built-in type name. An enum declares at least one value, each once                                                                                                                                                   |
| Every type reference  | Is a built-in type, a declared struct type or a declared enum. No field, input or function uses `decision`. Map keys are scalars (`bool`, `int`, `float`, `string`, `duration` or `timestamp`) or enums. No map value is an enum. Optionals don't nest. No list or map is optional |
| `input`, `fn`, `enum` | Inputs, host functions, decisions, enums and enum values share one namespace, and every name in it is unique, except that two enums may declare the same value. An enum value doesn't share its name with a struct type either. Reasons may share any of these names               |
| `decision`            | The kind declares at least one. Decision names are unique. Each declares exactly one `reason` field, listed inline. A reason is unique within its decision. Payload field names are unique within their decision                                                                   |
| `collect`             | Declared exactly once                                                                                                                                                                                                                                                              |
| `precedence`          | A scoped `precedence` names a declared decision                                                                                                                                                                                                                                    |
| `exclusive`           | Each entry is a declared decision or one of its declared reasons                                                                                                                                                                                                                   |
| `default`             | Declared at most once. Constructs a declared decision, passing a constant of the right type for every field without a default                                                                                                                                                      |
| `conflict`            | Declared at most once, and only with `collect one`. Follows the rules of `default`                                                                                                                                                                                                 |

An enum `Stage` whose values reuse a decision's and an input's name:

```text
deploy_approval.sigil:5:21: error: enum Stage: value "review" collides with decision "review"
  |
5 | enum Stage: build | review | release
  |                     ^^^^^^
  = help: inputs, host functions, decisions, enums and their values share one namespace; rename one of them

deploy_approval.sigil:5:30: error: enum Stage: value "release" collides with input "release"
  |
5 | enum Stage: build | review | release
  |                              ^^^^^^^
  = help: inputs, host functions, decisions, enums and their values share one namespace; rename one of them
```

| Checked by                                  | Reports                                                   |
| ------------------------------------------- | --------------------------------------------------------- |
| The kind loader behind `sigil check`        | every violation, with its position                        |
| `NewKind` in Go                             | the same rules; panics listing every problem at program start, so a kind that exists can always be exported |

## Canonical form

`Schema()` always writes the declarations in this order:

1. the header, with `, accepts: N` only when `N` is above 1,
2. the enums: first those the input struct, then the host functions, then the payload structs reach, in the order they first reach them, then the ones nothing reaches, in `WithEnum` order,
3. the struct types, in the order the input struct, then the host functions, then the payload structs first reach them,
4. the inputs, in field order, then the host functions, in `WithFunc` order,
5. the decisions, in `WithDecisions` or `WithCollect` order, each with `reason` first and then its payload fields in struct field order,
6. `collect`, the decision `precedence`, each scoped `precedence` and each `exclusive` set,
7. the `default`,
8. the `conflict` outcome.

- Blank lines separate the header, each enum, each type, the inputs, the functions, each decision, the resolution lines and the default, as `sigil fmt` lays them out.
- The `conflict` outcome goes on the line right after the default, with no blank line between them, so the two read as a pair.
- `sigil fmt` doesn't reorder declarations in a hand-written kind file. The order above is only what an export produces.
- Loading an exported file gives back the kind it came from. Fuzz tests check that round trip.
- [`sigil export --check`](/reference/cli/#sigil-export) compares a checked-in file with the host's `Schema()`. To run it in CI, see [Check policies in CI](/guides/ci/).
- A kind document in a bundle must match the host's kind; see [Kind documents in a bundle](/reference/bundles/#kind-documents-in-a-bundle).

## Versioning

A kind carries two numbers, and every policy and module pins the version it was written against:

```sigil
kind DeployApproval version 3, accepts: 2
```

```sigil
policy deploy.production: DeployApproval@2
```

The host only has its current kind. Every document compiles against it, whatever its pin says. The loader uses the pin three ways:

| Pin                              | Result                                                        |
| -------------------------------- | ------------------------------------------------------------- |
| from `accepts` up to `version`   | Loads normally                                                |
| below `accepts`                  | Compile error                                                 |
| above `version`                  | Compile error: the document was written for a kind this host doesn't have yet |

The rules for changing the numbers, set with `policy.WithVersion` and `policy.WithAccepts` in Go:

- Every change to the contract bumps `version`, including compatible ones.
- A breaking change also raises `accepts` to the new version.

| Change                                                                                                                                    | Effect                                                        |
| ----------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| Add an input, type field, function, decision or reason                                                                                    | Compatible                                                    |
| Add an enum, or a value to an enum, when no other enum declares that value                                                                | Compatible                                                    |
| Add a value that another enum already declares                                                                                            | Breaking                                                      |
| Reorder an enum's values                                                                                                                  | Compatible                                                    |
| Add a payload field with a default                                                                                                        | Compatible                                                    |
| Remove or rename anything, including an enum or one of its values                                                                         | Breaking                                                      |
| Change a type, including a `string` field to an enum                                                                                      | Breaking                                                      |
| Add a payload field without a default                                                                                                     | Breaking                                                      |
| Reorder `precedence`, add or reorder a scoped `precedence`, add an `exclusive` set, change `default`, or add, remove or change `conflict` | Breaking in behavior, even though every policy still compiles |
| Switch between `collect one` and `collect all`                                                                                            | Breaking                                                      |

::: warning Planned
[`sigil breaking`](/project/planned/#sigil-breaking) will check both rules in CI from the old and new kind files. Until it exists, review `version` and `accepts` changes by hand.
:::

- A name a newer kind adds that collides with a document's own name is resolved by the document's pin; see [Identifiers](/reference/policy-files/#identifiers). That includes an enum value.
- Adding a value that another enum already declares makes a bare use without context, such as `let t = standard`, ambiguous, whatever the document's pin; the fix is the qualified form, `Tier.standard`. A new enum whose values overlap an existing enum's counts too.
- A document reads the payload fields the host's kind declares now, whatever its pin, including through [`outcome.<decision>`](/reference/expressions/#candidates) in an assert. Payload fields aren't in the namespace, so adding one never collides with a policy's names.

Why pins work this way: [Adding a name never breaks a policy](/understanding/kinds/#adding-a-name-never-breaks-a-policy) and [Enums and versions](/understanding/kinds/#enums-and-versions). To change a kind step by step, see [Evolve a kind safely](/guides/evolve-a-kind/).
