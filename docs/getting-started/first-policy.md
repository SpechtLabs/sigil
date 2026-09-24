---
title: Your first policy
icon: mdi:flash
createTime: 2026/09/24 22:30:00
permalink: /getting-started/first-policy/
---

In this tutorial you'll write `deploy/production.sigil` from an empty file, one rule at a time, against the `DeployApproval` kind from the [tour](/getting-started/tour/). After each step you'll check the policy and evaluate it against a sample deploy, and along the way you'll hit the compile errors Sigil exists to produce. At the end you'll invoke it from a team policy and split it into the files the tour uses.

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

By the end, `deploy/` will hold two more files and a `payments/` directory will sit next to it.

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

Other files will import it by the name `deploy.production`, not by its path. Sigil finds documents by the names in their headers, so the file could be called anything, and one file could even hold several documents separated by `---`. Keeping the name and the path aligned, `deploy.production` in `deploy/production.sigil`, is a convention that makes a repository easy to navigate.

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

## Step 6: invoke it from a team policy

Try to evaluate the base policy on its own:

::: terminal Evaluate the base policy directly

```shell
$ sigil eval --kind deploy_approval.sigil --input owner-deploy.json deploy/production.sigil
deploy/production.sigil:4:7: error: required param "approvers" is not bound
  |
4 | param approvers: list<string>
  |       ^^^^^^^^^
  = help: bind it from a policy that invokes this one, as in
          production(approvers: [...]), or from Go with policy.Params
```

:::

A policy with a required param is a template until someone fills it in. Create `payments/production.sigil` next to it:

```sigil
policy payments.production: DeployApproval

use deploy.production

production(
  min_soak: 4h,
  approvers: ["payments-leads"],
)

when "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```

`use deploy.production` imports the policy under the name `production`, and nothing else. The call on line 5 is what adds its rules: a policy invocation looks like a decision constructor with named arguments, and it binds the invoked policy's params. `tiers` isn't mentioned, so it keeps its default.

::: terminal Evaluate the team policy

```shell
$ sigil eval --kind deploy_approval.sigil --input owner-deploy.json --policy payments.production deploy/ payments/
decision  review
reason    service_owner
policy    payments.production
payload   approvers = ["payments-leads"]

candidates
  review   service_owner     payments/production.sigil:5:1 → deploy/production.sigil:34:5
  approve  payments_sre      payments/production.sigil:11:3
```

:::

The team policy imports `deploy.production`, so the command passes both directories. The CLI reads every document in them into one bundle and resolves the import by name. The bundle now holds two policies, so `--policy` says which one to evaluate.

With `min_soak` lowered to four hours, the six-hour soak no longer trips `soak_too_short`. The base policy asks for review, the team's rule offers an approval, and review wins because it ranks higher in the kind's `precedence`. The first candidate's position is a call chain: the invocation on line 5 of the team file, then the rule on line 34 of the base.

::: tip Checking a base policy on its own
Whether `sigil check` should accept a base policy with unbound required params, treating it as a library, or report them the way `eval` does, isn't settled yet. Either way, checking a policy that invokes it checks the base too.
:::

## Step 7: protect the denies

An invocation can go anywhere a decision can, including inside a `when` block, where the block's condition gets added to every rule it brings in. That's useful: it's how a team says "these approvals, but only for some services". It's also a hole. Nothing stops a team from writing this:

```sigil
when false {
  production(min_soak: 4h, approvers: ["payments-leads"])
}
```

That switches off `not_eligible` and `soak_too_short` along with everything else. The fix is to keep the denies in a policy of their own and have the host require it. Split `deploy/production.sigil` into three files.

The shared matchers move to a module, a file that holds `let`s and nothing else. Create `deploy/common.sigil`:

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

The two denies move to `deploy/guardrails.sigil`, together with the param they read:

```sigil
policy deploy.guardrails: DeployApproval

use deploy.common.{eligible}

param min_soak: duration = 24h

when not eligible {
  deny("not_eligible")
}

when release.soak < min_soak and not release.hotfix {
  deny("soak_too_short")
}
```

`use deploy.common.{eligible}` imports one `let` by name. What's left in `deploy/production.sigil` is the approvals and reviews:

```sigil
policy deploy.production: DeployApproval

use deploy.common.{cleared, owns_service}

param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]

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

The host now loads every team policy with `policy.Require("deploy.guardrails")`, and a policy repository runs the same check in CI with `sigil check --require`. Try it on the team policy from step 6, which doesn't invoke the guardrails yet:

::: terminal Check the team policy against the requirement

```shell
$ sigil check --kind deploy_approval.sigil --require deploy.guardrails deploy/ payments/
payments/production.sigil:1:1: error: deploy.guardrails must be invoked unconditionally
  |
1 | policy payments.production: DeployApproval
  | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
  = help: the host requires deploy.guardrails for every DeployApproval policy.
          Add `use deploy.guardrails` and invoke it at the top level.
```

:::

The same check reports a second error, left out above: `deploy.production` no longer declares `min_soak`, so the call on line 5 passes an argument the policy doesn't have. The requirement check also fails if the call is there but sits inside a `when`. Update the team policy to invoke both policies. While you're at it, give services in PCI scope a second approver group, and only offer the SRE fast path to actors who are cleared for the service's regions:

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

Here the `when` around `production(...)` is exactly what you want: the block's condition is added to every rule the call brings in. The guardrails are invoked at the top level, so the check passes and every guardrail deny is always a candidate.

::: terminal Evaluate the split policy

```shell
$ sigil eval --kind deploy_approval.sigil --input owner-deploy.json --policy payments.production deploy/ payments/
decision  review
reason    service_owner
policy    payments.production
payload   approvers = ["payments-leads"]

candidates
  review   service_owner     payments/production.sigil:14:3 → deploy/production.sigil:16:5
  approve  payments_sre      payments/production.sigil:18:3
```

:::

Same decision as before the split; only the positions moved. These are the exact files the [tour](/getting-started/tour/#evaluating-it-by-hand) walks through with more inputs. To see everything the team policy can decide in one place, run `sigil explain` on it; the tour shows [its output](/getting-started/tour/#what-the-team-policy-adds-up-to).

## Step 8: pin the behaviour with test cases

A policy is only as trustworthy as the cases you've pinned down. `sigil test` (and the `policytest` package for `go test`) will run cases that pair an input JSON file with the decision and reason you expect. The file format isn't designed yet, so here are the cases this tutorial has already exercised, plus one for the PCI split, as a plain table:

| Policy | Input | Expected decision | Expected reason |
| --- | --- | --- | --- |
| `payments.production` | `owner-deploy.json` | `review` | `service_owner` |
| `payments.production` | `owner-deploy.json` with `"soak": "2h"` | `deny` | `soak_too_short` |
| `payments.production` | `wrong-lifecycle.json` | `deny` | `not_eligible` |
| `payments.production` | `owner-deploy.json` with the actor's teams set to `["checkout"]` | `deny` | `no_rule_matched` |
| `payments.production` | `owner-deploy.json` with `"compliance": "pci"` on the service | `review` | `service_owner` |

Because reasons are string literals, a test that expects `soak_too_short` breaks loudly if someone renames the reason, and a dashboard grouping by reason keeps working as long as the tests pass. The last case has the same decision and reason as the first; asserting on the payload's `approvers` as well is what would tell them apart, which is one reason the test format should allow payload assertions.

## Where you are now

You've written a policy with a required param, invoked it from a team policy, split its denies into guardrails the host requires, and seen the evaluator pick a winner from several candidates. From here:

- [Per-team policies](/guides/team-policies/) covers imports, invoking under conditions, binding params from Go, and what teams can and can't override.
- [Common patterns](/guides/patterns/) collects recipes for labels, optionals, quantifiers and time.
- The [policy files reference](/reference/policy-files/) is the precise definition of every statement you used.
