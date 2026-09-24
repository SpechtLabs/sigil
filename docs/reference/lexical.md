---
title: Lexical structure
icon: mdi:format-letter-case
createTime: 2026/09/24 22:30:00
permalink: /reference/lexical/
---

::: info Draft specification
This page specifies the language as designed. Nothing is implemented yet; see [Open questions](/project/open-questions/).
:::

Sigil source is UTF-8 text. The lexer turns it into a flat stream of tokens and throws away whitespace and comments, so newlines and indentation carry no meaning anywhere in the language. Every statement starts with a keyword or, for a policy invocation, with a name followed by `(`, which is how the parser finds statement boundaries (see [Grammar](/reference/grammar/)).

## Whitespace and comments

Spaces, tabs, carriage returns and newlines separate tokens and are otherwise ignored.

A comment starts with `//` and runs to the end of the line. There are no block comments.

```sigil
// This whole line is a comment.
let cleared = environment == "production" // so is this tail
```

## Identifiers

```text
identifier = [A-Za-z_][A-Za-z0-9_]*
```

Identifiers name inputs, types, fields, params, lets, host functions, decisions, quantifier variables and names bound by `use`. They're case-sensitive: `Release` and `release` are different names.

Unlike filt-rs, an identifier can't contain `-` or `.`. A key such as `platform.example.com/lifecycle` isn't a name; you reach it through map indexing:

```sigil
service.labels["platform.example.com/lifecycle"]
```

## Policy names

Policy names are a separate lexical form from identifiers: one or more identifiers joined by `.`, with no whitespace around the dots.

```text
policy_name = identifier ( "." identifier )*
```

`deploy.common`, `deploy.production` and `payments.production` are policy names; modules are named the same way. They only appear after the `policy`, `module` and `use` keywords, and a policy name resolves to a file path by turning each `.` into `/` and appending `.sigil`, so `deploy.production` loads `deploy/production.sigil`. In a selective import such as `use deploy.common.{cleared}`, the name ends where `.{` begins. See [Policy files](/reference/policy-files/).

::: tip Proposed
Dotted policy names need a lexing rule of their own. Treating them as their own token class, only valid after `policy`, `module` and `use`, is the proposed rule.
:::

## Keywords

These words are reserved and can't be used as identifiers.

| Group          | Keywords                                                                          |
| -------------- | --------------------------------------------------------------------------------- |
| Policy files   | `policy`, `module`, `use`, `as`, `param`, `let`, `when`                           |
| Kind files     | `kind`, `version`, `type`, `input`, `fn`, `decision`, `precedence`, `default`     |
| Operators      | `and`, `or`, `not`, `in`, `all`, `any`, `has`, `like`, `matches`                  |
| Literals       | `true`, `false`                                                                   |

The built-in type names (`bool`, `int`, `float`, `string`, `duration`, `timestamp`, `list`, `map`) aren't keywords. They only mean a type in type position, so a field declared as `duration: duration` is a field named `duration` of type `duration`. A kind can't declare a struct type with one of those names.

Decision names like `deny` or `approve` aren't keywords either. Each kind declares its own.

Keywords are allowed as field and payload names, because real Go structs have fields called `kind` or `type`. `service.type` is valid: the token after `.` is always read as a name. The same goes for field declarations in a `type` body, decision fields and named arguments. Top-level names such as inputs, params, lets and imported names must be plain identifiers. (proposed; without it, a Go field tagged `type` or `kind` couldn't be exported to a kind at all.)

## Literals

### Booleans

`true` and `false`.

### Integers and floats

`int` and `float` are distinct types, and the literal form decides which one you get.

```sigil
42      // int
0       // int
0.5     // float
3.0     // float
```

A float literal needs digits on both sides of the point: `.5` and `5.` aren't valid. Integer literals are decimal and fit in a signed 64-bit integer; a literal outside that range is a compile error. Negative numbers are written with unary minus (`-3`), which is an operator, not part of the literal.

The current design doesn't specify hex, octal, binary, exponent notation or `_` digit separators. They're not part of the language until someone decides otherwise.

### Strings

Double-quoted strings use Go's escape sequences (`\n`, `\t`, `\\`, `\"`, `é` and the rest of the Go set).

```sigil
"deployer"
"line one\nline two"
```

Raw strings use backticks, as in Go. Nothing inside them is an escape, which makes them the natural form for regular expressions:

```sigil
service.labels["team"] matches `^team-[a-z]+$`
```

A raw string can span lines. A double-quoted one can't.

### Durations

A duration literal is an integer followed immediately by a unit, and units can be chained without spaces.

| Unit | Meaning                                |
| ---- | -------------------------------------- |
| `ms` | millisecond                            |
| `s`  | second                                 |
| `m`  | minute                                 |
| `h`  | hour                                   |
| `d`  | exactly 24 hours                       |

```sigil
30m
1h30m
2d
500ms
```

`d` is a fixed 24 hours. It has no calendar or daylight-saving meaning, because the language has no clock or time zone to apply one to.

::: tip Proposed
Two rules the current design leaves open, proposed here. First, `d` means exactly `24h`. Second, in a chained literal each unit may appear at most once, largest first: `1h30m` is valid, `30m1h` and `1h1h` are compile errors. Fractional components such as `1.5h` aren't allowed; write `1h30m`.
:::

### Lists

```sigil
["standard", "internal"]
[]
```

Every element must have the same type. See [Types](/reference/types/) for how the empty list gets its type.

### Maps

```sigil
{"app.kubernetes.io/managed-by": "argocd", "platform.example.com/lifecycle": "ga"}
{}
```

Keys and values are expressions; every key must share one type and every value must share one type.

### Trailing commas

A trailing comma is allowed in every comma-separated list: list and map literals, function call arguments, decision payloads, policy invocation arguments and selective imports. It keeps diffs to one line and keeps text-templated output valid.

```sigil
production(
  approvers: ["payments-leads", "security-leads"],
  tiers: ["standard"],
)
```

## Operators and punctuation

| Token                       | Used for                                    |
| --------------------------- | ------------------------------------------- |
| `==` `!=` `<` `<=` `>` `>=` | Comparison                                  |
| `+` `-`                     | Arithmetic, unary minus                     |
| `??`                        | Optional default                            |
| `.`                         | Field access, qualified import access       |
| `[` `]`                     | List literals, indexing                     |
| `{` `}`                     | Map literals, rule bodies, type bodies, selective imports |
| `(` `)`                     | Grouping, calls, decision constructors, policy invocations |
| `:`                         | Type annotations, named arguments, quantifier bodies, map entries |
| `,`                         | Separators                                  |
| `=`                         | `let` bindings, param and payload defaults  |
| `->`                        | Return type in `fn` declarations            |
| `?`                         | Optional type prefix (`?string`)            |

The lexer uses longest match, so `??` is one token, `<=` is one token, and `->` is one token.
