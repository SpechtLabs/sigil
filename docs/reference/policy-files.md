---
title: Policy files
icon: mdi:file-document-outline
createTime: 2026/09/24 22:30:00
permalink: /reference/policy-files/
---

::: info Draft specification
This page specifies the language as designed. Nothing is implemented yet; see [Open questions](/project/open-questions/).
:::

A policy file holds exactly one policy. It opens with a `policy` header, lists its imports, and then contains any number of `param`, `let` and `when` statements and policy invocations in any order. Each statement starts with a keyword or with the name of an imported policy followed by `(`, so the file needs no separators and no significant whitespace.

Shared matchers live in a second file type, the [module](#modules), which holds only imports and `let`s.

## Statements at a glance

| Statement                          | Where                   | Meaning                                                                 |
| ---------------------------------- | ----------------------- | ----------------------------------------------------------------------- |
| `policy <name>: <Kind>`            | first statement         | Names the policy and the kind it implements                             |
| `use <path>`                       | after the header        | Imports names from another policy or module. Never adds rules by itself |
| `param <name>: <type> [= <expr>]`  | top level               | Typed input set at instantiation. No default means required             |
| `let <name> = <expr>`              | top level               | Named, reusable expression                                              |
| `when <expr> { ... }`              | top level or nested     | Rule block. Body holds nested `when` blocks, decisions and invocations  |
| `<policy>(<param>: <expr>, ...)`   | top level or nested     | Invokes an imported policy, adding its rules with its params bound      |

Kind files use a different set of statements; see [Kind files](/reference/kind-files/).

## `policy`

```sigil
policy deploy.production: DeployApproval
```

The header must be the first statement in the file, and a file must contain exactly one. The name is a dotted [policy name](/reference/lexical/) and the part after the colon names the kind whose contract the policy is checked against. Every input, host function, type and decision the policy can refer to comes from that kind.

Referring to a kind the host doesn't know is a compile error.

## `use`

`use` brings names from another file into scope. It never adds rules by itself: an imported policy contributes nothing until it's [invoked](#policy-invocation).

```sigil
use deploy.production                         // binds `production`
use deploy.production as approvals            // binds `approvals`
use deploy.common                             // qualified: common.cleared
use deploy.common.{cleared, owns_service}     // selective
use deploy.common.{owns_service as owner}     // selective with alias
```

- A path resolves to a file in the host's `fs.FS`: `deploy.common` loads `deploy/common.sigil`. See [Files](#files).
- The last path segment is the bound name unless `as` renames it.
- A whole-file import of a [module](#modules) binds a qualifier: `common.cleared` reads the module's `cleared`. A whole-file import of a policy binds a name you can invoke.
- A selective import, `use deploy.common.{...}`, binds each listed `let` under its own name, or under the name after `as`.
- The imported file must implement the same kind. Importing a module or policy written for another kind, or a kind file, is a compile error.
- Imports come right after the header, before any `param`, `let`, rule or invocation.
- An imported name that collides with an input, host function, param, `let` or another import is a compile error. Nothing shadows silently.
- There are no wildcard imports. Every name used in a file is either defined there or listed in a `use`, so a reader can always find where it comes from.
- The import graph must be acyclic.
- An unused import is a lint warning, not an error, so commenting out a rule while debugging doesn't break the build.

### Importing from a policy

A policy's `let`s can be imported too, but only if they don't depend on a param, directly or through other `let`s. A param has no value outside an invocation, so a param-dependent `let` means nothing in the importing file. Suppose `deploy.guardrails` declared `let soak_ok = release.soak >= min_soak or release.hotfix`. The compiler tracks the dependency and names the param:

```text
payments/production.sigil:5:24: error: cannot import `soak_ok` from deploy.guardrails
  |
5 | use deploy.guardrails.{soak_ok}
  |                        ^^^^^^^
  = help: it reads param `min_soak`, which has no value outside an invocation.
          Move it to a module, or compare against the value directly.
```

In practice this pushes shared matchers into modules, which is where they belong.

::: tip Proposed
Reading a policy's param-free `let` through a whole-file import (`guardrails.some_let`) follows the same rule as a selective import. The design only spells out the selective form.
:::

## `param`

```sigil
param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]
```

A param is a typed value supplied when the policy gets instantiated. It has a name, a type and optionally a default.

- A param without a default is required. Instantiating the policy without binding it is a compile error.
- A default must have the declared type and must be a constant expression: literals, list and map literals of literals, and arithmetic on those. It can't read inputs, lets or other params. (proposed; the current design doesn't say what a default may reference)
- The type can be any type from [Types](/reference/types/) except optional types, because an optional param would just be a param with a default.

Params are bound in one of two ways, both type-checked at compile time: by another policy that [invokes](#policy-invocation) this one, or by the Go host through `policy.Params` when it compiles or loads the policy. See the [Go API](/reference/go-api/).

::: tip Proposed
The ban on optional (`?T`) param types is proposed here; nothing else in the design settles it.
:::

## `let`

```sigil
let owns_service = actor.teams any in service.owners
let cleared =
  split(service.labels["regions"], ",") all in actor.regions
```

A `let` binds a name to an expression. The compiler infers the type from the expression; there's no annotation. The expression can use inputs, params, host functions, other lets and imported lets (`cleared`, or `common.cleared` through a whole-module import).

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

The body contains nested `when` blocks, [decision constructors](/reference/decisions/) and [policy invocations](#policy-invocation), and nothing else. There's no `let`, no `use` and no bare expression inside a body.

There's no `else`. Write `when not x { ... }` instead. A nested `when` fires only if every enclosing condition holds, which makes nesting a conjunction. The full rules live on [Evaluation semantics](/reference/evaluation/).

Whether a single body may contain more than one decision constructor is an [open question](/project/open-questions/). The examples in these docs use one constructor per body.

## Policy invocation

```sigil
use deploy.guardrails
use deploy.production

guardrails(min_soak: 4h)

when service.labels["compliance"] == "pci" {
  production(approvers: ["payments-leads", "security-leads"])
}
```

An imported policy is invoked like a decision constructor. A decision constructor produces one candidate; an invocation produces the invoked policy's whole candidate set, with its params bound to the arguments. Both can go anywhere the other can: at the top level or inside a `when` body.

An invocation inside `when` blocks adds the enclosing conditions to every rule of the invoked policy. It's the same rule as nested `when`: nesting is conjunction. The `production(...)` call above behaves exactly as if `deploy.production`'s rules were pasted inside the block. See [Evaluation semantics](/reference/evaluation/#invocation).

- Arguments are named-only, like decision payloads. Every param without a default must be bound, and each value must have the param's type. An unknown name, a type mismatch, or a required param left unbound is a compile error.
- Params with defaults can be left out. The parentheses are still required: `baseline()` invokes a policy (hypothetical here) whose params all have defaults.
- Arguments may reference constants and the invoking policy's own params, but not inputs or `let`s that read inputs. That keeps every invocation a static instantiation, so [`sigil explain`](/reference/cli/#sigil-explain) can print concrete values. A team can pass its own params through: `production(approvers: approvers)`.
- Only an imported policy of the same kind can be invoked. Invoking a module is a compile error.
- A policy can be invoked more than once with different arguments. Each call is a separate instantiation with its own params.
- The invocation graph must be acyclic. A policy that invokes itself, directly or through others, is a compile error.
- A host can require that certain policies are invoked unconditionally, so a `when` can't switch their denies off. See [Required policies](/reference/evaluation/#required-policies).

::: tip Proposed
An invocation's arguments follow the same trailing-comma and keyword-as-name rules as decision payloads. Invoking the same policy twice with identical arguments is legal, and the `duplicate-invocation` lint warns about it.
:::

## Modules

A module is a file of shared, typed matchers. It has no rules and no params, so importing from it can never change a decision by itself.

```sigil
module deploy.common: DeployApproval

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
```

- The header names the kind, because the `let`s read inputs and have to type-check against them.
- A module may contain `use` and `let` statements only. `param`, `when`, decision constructors and invocations are compile errors.
- A module may `use` other modules.
- Every `let` in a module is exported. Whether modules need private helpers is an [open question](/project/open-questions/).
- A host can't load or evaluate a module; it has no rules to evaluate.

## Name resolution

### Files

A policy or module name maps to a path in the host's `fs.FS` by replacing each `.` with `/` and appending `.sigil`:

| Name                  | File                        |
| --------------------- | --------------------------- |
| `deploy.common`       | `deploy/common.sigil`       |
| `deploy.production`   | `deploy/production.sigil`   |
| `payments.production` | `payments/production.sigil` |

The header inside the file must repeat the name the file was loaded under. A file at `deploy/production.sigil` whose header says `policy deploy.base` is a compile error. (proposed)

Kind files share the `.sigil` extension. A `use` that resolves to a file starting with `kind` is a compile error. (proposed)

In a selective import, the path before `.{` names the file and the names inside the braces name its `let`s: `use deploy.common.{cleared}` loads `deploy/common.sigil`.

### Identifiers

Each policy and module has one flat top-level namespace containing:

- the kind's inputs and host functions,
- the file's own params and lets,
- every name bound by a `use`.

Any collision between two of these is a compile error, and nothing shadows anything. A `param` named `release` in a kind that declares `input release` fails to compile, and so does a quantifier variable named `approvers` in a policy that has a param by that name.

Decision names live in their own namespace and only appear in constructor position, so a let called `deny` doesn't collide with the `deny` decision. An imported policy is called in the same position, so an import whose bound name is also a decision of the kind is a compile error: rename it with `as`. (proposed)

::: tip Proposed
The single flat namespace and the no-shadowing rule are proposed here to close a gap in the current design. The goal is that any name in a policy has exactly one meaning, which you can find without knowing scoping rules.
:::

## A complete file

The team policy from the [tour](/getting-started/tour/). It imports two platform policies and one shared matcher, invokes the guardrails unconditionally, picks approvers by compliance scope, and adds one approval of its own:

```sigil
policy payments.production: DeployApproval

use deploy.guardrails
use deploy.production
use deploy.common.{cleared}

guardrails(min_soak: 4h)

when service.labels["compliance"] == "pci" {
  production(approvers: ["payments-leads", "security-leads"])
}

when service.labels["compliance"] != "pci" {
  production(approvers: ["payments-leads"])
}

when cleared and "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```
