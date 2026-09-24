---
title: Types
icon: mdi:shape-outline
createTime: 2026/09/24 22:30:00
permalink: /reference/types/
---

::: info Draft specification
This page specifies the language as designed. Nothing is implemented yet; see [Open questions](/project/open-questions/).
:::

Sigil is statically typed. The compiler knows the type of every input, param, let and expression before a policy runs, and it checks them against the kind the policy implements. Nothing converts implicitly: an `int` never becomes a `float`, a `string` never becomes a `duration`, and a `?T` never becomes a `T` without `??`.

## Type summary

| Type           | Literal        | Zero value     | Notes                                                                    |
| -------------- | -------------- | -------------- | ------------------------------------------------------------------------ |
| `bool`         | `true`         | `false`        |                                                                          |
| `int`          | `3`            | `0`            | Signed 64-bit. No implicit conversion to `float`                         |
| `float`        | `0.5`          | `0.0`          | 64-bit IEEE 754. No implicit conversion to `int`                         |
| `string`       | `"a"`          | `""`           | Case-sensitive comparison                                                |
| `duration`     | `30m`          | `0s`           | Go `time.Duration`                                                       |
| `timestamp`    | none           | Go zero time   | Comes from input only                                                    |
| `list<T>`      | `["a", "b"]`   | `[]`           |                                                                          |
| `map<K, V>`    | `{"k": "v"}`   | `{}`           | Missing key yields the zero value of `V`, like Go                        |
| `?T`           | none           | absent         | Optional, from Go pointer fields. Must be unwrapped with `??` before use |
| Struct types   | none           | all fields zero | Declared in the kind, reached with `.field`                             |

Zero values matter in one place: a missing map key. `service.labels["absent"]` is `""`, and indexing a missing key in a `map<string, int>` gives `0`.

## Scalars

### `bool`

The only type a `when` condition, a quantifier body, or an operand of `and`, `or` and `not` can have.

### `int` and `float`

Two distinct numeric types. `3 == 3.0` is a compile error, and so is `count + 0.5` when `count` is an `int`. There's no conversion function in the language; if a policy needs one, the host declares it as an `fn` in the kind.

`int` is a signed 64-bit integer. Overflow in `+` or `-` is a runtime error. The Go mapping (`int` and `int64` both become `int`) is on [Kind files](/reference/kind-files/).

### `string`

A sequence of bytes, normally UTF-8. Equality is byte-for-byte and case-sensitive.

### `duration`

A length of time with nanosecond resolution, matching Go's `time.Duration`. Duration literals are described on [Lexical structure](/reference/lexical/). A bare number is never a duration: `release.soak > 30` is a compile error, `release.soak > 30m` is fine.

### `timestamp`

A point in time, matching Go's `time.Time`. It has no literal form and can only come from input. The language has no clock, so a policy that needs "now" gets it as an input from the host; see [Evaluation semantics](/reference/evaluation/).

## Collections

### `list<T>`

An ordered sequence of values of one type. List literals must be homogeneous: `["a", 1]` is a compile error.

An empty literal `[]` takes its element type from context: the declared type of a param, the other operand of a binary operator, or the payload field it's passed to. An empty list with no context, such as `let nothing = []`, is a compile error. (proposed)

### `map<K, V>`

An unordered collection of key-value pairs. Kinds exported from Go only produce `map<string, V>`, since `NewKind` rejects non-string keys, so in practice `K` is always `string`. Map literals follow the same homogeneity and empty-literal rules as lists.

Indexing a map with a missing key yields the zero value of `V`. Use `m has "k"` or `"k" in m` when absence and emptiness need to be told apart.

## Optional types: `?T`

An optional value is either a `T` or absent. Optional types come from Go pointer fields in the kind (`*string` becomes `?string`); policies can't write optional literals, and params can't be declared optional.

An optional must be unwrapped with `??` before anything else touches it:

```sigil
// assuming the kind declares `ticket: ?string` on Release
release.ticket ?? "none" == "CHG-1042"      // ok
release.ticket == "CHG-1042"                // compile error: ?string vs string
```

The compiler rejects comparisons, operators, indexing, field access and function arguments that receive a `?T` where a `T` is required.

::: warning Open question: optional structs
Struct types have no literal, so there's nothing to put on the right of `??` for a `?Release`. As specified, an optional struct field can't be used at all. The candidates are optional chaining (`release?.soak` yielding `?duration`), a presence test with flow typing (`when present(release) { ... }`), or having `NewKind` reject pointer-to-struct fields. See [Open questions](/project/open-questions/).
:::

## Struct types

A struct type is a named set of typed fields, declared in the kind:

```sigil
type Service {
  name: string
  tier: string
  owners: list<string>
  labels: map<string, string>
}
```

Policies reach fields with `.field` and can't construct struct values. Accessing a field the type doesn't declare is a compile error, which is how a typo like `service.teir` gets caught before it can silently disable a rule. See [Strict schema, forgiving data](/understanding/strictness/) for why.

Struct names and field names are identifiers. Struct types are nominal: two struct types with the same fields are still different types.

## Type rules by operator

The rules for each operator are on [Expressions](/reference/expressions/). In short:

| Operation                   | Allowed types                                                             |
| --------------------------- | ------------------------------------------------------------------------- |
| `==` `!=`                   | same type on both sides; `bool`, `int`, `float`, `string`, `duration`, `timestamp` |
| `<` `<=` `>` `>=`           | same type on both sides; `int`, `float`, `duration`, `timestamp`          |
| `and` `or` `not`            | `bool`                                                                    |
| `in`                        | `T in list<T>`, `K in map<K, V>`, `string in string`                      |
| `all in` `any in`           | `list<T>` on both sides                                                   |
| `has`                       | `map<K, V> has map<K, V>`, `map<K, V> has K`                              |
| `like` `matches`            | `string` and a string literal                                             |
| `??`                        | `?T ?? T`, result `T`                                                     |
| `+` `-`                     | see the arithmetic table on [Expressions](/reference/expressions/)        |

## Type inference

`let` bindings have no annotation; their type is the type of the expression. `param` declarations always carry a type. Decision payload fields and host function parameters take their types from the kind, and arguments must match exactly.
