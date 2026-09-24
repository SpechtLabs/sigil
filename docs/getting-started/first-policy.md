---
title: Your first policy
icon: mdi:flash
createTime: 2026/09/24 22:30:00
permalink: /getting-started/first-policy/
---

In this tutorial you'll write `deploy/production.sigil` from an empty file, one rule at a time, against the `DeployApproval` kind from the [tour](/getting-started/tour/). After each step you'll check the policy and evaluate it against a sample deploy, and along the way you'll hit two of the compile errors Sigil exists to produce.

::: info Illustrative CLI
Sigil is in the design phase and the `sigil` CLI doesn't exist yet. The commands, flags and output on this page show the intended experience; the exact spelling will change. The language itself is what this page specifies.
:::

## Set up the policy repo

A policy repo holds the exported kind file and a directory per policy namespace:

```text
policies/
├── deploy_approval.sigil
├── deploy/
│   └── production.sigil
├── owner-deploy.json
└── wrong-lifecycle.json
```

`deploy_approval.sigil` is the kind file the host generates from Go. It starts with the `kind` keyword, which is how tools tell it apart from a policy. You can copy it from the [tour](/getting-started/tour/#the-kind); you won't edit it.

`owner-deploy.json` is the deploy you'll evaluate throughout. A member of the payments team ships the `ledger` service to production after six hours of soak in staging:

```json
{
  "environment": "production",
  "release": { "soak": "6h", "hotfix": false },
  "service": {
    "name": "ledger",
    "tier": "standard",
    "owners": ["payments"],
    "labels": {
      "app.kubernetes.io/managed-by": "argocd",
      "platform.example.com/lifecycle": "ga",
      "regions": "eu-1"
    }
  },
  "actor": {
    "name": "ana",
    "teams": ["payments", "payments-sre"],
    "roles": ["deployer"],
    "regions": ["eu-1", "eu-2"]
  }
}
```

`wrong-lifecycle.json` is the same file with `"platform.example.com/lifecycle": "beta"` on the service.

## Step 1: the header

Every policy file starts with a `policy` line that names the policy and the kind it implements:

```sigil
policy deploy.production: DeployApproval
```

The name `deploy.production` has to match the file's path, `deploy/production.sigil`. That's how `use` finds it later.

A policy with no rules is valid. Evaluate it:

::: terminal Evaluate the empty policy

```shell
$ sigil eval --kind deploy_approval.sigil --input owner-deploy.json deploy/production.sigil
decision  deny
reason    no_rule_matched
policy    deploy.production
payload   (none)

candidates
  (none)
```

:::

No rule fired, so the kind's `default deny("no_rule_matched")` applies. That's your safety net for everything that follows: a deploy nobody wrote a rule for gets denied.

## Step 2: one deny rule

Require releases to soak in staging for a day before they reach production, unless they're hotfixes. Add a `when` block under the header:

```sigil
policy deploy.production: DeployApproval

when release.soak < 24h and not release.hotfix {
  deny("soak_too_short")
}
```

`release.soak` is a `duration` in the kind and `24h` is a duration literal, so the comparison type-checks. Comparing it to `24` or `"24h"` would be a compile error; Sigil never coerces between types. `not` binds tighter than `and`, so the condition reads the way it sounds.

::: terminal Evaluate a release with six hours of soak

```shell
$ sigil eval --kind deploy_approval.sigil --input owner-deploy.json deploy/production.sigil
decision  deny
reason    soak_too_short
policy    deploy.production
payload   (none)

candidates
  deny     soak_too_short    deploy/production.sigil:4:3
```

:::

The candidate list tells you where the decision came from, down to line and column. You never have to name rules; the reason and the position identify them.

## Step 3: a let, and your first compile error

Only some services go through this gate at all. Describe them once with a `let` and deny everything else:

```sigil
policy deploy.production: DeployApproval

let eligible =
  "deployer" in actor.roles
  and environment == "production"
  and service.lables has {
    "app.kubernetes.io/managed-by": "argocd",
    "platform.example.com/lifecycle": "ga",
  }

when not eligible {
  deny("not_eligible")
}

when release.soak < 24h and not release.hotfix {
  deny("soak_too_short")
}
```

There's a typo on line 6. Check the file:

::: terminal Check the policy

```shell
$ sigil check --kind deploy_approval.sigil deploy/production.sigil
deploy/production.sigil:6:15: error: unknown field "lables" on type Service
  |
6 |   and service.lables has {
  |               ^^^^^^
  = help: did you mean "labels"? Service declares: name, tier, owners, labels
```

:::

In a YAML matcher, or in a language where unknown fields resolve to `null`, this condition would compile and always be false. Here that happens to deny everything, which someone would notice quickly. In a deny rule whose condition is written the other way round, the same typo would switch the deny off, and nobody would notice at all. Sigil refuses to compile it. Fix the typo to `service.labels` and evaluate the wrong-lifecycle deploy:

::: terminal Evaluate a deploy of a beta service

```shell
$ sigil eval --kind deploy_approval.sigil --input wrong-lifecycle.json deploy/production.sigil
decision  deny
reason    not_eligible
policy    deploy.production
payload   (none)

candidates
  deny     not_eligible      deploy/production.sigil:12:3
  deny     soak_too_short    deploy/production.sigil:16:3
```

:::

Both denies fired. They share a decision, so precedence can't pick between them, and the earlier source position wins. The trace still lists both, so nothing is hidden.

A few things to notice about the `let`:

- It spans several lines. The grammar doesn't care about newlines or indentation, because every statement starts with a keyword.
- `environment` is an input of type `string`, not a struct, so you compare it directly.
- Label keys like `platform.example.com/lifecycle` are strings inside a map literal. Identifiers can't contain `.` or `/`, so you'd read a single one with `service.labels["platform.example.com/lifecycle"]`.
- The trailing comma after the last pair is fine.

## Step 4: a param instead of a magic number

The 24-hour soak is a policy decision that teams will want to tune. Turn it into a `param` with a default:

```sigil
policy deploy.production: DeployApproval

param min_soak: duration = 24h

// let eligible = ... unchanged

when not eligible {
  deny("not_eligible")
}

when release.soak < min_soak and not release.hotfix {
  deny("soak_too_short")
}
```

Behaviour doesn't change: nobody has bound `min_soak` yet, so it's `24h`. But now a team can lower it without copying the file, and the type checker makes sure whatever they pass is a `duration`.

## Step 5: approvals and reviews

The interesting part of the policy only applies when the actor is cleared for every region the service runs in. Add two more lets, two params, and a rule with nested rules inside it. Here's the complete file, with a mistake left in:

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
    review("service_owner", approver: approvers)
  }
}
```

::: terminal Check the policy

```shell
$ sigil check --kind deploy_approval.sigil deploy/production.sigil
deploy/production.sigil:34:29: error: decision review has no payload field "approver"
   |
34 |     review("service_owner", approver: approvers)
   |                             ^^^^^^^^
   = note: DeployApproval declares: decision review(reason: string, approvers: list<string>)
   = help: did you mean "approvers"?
```

:::

Payload fields come from the kind, and the error quotes the signature so you don't have to go looking for it. Rename the argument to `approvers:` and the file is the finished base policy.

Some notes on what you just wrote:

- The outer `when cleared` has no decision of its own. It only scopes the two rules inside it: a nested rule fires only when every enclosing condition holds.
- `split` is the host function declared in the kind. `all in` checks that every region listed on the service appears in the actor's regions, and `any in` in `owns_service` checks that the actor's teams and the service's owners overlap.
- `approve("release_manager")` passes no payload, so `bake` takes the kind's default of `1h`. Writing `approve("release_manager", bake: 2h)` would override it.
- `approvers` has no default, so it's required. That matters in the next step.

## Step 6: bind the params

Try to evaluate the base policy on its own:

::: terminal Evaluate the base policy directly

```shell
$ sigil eval --kind deploy_approval.sigil --input owner-deploy.json deploy/production.sigil
deploy/production.sigil:4:7: error: required param "approvers" is not bound
  |
4 | param approvers: list<string>
  |       ^^^^^^^^^
  = help: bind it from a policy that uses this one, as in
          use deploy.production(approvers: [...]), or from Go with policy.Params
```

:::

A policy with a required param is a template until someone fills it in. Create `payments/production.sigil` next to it:

```sigil
policy payments.production: DeployApproval

use deploy.production(
  min_soak: 4h,
  approvers: ["payments-leads"],
)

when "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```

::: terminal Evaluate the team policy

```shell
$ sigil eval --kind deploy_approval.sigil --input owner-deploy.json payments/production.sigil
decision  review
reason    service_owner
policy    payments.production
payload   approvers = ["payments-leads"]

candidates
  review   service_owner     deploy/production.sigil:34:5
  approve  payments_sre      payments/production.sigil:9:3
```

:::

With `min_soak` lowered to four hours, the six-hour soak no longer trips `soak_too_short`. The base policy asks for review, the team's rule offers an approval, and review wins because it ranks higher in the kind's `precedence`. The [tour](/getting-started/tour/#evaluating-it-by-hand) walks through more inputs against this exact pair of files.

::: tip Checking a base policy on its own
Whether `sigil check` should accept a base policy with unbound required params, treating it as a library, or report them the way `eval` does, isn't settled yet. Either way, checking the instantiation checks the base too.
:::

## Step 7: pin the behaviour with test cases

A policy is only as trustworthy as the cases you've pinned down. `sigil test` (and the `policytest` package for `go test`) will run cases that pair an input JSON file with the decision and reason you expect. The file format isn't designed yet, so here are the cases this tutorial has already exercised, as a plain table:

| Policy | Input | Expected decision | Expected reason |
| --- | --- | --- | --- |
| `payments.production` | `owner-deploy.json` | `review` | `service_owner` |
| `payments.production` | `owner-deploy.json` with `"soak": "2h"` | `deny` | `soak_too_short` |
| `payments.production` | `wrong-lifecycle.json` | `deny` | `not_eligible` |
| `payments.production` | `owner-deploy.json` with the actor's teams set to `["checkout"]` | `deny` | `no_rule_matched` |

Because reasons are string literals, a test that expects `soak_too_short` breaks loudly if someone renames the reason, and a dashboard grouping by reason keeps working as long as the tests pass.

## Where you are now

You've written a base policy with a required param, a team policy that binds it, and you've seen the evaluator pick a winner from several candidates. From here:

- [Per-team policies](/guides/team-policies/) covers `use ... as`, binding params from Go, and what teams can and can't override.
- [Common patterns](/guides/patterns/) collects recipes for labels, optionals, quantifiers and time.
- The [policy files reference](/reference/policy-files/) is the precise definition of every statement you used.
