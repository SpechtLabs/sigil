---
pageLayout: home
externalLinkIcon: false

config:
  - type: doc-hero
    hero:
      name: Sigil
      text: A small, typed policy language for Go hosts
      tagline: Write rules that turn host-provided input into a typed decision like approve, deny or review. Every decision carries a reason and a payload, every policy is type-checked against the host's contract, and every evaluation halts.
      actions:
        - text: Take the tour →
          link: /getting-started/tour/
          theme: brand
          icon: mdi:map-marker-path
        - text: Read the language reference
          link: /reference/policy-files/
          theme: alt
          icon: mdi:book-open-page-variant

  - type: features
    title: Why Sigil?
    description: Teams keep rebuilding YAML rule engines with label-selector matchers. Sigil replaces them with a language that's small enough to read on first contact and strict enough to trust with a deny rule.
    features:
      - title: Typed against a contract
        icon: mdi:file-certificate-outline
        details: The host defines a kind in Go (its inputs, functions and decisions). A typo like service.teir fails at compile time with a fix hint instead of silently switching a deny rule off.

      - title: Halts by construction
        icon: mdi:timer-sand-complete
        details: No loops, no recursion, no user-defined functions. Quantifiers range over finite input lists and regexes run on RE2, so the compiler can compute a worst-case cost and reject a policy before it ships.

      - title: Rule order never matters
        icon: mdi:sort-variant-off
        details: Every rule is evaluated, and the kind's precedence (deny over review over approve) picks the winner. Reordering a file never changes which decision wins.

      - title: Decisions explain themselves
        icon: mdi:message-text-outline
        details: Every decision takes a literal reason, so you can grep for it and count it in metrics. Payloads are named, typed fields the host reads back as Go structs.

      - title: Templating is a language feature
        icon: mdi:layers-triple
        details: Shared policies declare typed params, and team policies invoke them with their own values, optionally under conditions. No text/template, and the guardrails the host requires can't be switched off.

      - title: Parse once, evaluate many
        icon: mdi:lightning-bolt
        details: A compiled policy is immutable and safe for concurrent use, in the spirit of Go's regexp package. Hot reload is a pointer swap.

  - type: VPListCompare
    title: "YAML rule engine vs. Sigil"
    description: "The same approval policy, expressed two ways."
    left:
      title: "Hand-rolled YAML rules"
      description: "Stringly typed, checked at runtime, if at all"
      items:
        - title: "Typos fail open"
          description: "A misspelled field resolves to null, and the deny rule that used it quietly stops matching"
        - title: "Order-dependent"
          description: "First match wins, so moving a block changes who gets access"
        - title: "Templated with text"
          description: "Per-team variants come from text/template or YAML anchors, and whitespace breaks them"
        - title: "Opaque outcomes"
          description: "The result is allow or deny, without a reason you can alert on"

    right:
      title: "Sigil"
      description: "Typed, halting, and explainable"
      items:
        - title: "Typos fail to compile"
          description: "Every field, function and payload key is checked against the kind"
        - title: "Order-independent"
          description: "All rules run; precedence decides between the candidates"
        - title: "Typed params and invocation"
          description: "Teams invoke a shared policy with values, never with text substitution"
        - title: "Reason plus payload"
          description: "Every decision names why it happened and carries the data the host acts on"

  - type: custom

  - type: VPContributors
    repo: SpechtLabs/sigil
---

## A policy, start to finish

The host engineer defines a **kind** in Go and exports it. Here's a trimmed version of the kind used throughout these docs, which gates production deployments:

```sigil
kind DeployApproval version 1

input release: Release
input service: Service
input actor: Actor
input environment: string

decision deny(reason: string)
decision review(reason: string, approvers: list<string>)
decision approve(reason: string, bake: duration = 1h)

precedence deny > review > approve
default deny("no_rule_matched")
```

A platform team writes rules against it. Each `when` block that holds produces a candidate decision, and the highest-precedence candidate wins. Denies go in a guardrails policy:

```sigil
policy deploy.guardrails: DeployApproval

param min_soak: duration = 24h

when release.soak < min_soak and not release.hotfix {
  deny("soak_too_short")
}
```

Approvals and reviews go in another:

```sigil
policy deploy.production: DeployApproval

param approvers: list<string>

when actor.teams any in service.owners {
  review("service_owner", approvers: approvers)
}
```

A product team imports both with `use` and invokes them like decision constructors, with its own values. Inside a `when`, an invocation's rules only apply where the condition holds. The team adds its own approval, which can never override the `soak_too_short` deny:

```sigil
policy payments.production: DeployApproval

use deploy.guardrails
use deploy.production

guardrails(min_soak: 4h)

when service.labels["compliance"] == "pci" {
  production(approvers: ["payments-leads", "security-leads"])
}

when service.labels["compliance"] != "pci" {
  production(approvers: ["payments-leads"])
}

when "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```

The host loads it with `policy.Require("deploy.guardrails")`, so moving `guardrails(...)` inside a `when` fails the build. The [tour](/getting-started/tour/) walks through the full version of these files and evaluates them against real inputs.

## Side by side with a YAML rule engine

Here's the full deploy gate twice: once in Sigil, and once for the kind of YAML rule engine teams tend to build in-house. The YAML engine is made up but typical: rules run top to bottom, the first match wins, matchers are field paths with an operator, and per-team variants come from rendering the file with `text/template`. The `[1]` to `[4]` markers in the YAML are explained at the end of its tab.

::: tabs

@tab Sigil

The platform team's shared matchers:

```sigil title="deploy/common.sigil"
module deploy.common: DeployApproval

let owns_service = actor.teams any in service.owners
let cleared = split(service.labels["regions"], ",") all in actor.regions
let eligible = "deployer" in actor.roles
  and environment == "production"
  and service.labels has {
    "app.kubernetes.io/managed-by": "argocd",
    "platform.example.com/lifecycle": "ga",
  }
```

The guardrails, which the host requires every team policy to invoke:

```sigil title="deploy/guardrails.sigil"
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

The approvals and reviews:

```sigil title="deploy/production.sigil"
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

The payments team's policy, which invokes both with its own values:

```sigil title="payments/production.sigil"
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
        - field: service.labels.regions                               # [4]
          op: splitSubsetOf
          separator: ","
          valueFrom: actor.regions
        - { field: actor.teams, op: contains, value: payments-sre }
```

What the `[1]` to `[4]` markers point at:

1. **Order decides the outcome.** The team's approve has to sit below the denies, or it overrides them. Where it lands relative to `service-owner` changes behaviour, too: a service owner who's also in `payments-sre` gets approved here, because the team rule matches first. In Sigil every rule runs, `deny > review > approve` picks the winner, and that owner gets a review.
2. **Nothing is typed.** `"4h"` stays a string until the engine parses it at evaluation time, and a misspelled path such as `service.teir` resolves to nothing, so its rule quietly stops matching. Sigil checks both against the kind at compile time: `min_soak: 4h` is a `duration`, and `service.teir` is an [error with a fix hint](/understanding/strictness/).
3. **Templating is text.** The team's rules get spliced in as text, so a wrong `indent` produces a different YAML file instead of an error, and every team's values file has to know the template's internals. Sigil's invocations bind [typed params](/understanding/composition/), a team can only add candidates, and the guardrails the host requires can't be gated off.
4. **Nothing is named or shared.** The regions check is pasted into every rule that needs it, even into the team's values file, and the engine grew a one-off `splitSubsetOf` operator to express it. Giving PCI services other approvers means a second copy of the whole `service-owner` rule behind an `if`. Sigil names the check once as `let cleared` in a module, builds it from `split` and the general `all in`, and adds a condition to a shared policy by invoking it inside a `when`.

:::

::: warning Design phase
Sigil is being designed documentation-first. Nothing on this site is implemented yet: these pages **are** the specification, and they will change as the [open questions](/project/open-questions/) get answered. If something reads wrong, [open an issue](https://github.com/SpechtLabs/sigil/issues).
:::
