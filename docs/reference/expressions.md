---
title: Expressions
icon: mdi:function-variant
createTime: 2026/09/24 22:30:00
permalink: /reference/expressions/
---

::: info Draft specification
This page specifies the language as designed. Syntax, kinds, type checking, rules, decisions, asserts and the evaluation trace are implemented; composition (imports and invocation) and the CLI aren't yet. See [Open questions](/project/open-questions/).
:::

Expressions appear in `when` conditions, `assert` conditions, `let` bindings, param defaults, policy invocation arguments and decision payloads. Every expression has a static type that the compiler knows before evaluation, and nothing converts between types implicitly. The types themselves are on [Types](/reference/types/).

## Operator precedence

From lowest to highest:

| Level | Operators                   | Associativity   | Notes                                                                                     |
| ----- | --------------------------- | --------------- | ----------------------------------------------------------------------------------------- |
| 1     | `or`                        | left            | Short-circuits                                                                            |
| 1     | `xor`                       | none            | Evaluates both sides; doesn't mix with `or` without parentheses                           |
| 2     | `and`                       | left            | Short-circuits                                                                            |
| 3     | `not`                       | prefix          | Unary                                                                                     |
| 4     | `==` `!=` `<` `<=` `>` `>=` | none            | Strictly typed; no implicit coercion                                                      |
| 4     | `in`, `not in`              | none            | Element in list, substring in string                                                      |
| 4     | `all in`, `any in`          | none            | List subset and list intersection                                                         |
| 4     | `one in`, `exclusive in`    | none            | Exactly one, or at most one, element of a list is in another                              |
| 4     | `has`                       | none            | Map contains all given pairs, or a key                                                    |
| 4     | `like`, `matches`           | none            | Glob and RE2 regex; the pattern must be a literal                                         |
| 5     | `??`                        | right           | Default for optional values                                                               |
| 6     | `+` `-`                     | left            | Numbers, durations, timestamps                                                            |
| 7     | `-`, `present`              | prefix          | Unary minus; presence of an optional                                                      |
| 8     | `.field` `?.field` `[key]` `f(args)` | left (postfix) | Field access, optional chaining, map or list index, host function call              |

Parentheses override precedence as usual. Quantifiers (`any x in xs: ...`, `all x in xs: ...`) aren't in the table because they're prefix forms whose body extends as far right as possible; see [Quantifiers](#quantifiers).

A few consequences worth spelling out:

- `not` binds looser than comparisons, so `not a == b` means `not (a == b)`, and `not "admin" in actor.roles` means `not ("admin" in actor.roles)`. Prefer `"admin" not in actor.roles`.
- `??` binds tighter than comparisons, so `owner ?? "unknown" == "team-a"` means `(owner ?? "unknown") == "team-a"`.
- `+` binds tighter than `??`, so `a ?? b + c` means `a ?? (b + c)`.
- `xor` shares level 1 with `or` but can't be chained or mixed with it. `a xor b xor c` and `a or b xor c` are compile errors; parenthesize to say which grouping you mean.

::: tip Proposed
Level-4 operators are non-associative. `a < b < c`, `a == b == c` and `a in b == c` are compile errors; add parentheses to say what you mean. They all share one level, and refusing to chain them avoids a class of misreadings.
:::

## Boolean operators

`and`, `or`, `xor` and `not` take `bool` operands and produce `bool`. There's no truthiness: `when approvers { ... }` is a compile error because `approvers` is a `list<string>`, not a `bool`.

The operators are words, not `&&`, `||` and `!`. Words read better across multi-line conditions and pair with `not in`, `all in` and the other word operators.

`and` and `or` short-circuit and evaluate left to right. The right operand of `a and b` is never evaluated when `a` is false, which matters for [runtime errors](/reference/evaluation/): a list index or host function call on the right can't fault when the guard on the left fails.

`xor` is the textbook exclusive or: true when exactly one of its two operands is true.

| `a`     | `b`     | `a xor b` |
| ------- | ------- | --------- |
| `false` | `false` | `false`   |
| `false` | `true`  | `true`    |
| `true`  | `false` | `true`    |
| `true`  | `true`  | `false`   |

```sigil
release.hotfix xor release.scheduled
```

It can't short-circuit, because the result always depends on both sides, so both operands are evaluated and either can raise a runtime error. `xor` takes exactly two operands. Chaining it would compute parity (an odd number of true operands), which is almost never what a reader expects from `a xor b xor c`, so it's a compile error; for "exactly one of several" use [`one in`](#list-set-operators).

## Comparison

`==` and `!=` require both operands to have the same type. `3 == 3.0` is a compile error because `int` and `float` are different types, and so is `release.soak == 30` because `30` is an `int`, not a `duration`.

| Operators                   | Operand types                                         |
| --------------------------- | ----------------------------------------------------- |
| `==` `!=`                   | `bool`, `int`, `float`, `string`, `duration`, `timestamp` |
| `<` `<=` `>` `>=`           | `int`, `float`, `duration`, `timestamp`               |

String comparison is case-sensitive, unlike filt-rs, because Kubernetes labels and most identifiers in this domain are. `"Prod" == "prod"` is false.

Comparing an optional (`?T`) value is a compile error until it's unwrapped with `??`.

`==` doesn't work on lists, maps or structs. A policy rarely means "these two lists are identical"; it means subset, overlap or membership, which have their own operators. And one `==` would hide a walk over a whole nested value.

Strings aren't ordered. Byte-wise order is well defined, but it makes `"v10" < "v9"` true, which is exactly the result that gets a version rule wrong. Versions compare through a type the host defines with its own ordering; see [Host-ordered types](/reference/types/#host-ordered-types).

## Membership: `in` and `not in`

`in` has two meanings, picked by the type of the right-hand side:

| Form                 | Left type | Right type    | True when                         |
| -------------------- | --------- | ------------- | --------------------------------- |
| `x in xs`            | `T`       | `list<T>`     | `xs` contains an element equal to `x` |
| `s in t`             | `string`  | `string`      | `s` is a substring of `t`         |

```sigil
"deployer" in actor.roles                    // list element
"payments" in service.name                   // substring
service.tier in ["critical", "standard"]     // list literal
```

A map key is tested with [`has`](#map-containment-has), never with `in`, so there's one way to write it. `"env" in service.labels` is a compile error that suggests `service.labels has "env"`.

`x not in y` is exactly `not (x in y)`. The parser reads `not in` as a single operator when `not` follows an operand, and as unary `not` when it starts an expression.

## List set operators

`all in`, `any in`, `one in` and `exclusive in` take two lists of the same element type and produce `bool`.

| Form               | True when                                                         |
| ------------------ | ----------------------------------------------------------------- |
| `a all in b`       | every element of `a` is in `b` (subset)                           |
| `a any in b`       | at least one element of `a` is in `b` (intersection is non-empty) |
| `a one in b`       | exactly one distinct element of `a` is in `b`                     |
| `a exclusive in b` | at most one distinct element of `a` is in `b`                     |

```sigil
split(service.labels["regions"], ",") all in actor.regions
actor.teams any in service.owners
["prod-admin", "prod-auditor"] exclusive in actor.roles
[read, write, admin] one in outcome
```

`exclusive in` is mutual exclusion as separation-of-duties rules mean it: holding none of the listed values is fine, holding two is not. `one in` additionally requires one of them to be present. With two elements, `[a, b] one in xs` is `(a in xs) xor (b in xs)`.

Both count distinct elements of `a` that appear in `b`. Repeats don't count twice on either side, so `["x", "x"] exclusive in ["x"]` is true, and so is `["x", "y"] exclusive in ["x", "x"]`. A literal left side with duplicates, or with fewer than two elements, always gives the same answer, and the linter will warn about it.

An empty left side makes `all in` and `exclusive in` true, and `any in` and `one in` false. The vacuous truth of `[] all in b` is an [open question](/project/open-questions/): either keep the math and have the linter warn, or define an empty left side as false.

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

`m has k` is the only way to test for a key. `in` doesn't apply to maps, and `not in` doesn't either: write `not m has k`, which reads the same way because `not` binds looser than `has`.

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

A glob is `*` and `?` only, with `*` crossing `/` and `.`, and no character classes or escapes: Sigil matches strings, not paths. Anything richer belongs in `matches`.

## Optional default: `??`

`a ?? b` requires `a` to have an optional type `?T` and `b` to have type `T`. The result has type `T`: the value of `a` if present, otherwise `b`. `b` is only evaluated when `a` is absent.

```sigil
// assuming the kind declares `ticket: ?string` on Release
release.ticket ?? "none"
```

`??` is right-associative, so `a ?? b ?? c` means `a ?? (b ?? c)` and works when `a` and `b` are both `?T` and `c` is `T`.

Applying `??` to a value that isn't optional is a compile error.

Struct types have no literal, so an optional struct (`?Release`) can't be unwrapped with `??`. Its fields are read with optional chaining instead.

## Optional chaining: `?.`

`x?.name` reads a field of an optional struct. If `x` is absent, the result is absent; otherwise it's the field of the struct inside. The result is optional, so it's unwrapped with `??` like any other:

```sigil
// assuming the kind declares `release: ?Release`
release?.soak ?? 5m
```

A `?.` makes the rest of its chain optional too, as in TypeScript. A chain is a run of `.name`, `?.name` and `[index]` that no parentheses break. When a `?.` finds its operand absent, nothing after it in the chain runs, so it can't fail either:

```sigil
// Release declares `parent: ?Commit`; Commit declares `author: Actor` and `merged_by: ?Actor`
release.parent?.author.name ?? ""          // ?string, then string; `author` needs no `?.`
release.parent?.author.roles[5] ?? ""       // no index error when there's no parent
release.parent?.merged_by?.name ?? ""       // `merged_by` is optional itself, so it needs its own `?.`
```

- The type of a chain with a `?.` in it is its last link's type made optional. A last link that's optional already stays `?T`; optionals don't nest.
- A `?.` only skips what comes after an absent value. A link that is optional itself still needs its own `?.`: `release.parent?.merged_by.name` is a compile error that suggests `?.name`.
- Parentheses end a chain. `(release.parent?.author).name` reads a field of a `?Actor` and is a compile error.
- `?.` on a value that can't be absent is a compile error, like `??` on one: `service?.name` suggests `service.name`.
- `?.` reads struct fields only. There's no `?[` for indexing, because lists and maps can't be optional.

Optional chaining can't tell an absent struct from a present one whose field is zero: with `release?.soak ?? 0s`, both give `0s`. [`present`](#presence-present) can.

## Presence: `present`

`present x` is `true` when the optional `x` holds a value and `false` when it's absent. It tells absence apart from a zero value, which `??` can't:

```sigil
when not present release { deny("no_release") }
when present release.ticket { ... }                 // an empty ticket is present
when present release.parent?.merged_by { ... }      // any optional, including a chain
```

- The operand must be optional. `present` on a value that can't be absent is a compile error, like `??` and `?.`.
- It binds like unary minus, to one operand chain: `present release.parent and x` means `(present release.parent) and x`, and `present release.ticket ?? ""` is a compile error, because `present` applies first and yields a `bool`.
- It only tests. Inside `when present release { ... }`, `release` is still optional, and its fields are still read with `?.`. There's no flow typing that would narrow it to `Release`.

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

The table is complete: any other combination, including `+` on strings, is a compile error that says so.

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

Both rules, the body extending as far right as possible and no shadowing, are implemented.

## Decision values and `outcome`

Inside an `assert`, a policy can test what evaluation decided. Two things make that possible:

- A decision's name, used as an operand, is a value of type [`decision`](/reference/types/#decision). `approve` in `approve in outcome` refers to the decision, not to a constructor call; a constructor always has parentheses. Like `outcome`, a bare decision name is only a value inside an `assert` condition: `when deny == approve` has nothing to say, so it's a compile error that points at the constructor form.
- `outcome` is a `list<decision>` holding each distinct decision the host will get back, in the kind's declaration order. In a kind with `precedence` it holds exactly one element, the winner or the default. In a [collecting kind](/reference/kind-files/#collecting-kinds) it holds every decision that fired, or the default if the kind declares one and nothing fired.

```sigil
assert("sod_customer_dev",
  [customer_data_writer, development_environment_writer] exclusive in outcome)

assert("pii_needs_clearance",
  customer_data_writer not in outcome or actor.clearance == "pii")
```

`outcome` can only appear in an `assert` condition. A `when` condition or a `let` that read it could make a rule depend on its own result: `when admin not in outcome { admin("x") }` would fire exactly when it doesn't. See [Assertions](/reference/evaluation/#assertions) for when asserts run.

Decision values can be compared with `==` and `!=` and collected in lists, and nothing else. Reading a payload through `outcome` isn't possible yet; see [Open questions](/project/open-questions/#decision-values-and-outcome).

## Evaluation order

Operands are evaluated left to right. `and`, `or`, `??` and quantifiers skip work they don't need, and anything they skip can't raise a runtime error. Since expressions have no side effects and host functions are pure, evaluation order is otherwise unobservable.
