---
title: Kind files
icon: mdi:file-certificate-outline
createTime: 2026/09/24 22:30:00
permalink: /reference/kind-files/
---

::: info Draft specification
This page specifies the language as designed. Syntax, kinds, type checking and expression evaluation are implemented; rules and decisions, composition and the CLI aren't yet. See [Open questions](/project/open-questions/).
:::

A kind is the contract between a Go host and the policies it evaluates. It declares what input looks like, which host functions exist, which decisions a policy can produce, and whether one of them wins or all of them apply. Every policy names exactly one kind in its header and gets type-checked against it.

Kinds are defined in Go and exported to a kind file, the same way Go structs become an OpenAPI spec. Kind files use the same `.sigil` extension as policies and modules; the `kind` header tells them apart, and by convention the file is named after the kind (`deploy_approval.sigil`). Nobody writes a kind file by hand. The defining host never loads one; everyone else can.

```mermaid
flowchart LR
  A[Go structs] --> B[policy.NewKind]
  B -- Schema --> C[deploy_approval.sigil]
  C --> D[LoadKind<br/>dynamic check + eval]
  C --> E[sigil gen go<br/>typed Go code]
  C --> F[CLI, LSP, CI]
```

The exported kind file is the wire contract. Other Go services can load it dynamically or generate typed code from it, and tooling uses it without importing the host.

## A complete kind

```sigil
kind DeployApproval version 1

type Release {
  soak: duration
  hotfix: bool
}
type Service {
  name: string
  tier: string
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

decision deny(reason: string)
decision review(reason: string, approvers: list<string>)
decision approve(reason: string, bake: duration = 1h)

precedence deny > review > approve
default deny("no_rule_matched")
```

## Declarations

| Declaration  | Example                                                    | Purpose                                  |
| ------------ | ---------------------------------------------------------- | ---------------------------------------- |
| `kind`       | `kind DeployApproval version 1`                            | Name and contract version                |
| `type`       | `type Release { soak: duration }`                          | Struct types reachable from inputs       |
| `input`      | `input release: Release`                                   | Top-level names policies can read        |
| `fn`         | `fn split(string, string) -> list<string>`         | Host function signatures                 |
| `decision`   | `decision review(reason: string, approvers: list<string>)` | Decision constructors and payload schema |
| `precedence` | `precedence deny > review > approve`                       | Conflict resolution order                |
| `collect`    | `collect all`                                              | Every fired decision applies (instead of `precedence`) |
| `default`    | `default deny("no_rule_matched")`                          | Result when nothing fires; optional with `collect all` |

### `kind`

```sigil
kind DeployApproval version 1
```

The header must be the first statement, and a file holds exactly one kind. The name is an identifier; policies refer to the kind by it (`policy deploy.production: DeployApproval`). The version is a positive integer that changes when the contract does; see [Versioning](#versioning).

### `type`

```sigil
type Service {
  name: string
  tier: string
  owners: list<string>
  labels: map<string, string>
}
```

Declares a struct type with named, typed fields. Fields are written `name: type` with no separator between them; the parser finds the next field by its `name:` prefix. Field names must be unique within a type. A field's type may be any [type](/reference/types/), including another struct type and an optional `?T`.

Struct types must not be recursive, directly or through other types. Go allows `type Node struct { Next *Node }`, but policies can't loop, so a recursive type could only ever be read to a fixed depth; `NewKind` and the kind loader reject one, naming the cycle.

### `input`

```sigil
input service: Service
```

Declares a top-level name policies can read, and its type. Inputs are read-only. On the Go side each input is a field of the host's input struct with a `policy:"..."` tag.

### `fn`

```sigil
fn split(string, string) -> list<string>
```

Declares a host function's signature: the parameter types and the result type. Parameters have no names, because policies pass arguments positionally and a Go function's parameter names aren't recoverable by reflection anyway. The return type is required and can't be optional.

Host functions must be pure and deterministic. The Go implementation may return `(T, error)`; a non-nil error becomes a [runtime error](/reference/evaluation/).

### `decision`

```sigil
decision review(reason: string, approvers: list<string>)
decision approve(reason: string, bake: duration = 1h)
```

Declares a decision constructor and its payload schema.

- The first parameter must be `reason: string`. A decision without it is rejected.
- Every other parameter is a payload field with a type and an optional default. A field without a default is required at every call site.
- Defaults must be constants.
- Field names must be unique within a decision.

How policies call these is on [Decisions](/reference/decisions/).

### `precedence`

```sigil
precedence deny > review > approve
```

Ranks decisions from highest to lowest. When candidates of different decisions compete, the highest-ranked one wins. The declaration must name every declared decision exactly once, which makes precedence a total order. The Go side derives precedence from the order of `policy.WithDecisions(...)`, which is always total.

### `collect`

```sigil
collect all
```

Declares a collecting kind: instead of one winner, the host gets every candidate that fired. A kind has either `precedence` or `collect all`, never both and never neither, so leaving out a line can't silently switch a kind from one winner to many.

Collecting fits decisions that combine instead of competing, such as roles a user can hold at the same time:

```sigil
kind AccessGrant version 1

type Actor {
  name: string
  groups: list<string>
  clearance: string
}

input actor: Actor

decision read(reason: string)
decision write(reason: string)
decision admin(reason: string, ttl: duration = 8h)
decision customer_data_writer(reason: string)
decision development_environment_writer(reason: string)

collect all
```

A policy for this kind grants each role in its own `when` block, and several can fire for one actor. Nothing ranks them and no candidate can cancel another, so a collecting kind has no `deny` in the usual sense. Its guardrails are [asserts](/reference/policy-files/#assert) instead, typically in a policy the host [requires](/reference/evaluation/#required-policies):

```sigil
policy access.guardrails: AccessGrant

assert [customer_data_writer, development_environment_writer] exclusive in outcome,
  "sod_customer_dev"
```

How the candidates are ordered and returned is on [Evaluation semantics](/reference/evaluation/#collecting-kinds).

### `default`

```sigil
default deny("no_rule_matched")
```

The result when no rule fires. It's a decision constructor with a literal reason, and every payload value must be a constant.

A kind with `precedence` must declare a default. A collecting kind may leave it out, and then an evaluation where nothing fires returns no decisions at all.

## Validity rules

A kind is valid when:

- the header comes first and appears once,
- every type referenced anywhere is a built-in type or a declared struct type,
- no struct type is recursive,
- inputs and host functions share one namespace and every name in it is unique,
- every decision declares `reason: string` first,
- exactly one of `precedence` and `collect all` is declared,
- `precedence`, if declared, lists every decision exactly once,
- `default` is declared if `precedence` is, and constructs a declared decision with a literal reason and constant payload values that satisfy its schema.

`NewKind` enforces these rules on the Go side and panics at init if they fail, so a kind that exists can always be exported.

## Go type mapping

| Go                         | Sigil            |
| -------------------------- | ---------------- |
| `string`, `bool`           | `string`, `bool` |
| `int`, `int64`             | `int`            |
| `float64`                  | `float`          |
| `time.Duration`            | `duration`       |
| `time.Time`                | `timestamp`      |
| `[]T`                      | `list<T>`        |
| `map[K]T`, scalar `K`      | `map<K, T>`      |
| `*T`                       | `?T`             |
| struct with `policy:` tags | `type`           |

`NewKind` rejects anything else: channels, funcs, interfaces, map keys that aren't scalars, unexported fields. That strictness is what makes the round trip safe. `Import(Export(k))` must equal `k`, and that's a property test in the suite.

Payload structs map to decision fields the same way. A default comes from the struct tag:

```go
type ApproveData struct {
	Bake time.Duration `policy:"bake,default=1h"`
}
```

The reason is implicit on every decision and never appears in a payload struct.

::: warning Open question
`*Struct` maps to `?Struct`, which a policy can't currently unwrap because struct types have no literal. Whether `NewKind` should reject pointer-to-struct fields or the language should grow optional chaining is an [open question](/project/open-questions/).
:::

## Host functions across the boundary

A client that loads a kind can always type-check policies against it, because the `fn` signatures are in the file. Evaluating needs more: the client must bind a Go implementation for every declared function.

- `LoadKind` returns the kind and the list of functions still unbound.
- `Compile` works without bindings.
- `Eval` refuses to run until every function is bound.

A CI linter only needs the first half. A second service that evaluates policies needs both. See the [Go API](/reference/go-api/).

## Versioning

The exported kind carries a version, and `sigil breaking old/deploy_approval.sigil deploy_approval.sigil` flags incompatible changes in CI, modeled on `buf breaking`.

| Change                                         | Effect                                                         |
| ---------------------------------------------- | -------------------------------------------------------------- |
| Add an input, type field, function or decision | Compatible                                                     |
| Add a payload field with a default             | Compatible                                                     |
| Remove or rename anything                      | Breaking                                                       |
| Change a type                                  | Breaking                                                       |
| Add a payload field without a default          | Breaking                                                       |
| Reorder `precedence` or change `default`       | Breaking in behaviour, even though every policy still compiles |
| Switch between `precedence` and `collect all`  | Breaking                                                       |

::: warning Adding an input or function can collide
Inputs, host functions, params, lets and imported names share one flat namespace per policy, with no shadowing (see [Policy files](/reference/policy-files/)). A new `input approvers` therefore breaks every policy that already declares `param approvers`. `sigil breaking` only sees the two kind files, so it can't catch this; `sigil check` against the new kind can. Decision names share that namespace, because asserts use them as values, so a new decision collides the same way. (proposed)
:::

A policy's header names a kind but not a version. Whether policies should pin a kind version, and what happens when they don't match, is unspecified. For the operational side of changing a kind, see [Evolve a kind safely](/guides/evolve-a-kind/).
