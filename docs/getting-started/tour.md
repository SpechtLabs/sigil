---
title: A tour of the language
icon: mdi:map-marker-path
createTime: 2026/09/24 22:30:00
permalink: /getting-started/tour/
---

This page walks through one complete example: a gate for production deploys. Engineers ask to ship a release of a service to production, and the policy decides whether to approve it, send it to a human for review, or deny it. You'll read the three files that make up the setup, then push four deploys through the policy by hand.

## The kind

The kind is the contract between the Go host and every policy written for it. Nobody writes this file by hand; the host generates it from Go structs (see [Kind files](/reference/kind-files/)). Policy authors read it the way you'd read an API spec. It lives in `deploy_approval.sigil`:

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

fn split(s: string, sep: string) -> list<string>

decision deny(reason: string)
decision review(reason: string, approvers: list<string>)
decision approve(reason: string, bake: duration = 1h)

precedence deny > review > approve
default deny("no_rule_matched")
```

Reading top to bottom:

- `kind DeployApproval version 1` names the contract. Policies refer to it by that name.
- `type` declares struct types. `duration` is a built-in type, so `release.soak < 24h` type-checks without any parsing on your side. `soak` is how long the release has been running in staging.
- `input` lists the top-level names a policy can read: `release`, `service`, `actor` and `environment`. An input doesn't have to be a struct; `environment` is a plain string. Nothing else exists in a policy's scope unless the policy declares it.
- `fn split(...)` is a host function. Its implementation is Go's `strings.Split`; the kind only carries the signature, so the type checker knows it takes two strings and returns a list.
- The three `decision` lines are the only outcomes a policy can produce. Each one takes a `reason` first, then named payload fields. `approve` has a `bake`, how long the rollout sits in canary before it's promoted, which defaults to one hour.
- `precedence deny > review > approve` says who wins when several rules fire. A single deny beats any number of approvals.
- `default` is the answer when no rule fires at all. This kind fails closed.

## The base policy

The platform team owns `deploy/production.sigil`:

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

### The header and params

`policy deploy.production: DeployApproval` names the policy and the kind it implements. The dotted name maps to a path: `deploy.production` lives at `deploy/production.sigil`.

The three `param` lines are the knobs a team can turn. `min_soak` and `tiers` have defaults. `approvers` doesn't, so whoever instantiates this policy has to supply it, or compilation fails.

### The lets

A `let` names an expression so rules can refer to it. Lets live at the top level only, and they're evaluated against the same input as everything else.

- `owns_service` uses `any in`, the intersection operator: it's true when the actor is on at least one of the teams that own the service.
- `cleared` splits a comma-separated label into a list and checks that every element appears in the actor's regions, so the actor has to be cleared for every region the service runs in. `all in` is the subset operator. Note the index syntax `service.labels["regions"]`: identifiers can't contain `.` or `/`, so label keys always go through `[...]`.
- `eligible` chains three conditions with `and`. The middle one compares the `environment` input directly; string comparison is case-sensitive, so `"Production"` wouldn't match. The last one uses `has` with a map literal, which is true when the labels contain every listed key with exactly that value. The trailing comma after the last pair is legal, as it is in every list, map and argument list.

### The rules

A `when` block fires when its condition holds. Every `when` is evaluated, independently, and the order they appear in the file doesn't matter.

The first two rules deny. The third is a container: its nested rules only fire if `cleared` holds too, because nesting means "and". Inside it, a release manager shipping a critical service gets an approval with the kind's default bake, and a service owner shipping a standard or internal service gets sent to review.

`review("service_owner", approvers: approvers)` is a decision constructor. It doesn't return or stop anything; it adds a candidate to the pile. The first argument is the reason, always a string literal, so you can grep for it and count it in metrics. Everything after it is a named payload field from the kind. `approve("release_manager")` passes no payload at all, so `bake` takes its default.

There's no `else`. If you want "the other case", you write `when not x`, which says the same thing without implying an order.

## The team instantiation

The payments team ships several times a day, so they want a shorter soak, their own approvers, and a fast path for their SRE team. They write `payments/production.sigil`:

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

`use` pulls in every rule of `deploy.production` with its params bound. The team lowered `min_soak` to four hours and supplied the required `approvers`. `tiers` keeps its default.

The team's own rule can only add a candidate. It can't remove the base policy's denies, and because deny outranks approve, it can't beat them either.

## Evaluating it by hand

Evaluation has two steps. First, collect every decision constructor whose enclosing `when` conditions all hold; those are the candidates. Then pick the candidate whose decision ranks highest in `precedence`. If there are no candidates, the kind's `default` applies.

The inputs below are all evaluated against `payments.production`. Each one starts from the same eligible service, deployed to production:

```json
{
  "environment": "production",
  "service": {
    "name": "ledger",
    "tier": "standard",
    "owners": ["payments"],
    "labels": {
      "app.kubernetes.io/managed-by": "argocd",
      "platform.example.com/lifecycle": "ga",
      "regions": "eu-1"
    }
  }
}
```

The actors below all hold the `deployer` role and are cleared for `["eu-1", "eu-2"]`. So `eligible` is true, and `cleared` is true because `["eu-1"]` is a subset of `["eu-1", "eu-2"]`.

::: info Illustrative output
The result blocks below use the planned `sigil eval` output format. Nothing is implemented yet, so treat them as a picture of what the evaluator will return, not as a transcript.
:::

### A service owner ships after six hours of soak

```json
{
  "release": { "soak": "6h", "hotfix": false },
  "actor": {
    "name": "ana",
    "teams": ["payments", "payments-sre"],
    "roles": ["deployer"],
    "regions": ["eu-1", "eu-2"]
  }
}
```

Six hours is over the team's four-hour `min_soak`, so `soak_too_short` doesn't fire. The service tier is `standard`, which is in the default `tiers`, and the actor is on `payments`, which owns the service, so the base policy produces a review. The actor is also on `payments-sre`, so the team rule produces an approval.

```text
decision  review
reason    service_owner
policy    payments.production
payload   approvers = ["payments-leads"]

candidates
  review   service_owner     deploy/production.sigil:34:5
  approve  payments_sre      payments/production.sigil:9:3
```

Review wins because it ranks above approve. The payments team's fast path doesn't fire over the base policy's review; it only helps when the base policy stays silent. The `approvers` payload comes from the param the team bound.

### The same release after two hours

Change `"soak"` to `"2h"` and a third candidate appears:

```text
decision  deny
reason    soak_too_short
policy    payments.production
payload   (none)

candidates
  deny     soak_too_short    deploy/production.sigil:23:3
  review   service_owner     deploy/production.sigil:34:5
  approve  payments_sre      payments/production.sigil:9:3
```

The team's approval is still a candidate, and it still loses. This is the guarantee composition gives you: a deny from the base policy survives whatever a team adds on top. (The team did move the threshold, by binding `min_soak` to four hours; params aren't covered by that guarantee. [Per-team policies](/guides/team-policies/) has more on that.)

### Nothing fires

Now the actor is a developer from another team, outside the service's owners and outside `payments-sre`:

```json
{
  "release": { "soak": "30h", "hotfix": false },
  "actor": {
    "name": "ben",
    "teams": ["checkout"],
    "roles": ["deployer"],
    "regions": ["eu-1", "eu-2"]
  }
}
```

The service is still eligible and the soak is long enough, so neither deny fires. `cleared` is true, but the tier isn't `critical` and the actor doesn't own the service, so nothing inside the third rule fires. The team rule needs `payments-sre`, which the actor isn't on.

```text
decision  deny
reason    no_rule_matched
policy    payments.production
payload   (none)

candidates
  (none)
```

With no candidates, the kind's `default` applies. Note the difference from the previous case: the default isn't protected the way an explicit deny is. Had the actor been on `payments-sre`, the team's approval would have been the only candidate and would have won. That's on purpose; it's how teams add approvals of their own.

::: details When two approvals tie
Suppose the service is `critical`, and the actor is a release manager who is also on `payments-sre`. The review rule doesn't apply, because `critical` isn't in `tiers`, but two approvals fire:

```text
decision  approve
reason    release_manager
policy    payments.production
payload   bake = 1h

candidates
  approve  release_manager   deploy/production.sigil:29:5
  approve  payments_sre      payments/production.sigil:9:3
```

Precedence can't separate two candidates of the same decision, so for now the earliest source position wins, and rules from a `use`d policy count as earlier than the file's own rules. The release manager's approval wins with a bake of one hour (the kind's default), even though the team's own rule asked for 15 minutes. Here the tie happens to land on the more cautious payload, but that's luck: the rule looks at position, not at what's safer. Whether ties should instead merge, for example by taking the longest `bake`, is an [open question](/project/open-questions/).
:::

## What to read next

- [Your first policy](/getting-started/first-policy/) builds `deploy/production.sigil` one rule at a time, with the compiler errors you'd hit along the way.
- [Why rule order never matters](/understanding/order-independence/) explains the evaluation model you just used by hand.
- [Per-team policies](/guides/team-policies/) goes further with `use`, aliases and binding params from Go.
