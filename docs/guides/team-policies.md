---
title: Per-team policies
icon: mdi:account-group
createTime: 2026/09/24 22:30:00
permalink: /guides/team-policies/
---

This guide shows how to give each team its own version of a policy without copying it and without running it through a text templater. The platform writes shared policies with typed params, each team composes them by invoking them from its own policy or gets their params bound from Go, and the host makes sure the guardrails can't be switched off.

The examples build on the `DeployApproval` kind and the `deploy.*` files from the [tour](/getting-started/tour/).

## Split shared files by what they do

Put each kind of reuse in its own file, so that a team can tell from an import line what it's getting:

| File | Holds | Why separate |
| --- | --- | --- |
| `deploy/common.sigil`, a module | Shared `let`s only | Importing it can never change a decision |
| `deploy/guardrails.sigil` | Denies only | The host requires it, so its denies always apply |
| `deploy/production.sigil` | Approvals and reviews | Teams gate and tune it freely |

Keeping denies out of the approvals policy matters. A team is allowed to invoke `deploy.production` under any condition it likes, or not at all. If that policy held a deny too, gating it would switch the deny off, and the `gated-deny` lint would flag every team that does.

## Decide what teams may tune

Turn every value a team might reasonably want to change into a `param`. Give it a default when there's a sensible one; leave the default off when every team has to decide for itself.

```sigil
policy deploy.production: DeployApproval

use deploy.common.{cleared, owns_service}

param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]
```

`approvers` has no default, so `deploy.production` can't be evaluated until someone binds it. That's deliberate: a deploy gate with no reviewers makes no sense, and a missing required param is a compile error rather than a runtime surprise.

Anything you don't make a param is fixed for every team. In the platform's files, the eligibility labels and the rules themselves are fixed.

## Compose with invocations

A team that wants to tune params, add rules, or both, writes its own policy file. It imports the platform's policies with `use` and invokes them:

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

The rules:

- `use` only imports. `use deploy.production` binds the name `production`; nothing happens until the policy calls it.
- An invocation adds every rule of the invoked policy, with its params bound. Params you don't mention keep their defaults; required params you don't mention are a compile error.
- Arguments are named, and each one is type-checked against the param's declared type. `min_soak: "4h"` fails because a string isn't a duration. Arguments may use constants and the team policy's own params, but not inputs, so every invocation is a fixed instantiation.
- An invocation inside `when` blocks adds their conditions to every rule it brings in. The two `production(...)` calls above give PCI-scoped services a second approver group.
- The invoked policy must implement the same kind. Invoking a policy written for some other kind, say one for access requests, inside a `DeployApproval` policy is a compile error.
- `deploy.production` resolves to `deploy/production.sigil` in the file system the host passes to `Load`, so all the files need to live in the same policy tree.

To check what a composition adds up to, run [`sigil explain`](/reference/cli/#sigil-explain) on the team file. It prints every rule the policy can fire, with each call's conditions pushed into the rule and each param replaced by its bound value.

## Require the guardrails from the host

An invocation inside `when` only contributes while the condition holds, and that includes the guardrails. A team policy that wrapped `guardrails(...)` in a `when` would switch off its denies whenever the condition is false. The host rules that out by naming the policies every root policy must invoke unconditionally:

```go
p, err := Deploy.Load(policies, "payments.production",
	policy.Require("deploy.guardrails"))
```

The compiler checks that `deploy.guardrails` is reachable from the root through top-level invocations only. Here's what a team sees if it tries to skip the guardrails for PCI services:

```text
payments/production.sigil:10:3: error: deploy.guardrails must be invoked unconditionally
   |
 9 | when service.labels["compliance"] != "pci" {
10 |   guardrails(min_soak: 4h)
   |   ^^^^^^^^^^^^^^^^^^^^^^^^
   = help: the host requires deploy.guardrails for every DeployApproval policy.
           Move the call to the top level.
```

A team policy that doesn't invoke the guardrails at all fails the same way. Put the `Require` wherever the host loads team policies, so no team can forget it.

::: info Planned API
The Go API is planned, not implemented. See the [Go API reference](/reference/go-api/) for the full sketch.
:::

## Bind params from Go instead

If a team only needs different param values and no rules of its own, it doesn't need a policy file at all. The platform writes one policy that composes the guardrails and the approvals and passes its own params through:

```sigil
policy deploy.gate: DeployApproval

use deploy.guardrails
use deploy.production

param min_soak: duration = 24h
param approvers: list<string>

guardrails(min_soak: min_soak)
production(approvers: approvers)
```

The host then binds the params when it loads that policy, for example straight from a Kubernetes custom resource:

```go
type DeployGateSpec struct {
	Approvers []string        `json:"approvers"`
	MinSoak   metav1.Duration `json:"minSoak"`
}

func compileFor(spec DeployGateSpec) (*policy.Policy[Input], error) {
	return Deploy.Load(policies, "deploy.gate",
		policy.Params{
			"approvers": spec.Approvers,
			"min_soak":  spec.MinSoak.Duration,
		},
		policy.Require("deploy.guardrails"),
	)
}
```

Params bound from Go go through the same type check as invocation arguments. Pass an `int` where the policy declares a `duration` and `Load` returns a compile error naming the param and both types. No text is generated at any point, so there's nothing to escape and nothing a malicious CR field can inject.

Pick a team policy file when the team wants to own rules or conditions. Pick Go bindings when the team's input is data (a list of approvers, a soak time) that already lives in a CRD or a config service.

## Reuse the platform's matchers

A team rule often needs the same conditions the platform already spelled out. Import them from the module:

```sigil
use deploy.common.{cleared, owns_service}

when cleared and owns_service
  and "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```

The team's approval now only fires when the actor is cleared for every region the service runs in and is on one of its owning teams, and nobody copied the region-matching logic. Other forms work too:

```sigil
use deploy.common                            // qualified: common.cleared
use deploy.common.{owns_service as owner}    // renamed: owner
```

Imported names live in the same top-level namespace as inputs, params and lets. Importing something as `release` or `actor` is a compile error, because it would collide with an input.

A policy's own `let`s can be imported the same way, as long as they don't read a param. A param has no value outside an invocation, so the compiler rejects the import and suggests moving the `let` to a module.

## Invoke the same policy twice

A policy can invoke the same policy more than once with different arguments, for example once per region. Take a policy that gates deploys touching one region:

```sigil
policy deploy.regional: DeployApproval

param region: string
param approvers: list<string>

let in_scope = region in split(service.labels["regions"], ",")

when in_scope and "deployer" in actor.roles {
  review("regional_deploy", approvers: approvers)
}
```

A team invokes it for two regions:

```sigil
policy payments.regions: DeployApproval

use deploy.guardrails
use deploy.regional

guardrails(min_soak: 4h)

regional(region: "eu-1", approvers: ["payments-leads"])
regional(region: "us-1", approvers: ["payments-leads", "us-platform"])
```

Each call is a separate instantiation with its own params. A service that only runs in `us-1` only matches the second call's rule, so it gets both approver groups. The trace records the call chain for every candidate, so a review from the `us-1` call reads `payments/regions.sigil:9:1 → deploy/regional.sigil:9:3`, and it's clear which call produced it.

::: warning Composition is a union
Every rule of every invocation runs against every input, unless a `when` around the call says otherwise. If `deploy.regional` had an unscoped rule such as `when not in_scope { deny("out_of_region") }`, the `eu-1` call would deny every service that runs only in `us-1`, and the other way round. A policy meant to be invoked more than once should guard each rule with its scoping condition, as `in_scope` does above, or the caller should gate each call.
:::

## Know what a team can and can't change

Composition adds candidates and never removes them. Combined with the kind's `precedence deny > review > approve` and the host's `Require`, that gives you a clear line:

| A team can | A team can't |
| --- | --- |
| Add approvals that win when the platform's policies say nothing, turning the kind's default deny into an approve | Override a deny from a required policy |
| Add reviews and denies, making the result stricter | Gate a required policy behind `when`, or leave it out |
| Gate, repeat or skip policies the host doesn't require | Turn a review into an approve (review outranks approve) |
| Change any param, including loosening ones like `min_soak` | Remove or edit a rule of an invoked policy |
| Import shared matchers from modules | Import a `let` that reads a param |

The first row is the point of the whole mechanism: the guardrails' `not_eligible` and `soak_too_short` denies hold no matter what a team adds. The [tour](/getting-started/tour/#the-same-release-after-two-hours) shows a team approval losing to a guardrail deny.

The fourth row is the gap. A team can invoke `guardrails(min_soak: 0s)`, and the `soak_too_short` rule dutifully compares against zero, which switches the soak requirement off. Constraining params, either with bounds in the declaration like `param min_soak: duration = 24h min 1h` or with bounds the host sets alongside `Require`, is an [open question](/project/open-questions/). Until that's settled, review team policies that change safety-relevant params, or bind those params from Go where the platform controls the values.

## Further reading

- [Composition without templating](/understanding/composition/) explains why `use` only imports and what `Require` guarantees.
- [Policy files](/reference/policy-files/) is the precise definition of `use`, `param`, invocation and modules.
- [Evaluation semantics](/reference/evaluation/) covers how invocations flatten and how ties between candidates of the same decision resolve.
