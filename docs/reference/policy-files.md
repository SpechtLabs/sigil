---
title: Policy files
icon: mdi:file-document-outline
createTime: 2026/09/24 22:30:00
permalink: /reference/policy-files/
---

::: info Draft specification
This page specifies the language as designed. Nothing is implemented yet; see [Open questions](/project/open-questions/).
:::

A policy file holds exactly one policy. It opens with a `policy` header and then contains any number of `use`, `param`, `let` and `when` statements in any order. Each statement starts with its keyword, so the file needs no separators and no significant whitespace.

## Statements at a glance

| Statement                          | Where                   | Meaning                                                               |
| ---------------------------------- | ----------------------- | --------------------------------------------------------------------- |
| `policy <name>: <Kind>`            | first statement         | Names the policy and the kind it implements                           |
| `use <name>(<param>: <expr>, ...)` | top level               | Includes another policy of the same kind, binding its params          |
| `param <name>: <type> [= <expr>]`  | top level               | Typed input set at instantiation. No default means required           |
| `let <name> = <expr>`              | top level               | Named, reusable expression                                            |
| `when <expr> { ... }`              | top level or nested     | Rule block. Body holds nested `when` blocks and decision constructors |

Kind files use a different set of statements; see [Kind files](/reference/kind-files/).

## `policy`

```sigil
policy deploy.production: DeployApproval
```

The header must be the first statement in the file, and a file must contain exactly one. The name is a dotted [policy name](/reference/lexical/) and the part after the colon names the kind whose contract the policy is checked against. Every input, host function, type and decision the policy can refer to comes from that kind.

Referring to a kind the host doesn't know is a compile error.

## `param`

```sigil
param min_soak: duration = 24h
param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]
```

A param is a typed value supplied when the policy gets instantiated. It has a name, a type and optionally a default.

- A param without a default is required. Instantiating the policy without binding it is a compile error.
- A default must have the declared type and must be a constant expression: literals, list and map literals of literals, and arithmetic on those. It can't read inputs, lets or other params. (proposed; the current design doesn't say what a default may reference)
- The type can be any type from [Types](/reference/types/) except optional types, because an optional param would just be a param with a default.

Params are bound in one of two ways, both type-checked at compile time: by another policy through `use`, or by the Go host through `policy.Params` when it compiles or loads the policy. See the [Go API](/reference/go-api/).

::: tip Proposed
The ban on optional (`?T`) param types is proposed here; nothing else in the design settles it.
:::

## `let`

```sigil
let owns_service = actor.teams any in service.owners
let cleared =
  split(service.labels["regions"], ",") all in actor.regions
```

A `let` binds a name to an expression. The compiler infers the type from the expression; there's no annotation. The expression can use inputs, params, host functions, other lets and aliased lets from a `use` (`base.cleared`).

- `let` is only allowed at the top level of a file. Block-scoped bindings are an [open question](/project/open-questions/).
- Lets must form a directed acyclic graph. `let a = b` together with `let b = a` is a compile error, and so is any longer cycle.
- A let is a name for an expression, not a mutable variable. It can't be reassigned, and binding the same name twice is a compile error.
- The order of `let` statements in a file doesn't matter; a let may refer to one declared further down.

## `when`

```sigil
when cleared {
  when service.tier == "critical"
    and "release_manager" in actor.roles {
    approve("release_manager")
  }

  when service.tier in tiers
    and owns_service {
    review("service_owner", approvers: approvers)
  }
}
```

A `when` block has a condition and a body in braces. The condition must have type `bool`; anything else is a compile error, so there's no truthiness.

The body contains nested `when` blocks and [decision constructors](/reference/decisions/), and nothing else. There's no `let`, no `use` and no bare expression inside a body.

There's no `else`. Write `when not x { ... }` instead. A nested `when` fires only if every enclosing condition holds, which makes nesting a conjunction. The full rules live on [Evaluation semantics](/reference/evaluation/).

Whether a single body may contain more than one decision constructor is an [open question](/project/open-questions/). The examples in these docs use one constructor per body.

## `use`

```sigil
use deploy.production(
  min_soak: 4h,
  approvers: ["payments-leads"],
)
```

`use` includes every rule of another policy, with that policy's params bound to the given values.

- Bindings are named. Each name must match a `param` declared by the used policy and the value must have the param's type. An unknown name, a type mismatch, or a required param left unbound is a compile error.
- Params with defaults can be left out. The parentheses are still required: `use shared.baseline()` includes a policy (hypothetical here) whose params all have defaults.
- Binding values are ordinary expressions evaluated in the using policy's scope, so a team policy can pass its own params through: `use deploy.production(approvers: approvers)`.
- The used policy must implement the same kind. Using a policy of a different kind is a compile error.
- The `use` graph must be acyclic. A policy that uses itself, directly or through others, is a compile error.

::: warning Unspecified
Binding values are expressions, but the current design doesn't say whether they may read inputs. Allowing only params, lets and constants would keep a used policy's params stable for the whole evaluation. Until that's settled, treat input-dependent bindings as unspecified.
:::

### Aliases

```sigil
use deploy.production(approvers: ["payments-leads"]) as base

when base.cleared and "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```

`as <alias>` exposes the used policy's lets as `<alias>.<let>`. Without an alias, the used policy's lets aren't visible at all; its rules still run.

A policy may use the same base more than once, as long as each extra use has a distinct alias, for example once per service tier with different approvers:

```sigil
use deploy.production(tiers: ["standard"], approvers: ["payments-leads"]) as standard
use deploy.production(tiers: ["internal"], approvers: ["payments-sre"]) as internal
```

Each use is a separate instance with its own params, and each contributes its own rules.

::: tip Proposed
Using the same policy twice without an alias to tell the instances apart is a compile error. Aliases only expose lets; params and inputs of the used policy aren't reachable through them.
:::

## Name resolution

### Files

A policy name maps to a path in the host's `fs.FS` by replacing each `.` with `/` and appending `.sigil`:

| Name                  | File                        |
| --------------------- | --------------------------- |
| `deploy.production`   | `deploy/production.sigil`   |
| `payments.production` | `payments/production.sigil` |

The `policy` header inside the file must repeat the name the file was loaded under. A file at `deploy/production.sigil` whose header says `policy deploy.base` is a compile error. (proposed)

Kind files share the `.sigil` extension. A `use` that resolves to a file starting with `kind` instead of `policy` is a compile error. (proposed)

### Identifiers

Each policy has one flat top-level namespace containing:

- the kind's inputs and host functions,
- the policy's own params and lets,
- the aliases introduced by `use ... as`.

Any collision between two of these is a compile error, and nothing shadows anything. A `param` named `release` in a kind that declares `input release` fails to compile, and so does a quantifier variable named `approvers` in a policy that has a param by that name.

Decision names live in their own namespace and only appear in constructor position, so a let called `deny` doesn't collide with the `deny` decision. (proposed)

::: tip Proposed
The single flat namespace and the no-shadowing rule are proposed here to close a gap in the current design. The goal is that any name in a policy has exactly one meaning, which you can find without knowing scoping rules.
:::

## A complete file

```sigil
policy deploy.production: DeployApproval

param min_soak: duration = 24h
param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]

let owns_service = actor.teams any in service.owners
let cleared =
  split(service.labels["regions"], ",") all in actor.regions
let eligible =
  "deployer" in actor.roles
  and environment == "production"
  and service.labels has {
    "app.kubernetes.io/managed-by": "argocd",
    "platform.example.com/lifecycle": "ga",
  }

when not eligible {
  deny("not_eligible")
}

when release.soak < min_soak and not release.hotfix {
  deny("soak_too_short")
}

when cleared {
  when service.tier == "critical"
    and "release_manager" in actor.roles {
    approve("release_manager")
  }

  when service.tier in tiers
    and owns_service {
    review("service_owner", approvers: approvers)
  }
}
```
