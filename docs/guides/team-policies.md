---
title: Per-team policies
icon: mdi:account-group
createTime: 2026/09/24 22:30:00
permalink: /guides/team-policies/
---

By the end of this guide, the platform's shared policies take typed params, each team gets its own version by invoking them or by binding their params from Go, and the host makes sure no team can switch the guardrails off. Nothing is copied or run through a text templater.

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
policy deploy.production: DeployApproval@1

use deploy.common.{cleared, owns_service}

param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]
```

`approvers` has no default, because a deploy gate with no reviewers makes no sense. `deploy.production` can't be evaluated until someone binds it, and a policy that invokes it without `approvers` fails to compile instead of surprising anyone at run time.

Anything you don't make a param is fixed for every team. In the platform's files, the eligibility labels and the rules themselves are fixed.

Bound every param a team could use to switch a guardrail off. Without a bound, a team can invoke `guardrails(min_soak: 0s)`, and the `soak_too_short` rule dutifully compares against zero. Declare the lowest value you accept with [`min`](/reference/policy-files/#bounds), and that invocation fails to compile:

```sigil
param min_soak: duration = 24h, min: 1h
```

Lists such as `approvers` can't be bounded, so review team policies that change them, or [bind them from Go](#bind-params-from-go-instead) where the platform controls the values.

## Compose with invocations

A team that wants to tune params, add rules, or both, writes its own policy file. It imports the platform's policies with `use` and invokes them:

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
  approve(payments_sre, bake: 15m)
}
```

Each invocation adds the invoked policy's rules with its params bound by named, type-checked arguments, and an invocation inside `when` adds the condition to every rule it brings in, which is how the two `production(...)` calls above give PCI-scoped services a second approver group. [Policy invocation](/reference/policy-files/#policy-invocation) has the full rules.

To check what a composition adds up to, run [`sigil explain`](/reference/cli/#sigil-explain) on the team file. It prints every rule the policy can fire, with each call's conditions pushed into the rule and each param replaced by its bound value.

## Require the guardrails from the host

An invocation inside `when` only contributes while the condition holds, and that includes the guardrails. A team policy that wrapped `guardrails(...)` in a `when` would switch off its denies whenever the condition is false. The host rules that out by naming the policies every root policy must invoke unconditionally:

```go
p, err := Deploy.Load(policies, "payments.production",
	policy.Require("deploy.guardrails"))
```

The compiler checks that `deploy.guardrails` is reachable from the root through top-level invocations only. A team that gates the guardrails to skip them for PCI services gets:

```text
payments/production.sigil:7:3: error: deploy.guardrails must be invoked unconditionally
  |
7 |   guardrails(min_soak: 4h)
  |   ^^^^^^^^^^^^^^^^^^^^^^^^
  = help: the host requires deploy.guardrails for every DeployApproval policy; move the call to the top level
```

A team policy that doesn't invoke the guardrails at all fails too, with `payments.production doesn't invoke deploy.guardrails`. In CI, `sigil check --require deploy.guardrails` runs the same check; see [Check policies in CI](/guides/ci/#require-the-guardrails). Put the `Require` wherever the host loads team policies, so no team can forget it.

When teams can write to the bundle, also pass `policy.From` with a source only the platform controls, since `Require` on its own only checks a name ([why](/understanding/bundles/#why-required-policies-need-a-trusted-source)):

```go
p, err := Deploy.Load(teamFS, "payments.production",
	policy.Require("deploy.guardrails", policy.From(platformFS)))
```

[Policies in a ConfigMap](/guides/configmaps/#load-it-in-the-service) shows the full setup, and [Trusted sources](/reference/bundles/#trusted-sources) the rules.

## Bind params from Go instead

If a team only needs different param values and no rules of its own, it doesn't need a policy file at all. The platform writes one policy that composes the guardrails and the approvals and passes its own params through:

```sigil
policy deploy.gate: DeployApproval@1

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
  approve(payments_sre, bake: 15m)
}
```

The team's approval now only fires when the actor is cleared for every region the service runs in and is on one of its owning teams, and nobody copied the region-matching logic. Other forms work too:

```sigil
use deploy.common                         // qualified: common.cleared
use deploy.common.{owns_service as owner} // renamed: owner
```

Only `pub let`s can be imported, and an imported name can't collide with an input or any other name; see [`use`](/reference/policy-files/#use), [Exporting lets](/reference/policy-files/#exporting-lets) and [Identifiers](/reference/policy-files/#identifiers).

## Invoke the same policy twice

A policy can invoke the same policy more than once with different arguments, for example once per region. Take a policy that gates deploys touching one region. Its `regional_deploy` reason isn't in the tour's kind, so the kind would need it added to `decision review`:

```sigil
policy deploy.regional: DeployApproval@1

param region: string
param approvers: list<string>

let in_scope = region in split(service.labels["regions"], ",")

when in_scope and "deployer" in actor.roles {
  review(regional_deploy, approvers: approvers)
}
```

A team invokes it for two regions:

```sigil
policy payments.regions: DeployApproval@1

use deploy.guardrails
use deploy.regional

guardrails(min_soak: 4h)

regional(region: "eu-1", approvers: ["payments-leads"])

regional(region: "us-1", approvers: ["payments-leads", "us-platform"])
```

Each call is a separate instantiation with its own params. A service that only runs in `us-1` only matches the second call's rule, so it gets both approver groups. The trace records the call chain for every candidate, so a review from the `us-1` call reads `payments/regions.sigil:10:1 → deploy/regional.sigil:9:3`, and it's clear which call produced it.

::: warning Composition is a union
Every rule of every invocation runs against every input, unless a `when` around the call says otherwise. If `deploy.regional` had an unscoped rule such as `when not in_scope { deny(out_of_region) }`, the `eu-1` call would deny every service that runs only in `us-1`, and the other way round. A policy meant to be invoked more than once should guard each rule with its scoping condition, as `in_scope` does above, or the caller should gate each call.
:::

## Know what a team can and can't change

Composition adds candidates and never removes them, so with the guardrails required and their params [bounded](#decide-what-teams-may-tune), a team can make the result stricter or approve what the platform leaves open, but never override a guardrail's deny. [Composition without templating](/understanding/composition/) lays out exactly what a team can and can't change, and why; the [tour](/getting-started/tour/#the-same-release-after-two-hours) shows a team approval losing to a guardrail deny.

## Further reading

- [Composition without templating](/understanding/composition/) explains why `use` only imports and what `Require` guarantees.
- [Policy files](/reference/policy-files/) is the precise definition of `use`, `param`, invocation and modules.
- [Evaluation semantics](/reference/evaluation/) covers how invocations flatten and how ties between candidates of the same decision resolve.
- [Test your policies](/guides/test-policies/) pins what each team policy decides, and [Check policies in CI](/guides/ci/) runs the checks on every pull request.
