---
title: Prior art
icon: mdi:bookshelf
createTime: 2026/09/24 22:30:00
permalink: /understanding/prior-art/
---

Sigil borrows heavily. Its closest relative is Cedar, for schema validation and deny-overrides semantics, while the expression layer takes most of its shape from filt-rs. If you already use one of these tools, this page shows where Sigil will feel familiar and where it won't. The table sums it up; the sections below say why each choice went the way it did.

| Project                                                      | What Sigil takes                                                                                                         | What Sigil avoids                                                                           |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------- |
| [filt-rs](https://github.com/SierraSoftworks/filters)        | Readable operators such as `in`, `like` and `matches`, duration literals, parse once / eval many, errors with a fix hint | Unknown properties resolving to `null`. Fine for filters, but it makes deny rules fail open |
| [Cedar](https://www.cedarpolicy.com/)                        | Schema-checked policies, forbid overrides permit, policy templates with slots                                            | Its principal/action/resource model is too narrow for arbitrary host inputs                 |
| [CEL](https://github.com/google/cel-go)                      | Non-Turing-complete by construction, host-declared variables and functions                                               | It's an expression language only, with no notion of rules, decisions or composition         |
| [Rego](https://www.openpolicyagent.org/docs/policy-language) | Nothing syntactic. The lesson is the learning curve                                                                      | Datalog semantics, implicit iteration, partial rule sets that engineers struggle to read    |
| HCL / YAML DSLs                                              | Declarative feel                                                                                                         | Block nesting that fights templating, anchors as a reuse mechanism, stringly-typed matchers |

## filt-rs

[filt-rs](https://github.com/SierraSoftworks/filters) is a small Rust filter language, and much of Sigil's expression syntax comes from it. Operators like `in`, `like` and `matches` read naturally to people who've never seen the language, and duration literals such as `30m` or `1h30m` remove a whole class of unit bugs. filt-rs parses a filter once and evaluates it against any number of objects, which is the model Sigil's compiled policies follow.

filt-rs also gets error messages right: where the problem is, what went wrong and a concrete fix. Sigil's diagnostics follow the same idea, with a file, line and column, the offending source underlined, and a `help:` line.

Sigil departs from filt-rs in how the host supplies data. A filt-rs host implements a single-method `Filterable` trait that looks properties up by name at evaluation time. A Sigil host hands over its own Go structs, and the kind records every field path once, when the host defines the kind, so the compiler can check each field a policy reads.

The part Sigil deliberately doesn't copy is unknown properties evaluating to `null`. For a filter, that's a reasonable choice, because a typo just means the filter matches nothing and you notice. In a policy it's dangerous. A deny rule with a typo in its condition never fires, and the request sails through to whatever lower-precedence rule approves it. [Strict schema, forgiving data](/understanding/strictness/) is the long version of that argument.

Sigil also differs on case sensitivity. filt-rs string operators fold case by default and offer `_cs` variants; Sigil's string comparison is always case-sensitive, because Kubernetes labels and most identifiers in this domain are. [Why the operators refuse to guess](/understanding/language-choices/#why-the-operators-refuse-to-guess) covers that and the other operator choices.

## Cedar

[Cedar](https://www.cedarpolicy.com/), from AWS, is the closest thing to Sigil in spirit. Policies validate against a schema before they run, `forbid` always beats `permit`, and policy templates with slots let you stamp out per-tenant variants without copying text.

All three ideas show up in Sigil in a generalized form. The schema becomes the [_kind_](/understanding/kinds/). Forbid-overrides-permit becomes a `precedence` declaration, so a host can define `deny > review > approve` or any other order its decisions need. Templates with slots become typed `param`s bound by invoking a policy.

What Sigil can't take is Cedar's fixed data model. Every Cedar request is a principal, an action, a resource and a context. That fits access control well and fits "should this hotfix ship to production before it finished soaking in staging" badly. Sigil lets the host define arbitrary typed inputs instead.

## CEL

[CEL](https://github.com/google/cel-go) influenced Sigil's finite expression language and host-declared inputs and functions. CEL can also estimate an expression's cost before running it. Sigil doesn't, and [Why there's no cost budget](/understanding/halting/#why-there-s-no-cost-budget) says why.

CEL stops at expressions, though. It has no rules, no decisions, no notion of combining several conditions into an outcome and no composition story. Every project that embeds CEL for policies ends up building those pieces around it, which is the same rebuild-it-again problem Sigil exists to stop.

## Rego

[Rego](https://www.openpolicyagent.org/docs/policy-language) contributes nothing syntactic. The lesson it teaches is about learning curves.

Rego is powerful and its Datalog roots make some things elegant, but engineers who don't write it daily struggle with implicit iteration (`some x; input.roles[x] == "admin"`), partial rule sets that merge across files, and the fact that a reference to a missing value makes the whole rule body undefined, so the rule silently doesn't apply. Sigil's quantifiers are explicit (`any r in actor.roles: r like "sre-*"`), its rules are independent blocks, and its schema checking turns most "undefined" cases into compile errors.

## HCL and YAML DSLs

The declarative feel is worth keeping: a policy should describe conditions and outcomes, not a procedure. What goes wrong with YAML-based rule engines is everything around that feel. Deeply nested blocks fight text templating, because indentation becomes load-bearing. Anchors and aliases end up as the reuse mechanism, which nobody enjoys debugging. Matchers are strings interpreted at runtime, so `tier: "critical"` and `teir: "critical"` are both valid YAML.

Sigil keeps statements keyword-led and whitespace-insensitive so templating can't break them, replaces anchors with modules, imports and `let`, and types every matcher against the kind.

### The same deploy gate, both ways

Here is a deploy gate written against the [`DeployApproval` kind](/reference/kind-files/#a-complete-kind) twice: once in Sigil, and once for the kind of YAML rule engine teams tend to build in-house. The YAML engine is made up but typical: rules run top to bottom, the first match wins, matchers are field paths with an operator, and per-team variants come from rendering the file with `text/template`. The `[1]` to `[4]` markers in the YAML are explained at the end of its tab.

::: tabs

@tab Sigil

The platform team's shared matchers:

```sigil title="deploy/common.sigil"
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

The guardrails, which the host requires every team policy to invoke:

```sigil title="deploy/guardrails.sigil"
policy deploy.guardrails: DeployApproval@1

use deploy.common.{eligible}

param min_soak: duration = 24h

when not eligible {
  deny(reason: not_eligible)
}

when release.soak < min_soak and not release.hotfix {
  deny(reason: soak_too_short)
}
```

The approvals and reviews:

```sigil title="deploy/production.sigil"
policy deploy.production: DeployApproval@1

use deploy.common.{cleared, owns_service}

param approvers: list<string>
param tiers: list<Tier> = [standard, internal]

when cleared {
  when service.tier == critical
    and "release_manager" in actor.roles {
    approve(reason: release_manager)
  }

  when service.tier in tiers
    and owns_service {
    review(reason: service_owner, approvers: approvers)
  }
}
```

The payments team's policy, which invokes both with its own values:

```sigil title="payments/production.sigil"
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
  approve(reason: payments_sre, bake: 15m)
}
```

@tab YAML rule engine

The platform team's rules, as a template:

```yaml title="deploy-gate.yaml.tmpl"
# Rendered once per team with text/template, then loaded by the engine.
# Rules run top to bottom and the first match wins.            [1]
default:
  decision: deny
  reason: no_rule_matched

rules:
  - name: not-eligible
    decision: deny
    reason: not_eligible
    match:
      any:
        - { field: actor.roles, op: notContains, value: deployer }
        - { field: environment, op: notEquals, value: production }
        - field: service.labels
          op: notMatchLabels
          value:
            app.kubernetes.io/managed-by: argocd
            platform.example.com/lifecycle: ga

  - name: soak-too-short
    decision: deny
    reason: soak_too_short
    match:
      all:
        - { field: release.soak, op: lessThan, value: "{{ .MinSoak }}" }  # [2]
        - { field: release.hotfix, op: equals, value: false }

  # Team rules go here: below the denies, above everything else.   [1]
{{- range .ExtraRules }}
{{ list . | toYaml | indent 2 }}                                   # [3]
{{- end }}

  - name: release-manager
    decision: approve
    reason: release_manager
    payload: { bake: 1h }
    match:
      all:
        - field: service.labels.regions                               # [4]
          op: splitSubsetOf
          separator: ","
          valueFrom: actor.regions
        - { field: service.tier, op: equals, value: critical }
        - { field: actor.roles, op: contains, value: release_manager }

  - name: service-owner
    decision: review
    reason: service_owner
    payload:
      approvers: {{ .Approvers | toJson }}
    match:
      all:
        - field: service.labels.regions                               # [4]
          op: splitSubsetOf
          separator: ","
          valueFrom: actor.regions
        - field: service.tier
          op: in
          value: {{ .Tiers | default (list "standard" "internal") | toJson }}
        - { field: actor.teams, op: intersects, valueFrom: service.owners }
{{- if .PCIApprovers }}
        - { field: service.labels.compliance, op: notEquals, value: pci }

  - name: service-owner-pci                                           # [4]
    decision: review
    reason: service_owner
    payload:
      approvers: {{ .PCIApprovers | toJson }}
    match:
      all:
        - field: service.labels.regions
          op: splitSubsetOf
          separator: ","
          valueFrom: actor.regions
        - { field: service.labels.compliance, op: equals, value: pci }
        - field: service.tier
          op: in
          value: {{ .Tiers | default (list "standard" "internal") | toJson }}
        - { field: actor.teams, op: intersects, valueFrom: service.owners }
{{- end }}
```

The payments team's values file, which fills in the template:

```yaml title="teams/payments.values.yaml"
MinSoak: 4h
Approvers: [payments-leads]
PCIApprovers: [payments-leads, security-leads]
ExtraRules:
  - name: payments-sre
    decision: approve
    reason: payments_sre
    payload: { bake: 15m }
    match:
      all:
        - field: service.labels.regions # [4]
          op: splitSubsetOf
          separator: ","
          valueFrom: actor.regions
        - { field: actor.teams, op: contains, value: payments-sre }
```

What the `[1]` to `[4]` markers point at:

1. **Order decides the outcome.** The team's approve has to sit below the denies, or it overrides them. Where it lands relative to `service-owner` changes behavior, too: a service owner who's also in `payments-sre` gets approved here, because the team rule matches first. In Sigil every rule runs, `deny > review > approve` picks the winner, and that owner gets a review.
2. **Nothing is typed.** `"4h"` stays a string until the engine parses it at evaluation time, and a misspelled path such as `service.teir` resolves to nothing, so its rule quietly stops matching. So does a misspelled value such as `critcal`, which no service ever has. Sigil checks all three against the kind at compile time: `min_soak: 4h` is a `duration`, and `service.teir` and `critcal` are [errors with a fix hint](/understanding/strictness/).
3. **Templating is text.** The team's rules get spliced in as text, so a wrong `indent` produces a different YAML file instead of an error, and every team's values file has to know the template's internals. Sigil's invocations bind [typed params](/understanding/composition/), a team can only add candidates, and the guardrails the host requires can't be gated off.
4. **Nothing is named or shared.** The regions check is pasted into every rule that needs it, even into the team's values file, and the engine grew a one-off `splitSubsetOf` operator to express it. Giving PCI services other approvers means a second copy of the whole `service-owner` rule behind an `if`. Sigil names the check once as `let cleared` in a module, builds it from `split` and the general `all in`, and adds a condition to a shared policy by invoking it inside a `when`.

:::
