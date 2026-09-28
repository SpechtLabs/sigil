---
title: Policy files
icon: mdi:file-document-outline
createTime: 2026/09/24 22:30:00
permalink: /reference/policy-files/
---

::: info Draft specification
This page specifies the language as designed. Syntax, kinds, type checking and expression evaluation are implemented; rules and decisions, composition and the CLI aren't yet. See [Open questions](/project/open-questions/).
:::

A policy opens with a `policy` header, lists its imports, and then contains any number of `param`, `let`, `when` and `assert` statements and policy invocations in any order. Each statement starts with a keyword or with the name of an imported policy followed by `(`, so a policy needs no separators and no significant whitespace.

Shared matchers live in a second kind of document, the [module](#modules), which holds only imports and `let`s.

A `.sigil` file holds one or more documents, each a policy, a module or a kind. The host loads a bundle of files and finds documents by the names in their headers, not by file path, so a whole policy tree can live in one file, for example a single key of a Kubernetes ConfigMap. See [Bundles and resolution](#bundles-and-resolution).

## Statements at a glance

| Statement                          | Where                   | Meaning                                                                 |
| ---------------------------------- | ----------------------- | ----------------------------------------------------------------------- |
| `policy <name>: <Kind>@<N>`        | first statement         | Names the policy, the kind it implements and the kind version it was written against |
| `use <path>`                       | after the header        | Imports names from another policy or module. Never adds rules by itself |
| `param <name>: <type> [= <expr>] [, min: <expr>] [, max: <expr>]` | top level | Typed input set at instantiation. No default means required; bounds limit what a caller may bind |
| `[pub] let <name> = <expr>`        | top level or nested     | Named, reusable expression. `pub` lets other documents import it; only at the top level |
| `when <expr> { ... }`              | top level or nested     | Rule block. Body holds nested `when` blocks, decisions, asserts and invocations |
| `assert("<reason>", <expr>)`       | top level or nested     | Condition that must hold, or evaluation fails with an assertion error   |
| `<policy>(<param>: <expr>, ...)`   | top level or nested     | Invokes an imported policy, adding its rules with its params bound      |

Kind files use a different set of statements; see [Kind files](/reference/kind-files/).

## `policy`

```sigil
policy deploy.production: DeployApproval@1
```

The header must be the first statement in the file, and a file must contain exactly one. The name is a dotted [policy name](/reference/lexical/) and the part after the colon names the kind whose contract the policy is checked against. Every input, host function, type and decision the policy can refer to comes from that kind.

`@1` pins the kind version the policy was written against. The pin is required, and a header without one is a compile error that suggests the kind's current version. The policy always compiles against the kind the host has; the pin decides whether the host accepts it at all, and how a name the kind added later is treated:

- A pin older than the kind's `accepts` is rejected, because the host has declared a breaking change since.
- A pin newer than the kind's version is rejected, because the host doesn't have that kind yet.
- A pin older than the kind's version, but still accepted, lets the policy keep a name the kind added since; see [Identifiers](#identifiers).

Moving a pin forward is a one-line diff that says "we checked this against the new version", which is what a reviewer should look for. The versioning rules are on [Kind files](/reference/kind-files/#versioning).

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

- A path names a document in the bundle: `use deploy.common` finds the document whose header is `module deploy.common`, in whichever file it lives. See [Bundles and resolution](#bundles-and-resolution).
- The last path segment is the bound name unless `as` renames it.
- A whole import of a [module](#modules) binds a qualifier: `common.cleared` reads the module's `cleared`. A whole import of a policy binds a name you can invoke.
- A selective import, `use deploy.common.{...}`, binds each listed `let` under its own name, or under the name after `as`.
- The imported document must implement the same kind. Importing a module or policy written for another kind, or a kind document, is a compile error.
- Imports come right after the header, before any `param`, `let`, rule or invocation.
- An imported name that collides with an input, host function, param, `let` or another import is a compile error. Nothing shadows silently.
- There are no wildcard imports. Every name used in a document is either defined there or listed in a `use`, so a reader can always find where it comes from.
- The import graph must be acyclic.
- An unused import is a lint warning, not an error, so commenting out a rule while debugging doesn't break the build.

### Importing from a policy

A policy's `pub let`s can be imported too, the same way as a module's. A `pub let` in a policy can't depend on a param, directly or through other `let`s: a param has no value outside an invocation, so a param-dependent `let` would mean nothing in the importing document. The compiler tracks the dependency and reports it where the `let` is declared, not where someone imports it:

```text
deploy/guardrails.sigil:5:9: error: let `soak_ok` can't be `pub`: it reads param `min_soak`
  |
5 | pub let soak_ok = release.soak >= min_soak or release.hotfix
  |         ^^^^^^^
  = help: a param has no value outside an invocation; move the let to a module, or drop `pub`
```

In practice this pushes shared matchers into modules, which is where they belong.

::: tip Proposed
Reading a policy's param-free `let` through a whole import (`guardrails.some_let`) follows the same rule as a selective import. The design only spells out the selective form.
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

### Bounds

```sigil
param min_soak: duration = 24h, min: 1h, max: 48h
```

`min` and `max` limit the values a caller may bind. A guardrail uses them so that a team invoking it can adjust a threshold but can't switch the rule off: `guardrails(min_soak: 0s)` is a compile error.

- Bounds apply to `int`, `float` and `duration` params. Each is optional, both are inclusive, and they may come in either order.
- A bound is a constant of the param's type, like a default. `min` can't be above `max`, and the default must lie between them.
- Invocation arguments are constants, so every bound is checked when the policy compiles, at the argument that breaks it. A value bound from Go through `policy.Params` is checked by `Load`. Nothing is left to evaluation time, so a bad value never fails an evaluation.
- `min` and `max` are names in a named-argument position, not keywords, so a kind can still declare `fn max(int, int) -> int`.

```text
teams/payments.sigil:5:22: error: min_soak: 0s is below the minimum 1h
  |
5 | guardrails(min_soak: 0s)
  |                      ^^
  = help: deploy.guardrails declares `param min_soak: duration = 24h, min: 1h, max: 48h`
```

The declaration checks are implemented; checking invocation arguments and `policy.Params` against the bounds comes with invocation in the composition milestone. Bounds are only as trustworthy as the file that declares them: for a required guardrail, [`policy.From`](/reference/go-api/#where-required-policies-come-from) loads that file from a source the host trusts.

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

- A top-level `let` is visible in the whole document. A `let` inside a `when` body is visible in that body and the blocks nested in it, and nowhere else, not even in the `when`'s own condition. See [Scoped lets](#scoped-lets).
- A `let` is private to its document unless it's declared `pub let`. Only a `pub let` can be imported. See [Exporting lets](#exporting-lets).
- Lets must form a directed acyclic graph. `let a = b` together with `let b = a` is a compile error, and so is any longer cycle.
- A let is a name for an expression, not a mutable variable. It can't be reassigned, and binding the same name twice is a compile error.
- The order of `let` statements in a file doesn't matter; a let may refer to one declared further down. The same holds inside a `when` body.

### Scoped lets

```sigil
when active {
  let sre = any r in actor.roles: r like "sre-*"
  when sre and release.hotfix { approve("sre_hotfix") }
  when sre and not release.hotfix { review("sre_change", approvers: approvers) }
}
```

A `let` inside a body names a sub-expression that several nested blocks share, without making it visible to the rest of the document.

- It can read everything its body can: inputs, params, top-level lets, imports, and the lets of every enclosing body.
- It can't shadow anything. Its name can't be taken by an input, host function, decision, param, import or any other `let` in the document, including a `let` in an unrelated `when` body. Names are unique per document so that a trace and [`sigil explain`](/reference/cli/#sigil-explain) can name every `let` without saying which block it came from.
- It can't be `pub`. Nothing outside its body can see it, so there's nothing to export.
- It's only evaluated when its body is reached, so it can rely on the enclosing conditions: in `when len(xs) > 0 { let first = xs[0] ... }` the index can't fail. See [Evaluation semantics](/reference/evaluation/#lets).

### Exporting lets

```sigil
module deploy.common: DeployApproval@1

pub let cleared = split(service.labels["regions"], ",") all in actor.regions and not restricted
let restricted = service.labels has "restricted"
```

`pub let` makes a top-level `let` importable. A `let` without `pub` is private: other lets and rules in the same document can use it, and a `use` that names it is a compile error. The rule is the same for modules and policies, so a module author can refactor private helpers without breaking an importer, and a policy never exports something by accident.

Because a private `let` has no readers outside its document, one that nothing reads is a lint warning; see [`unused-let`](/reference/cli/#lints).

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

The body contains nested `when` blocks, [scoped lets](#scoped-lets), [decision constructors](/reference/decisions/), [asserts](#assert) and [policy invocations](#policy-invocation), and nothing else. There's no `param`, no `use` and no bare expression inside a body.

There's no `else`. Write `when not x { ... }` instead. A nested `when` fires only if every enclosing condition holds, which makes nesting a conjunction. The full rules live on [Evaluation semantics](/reference/evaluation/).

Whether a single body may contain more than one decision constructor is an [open question](/project/open-questions/). The examples in these docs use one constructor per body.

## `assert`

```sigil
assert("negative_soak", release.soak >= 0s)

when service.tier == "critical" {
  assert("critical_needs_team_label", service.labels has "team")
}

assert("sod_customer_dev",
  [customer_data_writer, development_environment_writer] exclusive in outcome)
```

An assert states something that must be true whenever it's reached. If its condition is false, evaluation fails: `Eval` returns an assertion error, and the host records it as an error, not as a decision. Use a decision for an outcome you expect, such as denying a deploy that hasn't soaked, and an assert for a state that means the policy, the host or the input is wrong.

- The condition must have type `bool`. It can read inputs, params, lets and imported lets, and, unlike any other expression, [`outcome`](/reference/expressions/#decision-values-and-outcome), the decisions evaluation produced.
- The reason comes first, as in a [decision constructor](/reference/decisions/), and follows the same rules as a [decision reason](/reference/decisions/#the-reason): it's a string literal, a stable identifier for metrics and grep, and dynamic text isn't allowed. `assert(cond, "reason")` is a parse error that says so.
- An assert may appear at the top level or inside a `when` body. Inside a body it's only checked when every enclosing condition holds, the same conjunction rule as for decisions. An assert in an invoked policy gets the invocation's enclosing conditions too.
- An assert never produces a candidate and never changes the outcome. It can only fail the evaluation.
- An assert whose condition reads `outcome` is an outcome assert and is checked once the outcome exists. Every other assert is an input assert and is checked before any rule runs, so it works as a precondition. The checker decides which from the condition; there's no keyword for it.

`xor`, `one in` and `exclusive in` exist mostly for asserts. `exclusive in` over `outcome` is how a [collecting kind](/reference/kind-files/#collecting-kinds) keeps two decisions from being granted together. The two phases in detail, and what the host gets back when one fails, is on [Evaluation semantics](/reference/evaluation/#assertions).

::: tip Proposed
The `assert` statement, its syntax and its semantics are proposed. The open parts are listed under [Assertions](/project/open-questions/#assertions).
:::

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

A module is a file of shared, typed matchers. It has no rules and no params, so importing from it can never change a decision by itself. Its header pins a kind version the same way a policy's does.

```sigil
module deploy.common: DeployApproval@1

pub let owns_service = actor.teams any in service.owners
pub let cleared =
  split(service.labels["regions"], ",") all in actor.regions
pub let eligible =
  "deployer" in actor.roles
  and environment == "production"
  and service.labels has {
    "app.kubernetes.io/managed-by": "argocd",
    "platform.example.com/lifecycle": "ga",
  }
```

- The header names the kind, because the `let`s read inputs and have to type-check against them.
- A module may contain `use` and `let` statements only. `param`, `when`, `assert`, decision constructors and invocations are compile errors.
- A module may `use` other modules.
- Only `pub let`s are exported. A module's other lets are private helpers for its own `pub let`s.
- A host can't load or evaluate a module; it has no rules to evaluate.

## Bundles and resolution

### Documents

A file holds one or more documents. A document is a policy, a module or a kind, and it starts with its header: `policy`, `module` or `kind`. It ends where the next header starts, or at the end of the file.

```sigil
module deploy.common: DeployApproval@1

pub let cleared =
  split(service.labels["regions"], ",") all in actor.regions

---

policy deploy.guardrails: DeployApproval@1

param min_soak: duration = 24h

when release.soak < min_soak and not release.hotfix {
  deny("soak_too_short")
}
```

A header keyword can't start a statement inside a document, so the parser knows a new document has started when it meets one at the top level. Inside braces, after `.` or as a named argument, `policy`, `module` and `kind` are ordinary names: a `type Resource { kind: string }` body or a `resource.kind` read never ends a document. See [Grammar](/reference/grammar/#source-files). The `---` separator is optional. It's there because YAML users expect it, and because it makes document boundaries easy to scan in a long file. `sigil fmt` always writes it between documents, so a bundle has one canonical form. See [Lexical structure](/reference/lexical/#document-separators) for how `---` lexes.

- A `---` before the first document or after the last one is allowed, and so are several in a row. `sigil fmt` removes the extras. (proposed)
- A comment belongs to the document that follows it, so a comment directly above a header stays with that document when a tool extracts or moves it. (proposed)
- A file with no documents at all is valid and contributes nothing.

### Bundles

A bundle is every document in every file the host or the CLI loads, indexed by the name in each header. Files are plain containers:

- `use deploy.common` resolves to the document named `deploy.common`, wherever it is. One key per team, one file per policy, or everything in one file all resolve the same way.
- A name defined twice in a bundle is a compile error that points at both definitions.
- Kind documents aren't part of the index, and they're never taken as the contract. The kind always comes from the host's Go definition, or from `--kind` in the CLI. A kind document with the same kind name must match that contract exactly, or the load fails, which catches a stale export; one for another kind is ignored. A `use` of a kind's name is a compile error.
- The host names the root policy explicitly, so one bundle can hold many policies and the host picks the entry point. A module can't be a root; it has no rules to evaluate.
- A host can load required policies from a separate, trusted source with `policy.From`. Every name that source defines is reserved, and a bundle document that claims one is a compile error. See [Where required policies come from](/reference/go-api/#where-required-policies-come-from).

A bundle is loaded as a whole. A syntax error in any document fails the load, even in a document the root never uses. That keeps loading simple and predictable; the place to catch a broken document is `sigil check` in CI, before a bundle ships. On a hot reload, the host keeps the last policy that loaded; see [Hot reload](/reference/go-api/#hot-reload). Giving each team its own key or file keeps one team's mistakes out of other teams' review diffs, even though the load still fails as a whole. See [Policies in a ConfigMap](/guides/configmaps/).

```text
policies.sigil:42:1: error: policy payments.access is defined twice
   |
42 | policy payments.access: DeployApproval@1
   | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
   = note: first defined at teams/payments.sigil:1:1
```

### Loading files

The host passes the bundle as an `fs.FS`; see the [Go API](/reference/go-api/#loading-and-evaluating). The loader reads every file whose name ends in `.sigil`, in every directory, so ConfigMap keys need the extension too (`policies.sigil`). It skips every file or directory whose name starts with `.`. Skipping dot entries is what makes a mounted ConfigMap volume work: kubelet keeps the real files in a hidden `..2026_09_25_…` directory, links it as `..data`, and links each key at the top level, so without the rule every document would be read three times and collide with itself. Symbolic links are followed: the loader checks each entry with `fs.Stat`, not with the directory entry's type, because in a mounted ConfigMap every key at the top level is a symbolic link.

The CLI takes files, directories and stdin; see [CLI & editor tooling](/reference/cli/#inputs).

### File names

Nothing in the language ties a file's path to the names inside it. In a policy repository, keeping them aligned still helps readers and lets CODEOWNERS map to namespaces, so it's a lint: `path-matches-name` warns when a file holds a document whose name doesn't match the file's path, with each `.` turned into `/` and `.sigil` appended.

| Name                  | Expected file               |
| --------------------- | --------------------------- |
| `deploy.common`       | `deploy/common.sigil`       |
| `deploy.production`   | `deploy/production.sigil`   |
| `payments.production` | `payments/production.sigil` |

A file whose documents all share a prefix matches when its path is that prefix, so `deploy.sigil` may hold `deploy.common`, `deploy.guardrails` and `deploy.production`. The lint is off by default. It's a review aid, not a security control: it helps in a repository, and does nothing for a bundle that arrives through `policy.MapFS`. Required policies are protected by `policy.From`; see [Required policies](/reference/evaluation/#required-policies).

::: tip Proposed
The prefix rule for multi-document files is proposed here. The lint itself follows from resolving by name.
:::

### Identifiers

Each policy and module has one flat top-level namespace containing:

- the kind's inputs, host functions and decisions,
- the document's own params and lets, including the lets inside `when` bodies,
- every name bound by a `use`.

Any collision between two of these is a compile error, and nothing shadows anything. A `param` named `release` in a kind that declares `input release` fails to compile, and so does a quantifier variable named `approvers` in a policy that has a param by that name.

There's one exception, and it only exists so that adding a name to a kind never breaks a policy. When a document pinned below the kind's current version collides with an input, host function or decision, the kind must have added that name after the document was written, because the collision would have been an error at the pinned version. The document's own name wins, the kind's new name is out of reach in that document, and the `shadowed-kind-name` lint says so. The same collision in a document pinned to the current version is an error. Collisions between two of the document's own names are always errors.

Decision names are part of that namespace, because a bare decision name is a [value](/reference/types/#decision) in `assert` conditions. A let called `deny` in a kind that declares `decision deny` is a compile error, and so is an import whose bound name is a decision: rename it with `as`. (proposed; an earlier draft kept decisions in a namespace of their own, which only worked while they appeared in constructor position alone. See [Open questions](/project/open-questions/#decision-values-and-outcome).)

::: tip Proposed
The single flat namespace and the no-shadowing rule are proposed here to close a gap in the current design. The goal is that any name in a policy has exactly one meaning, which you can find without knowing scoping rules.
:::

## A complete file

The team policy from the [tour](/getting-started/tour/). It imports two platform policies and one shared matcher, invokes the guardrails unconditionally, picks approvers by compliance scope, and adds one approval of its own:

```sigil
policy payments.production: DeployApproval@1

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
