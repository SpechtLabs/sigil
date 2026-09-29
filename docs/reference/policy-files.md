---
title: Policy files
icon: mdi:file-document-outline
createTime: 2026/09/24 22:30:00
permalink: /reference/policy-files/
---

The statements of policy and module documents in `.sigil` files.

A complete team policy is in the [tour](/getting-started/tour/#the-team-policy).

## Statements at a glance

| Statement                                                         | Where               | Meaning                                                                                          |
| ----------------------------------------------------------------- | ------------------- | ------------------------------------------------------------------------------------------------ |
| `policy <name>: <Kind>@<N>`                                       | first statement     | Names the policy, the kind it implements and the kind version it was written against             |
| `use <path>`                                                      | after the header    | Imports names from another policy or module. Never adds rules by itself                          |
| `param <name>: <type> [= <expr>] [, min: <expr>] [, max: <expr>]` | top level           | Typed input set at instantiation. No default means required; bounds limit what a caller may bind |
| `[pub] let <name> = <expr>`                                       | top level or nested | Named, reusable expression. `pub` lets other documents import it; only at the top level          |
| `when <expr> { ... }`                                             | top level or nested | Rule block. Body holds nested `when` blocks, decisions, asserts and invocations                  |
| `assert("<reason>", <expr>)`                                      | top level or nested | Condition that must hold, or evaluation fails with an assertion error                            |
| `<policy>(<param>: <expr>, ...)`                                  | top level or nested | Invokes an imported policy, adding its rules with its params bound                               |

After the imports, statements come in any order. [Modules](#modules) allow only `use` and `let`. Kind files use a different set of statements; see [Kind files](/reference/kind-files/).

## `policy`

```sigil
policy deploy.production: DeployApproval@1
```

- The header is the first statement of a policy document. Each policy document has exactly one.
- The name is a dotted [policy name](/reference/lexical/#policy-names).
- The part after the colon names the kind the policy is checked against. Every input, host function, type and decision the policy can refer to comes from that kind.
- Referring to a kind the host doesn't know is a compile error.
- `@1` pins the kind version the policy was written against. The pin is required; a missing pin is an error that suggests the kind's current version. Which pins the host accepts: [Versioning](/reference/kind-files/#versioning). How a name the kind added later is treated: [Identifiers](#identifiers).

Why: [Kinds as contracts](/understanding/kinds/).

## `use`

```sigil
use deploy.production                         // binds `production`
use deploy.production as approvals            // binds `approvals`
use deploy.common                             // qualified: common.cleared
use deploy.common.{cleared, owns_service}     // selective
use deploy.common.{owns_service as owner}     // selective with alias
```

`use` brings names from another document into scope. It never adds rules by itself: an imported policy contributes nothing until it's [invoked](#policy-invocation).

| Form                  | Binds                                                       |
| --------------------- | ----------------------------------------------------------- |
| `use a.b` of a module | Qualifier `b`: `b.cleared` reads the module's `cleared`     |
| `use a.b` of a policy | Name `b`, to invoke and to read its `pub let`s as `b.<let>` |
| `use a.b as c`        | The same, under the name `c`                                |
| `use a.b.{x, y}`      | Each listed `pub let` under its own name                    |
| `use a.b.{x as z}`    | The listed `pub let` under the name after `as`              |

- The last path segment is the bound name unless `as` renames it.
- A path names a document in the bundle: `use deploy.common` finds the document whose header is `module deploy.common`, in whichever file it lives. See [Name resolution](/reference/bundles/#name-resolution).
- The imported document must implement the same kind. Importing a module or policy written for another kind, or a kind document, is a compile error.
- Imports come right after the header, before any `param`, `let`, rule or invocation.
- An imported name that collides with an input, host function, param, `let` or another import is a compile error. Nothing shadows silently.
- There are no wildcard imports. Every name used in a document is either defined there or listed in a `use`.
- The import graph must be acyclic.
- An unused import is the [`unused-import`](/reference/lints/) lint warning, not an error.

```text
payments/production.sigil:3:31: error: `release` is already the name of an input
  |
3 | use deploy.common.{cleared as release}
  |                               ^^^^^^^
  = help: every name in a document means one thing; rename one of them
```

### Importing from a policy

- A policy's `pub let`s are imported the same way as a module's: selectively, `use deploy.guardrails.{is_hotfix}`, or through a whole import, `guardrails.is_hotfix`.
- A whole import of a policy also binds the name you invoke it by, so one `use` serves both.
- A `pub let` in a policy can't depend on a param, directly or through other `let`s. The compiler reports it where the `let` is declared, not where someone imports it.

```text
deploy/guardrails.sigil:5:9: error: let `soak_ok` can't be `pub`: it reads param `min_soak`
  |
5 | pub let soak_ok = release.soak >= min_soak or release.hotfix
  |         ^^^^^^^
  = help: a param has no value outside an invocation; move the let to a module, or drop `pub`
```

## `param`

```sigil
param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]
```

A param is a typed value supplied when the policy gets instantiated. It has a name, a type and optionally a default.

- A param without a default is required. Instantiating the policy without binding it is a compile error.
- A default must have the declared type and must be a constant expression: literals, list and map literals of literals, and arithmetic on those. It can't read inputs, lets or other params.
- The type can be any type from [Types](/reference/types/) except optional types.

| Bound by                                                                                                                       | Checked                          |
| ------------------------------------------------------------------------------------------------------------------------------ | -------------------------------- |
| A policy that [invokes](#policy-invocation) this one                                                                           | At compile time, at the argument |
| The Go host, through `policy.Params` when it compiles or loads the policy; see [Load options](/reference/go-api/#load-options) | By `Load` or `Compile`           |

### Bounds

```sigil
param min_soak: duration = 24h, min: 1h, max: 48h
```

`min` and `max` limit the values a caller may bind: `guardrails(min_soak: 0s)` is a compile error.

- Bounds apply to `int`, `float` and `duration` params.
- Each is optional, both are inclusive, and they may come in either order.
- A bound is a constant of the param's type, like a default.
- `min` can't be above `max`, and the default must lie between them.
- An invocation argument is checked against the bounds when the policy compiles, at the argument that breaks it.
- A value bound from Go through `policy.Params` is checked by `Load` or `Compile`.
- Nothing is left to evaluation time, so a bad value never fails an evaluation.
- `min` and `max` are names in a named-argument position, not keywords, so a kind can still declare `fn max(int, int) -> int`.

```text
teams/payments.sigil:5:22: error: min_soak: 0s is below the minimum 1h
  |
5 | guardrails(min_soak: 0s)
  |                      ^^
  = help: deploy.guardrails declares `param min_soak: duration = 24h, min: 1h, max: 48h`
```

For a required guardrail, [`policy.From`](/reference/bundles/#trusted-sources) loads the file that declares its bounds from a trusted source. Why: [Composition without templating](/understanding/composition/).

## `let`

```sigil
let owns_service = actor.teams any in service.owners
let cleared = split(service.labels["regions"], ",") all in actor.regions
```

A `let` binds a name to an expression.

- The compiler infers the type from the expression. There's no annotation.
- The expression can use inputs, params, host functions, other lets and imported lets (`cleared`, or `common.cleared` through a whole-module import).
- A top-level `let` is visible in the whole document. A `let` inside a `when` body is visible in that body and the blocks nested in it, and nowhere else, not even in the `when`'s own condition. See [Scoped lets](#scoped-lets).
- A `let` is private to its document unless it's declared `pub let`. Only a `pub let` can be imported. See [Exporting lets](#exporting-lets).
- Lets must form a directed acyclic graph. `let a = b` together with `let b = a` is a compile error, and so is any longer cycle.
- A let is a name for an expression, not a mutable variable. It can't be reassigned, and binding the same name twice is a compile error.
- The order of `let` statements doesn't matter; a let may refer to one declared further down. The same holds inside a `when` body.

```text
deploy/fresh.sigil:4:37: error: let `fresh` depends on itself
  |
4 | let settled = release.hotfix or not fresh
  |                                     ^^^^^
  = help: lets form a directed acyclic graph; a let can't depend on itself, even through other lets
```

### Scoped lets

```sigil
when active {
  let sre = any r in actor.roles: r like "sre-*"
  when sre and release.hotfix { approve(release_manager) }
  when sre and not release.hotfix { review(service_owner, approvers: approvers) }
}
```

A `let` inside a body names a sub-expression that several nested blocks share, without making it visible to the rest of the document.

- It can read everything its body can: inputs, params, top-level lets, imports, and the lets of every enclosing body.
- It can't shadow anything. Its name can't be taken by an input, host function, decision, param, import or any other `let` in the document, including a `let` in an unrelated `when` body.
- It can't be `pub`.
- It's only evaluated when its body is reached, so it can rely on the enclosing conditions: in `when len(xs) > 0 { let first = xs[0] ... }`, with `len` a host function the kind declares, the index can't fail. See [Evaluation semantics](/reference/evaluation/#lets).

### Exporting lets

```sigil
module deploy.common: DeployApproval@1

pub let cleared = split(service.labels["regions"], ",") all in actor.regions and not restricted
let restricted = service.labels has "restricted"
```

- `pub let` makes a top-level `let` importable.
- A `let` without `pub` is private: other lets and rules in the same document can use it, and a `use` that names it is a compile error.
- The rule is the same for modules and policies.
- A private `let` that nothing reads is the [`unused-let`](/reference/lints/) lint warning.

## `when`

```sigil
when cleared {
  when service.tier == "critical"
    and "release_manager" in actor.roles {
    approve(release_manager)
  }

  when service.tier in tiers
    and owns_service {
    review(service_owner, approvers: approvers)
  }
}
```

A `when` block has a condition and a body in braces.

- The condition must have type `bool`. Anything else is a compile error; there's no truthiness.
- The body contains nested `when` blocks, [scoped lets](#scoped-lets), [decision constructors](/reference/decisions/), [asserts](#assert) and [policy invocations](#policy-invocation), and nothing else. There's no `param`, no `use` and no bare expression inside a body.
- There's no `else`. Write `when not x { ... }` instead.
- A nested `when` fires only if every enclosing condition holds: nesting is a conjunction. See [Evaluation semantics](/reference/evaluation/).
- A body may contain more than one decision constructor. Each one that's reached becomes its own candidate.

## `assert`

```sigil
assert("negative_soak", release.soak >= 0s)

when service.tier == "critical" {
  assert("critical_needs_team_label", service.labels has "team")
}

assert("sod_customer_dev", [customer_data_writer, development_environment_writer] exclusive in outcome)
```

An assert states something that must be true whenever it's reached. If its condition is false, evaluation fails: `Eval` returns an assertion error, and the host records it as an error, not as a decision.

- The condition must have type `bool`. It can read inputs, params, lets and imported lets, and, unlike any other expression, [`outcome`](/reference/expressions/#decision-values-and-outcome), the decisions evaluation produced, with their payloads through [`outcome.<decision>`](/reference/expressions/#candidates).
- The reason comes first, as in a [decision constructor](/reference/decisions/). It's a string literal, not a name the kind declares. Dynamic text isn't allowed.
- `assert(cond, "reason")` is a parse error that says so.
- An assert may appear at the top level or inside a `when` body. Inside a body it's only checked when every enclosing condition holds. An assert in an invoked policy gets the invocation's enclosing conditions too.
- An assert never produces a candidate and never changes the outcome. It can only fail the evaluation.

An assert that reads `outcome` is checked after the rules, every other assert before them; see [Assertions](/reference/evaluation/#assertions). `exclusive in` over `outcome` keeps two decisions of a [collecting kind](/reference/kind-files/#collecting-kinds) from being granted together. The two phases, and what the host gets back when one fails, are on [Evaluation semantics](/reference/evaluation/#assertions).

Why: [Asserts and decisions](/understanding/asserts/). Open design questions: [Assertions](/project/open-questions/#assertions).

## Policy invocation

```sigil
use deploy.guardrails
use deploy.production

guardrails(min_soak: 4h)

when service.labels["compliance"] == "pci" {
  production(approvers: ["payments-leads", "security-leads"])
}
```

An imported policy is invoked like a decision constructor. A decision constructor produces one candidate; an invocation produces the invoked policy's whole candidate set, with its params bound to the arguments. Both can go at the top level or inside a `when` body.

- An invocation inside `when` blocks adds the enclosing conditions to every rule of the invoked policy. The `production(...)` call above behaves exactly as if `deploy.production`'s rules were pasted inside the block. See [Evaluation semantics](/reference/evaluation/#invocation).
- Arguments are named-only, like decision payloads, and a trailing comma is allowed.
- Every param without a default must be bound, and each value must have the param's type. An unknown name, a type mismatch, or a required param left unbound is a compile error.
- Params with defaults can be left out. The parentheses are still required: `baseline()` invokes a policy (hypothetical here) whose params all have defaults.
- Arguments may reference constants and the invoking policy's own params, but not inputs or `let`s, not even a `let` that only holds a constant. A team can pass its own params through: `production(approvers: approvers)`.
- Only an imported policy of the same kind can be invoked. Invoking a module is a compile error.
- A policy can be invoked more than once with different arguments. Each call is a separate instantiation with its own params.
- Invoking a policy twice with identical arguments is legal; the [`duplicate-invocation`](/reference/lints/) lint warns about it.
- The invocation graph must be acyclic. A policy that invokes itself, directly or through others, is a compile error.
- A host can require that certain policies are invoked unconditionally, so a `when` can't switch their denies off. See [Required policies](/reference/evaluation/#required-policies).

Why: [Composition without templating](/understanding/composition/).

## Modules

```sigil
module deploy.common: DeployApproval@1

pub let owns_service = actor.teams any in service.owners
pub let cleared = split(service.labels["regions"], ",") all in actor.regions
pub let eligible = "deployer" in actor.roles
  and environment == "production"
  and service.labels has {
    "app.kubernetes.io/managed-by": "argocd",
    "platform.example.com/lifecycle": "ga",
  }
```

A module is a document of shared, typed matchers. It has no rules and no params, so importing from it can never change a decision by itself.

- The header names the kind and pins a kind version the same way a policy's does. The `let`s read inputs and type-check against them.
- A module may contain `use` and `let` statements only. `param`, `when`, `assert`, decision constructors and invocations are compile errors.
- A module may `use` other modules.
- Only `pub let`s are exported. A module's other lets are private helpers for its own `pub let`s.
- A host can't load or evaluate a module. `Load` on a module's name fails with `deploy.common is a module, not a policy`.

```text
deploy/common.sigil:3:1: error: a module can't contain `param`
  |
3 | param min_soak: duration = 24h
  | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
  = help: a module holds only imports and lets; rules, params and invocations belong in a policy
```

## Identifiers

Each policy and module has one flat top-level namespace containing:

- the kind's inputs, host functions and decisions,
- the document's own params and lets, including the lets inside `when` bodies,
- every name bound by a `use`.

| Case                                                                                                 | Result                                                                                                                                                  |
| ---------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Two of these names collide                                                                           | Compile error. Nothing shadows anything                                                                                                                 |
| A `param` named `release` in a kind that declares `input release`                                    | Compile error                                                                                                                                           |
| A quantifier or filter variable named `approvers` in a policy with a param of that name              | Compile error                                                                                                                                           |
| A `let` called `deny` in a kind that declares `decision deny`                                        | Compile error. Decision names are in the namespace, because a bare decision name is a [value](/reference/types/#decision) in `assert` conditions        |
| An import whose bound name is a decision                                                             | Compile error; rename it with `as`                                                                                                                      |
| A reason with the same name as anything else                                                         | Allowed. Reasons aren't in the namespace: `let release_manager = ...` is fine next to `approve(release_manager)`                                        |
| A document pinned below the kind's current version collides with an input, host function or decision | Allowed. The document's own name wins, the kind's name is out of reach in that document, and the [`shadowed-kind-name`](/reference/lints/) lint says so |
| The same collision in a document pinned to the current version                                       | Compile error                                                                                                                                           |
| Two of the document's own names collide, at any pin                                                  | Compile error                                                                                                                                           |

Reasons only appear after a decision's name, as `approve.release_manager`, or in a constructor's first slot.

Why: [Why the language looks like this](/understanding/language-choices/) and [Adding a name never breaks a policy](/understanding/kinds/#adding-a-name-never-breaks-a-policy).
