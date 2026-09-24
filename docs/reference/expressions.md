---
title: Expressions
icon: mdi:function-variant
createTime: 2026/09/24 22:30:00
permalink: /reference/expressions/
---

::: info Draft specification
This page specifies the language as designed. Nothing is implemented yet; see [Open questions](/project/open-questions/).
:::

Expressions appear in `when` conditions, `let` bindings, param defaults, policy invocation arguments and decision payloads. Every expression has a static type that the compiler knows before evaluation, and nothing converts between types implicitly. The types themselves are on [Types](/reference/types/).

## Operator precedence

From lowest to highest:

| Level | Operators                   | Associativity   | Notes                                                                                     |
| ----- | --------------------------- | --------------- | ----------------------------------------------------------------------------------------- |
| 1     | `or`                        | left            | Short-circuits                                                                            |
| 2     | `and`                       | left            | Short-circuits                                                                            |
| 3     | `not`                       | prefix          | Unary                                                                                     |
| 4     | `==` `!=` `<` `<=` `>` `>=` | none            | Strictly typed; no implicit coercion                                                      |
| 4     | `in`, `not in`              | none            | Element in list, key in map, substring in string                                          |
| 4     | `all in`, `any in`          | none            | List subset and list intersection                                                         |
| 4     | `has`                       | none            | Map contains all given pairs, or a key                                                    |
| 4     | `like`, `matches`           | none            | Glob and RE2 regex; the pattern must be a literal                                         |
| 5     | `??`                        | right           | Default for optional values                                                               |
| 6     | `+` `-`                     | left            | Numbers, durations, timestamps                                                            |
| 7     | `-`                         | prefix          | Unary minus                                                                               |
| 8     | `.field` `[key]` `f(args)`  | left (postfix)  | Field access, map or list index, host function call                                       |

Parentheses override precedence as usual. Quantifiers (`any x in xs: ...`, `all x in xs: ...`) aren't in the table because they're prefix forms whose body extends as far right as possible; see [Quantifiers](#quantifiers).

A few consequences worth spelling out:

- `not` binds looser than comparisons, so `not a == b` means `not (a == b)`, and `not "admin" in actor.roles` means `not ("admin" in actor.roles)`. Prefer `"admin" not in actor.roles`.
- `??` binds tighter than comparisons, so `owner ?? "unknown" == "team-a"` means `(owner ?? "unknown") == "team-a"`.
- `+` binds tighter than `??`, so `a ?? b + c` means `a ?? (b + c)`.

::: tip Proposed
Level-4 operators are non-associative. `a < b < c`, `a == b == c` and `a in b == c` are compile errors; add parentheses to say what you mean. They all share one level, and refusing to chain them avoids a class of misreadings.
:::

## Boolean operators

`and`, `or` and `not` take `bool` operands and produce `bool`. There's no truthiness: `when approvers { ... }` is a compile error because `approvers` is a `list<string>`, not a `bool`.

`and` and `or` short-circuit and evaluate left to right. The right operand of `a and b` is never evaluated when `a` is false, which matters for [runtime errors](/reference/evaluation/): a list index or host function call on the right can't fault when the guard on the left fails.

Whether the language spells these `and`/`or`/`not` or `&&`/`||`/`!` is an [open question](/project/open-questions/). These docs use the words.

## Comparison

`==` and `!=` require both operands to have the same type. `3 == 3.0` is a compile error because `int` and `float` are different types, and so is `release.soak == 30` because `30` is an `int`, not a `duration`.

| Operators                   | Operand types                                         |
| --------------------------- | ----------------------------------------------------- |
| `==` `!=`                   | `bool`, `int`, `float`, `string`, `duration`, `timestamp` |
| `<` `<=` `>` `>=`           | `int`, `float`, `duration`, `timestamp`               |

String comparison is case-sensitive, unlike filt-rs, because Kubernetes labels and most identifiers in this domain are. `"Prod" == "prod"` is false.

Comparing an optional (`?T`) value is a compile error until it's unwrapped with `??`.

::: warning Unspecified
The current design doesn't say whether `==` works on lists, maps or struct values, or whether strings can be ordered with `<` (where `"v10" < "v9"` would be true). Neither is allowed until that's decided.
:::

## Membership: `in` and `not in`

`in` has three meanings, picked by the type of the right-hand side:

| Form                 | Left type | Right type    | True when                         |
| -------------------- | --------- | ------------- | --------------------------------- |
| `x in xs`            | `T`       | `list<T>`     | `xs` contains an element equal to `x` |
| `k in m`             | `K`       | `map<K, V>`   | `m` has key `k`                   |
| `s in t`             | `string`  | `string`      | `s` is a substring of `t`         |

```sigil
"deployer" in actor.roles                    // list element
"regions" in service.labels                  // map key
"payments" in service.name                   // substring
service.tier in ["critical", "standard"]     // list literal
```

`x not in y` is exactly `not (x in y)`. The parser reads `not in` as a single operator when `not` follows an operand, and as unary `not` when it starts an expression.

## List set operators: `all in` and `any in`

Both take two lists of the same element type and produce `bool`.

| Form          | True when                                         |
| ------------- | ------------------------------------------------- |
| `a all in b`  | every element of `a` is in `b` (subset)           |
| `a any in b`  | at least one element of `a` is in `b` (intersection is non-empty) |

```sigil
split(service.labels["regions"], ",") all in actor.regions
actor.teams any in service.owners
```

An empty left side makes `all in` true and `any in` false. The vacuous truth of `[] all in b` is an [open question](/project/open-questions/): either keep the math and have the linter warn, or define an empty left side as false.

::: tip Split returns a list with one empty string
Host functions follow their Go implementation. Go's `strings.Split("", ",")` returns `[""]`, not `[]`. So when the `regions` label is missing, `split(service.labels["regions"], ",")` yields `[""]`, and `[""] all in actor.regions` is false. That example fails closed by accident of `split`, not by design; don't rely on it for a different function.
:::

## Map containment: `has`

`has` takes a map on the left and either a map or a key on the right.

| Form              | Right type     | True when                                                     |
| ----------------- | -------------- | ------------------------------------------------------------- |
| `m has {k: v, ...}` | `map<K, V>`  | every pair on the right is in `m` with an equal value         |
| `m has k`         | `K`            | `m` has key `k`                                               |

```sigil
service.labels has {
  "app.kubernetes.io/managed-by": "argocd",
  "platform.example.com/lifecycle": "ga",
}

service.labels has "app.kubernetes.io/managed-by"
```

The right-hand map doesn't have to be a literal. An empty right-hand map makes `has` true.

`m has "k"` and `"k" in m` mean the same thing. Keeping both forms, or letting `sigil fmt` rewrite one into the other, is an [open question](/project/open-questions/).

## Pattern matching: `like` and `matches`

Both take a `string` on the left and a pattern on the right, and the pattern must be a string literal (plain or raw) so it compiles once, at policy compile time. A pattern built from an expression is a compile error, and so is a pattern that fails to compile.

`like` is a glob. `*` matches any run of characters, including an empty one, and `?` matches exactly one character. Every other character matches itself.

```sigil
service.name like "payments-*"
```

`matches` is a Go RE2 regular expression. It's true if the pattern matches anywhere in the string, as with Go's `regexp.MatchString`, so anchor with `^` and `$` when you mean the whole string. Raw strings avoid double escaping:

```sigil
service.labels["team"] matches `^team-[a-z]+$`
```

RE2 runs in linear time in the input length, which keeps the [halting guarantee](/understanding/halting/) intact.

::: tip Proposed
The current design says "glob" without defining it. The proposal is `*` and `?` only, with `*` crossing `/`, and no character classes or escapes. Anything richer belongs in `matches`.
:::

## Optional default: `??`

`a ?? b` requires `a` to have an optional type `?T` and `b` to have type `T`. The result has type `T`: the value of `a` if present, otherwise `b`. `b` is only evaluated when `a` is absent.

```sigil
// assuming the kind declares `ticket: ?string` on Release
release.ticket ?? "none"
```

`??` is right-associative, so `a ?? b ?? c` means `a ?? (b ?? c)` and works when `a` and `b` are both `?T` and `c` is `T`.

Applying `??` to a value that isn't optional is a compile error. (proposed)

Struct types have no literal, so an optional struct (`?Release`) can't be unwrapped with `??` today. How to fix that is an [open question](/project/open-questions/).

## Arithmetic

Binary `+` and `-` are left-associative and defined only for these combinations:

| Left        | Operator | Right       | Result      |
| ----------- | -------- | ----------- | ----------- |
| `int`       | `+` `-`  | `int`       | `int`       |
| `float`     | `+` `-`  | `float`     | `float`     |
| `duration`  | `+` `-`  | `duration`  | `duration`  |
| `timestamp` | `+` `-`  | `duration`  | `timestamp` |
| `timestamp` | `-`      | `timestamp` | `duration`  |

Unary `-` applies to `int`, `float` and `duration`.

There's no `+` on strings or lists and no `*`, `/` or `%` at all. Integer and duration overflow is a runtime error, not a wrap-around; see [Evaluation semantics](/reference/evaluation/).

```sigil
release.soak + 2h >= min_soak
now - release.built_at > 2h        // assuming an input `now` and a field `built_at`, both timestamps
```

::: tip Proposed
The result types in this table, and the absence of string concatenation, are proposed. The current design lists `+` and `-` for "numbers, durations, timestamps" without spelling out the combinations.
:::

## Field access, indexing and calls

These are postfix and bind tightest.

`x.field` reads a field of a struct value. A field the struct type doesn't declare is a compile error:

```text
deploy/production.sigil:9:16: error: unknown field "teir" on type Service
  |
9 |   when service.teir == "critical"
  |                ^^^^
  = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

That message format is illustrative; the exact layout isn't fixed yet.

`common.name` reads a `let` through a whole-file import such as `use deploy.common`. See [Policy files](/reference/policy-files/#use).

`m[k]` indexes a map. `k` must have the map's key type. A missing key yields the zero value of the value type, like Go: `service.labels["absent"]` is `""`.

`xs[i]` indexes a list with an `int`. An index out of range, including a negative one, is a runtime error.

`f(a, b)` calls a host function declared in the kind with an `fn` signature. Arguments are positional, and their count and types must match the signature. Calling a name the kind doesn't declare is a compile error; policies can't define functions. Host functions must be pure. An error returned by a host function is a runtime error.

## Quantifiers

A quantifier tests a condition against every element of a list:

```sigil
any r in actor.roles: r like "sre-*"
all r in actor.roles: r != "admin"
```

| Form                  | True when                             | Empty list |
| --------------------- | ------------------------------------- | ---------- |
| `any x in xs: body`   | `body` holds for at least one element | false      |
| `all x in xs: body`   | `body` holds for every element        | true       |

The range `xs` must be a list; quantifying over a map is a compile error. The body must be `bool`. `x` is bound to each element in turn, has the list's element type, and is only visible inside the body. Evaluation stops at the first element that decides the result.

A quantifier starts an expression, while the binary `all in` and `any in` operators follow an operand. That position is how the parser tells `all r in xs: ...` apart from `a all in b`; the [Grammar](/reference/grammar/) page has the details.

The body extends as far right as possible:

```sigil
any r in actor.roles: r like "sre-*" and eligible
// parses as
any r in actor.roles: (r like "sre-*" and eligible)
```

To end a quantifier early, wrap it in parentheses:

```sigil
(any r in actor.roles: r like "sre-*") and eligible
```

The quantifier variable follows the no-shadowing rule: naming it after an input, param, let, imported name or host function is a compile error.

::: tip Proposed
The "extends as far right as possible" rule and the no-shadowing rule for quantifier variables are proposed. The current design shows quantifiers only in isolation.
:::

## Evaluation order

Operands are evaluated left to right. `and`, `or`, `??` and quantifiers skip work they don't need, and anything they skip can't raise a runtime error. Since expressions have no side effects and host functions are pure, evaluation order is otherwise unobservable.
