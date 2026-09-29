---
title: Lexical structure
icon: mdi:format-letter-case
createTime: 2026/09/24 22:30:00
permalink: /reference/lexical/
---

The tokens a `.sigil` file, which is UTF-8 text, is made of: whitespace, comments, identifiers, keywords, literals and operators.

Why: [Why the language looks like this](/understanding/language-choices/).

## Whitespace and comments

- Spaces, tabs, carriage returns and newlines separate tokens and are otherwise ignored. Newlines and indentation carry no meaning anywhere in the language; see [Statement boundaries](/reference/grammar/#statement-boundaries).
- A comment starts with `//` and runs to the end of the line, not including the line break (`\n` or `\r\n`).
- There are no block comments.
- The parser treats a comment like whitespace. The lexer still emits it as a token, so tools that rewrite source, such as `sigil fmt`, can put it back where it was.

```sigil
// This whole line is a comment.
let cleared = environment == "production" // so is this tail
```

## Identifiers

```text
identifier = [A-Za-z_][A-Za-z0-9_]*
```

- Identifiers name inputs, types, fields, params, lets, host functions, decisions, quantifier and filter variables and names bound by `use`.
- Case-sensitive: `Release` and `release` are different names.
- ASCII only. No `-` or `.`.

A key such as `platform.example.com/lifecycle` isn't a name; reach it through map indexing:

```sigil
service.labels["platform.example.com/lifecycle"]
```

## Policy names

```text
policy_name = identifier ( "." identifier )*
```

- One or more identifiers joined by `.`, with no whitespace around the dots.
- `deploy.common`, `deploy.production` and `payments.production` are policy names. Modules are named the same way.
- Every segment is an identifier, so a keyword can't be one: `access.type` and `deploy.default` aren't valid names.
- Policy names only appear after the `policy`, `module` and `use` keywords.
- A name in a `use` refers to the document whose header carries that name, wherever it lives in the bundle. It isn't a file path. See [Name resolution](/reference/bundles/#name-resolution).
- In a selective import such as `use deploy.common.{cleared}`, the name ends where `.{` begins.
- The lexer doesn't know about policy names: `deploy.common` is three tokens, and the parser joins them and rejects whitespace around the dots. The [grammar](/reference/grammar/#policy-files) defines `PolicyName` from tokens.

## Keywords

These words are reserved and can't be used as identifiers.

| Group          | Keywords                                                                          |
| -------------- | --------------------------------------------------------------------------------- |
| Policy files   | `policy`, `module`, `use`, `as`, `param`, `let`, `pub`, `when`, `assert`          |
| Kind files     | `kind`, `version`, `type`, `input`, `fn`, `decision`, `precedence`, `collect`, `default` |
| Operators      | `and`, `or`, `xor`, `not`, `in`, `all`, `any`, `filter`, `one`, `exclusive`, `has`, `like`, `matches`, `present` |
| Values         | `true`, `false`, `outcome`                                                        |

Words that aren't keywords:

| Word | Status |
| --- | --- |
| `bool`, `int`, `float`, `string`, `duration`, `timestamp`, `list`, `map` | Built-in type names. They only mean a type in type position, so `duration: duration` is a field named `duration` of type `duration`. A kind can't declare a struct type with one of these names |
| Decision names, such as `deny` or `approve` | Each kind declares its own. They're identifiers in the policy's namespace like inputs and lets; see [Identifiers](/reference/policy-files/#identifiers) |
| `ordered` | Not reserved |

::: warning Planned
[Host-ordered types](/project/planned/#host-ordered-types) would use `ordered` in `type Version ordered`.
:::

### Keywords as names

- Keywords are allowed as field and payload names: `service.type` is valid, because the token after `.` is always read as a name.
- The same holds for field declarations in a `type` body, decision fields and named arguments.
- Top-level names (inputs, params, lets, imported names, host functions, decisions, types), decision reasons, and quantifier and filter variables must be plain identifiers; see [Keywords as field names](/reference/grammar/#keywords-as-field-names).

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

- A float literal needs digits on both sides of the point: `.5` and `5.` aren't valid.
- Integer literals are decimal and fit in a signed 64-bit integer. A literal outside that range is a compile error.
- Leading zeros are allowed and carry no meaning: `007` is the integer seven, never octal. `sigil fmt` leaves them as written.
- Negative numbers are written with unary minus (`-3`), which is an operator, not part of the literal.
- Any other letter right after digits starts a duration unit, so `5w` is an error about an unknown duration unit.

Other numeric notations are lexical errors. Each error names the notation and writes the number out for you:

| Notation | Example | Suggestion |
| --- | --- | --- |
| Hexadecimal | `0x10` | `16` |
| Octal | `0o20` | `16` |
| Binary | `0b10000` | `16` |
| Exponent | `1e3` | `1000` |
| Exponent with a fraction | `1.5e3` | the float `1500.0` |
| `_` digit separator | `1_000` | `1000` |

```text
20:21 (r): error: `0x10` is hex notation, which Sigil doesn't have
   |
20 | when release.soak < 0x10 or 1e3 > 1_000 {
   |                     ^^^^
   = help: write the decimal integer `16`
```

### Strings

Double-quoted strings use Go's escape sequences for interpreted string literals: `\n`, `\t`, `\\`, `\"`, `\xhh`, `\u00e9`, `\U0001F600`, three-digit octal `\101` and the rest of the Go set.

```sigil
"deployer"
"line one\nline two"
```

Raw strings use backticks, as in Go. Nothing inside them is an escape:

```sigil
service.labels["team"] matches `^team-[a-z]+$`
```

- An unknown escape such as `\'` is an error at the backslash.
- A raw string can span lines. A double-quoted one can't.

### Durations

A duration literal is an integer followed immediately by a unit. Units can be chained without spaces.

| Unit | Meaning                                |
| ---- | -------------------------------------- |
| `ms` | millisecond                            |
| `s`  | second                                 |
| `m`  | minute                                 |
| `h`  | hour                                   |
| `d`  | exactly 24 hours, with no calendar or daylight-saving meaning |

```sigil
30m
1h30m
2d
500ms
```

| Literal | Valid | Rule |
| --- | --- | --- |
| `1h30m` | yes | Each unit at most once, largest first |
| `30m1h` | no | Units out of order |
| `1h1h` | no | Unit repeated |
| `1h30` | no | Last component has no unit |
| `1.5h` | no | No fractional components; write `1h30m` |
| `107000d` | no | Above about 292 years, the range of Go's `time.Duration` |
| `-30m` | yes | Unary minus applied to `30m`; there's no negative duration literal |

#### Printed durations

When Sigil prints a duration value, as a param's value in `sigil explain`, a payload in `sigil eval` or a mismatch in `sigil test`:

- It normalizes the value to the largest units, each once: `24h` prints as `1d`, `1500m` as `1d1h`, and zero as `0s`.
- A literal in source is printed as written, and `sigil fmt` doesn't rewrite it.
- A value with a sub-millisecond part, which only a Go host can produce, prints with a `+Nns` suffix, such as `1s+500ns`. That isn't valid source.

### Lists

```sigil
["standard", "internal"]
[]
```

Every element must have the same type. See [Types](/reference/types/#collections) for how the empty list gets its type.

### Maps

```sigil
{"app.kubernetes.io/managed-by": "argocd", "platform.example.com/lifecycle": "ga"}
{}
```

- Keys and values are expressions.
- Every key shares one type, and every value shares one type.
- A key must be a scalar: `bool`, `int`, `float`, `string`, `duration` or `timestamp`.
- A key is parsed at the precedence of `??`, so a comparison used as a key needs parentheses.

### Trailing commas

A trailing comma is allowed in every comma-separated list:

| Where | Example |
| --- | --- |
| List and map literals | `["a", "b",]` |
| Host function call arguments | `f(a, b,)` |
| Decision constructors | `review(service_owner, approvers: approvers,)` |
| Policy invocation arguments | `production(approvers: approvers,)` |
| `assert` | `assert("no_root", ok,)` |
| Selective imports | `use deploy.common.{cleared, owns_service,}` |
| `fn` parameters (kind files) | `fn f(string, int,) -> bool` |
| Decision payload fields (kind files) | `decision review(approvers: list<string>,) { ... }` |

```sigil
production(
  approvers: ["payments-leads", "security-leads"],
  tiers: ["standard"],
)
```

## Operators and punctuation

| Token                       | Used for                                    |
| --------------------------- | ------------------------------------------- |
| `==` `!=` `<` `<=` `>` `>=` | Comparison; `<` `>` also delimit type arguments (`list<T>`), and `>` orders a kind's `precedence` |
| `+` `-`                     | Arithmetic, unary minus                     |
| `??`                        | Optional default                            |
| `?.`                        | Optional chaining                           |
| `.`                         | Field access, qualified import access, a decision's reason (`approve.release_manager`) |
| `[` `]`                     | List literals, indexing                     |
| `{` `}`                     | Map literals, rule bodies, type bodies, reason blocks, selective imports |
| `(` `)`                     | Grouping, calls, decision constructors, policy invocations |
| `:`                         | Type annotations, named arguments, quantifier and filter bodies, map entries |
| `,`                         | Separators                                  |
| `=`                         | `let` bindings, param and payload defaults  |
| `->`                        | Return type in `fn` declarations            |
| `?`                         | Optional type prefix (`?string`)            |
| `@`                         | Kind version pin in a header (`DeployApproval@2`) |
| `---`                       | Document separator                          |

The lexer uses longest match, so `??`, `?.`, `<=`, `->` and `---` are each one token.

## Document separators

```sigil
module deploy.common: DeployApproval@1

pub let owns_service = actor.teams any in service.owners

---

policy deploy.production: DeployApproval@1

use deploy.common.{owns_service}
```

- A file can hold several documents, and `---` may separate them, as in YAML.
- `---` is a single token by longest match.
- No statement starts with `-`, so a `---` between two statements always ends the current document.
- `---` is optional: the next header ends a document too.
- `sigil fmt` always writes it between documents, on a line of its own with a blank line on each side.
- Where extra separators may appear, and how comments attach to headers, is on [Documents](/reference/bundles/#documents).

| Source | Lexes as | Result |
| --- | --- | --- |
| `a---b` | `a`, `---`, `b` | Error |
| `a - --b` | `a`, `-`, `-`, `-`, `b` | `a - (-(-b))` |
| `----` | `---`, `-` | Error |

```text
policies.sigil:12:21: error: `---` separates documents and can't appear inside an expression
   |
12 | let tight = min_soak---1h
   |                     ^^^
   = help: if you meant arithmetic, put spaces between the minus signs
```

To put a bundle in a YAML block scalar, see [Policies in a ConfigMap](/guides/configmaps/).
