---
title: Types
icon: mdi:shape-outline
createTime: 2026/09/24 22:30:00
permalink: /reference/types/
---

::: info Draft specification
This page specifies the language as designed. Syntax, kinds, type checking, rules, decisions, asserts and the evaluation trace are implemented; composition (imports and invocation) and the CLI aren't yet. See [Open questions](/project/open-questions/).
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
| `?T`           | none           | absent         | Optional, from Go pointer fields. Unwrapped with `??`; an optional struct is read with `?.` |
| Struct types   | none           | all fields zero | Declared in the kind, reached with `.field`                             |
| Ordered types  | none           | none           | Opaque, declared in the kind, ordered by the host (proposed)             |
| `decision`     | `approve`      | none           | A decision's name as a value; only useful in `assert` (proposed)         |

Zero values matter in one place: a missing map key. `service.labels["absent"]` is `""`, and indexing a missing key in a `map<string, int>` gives `0`.

## Scalars

### `bool`

The only type a `when` condition, an `assert` condition, a quantifier body, or an operand of `and`, `or`, `xor` and `not` can have.

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

An empty literal `[]` takes its element type from context: the declared type of a param, the other operand of a binary operator, or the payload field it's passed to. An empty list with no context, such as `let nothing = []`, is a compile error.

### `map<K, V>`

An unordered collection of key-value pairs. The key type follows Go's rule for map keys: any scalar (`bool`, `int`, `float`, `string`, `duration`, `timestamp`), never a list, map, optional or `decision`. Structs can't be keys, because they have no equality. In practice most keys are strings, because labels are. Map literals follow the same homogeneity and empty-literal rules as lists.

Indexing a map with a missing key yields the zero value of `V`. Use `m has "k"` when absence and emptiness need to be told apart; `"k" in m` is a compile error.

## `decision`

Every decision the kind declares is also a value of type `decision`, written as its bare name: `approve`, `customer_data_writer`. The constructor `approve("release_manager")` builds a candidate; the bare `approve` only names the decision. The type is closed: its values are exactly the kind's decisions, so a misspelled decision name is a compile error like any other unknown name.

`decision` values support `==`, `!=` and the list operators, and appear in list literals such as `[read, write, admin]`. The one place they come from evaluation is [`outcome`](/reference/expressions/#decision-values-and-outcome), a `list<decision>` that only `assert` conditions can read.

A param can't have type `decision` or `list<decision>`, and a `decision` has no zero value, so it can't be a map value. Like `outcome`, a bare decision name is only a value inside an `assert` condition.

## Optional types: `?T`

An optional value is either a `T` or absent. Optional types come from Go pointer fields in the kind (`*string` becomes `?string`); policies can't write optional literals, and params can't be declared optional. Lists and maps can't be optional: a Go pointer to a slice or a map is rejected by `NewKind`, and `?list<T>` or `?map<K, V>` is an error in a kind file, because an absent collection would read the same as an empty one.

An optional must be unwrapped with `??` before anything else touches it:

```sigil
// assuming the kind declares `ticket: ?string` on Release
release.ticket ?? "none" == "CHG-1042"      // ok
release.ticket == "CHG-1042"                // compile error: ?string vs string
```

The compiler rejects comparisons, operators, indexing, field access and function arguments that receive a `?T` where a `T` is required.

An optional struct has no literal to put on the right of `??`, so its fields are read with [optional chaining](/reference/expressions/#optional-chaining) instead. `release?.soak` is a `?duration`, which then unwraps as usual:

```sigil
// assuming the kind declares `release: ?Release`
release?.soak ?? 0s >= 24h
```

`present x` tests whether an optional holds a value, without unwrapping it; see [Presence](/reference/expressions/#presence-present).

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

Structs have no equality. `==` doesn't apply to them, and neither do `in` and the list operators when the elements are structs: `service.owner in owners` is a compile error that suggests comparing a field that identifies them, such as `service.owner.name in owner_names`.

## Host-ordered types

::: tip Proposed
Host-ordered types are the chosen direction for versions and other domain values with their own ordering. They aren't implemented yet.
:::

Some values have an order the language can't know: semantic versions, calendar versions, a vendor's release numbers. A host declares a type for them in the kind, and the ordering comes from Go:

```sigil
// kind file
type Version ordered
fn semver(string) -> Version
```

```sigil
// policy
when semver(release.version) < semver("1.4.0") {
  deny("client_too_old")
}
```

- **The Go side.** The host registers the Go type explicitly, and names it for policies:

  ```go
  policy.WithOrdered[*semver.Version]("Version")
  ```

  The type needs a method `Compare(T) int` that returns a negative number, zero or a positive number, the convention `time.Time`, `netip.Addr` and most version libraries already follow. The method is mandatory: `NewKind` panics if the type lacks it. It's never declared as an `fn` in the kind, so the kind file only says that the type is ordered, not how.
- **Pointers.** The registered Go type is exact. Most version libraries put `Compare` on a pointer, so registering `*semver.Version` makes that pointer type the ordered `Version`, and a field one pointer deeper, `**semver.Version`, is `?Version`. A nil value of a registered pointer type that reaches a comparison is a runtime error; a field that can really be missing should be declared one pointer deeper, as an optional. A type that isn't registered keeps its usual mapping even if it has a `Compare` method, so adding a method in Go never changes the contract by itself.
- **Text.** Traces, errors and test output print a value as text. `NewKind` picks the method once, when the type is registered: `MarshalText` from `encoding.TextMarshaler` if the type has it, otherwise `String` from `fmt.Stringer`. A type with neither makes `NewKind` panic, because the fallback, Go's `%v`, can print a pointer's address and would break [determinism](/reference/evaluation/#determinism). If `MarshalText` returns an error for a value, `String` is used when the type has it, and otherwise the text is `<Version: error text>`, so printing a trace never fails an evaluation.
- **JSON input.** `sigil eval` and `sigil test` read input through `encoding/json`, so a field of the type decodes from a JSON string when the type implements `encoding.TextUnmarshaler` (or `json.Unmarshaler`). Nothing requires it, but without it an input file can't set the field.
- **Operators.** `<`, `<=`, `>`, `>=`, `==` and `!=` call `Compare`. So do `in` and the list operators when the elements are of the type. Only values of the same type compare: a `Version` never compares with another ordered type or with a string.
- **Opaque.** A policy can't read inside the value, has no literal for it and can't declare a param of the type. Values come from inputs and host functions, such as `semver` above. Parsing stays with the host, so semver, calver or a custom scheme are all just Go.
- **Errors.** A string the parsing function rejects is that function's error, which is a [runtime error](/reference/evaluation/#runtime-errors). `Compare` must be a total order, pure and deterministic, the same contract as a host function.
- **Map keys.** An ordered type can't be a map key.

A built-in `version` type was considered and rejected. Semver's order isn't a layout that a `time.Parse`-style format string could describe: prereleases sort before releases, their identifiers compare numerically or lexically depending on their content, and build metadata is ignored. The language would have to own those rules, and every other scheme's, forever.

## Type rules by operator

The rules for each operator are on [Expressions](/reference/expressions/). In short:

| Operation                   | Allowed types                                                             |
| --------------------------- | ------------------------------------------------------------------------- |
| `==` `!=`                   | same type on both sides; `bool`, `int`, `float`, `string`, `duration`, `timestamp`, `decision`, ordered types |
| `<` `<=` `>` `>=`           | same type on both sides; `int`, `float`, `duration`, `timestamp`, ordered types |
| `and` `or` `xor` `not`      | `bool`                                                                    |
| `in`                        | `T in list<T>`, `string in string`; `T` without structs; map keys use `has` |
| `all in` `any in`           | `list<T>` on both sides                                                   |
| `one in` `exclusive in`     | `list<T>` on both sides                                                   |
| `has`                       | `map<K, V> has map<K, V>`, `map<K, V> has K`                              |

`in`, the list operators and `has` compare elements structurally: two lists are equal when they have the same elements in the same order, two maps when they have the same keys with equal values. That's what makes `["eu-1"] in [["eu-1"], ["us-1"]]` work. Elements that are or contain structs can't be compared, because structs have no equality.
| `like` `matches`            | `string` and a string literal                                             |
| `??`                        | `?T ?? T`, result `T`                                                     |
| `+` `-`                     | see the arithmetic table on [Expressions](/reference/expressions/)        |

## Type inference

`let` bindings have no annotation; their type is the type of the expression. `param` declarations always carry a type. Decision payload fields and host function parameters take their types from the kind, and arguments must match exactly.
