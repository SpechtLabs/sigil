---
title: Expressions
icon: mdi:function-variant
createTime: 2026/09/24 22:30:00
permalink: /reference/expressions/
---

Every operator in the policy language: its precedence, the operand types it accepts and what it evaluates to.

- Expressions appear in `when` conditions, `assert` conditions, `let` bindings, param defaults, policy invocation arguments and decision payloads.
- Every expression has a static type that the compiler knows before evaluation.
- Nothing converts between types implicitly. The types are on [Types](/reference/types/).

Why the operators look the way they do: [Why the language looks like this](/understanding/language-choices/).

## Operator precedence

From lowest to highest:

| Level | Operators                            | Associativity  | Notes                                                                  |
| ----- | ------------------------------------ | -------------- | ---------------------------------------------------------------------- |
| 1     | `or`                                 | left           | Short-circuits                                                         |
| 1     | `xor`                                | none           | Evaluates both sides; doesn't mix with `or` without parentheses        |
| 2     | `and`                                | left           | Short-circuits                                                         |
| 3     | `not`                                | prefix         | Unary                                                                  |
| 4     | `==` `!=` `<` `<=` `>` `>=`          | none           | Strictly typed; no implicit coercion                                   |
| 4     | `in`, `not in`                       | none           | Element in list, substring in string                                   |
| 4     | `all in`, `any in`                   | none           | List subset and list intersection                                      |
| 4     | `one in`, `exclusive in`             | none           | Exactly one, or at most one, element of a list is in another           |
| 4     | `has`                                | none           | Map contains all given pairs, or a key                                 |
| 4     | `like`, `matches`                    | none           | Glob and RE2 regex; the pattern must be a literal                      |
| 5     | `??`                                 | right          | Default for optional values                                            |
| 6     | `+` `-`                              | left           | Numbers, durations, timestamps                                         |
| 7     | `-`, `present`                       | prefix         | Unary minus; presence of an optional                                   |
| 8     | `.field` `?.field` `[key]` `f(args)` | left (postfix) | Field access, optional chaining, map or list index, host function call |

- Parentheses override precedence.
- Quantifiers (`any x in xs: ...`, `all x in xs: ...`) and filters (`filter x in xs: ...`) aren't in the table. They're prefix forms whose body extends as far right as possible; see [Quantifiers](#quantifiers) and [Filters](#filters).
- Level-4 operators are non-associative. `a < b < c`, `a == b == c` and `a in b == c` are compile errors; add parentheses.
- `xor` shares level 1 with `or` but can't be chained or mixed with it. `a xor b xor c` and `a or b xor c` are compile errors; add parentheses.

| Expression                       | Parses as                                                           |
| -------------------------------- | ------------------------------------------------------------------- |
| `not a == b`                     | `not (a == b)`                                                      |
| `not "admin" in actor.roles`     | `not ("admin" in actor.roles)`; prefer `"admin" not in actor.roles` |
| `owner ?? "unknown" == "team-a"` | `(owner ?? "unknown") == "team-a"`                                  |
| `a ?? b + c`                     | `a ?? (b + c)`                                                      |

## Boolean operators

`and`, `or`, `xor` and `not` take `bool` operands and produce `bool`.

- There's no truthiness. `when approvers { ... }` is a compile error because `approvers` is a `list<string>`, not a `bool`.
- `and` and `or` short-circuit and evaluate left to right. The right operand of `a and b` isn't evaluated when `a` is false, so a list index or host function call on the right can't raise a [runtime error](/reference/evaluation/#runtime-errors) when the guard on the left fails.
- `xor` is true when exactly one of its two operands is true. It doesn't short-circuit: both operands are evaluated, and either can raise a runtime error.
- `xor` takes exactly two operands. Chaining it is a compile error; for "exactly one of several", use [`one in`](#list-set-operators).

| `a`     | `b`     | `a xor b` |
| ------- | ------- | --------- |
| `false` | `false` | `false`   |
| `false` | `true`  | `true`    |
| `true`  | `false` | `true`    |
| `true`  | `true`  | `false`   |

```sigil
release.hotfix xor release.scheduled
```

## Comparison

| Operators         | Operand types                                                                                         |
| ----------------- | ----------------------------------------------------------------------------------------------------- |
| `==` `!=`         | `bool`, `int`, `float`, `string`, `duration`, `timestamp`, [`decision`](#decision-values-and-outcome) |
| `<` `<=` `>` `>=` | `int`, `float`, `duration`, `timestamp`                                                               |

- Both operands must have the same type. `3 == 3.0` is a compile error (`int` against `float`), and so is `release.soak == 30` (`duration` against `int`).
- String comparison is case-sensitive: `"Prod" == "prod"` is false.
- Comparing an optional (`?T`) value is a compile error until it's unwrapped with `??`.
- `==` and `!=` don't apply to lists, maps or structs. For lists, use the [membership](#membership-in-and-not-in) and [set](#list-set-operators) operators.
- `!=` isn't defined for lists, so `xs != []` is a compile error that suggests `any x in xs: true`; see [Test whether a list is empty](/guides/patterns/#test-whether-a-list-is-empty).
- Strings aren't ordered. `<` on strings is a compile error. To compare versions, declare a host function in the kind; see [Compare versions](/guides/patterns/#compare-versions).

```sigil
release.soak >= min_soak
service.tier == "critical"
```

```text
deploy/production.sigil:7:6: error: `==` needs operands of the same type, found duration and int
  |
7 | when release.soak == 30 {
  |      ^^^^^^^^^^^^^^^^^^
  = help: a bare number is never a duration; write a literal like `30m`

deploy/production.sigil:3:6: error: `!=` isn't defined for list<string>
  |
3 | when actor.regions != [] {
  |      ^^^^^^^^^^^^^^^^^^^
  = help: lists have no `!=`; to test that `actor.regions` isn't empty, write `any x in actor.regions: true`, or call a host function such as `len` if the kind declares one
```

::: warning Planned
[Host-ordered types](/project/planned/#host-ordered-types) would let `<` compare values such as versions directly.
:::

## Membership: `in` and `not in`

The type of the right-hand side picks the meaning of `in`:

| Form      | Left type | Right type | True when                             |
| --------- | --------- | ---------- | ------------------------------------- |
| `x in xs` | `T`       | `list<T>`  | `xs` contains an element equal to `x` |
| `s in t`  | `string`  | `string`   | `s` is a substring of `t`             |

```sigil
"deployer" in actor.roles                    // list element
"payments" in service.name                   // substring
service.tier in ["critical", "standard"]     // list literal
```

- A map key is tested with [`has`](#map-containment-has), never with `in`. `"env" in service.labels` is a compile error that suggests `service.labels has "env"`.
- `x not in y` is exactly `not (x in y)`.
- The parser reads `not in` as one operator when `not` follows an operand, and as unary `not` when it starts an expression.

## List set operators

`all in`, `any in`, `one in` and `exclusive in` take two lists of the same element type and produce `bool`.

| Form               | True when                                                         | Empty left side |
| ------------------ | ----------------------------------------------------------------- | --------------- |
| `a all in b`       | every element of `a` is in `b` (subset)                           | true            |
| `a any in b`       | at least one element of `a` is in `b` (intersection is non-empty) | false           |
| `a one in b`       | exactly one distinct element of `a` is in `b`                     | false           |
| `a exclusive in b` | at most one distinct element of `a` is in `b`                     | true            |

```sigil
split(service.labels["regions"], ",") all in actor.regions
actor.teams any in service.owners
["prod-admin", "prod-auditor"] exclusive in actor.roles
[read, write, admin] one in outcome
```

- `exclusive in` is true when `b` holds none or one of the listed values, and false when it holds two or more. `one in` also requires one to be present.
- With two elements, `[a, b] one in xs` is `(a in xs) xor (b in xs)`.
- `one in` and `exclusive in` count distinct elements of `a` that appear in `b`. Repeats don't count twice on either side: `["x", "x"] exclusive in ["x"]` and `["x", "y"] exclusive in ["x", "x"]` are both true.
- With a one-element list on the left, `exclusive in` is always true and `one in` means `in`. No lint flags either yet.

Whether `[] all in b` should stay vacuously true is an [open question](/project/open-questions/#vacuous-all-in).

## Map containment: `has`

`has` takes a map on the left and either a map or a key on the right.

| Form                | Right type  | True when                                             |
| ------------------- | ----------- | ----------------------------------------------------- |
| `m has {k: v, ...}` | `map<K, V>` | every pair on the right is in `m` with an equal value |
| `m has k`           | `K`         | `m` has key `k`                                       |

```sigil
service.labels has {
  "app.kubernetes.io/managed-by": "argocd",
  "platform.example.com/lifecycle": "ga",
}

service.labels has "app.kubernetes.io/managed-by"
```

- The right-hand map doesn't have to be a literal.
- An empty right-hand map makes `has` true.
- `m has k` is the only way to test for a key. Neither `in` nor `not in` applies to maps; write `not m has k`, which parses as `not (m has k)`.

## Pattern matching: `like` and `matches`

Both take a `string` on the left and a pattern on the right.

- The pattern must be a string literal, plain or raw, optionally in parentheses. It compiles once, when the policy compiles.
- A pattern built from an expression, even a `let` that holds a literal, is a compile error.

| Operator  | Syntax                                                                                                                                        | Matches                                                                                            | Invalid pattern |
| --------- | --------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- | --------------- |
| `like`    | glob: `*` is any run of characters, including none; `?` is exactly one character; every other character, `[` and `\` included, matches itself | the whole string; `*` crosses `/` and `.`                                                          | impossible      |
| `matches` | Go RE2 regular expression                                                                                                                     | anywhere in the string, as Go's `regexp.MatchString`; anchor with `^` and `$` for the whole string | compile error   |

```sigil
service.name like "payments-*"
service.labels["team"] matches `^team-[a-z]+$`   // a raw string avoids double escaping
```

- A glob has no character classes or escapes. Use `matches` for anything richer.
- RE2 runs in linear time in the input length. Why that matters: [Halting by construction](/understanding/halting/).

## Optional default

`a ?? b` unwraps an optional, with `b` as the default when `a` is absent.

```sigil
// assuming the kind declares `ticket: ?string` on Release
release.ticket ?? "none"
```

- `a ?? b` requires `a` of optional type `?T` and `b` of type `T`. The result is `T`: the value of `a` if present, otherwise `b`.
- `b` is only evaluated when `a` is absent.
- `??` is right-associative: `a ?? b ?? c` means `a ?? (b ?? c)` and works when `a` and `b` are `?T` and `c` is `T`.
- `??` on a value that isn't optional is a compile error.
- Struct types have no literal, so the only default for an optional struct (`?Release`) is another value of that struct type, such as an input: `(parent_release ?? release).soak`. Its fields are usually read with [optional chaining](#optional-chaining) instead.

## Optional chaining

`x?.name` reads a field of an optional struct. If `x` is absent, the result is absent; otherwise it's the field of the struct inside. The result is optional and is unwrapped with `??`:

```sigil
// assuming the kind declares `release: ?Release`
release?.soak ?? 5m
```

A chain is a run of `.name`, `?.name` and `[index]` that no parentheses break. A `?.` makes the rest of its chain optional, as in TypeScript. When a `?.` finds its operand absent, nothing after it in the chain runs, so nothing after it can fail:

```sigil
// Release declares `parent: ?Commit`;
// Commit declares `author: Actor` and `merged_by: ?Actor`

release.parent?.author.name ?? ""          // ?string, then string; `author` needs no `?.`
release.parent?.author.roles[5] ?? ""      // no index error when there's no parent
release.parent?.merged_by?.name ?? ""      // `merged_by` is optional itself, so it needs its own `?.`
```

- The type of a chain with a `?.` in it is its last link's type made optional. A last link that's already optional stays `?T`; optionals don't nest.
- A `?.` only skips what comes after an absent value. A link that's optional itself needs its own `?.`: `release.parent?.merged_by.name` is a compile error that suggests `?.name`.
- Parentheses end a chain. `(release.parent?.author).name` reads a field of a `?Actor` and is a compile error.
- `?.` on a value that can't be absent is a compile error: `service?.name` suggests `service.name`.
- `?.` reads struct fields only. There's no `?[`, because a kind can't declare an optional list or map. A chain that ends at a list or map field is optional: `release.parent?.author.roles` is a `?list<string>`, unwrapped with `?? []`.
- Optional chaining can't tell an absent struct from a present one whose field is zero: with `release?.soak ?? 0s`, both give `0s`. [`present`](#presence-present) can.

## Presence: `present`

`present x` is `true` when the optional `x` holds a value and `false` when it's absent. It tells absence apart from a zero value.

```sigil
when not present release { deny(no_release) }
when present release.ticket { ... }                 // an empty ticket is present
when present release.parent?.merged_by { ... }      // any optional, including a chain
```

- The operand must be optional. `present` on a value that can't be absent is a compile error.
- It binds like unary minus, to one operand chain. `present release.parent and x` means `(present release.parent) and x`. `present release.ticket ?? ""` is a compile error: `present` applies first and yields a `bool`.
- It only tests. Inside `when present release { ... }`, `release` is still optional and its fields are still read with `?.`. There's no flow typing.

## Arithmetic

Binary `+` and `-` are left-associative and defined only for these combinations:

| Left        | Operator | Right       | Result      |
| ----------- | -------- | ----------- | ----------- |
| `int`       | `+` `-`  | `int`       | `int`       |
| `float`     | `+` `-`  | `float`     | `float`     |
| `duration`  | `+` `-`  | `duration`  | `duration`  |
| `timestamp` | `+` `-`  | `duration`  | `timestamp` |
| `timestamp` | `-`      | `timestamp` | `duration`  |

```sigil
release.soak + 2h >= min_soak
now - release.built_at > 2h        // assuming an input `now` and a field `built_at`, both timestamps
```

- Unary `-` applies to `int`, `float` and `duration`.
- The table is complete. Any other combination, including `+` on strings or lists, is a compile error that says so.
- There's no `*`, `/` or `%`.
- Integer and duration overflow is a [runtime error](/reference/evaluation/#runtime-errors), not a wrap-around.

## Field access, indexing and calls

These are postfix and bind tightest.

| Form          | Reads                                                           | Rules                                                                                                                           |
| ------------- | --------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `x.field`     | a field of a struct value                                       | A field the struct type doesn't declare is a compile error                                                                      |
| `common.name` | a `let` through a whole-file import such as `use deploy.common` | See [Policy files](/reference/policy-files/#use)                                                                                |
| `m[k]`        | a map value                                                     | `k` must have the map's key type. A missing key yields the value type's zero value, like Go: `service.labels["absent"]` is `""` |
| `xs[i]`       | a list element                                                  | `i` is an `int`. An index out of range, including a negative one, is a runtime error                                            |
| `f(a, b)`     | a host function declared with `fn` in the kind                  | Arguments are positional; their count and types must match the signature                                                        |

```text
deploy/production.sigil:9:16: error: unknown field "teir" on type Service
  |
9 |   when service.teir == "critical" { approve(release_manager) }
  |                ^^^^
  = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

Host function calls:

- There are no built-in functions. `split`, `len` and every other function exist only when the kind declares them.
- Calling a name the kind doesn't declare is a compile error. Policies can't define functions.
- A host function isn't a value; its bare name without parentheses is a compile error.
- Host functions must be pure.
- An error returned by a host function is a runtime error.

## Quantifiers

```sigil
any r in actor.roles: r like "sre-*"
all r in actor.roles: r != "admin"
```

| Form                | True when                             | Empty list |
| ------------------- | ------------------------------------- | ---------- |
| `any x in xs: body` | `body` holds for at least one element | false      |
| `all x in xs: body` | `body` holds for every element        | true       |

- The range `xs` must be a list. Quantifying over a map is a compile error.
- The body must be `bool`.
- `x` is bound to each element in turn, has the list's element type, and is only visible inside the body.
- `x` follows the no-shadowing rule: naming it after an input, param, let, imported name or host function is a compile error.
- Evaluation stops at the first element that decides the result.
- A quantifier starts an expression; the binary `all in` and `any in` follow an operand. The parser tells `all r in xs: ...` from `a all in b` by that position; see [Grammar](/reference/grammar/).

The body extends as far right as possible. To end a quantifier early, wrap it in parentheses:

```sigil
any r in actor.roles: r like "sre-*" and eligible
// parses as
any r in actor.roles: (r like "sre-*" and eligible)

(any r in actor.roles: r like "sre-*") and eligible
```

`sigil fmt` adds parentheses around every quantifier body whose top level is `and`, `or` or `xor`. They don't change how it parses.

## Filters

```sigil
filter a in approvers: a != requestor.name
filter r in actor.roles: r like "prod-*"
```

`filter x in xs: body` keeps the elements of `xs` for which `body` holds.

- The result has the type of `xs`: filtering a `list<string>` gives a `list<string>`.
- Elements that pass keep their order. An element that's in the list twice and passes is kept twice.
- When none passes, the result is the empty list.
- A filter never stops early. Its body runs for every element.
- The quantifier rules apply; see [Quantifiers](#quantifiers).
- As the operand of an operator, a filter needs parentheses.
- A filter or a quantifier can't be an [invocation](/reference/policy-files/#policy-invocation) argument; arguments are bound when the policy compiles.

```sigil
"prod-admin" in (filter r in actor.roles: r like "prod-*")
(filter r in actor.roles: r like "prod-*") all in allowed_roles
let approvers = filter a in managers: a != requestor.name
```

For the approver recipe, including what to do when the filter leaves nobody, see [Keep the requestor off the approvers](/guides/patterns/#keep-the-requestor-off-the-approvers).

## Decision values and `outcome`

Inside an `assert` condition, a policy can test what evaluation decided.

| Operand                   | Type                                     | Meaning                                                                               |
| ------------------------- | ---------------------------------------- | ------------------------------------------------------------------------------------- |
| `approve`                 | [`decision`](/reference/types/#decision) | the decision with any reason                                                          |
| `approve.release_manager` | `decision`                               | the decision with that reason only                                                    |
| `outcome`                 | `list<decision>`                         | each distinct decision and reason the host gets back, in the kind's declaration order |

```sigil
assert("sod_customer_dev", [customer_data_writer, development_environment_writer] exclusive in outcome)
assert("rm_needs_ticket", approve.release_manager not in outcome or present release.ticket)
```

- A decision's bare name is a value; a [constructor](/reference/decisions/#constructors-not-calls) always has parentheses. `approve in outcome` is never a call.
- Decision values and `outcome` exist only inside `assert` conditions.
- `when deny == approve` is a compile error that points at the constructor form.
- `outcome` in a `when` condition or a `let` is a compile error: "`outcome` can only be read in an assert condition".
- In a `collect one` kind, `outcome` holds exactly one element: the winner or the default.
- In a [collecting kind](/reference/evaluation/#collecting-kinds), `outcome` holds every outcome that fired, or the default if the kind declares one and nothing fired.
- Membership over `outcome` matches: a bare decision is in `outcome` when any of its reasons is, and a qualified one only when that reason is.
- `==` and `!=` between two decision values are exact: `approve == approve.lgtm` is false.
- Decision values can be compared with `==` and `!=`, tested with `in` and the list operators, and collected in lists, and nothing else.
- A decision can't be a map value, because it has no zero value for a missing key: `{"a": approve}` is a compile error.
- To read what a decision carries, go through its [candidates](#candidates).
- When asserts run: [Assertions](/reference/evaluation/#assertions). Why `outcome` is readable only there: [Asserts and decisions](/understanding/asserts/).

### Candidates

`outcome.<decision>` is the list of that decision's candidates the host gets back, each with the decision's payload fields and its `reason`. Every field is checked against the kind.

```sigil
assert("no_self_review", all r in outcome.review: requestor.name not in r.approvers)
assert("short_admin_grants", all g in outcome.admin: g.ttl <= 8h)
```

- A reason after the decision narrows the list: `outcome.review.manager_approval`.
- On one candidate, `r.reason` is a [decision value](#decision-values-and-outcome) with its reason, so `r.reason == review.manager_approval` and `r.reason in [review]` both work.
- A kind can't declare a payload field called `reason`. A decision without a payload gives candidates that only have `reason`.
- `all` over no candidates is true.
- A policy can range over candidates with `any`, `all` or [`filter`](#filters) and read each one's fields, and nothing else. Indexing the list, comparing candidates with `==` or `in`, and putting one into a list or map literal are compile errors that say so.
- A filter over candidates is a list of candidates, with the same rules.
- Candidates only exist in `assert` conditions.

The list holds exactly the candidates the host acts on, the ones `Decision[T].MatchAll` returns in Go:

| Kind                                                  | `outcome.<decision>` holds                                                                                                                                             |
| ----------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `collect one`                                         | At most one: the winner, or the default when nothing fired and the default is of that decision. Under `precedence`, a candidate that lost to a higher rank isn't in it |
| [collecting](/reference/evaluation/#collecting-kinds) | Every candidate of the decision at the top rank, after equal ones fold. Two reviews with different approvers are both there                                            |

Why guardrails use `all`, and why candidates have no equality or order: [Asserts and decisions](/understanding/asserts/).

## Evaluation order

- Operands are evaluated left to right.
- `and`, `or`, `??` and quantifiers skip work they don't need. Anything they skip can't raise a runtime error.
- A filter runs its body for every element, so a body that raises a runtime error for any element fails the filter.
- Expressions have no side effects and host functions are pure, so evaluation order is otherwise unobservable.
