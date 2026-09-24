---
title: Per-team policies
icon: mdi:account-group
createTime: 2026/09/24 22:30:00
permalink: /guides/team-policies/
---

This guide shows how to give each team its own version of a policy without copying it and without running it through a text templater. You write one base policy with typed params, and each team either instantiates it with `use` or gets its params bound from Go.

The examples build on the `DeployApproval` kind and the `deploy.production` base policy from the [tour](/getting-started/tour/).

## Decide what teams may tune

Start from the base policy and turn every value a team might reasonably want to change into a `param`. Give it a default when there's a sensible one; leave the default off when every team has to decide for itself.

```sigil
policy deploy.production: DeployApproval

param min_soak: duration = 24h
param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]
```

`approvers` has no default, so the base policy can't be evaluated until someone binds it. That's deliberate: a deploy gate with no reviewers makes no sense, and a missing required param is a compile error rather than a runtime surprise.

Anything you don't make a param is fixed for every team. In `deploy.production`, the eligibility labels and the rules themselves are fixed.

## Instantiate the base with `use`

A team that wants to tune params, add rules, or both, writes its own policy file and pulls the base in with `use`:

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

The rules:

- `use` includes every rule of the named policy, with its params bound. Params you don't mention keep their defaults; required params you don't mention are a compile error.
- Arguments are named, and each one is type-checked against the param's declared type. `min_soak: "4h"` fails because a string isn't a duration.
- The used policy must implement the same kind. Using a policy written for some other kind, say one for access requests, inside a `DeployApproval` policy is a compile error.
- `deploy.production` resolves to `deploy/production.sigil` in the file system the host passes to `Load`, so both files need to live in the same policy tree.

## Bind params from Go instead

If a team only needs different param values and no rules of its own, it doesn't need a policy file at all. The host can bind params when it loads the base policy, for example straight from a Kubernetes custom resource:

```go
type DeployGateSpec struct {
	Approvers []string        `json:"approvers"`
	MinSoak   metav1.Duration `json:"minSoak"`
}

func compileFor(spec DeployGateSpec) (*policy.Policy[Input], error) {
	return Deploy.Load(policies, "deploy.production", policy.Params{
		"approvers": spec.Approvers,
		"min_soak":  spec.MinSoak.Duration,
	})
}
```

::: info Planned API
The Go API is planned, not implemented. See the [Go API reference](/reference/go-api/) for the full sketch.
:::

Params bound from Go go through the same type check as params bound by `use`. Pass an `int` where the policy declares a `duration` and `Load` returns a compile error naming the param and both types. No text is generated at any point, so there's nothing to escape and nothing a malicious CR field can inject.

Pick `use` when the team wants to own rules. Pick Go bindings when the team's input is data (a list of approvers, a soak time) that already lives in a CRD or a config service.

## Reuse the base policy's matchers

A team rule often needs the same conditions the base policy already spelled out. Give the `use` an alias and refer to its lets through it:

```sigil
policy payments.production: DeployApproval

use deploy.production(
  min_soak: 4h,
  approvers: ["payments-leads"],
) as base

when base.cleared and base.owns_service
  and "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```

`base.cleared` and `base.owns_service` are the base policy's `let`s, evaluated with the params this `use` bound. The team's approval now only fires when the actor is cleared for every region the service runs in and is on one of its owning teams, and nobody copied the region-matching logic.

The alias lives in the same top-level namespace as inputs, params and lets. Calling it `release` or `actor` is a compile error, because it would collide with an input.

## Use the same base twice

A policy can `use` the same base more than once under different aliases, for example once per region with different params. This only works well if the base scopes its rules by param. Take a base that gates deploys touching one region:

```sigil
policy deploy.regional: DeployApproval

param region: string
param approvers: list<string>
param min_soak: duration = 24h

let in_scope = region in split(service.labels["regions"], ",")

when in_scope and release.soak < min_soak {
  deny("soak_too_short")
}

when in_scope and "deployer" in actor.roles {
  review("regional_deploy", approvers: approvers)
}
```

A team instantiates it for two regions:

```sigil
policy payments.regions: DeployApproval

use deploy.regional(
  region: "eu-1",
  approvers: ["payments-leads"],
  min_soak: 4h,
) as eu

use deploy.regional(
  region: "us-1",
  approvers: ["payments-leads", "us-platform"],
) as us
```

A service that only runs in `us-1` only matches the `us` instance's rules, so it gets the 24-hour soak and both approver groups.

::: warning Composition is a union, including denies
Every rule of every `use`d instance runs against every input. If `deploy.regional` had an unscoped rule such as `when not in_scope { deny("out_of_region") }`, the `eu` instance would deny every service that runs only in `us-1`, and the `us` instance would deny every service that runs only in `eu-1`. A base policy meant to be used more than once should guard each rule with its scoping condition, as `in_scope` does above.
:::

Both instances produce candidates with the same reason from the same source position. How the trace tells them apart, for example by alias, isn't specified yet.

## Know what a team can and can't change

Composition adds candidates and never removes them. Combined with the kind's `precedence deny > review > approve`, that gives you a clear line:

| A team can | A team can't |
| --- | --- |
| Add approvals that win when the base policy says nothing, turning the kind's default deny into an approve | Override a deny the base policy makes explicitly |
| Add reviews and denies, making the result stricter | Turn a base review into an approve (review outranks approve) |
| Change any param, including loosening ones like `min_soak` | Remove or edit a base rule |
| Reuse base lets through an alias | See base lets without an alias |

The first row is the point of the whole mechanism: the base policy's `not_eligible` and `soak_too_short` denies hold no matter what a team adds. The [tour](/getting-started/tour/#the-same-release-after-two-hours) shows a team approval losing to a base deny.

The third row is the gap. A team can bind `min_soak: 0s` and the base policy's `soak_too_short` rule dutifully compares against zero, which switches the soak requirement off. Constraining params, for example with bounds like `param min_soak: duration = 24h min 1h`, is an [open question](/project/open-questions/). Until that's settled, review team policies that change safety-relevant params, or bind those params from Go where the platform controls the values.

## Further reading

- [Composition without templating](/understanding/composition/) explains why composition is a union and what that guarantees.
- [Policy files](/reference/policy-files/) is the precise definition of `param` and `use`.
- [Evaluation semantics](/reference/evaluation/) covers how ties between candidates of the same decision resolve.
