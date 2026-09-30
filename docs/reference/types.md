---
title: Types
icon: mdi:shape-outline
createTime: 2026/09/24 22:30:00
permalink: /reference/types/
---

Sigil's types: what each one holds, its literal, its zero value and which operators accept it.

Sigil is statically typed. The compiler knows the type of every input, param, let and expression before a policy runs, and checks them against the kind the policy implements. Nothing converts implicitly: an `int` never becomes a `float`, a `string` never becomes a `duration` or an enum, and a `?T` never becomes a `T` without `??`.

## Type summary

| Type         | Literal      | Zero value      | Notes                                                                                          |
| ------------ | ------------ | --------------- | ---------------------------------------------------------------------------------------------- |
| `bool`       | `true`       | `false`         |                                                                                                |
| `int`        | `3`          | `0`             | Signed 64-bit. No implicit conversion to `float`                                               |
| `float`      | `0.5`        | `0.0`           | 64-bit IEEE 754. No implicit conversion to `int`                                               |
| `string`     | `"a"`        | `""`            | Case-sensitive comparison                                                                      |
| `duration`   | `30m`        | `0s`            | Go `time.Duration`                                                                             |
| `timestamp`  | none         | Go zero time    | Comes from input only                                                                          |
| `list<T>`    | `["a", "b"]` | `[]`            |                                                                                                |
| `map<K, V>`  | `{"k": "v"}` | `{}`            | Missing key yields the zero value of `V`, like Go                                              |
| `?T`         | none         | absent          | Optional, from Go pointer fields. Unwrapped with `??`; an optional struct is read with `?.`    |
| Struct types | none         | all fields zero | Declared in the kind, reached with `.field`                                                    |
| Enums        | `critical`   | none            | Declared in the kind. A bare value typed by context, or `Tier.critical`. Not ordered           |
| `decision`   | `approve`    | none            | A decision's name as a value; only useful in `assert`                                          |
| Candidates   | none         | none            | `outcome.review` in an `assert`: one decision's payload and reason. Ranged over, never indexed |

Zero values matter in one place, a missing map key: `service.labels["absent"]` is `""`, and indexing a missing key in a `map<string, int>` gives `0`.

::: warning Planned
Opaque types ordered by the host, for versions and similar values: see [Host-ordered types](/project/planned/#host-ordered-types).
:::

## Scalars

### `bool`

The only type a `when` condition, an `assert` condition, a quantifier or filter body, or an operand of `and`, `or`, `xor` and `not` can have.

### `int` and `float`

- Two distinct numeric types. `3 == 3.0` is a compile error, and so is `count + 0.5` when `count` is an `int`.
- There's no conversion function in the language. A host that needs one declares it as an `fn` in the kind.
- `int` is a signed 64-bit integer. Overflow in `+` or `-` is a runtime error.
- The lexer checks an integer literal before applying unary minus. Write the minimum signed value as `(-9223372036854775807 - 1)`; kind export uses that spelling.
- Float literals must fit in a `float64`. Constant arithmetic that produces a non-finite float is a compile error.
- Exported floats use decimal notation, since Sigil has no exponent syntax.
- Go's `int` and `int64` both become `int`; see [Go type mapping](/reference/go-api/#go-type-mapping).

### `string`

A sequence of bytes, normally UTF-8. Equality is byte-for-byte and case-sensitive.

### `duration`

- A length of time with nanosecond resolution, matching Go's `time.Duration`.
- Literals use the units `ms`, `s`, `m`, `h` and `d` (a fixed 24 hours); see [Durations](/reference/lexical/#durations).
- Printed values are normalized to the largest units, so a `24h` param shows up as `1d` in a trace.
- A bare number is never a duration: `release.soak > 30` is a compile error, `release.soak > 30m` is fine.

### `timestamp`

- A point in time, matching Go's `time.Time`.
- No literal form. It can only come from input.
- The language has no clock. A policy that needs "now" gets it as an input from the host; see [Evaluation semantics](/reference/evaluation/).

## Collections

### `list<T>`

An ordered sequence of values of one type. List literals must be homogeneous: `["a", 1]` is a compile error.

An empty literal `[]` takes its element type from context:

- the declared type of a param,
- the other operand of `in`, `all in`, `any in`, `one in`, `exclusive in`, `has` or `??`,
- the payload field or host function parameter it's passed to,
- another element of the list or map it sits in.

An empty list with no context, such as `let nothing = []`, is a compile error. `actor.regions != []` is a compile error too: the `[]` takes `list<string>` from the other side, and `==` and `!=` aren't defined for lists. To test for emptiness, use a quantifier or a host function; see [Test whether a list is empty](/guides/patterns/#test-whether-a-list-is-empty).

### `map<K, V>`

An unordered collection of key-value pairs.

- The key type is any scalar, `bool`, `int`, `float`, `string`, `duration` or `timestamp`, following Go's rule for map keys, or an [enum](#enums).
- A list, map, optional, `decision` or struct can't be a key. Structs have no equality.
- A key in a literal is an expression, so a string key is quoted: `{"team": "payments"}`. A bare `team` would read a name.
- Map literals follow the same homogeneity and empty-literal rules as lists.
- Indexing a map with a missing key yields the zero value of `V`. An enum has no zero value, so it can't be `V`.
- `m has "k"` tells absence from emptiness. `"k" in m` is a compile error.

## `decision`

A decision's name, `approve`, or a decision with one of its reasons, `approve.release_manager`, as a value.

- The type is closed: its values are exactly the kind's decisions and their reasons.
- `decision` has no name in source, so a param, input or field can't be declared with it.

Operators, `outcome` and where decision values may appear: [Decision values and `outcome`](/reference/expressions/#decision-values-and-outcome).

## Candidates

One of a decision's candidates in `outcome.<decision>`, with the decision's payload fields and `reason`.

- A candidate has no name in source and no equality.
- Type errors name it after its decision, `review candidate`, and the list `list<review candidate>`.

What a policy can do with candidates, and what the list holds in each `collect` mode: [Candidates](/reference/expressions/#candidates).

## Optional types: `?T`

An optional value is either a `T` or absent.

- Optional types come from Go pointer fields in the kind: `*string` becomes `?string`.
- Policies can't write optional literals, and params can't be declared optional.
- A kind can't declare an optional list or map. A Go pointer to a slice or a map is rejected by `NewKind`, and `?list<T>` or `?map<K, V>` is an error in a kind file.
- An [optional chain](/reference/expressions/#optional-chaining) that ends at a list or map field still has type `?list<T>` or `?map<K, V>`, and unwraps with `?? []` or `?? {}`.
- The compiler rejects comparisons, operators, indexing, field access and function arguments that receive a `?T` where a `T` is required. Unwrap with `??` first:

```sigil
// assuming the kind declares `ticket: ?string` on Release
release.ticket ?? "none" == "CHG-1042"      // ok
release.ticket == "CHG-1042"                // compile error: `==` can't compare ?string
```

An optional struct has no literal to put on the right of `??`, so its fields are read with [optional chaining](/reference/expressions/#optional-chaining). `release?.soak` is a `?duration`, which then unwraps as usual:

```sigil
// assuming the kind declares `release: ?Release`
release?.soak ?? 0s >= 24h
```

`present x` tests whether an optional holds a value, without unwrapping it; see [Presence](/reference/expressions/#presence-present).

## Struct types

```sigil
type Service {
  name: string
  tier: Tier
  owners: list<string>
  labels: map<string, string>
}
```

A struct type is a named set of typed fields, declared in the kind.

- Policies reach fields with `.field` and can't construct struct values.
- Accessing a field the type doesn't declare, such as `service.teir`, is a compile error.
- Struct names and field names are identifiers.
- Struct types are nominal: two struct types with the same fields are still different types.
- Structs have no equality. `==` doesn't apply to them, and neither do `in` and the list operators when the elements are structs.

`service.owner in owners` is a compile error that suggests comparing a field that identifies them, such as `service.owner.name in owner_names`.

Why: [Strict schema, forgiving data](/understanding/strictness/).

## Enums

```sigil
enum Tier: critical | standard | internal
```

```sigil
param tiers: list<Tier> = [standard, internal]

when service.tier == critical { ... }
when service.tier in tiers { ... }
```

An enum is a closed set of named values, declared in the kind; see [`enum`](/reference/kind-files/#enum).

| Where                                                                         | Allowed                                                       |
| ----------------------------------------------------------------------------- | ------------------------------------------------------------- |
| Struct field, input, host function parameter and result, payload field, param | Yes                                                           |
| List element `list<Tier>`, optional `?Tier`, map key `map<Tier, V>`           | Yes                                                           |
| Map value `map<K, Tier>`                                                      | No: a missing key yields the zero value, and an enum has none |

- A value is its bare name, `critical`, which takes its enum from context, or qualified with its enum, `Tier.critical`; see [Enum values](/reference/expressions/#enum-values).
- Enums are nominal. Two enums with the same values are different types, an enum is never a `string`, and a string literal never converts to one.
- Values have equality: `==`, `!=`, `in`, the list operators, and `has` on a map with enum keys. They have no order, so `<`, `like`, `matches` and `+` don't apply.
- A param of an enum type takes a constant default, and no bounds.
- Traces, `sigil eval` output and JSON print a value by its name: `critical`.
- A host value outside the set is a [runtime error](/reference/evaluation/#runtime-errors) when a rule reads it.

```text
deploy/production.sigil:9:8: error: `==` needs operands of the same type, found Tier and string
  |
9 |   when service.tier == "critical"
  |        ^^^^^^^^^^^^^^^^^^^^^^^^^^
  = help: an enum value is a bare name; write `critical`
```

Why: [Typos in values](/understanding/strictness/#typos-in-values).

## Type rules by operator

The rules for each operator are on [Expressions](/reference/expressions/).

| Operation               | Allowed types                                                                                           |
| ----------------------- | ------------------------------------------------------------------------------------------------------- |
| `==` `!=`               | same type on both sides; `bool`, `int`, `float`, `string`, `duration`, `timestamp`, `decision`, an enum |
| `<` `<=` `>` `>=`       | same type on both sides; `int`, `float`, `duration`, `timestamp`                                        |
| `and` `or` `xor` `not`  | `bool`                                                                                                  |
| `in`                    | `T in list<T>`, `string in string`; `T` without structs; map keys use `has`                             |
| `all in` `any in`       | `list<T>` on both sides                                                                                 |
| `one in` `exclusive in` | `list<T>` on both sides                                                                                 |
| `has`                   | `map<K, V> has map<K, V>`, `map<K, V> has K`                                                            |
| `like` `matches`        | `string` and a string literal                                                                           |
| `??`                    | `?T ?? T`, result `T`                                                                                   |
| `+` `-`                 | see the arithmetic table on [Expressions](/reference/expressions/)                                      |

- `in`, the list operators and `has` compare elements structurally: two lists are equal when they have the same elements in the same order, two maps when they have the same keys with equal values. So `["eu-1"] in [["eu-1"], ["us-1"]]` works.
- Elements that are or contain structs or candidates can't be compared; neither has equality.

## Type inference

- `let` bindings have no annotation. Their type is the type of the expression.
- `param` declarations always carry a type.
- A bare enum value takes its enum from the context it's used in, and a qualified one, `Tier.critical`, names it; see [Enum values](/reference/expressions/#enum-values).
- Decision payload fields and host function parameters take their types from the kind, and arguments must match exactly.
