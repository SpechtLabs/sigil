---
pageLayout: home
externalLinkIcon: false

config:
  - type: doc-hero
    hero:
      name: Sigil
      text: A small, typed language for decision logic in Go
      tagline: Write rules that turn your program's input into a typed decision, such as paging the on-call, rolling out a feature or approving a deploy. Every decision carries a reason and a payload, and every policy is checked against a contract your Go code defines.
      actions:
        - text: Take the tour →
          link: /getting-started/tour/
          theme: brand
          icon: mdi:map-marker-path
        - text: Learn it step by step
          link: /getting-started/define-the-input/
          theme: alt
          icon: mdi:stairs
        - text: Read the reference
          link: /reference/policy-files/
          theme: alt
          icon: mdi:book-open-page-variant

  - type: features
    title: Why Sigil?
    description: Most services grow a rule engine by accident, one YAML matcher at a time. Sigil is a language small enough to read on first contact and strict enough to trust with the rules that matter.
    features:
      - title: Typed against a contract
        icon: mdi:file-certificate-outline
        details: Your Go code defines the kind, with its inputs, enums, functions and decisions. A typo like alert.severty, or a severity spelled critcal, fails at compile time with a fix hint instead of silently switching a rule off.

      - title: Halts by construction
        icon: mdi:timer-sand-complete
        details: No loops, no recursion, no user-defined functions. Quantifiers range over finite lists and regexes use RE2. Host functions must terminate. Static cost budgets are planned.

      - title: Rule order never matters
        icon: mdi:sort-variant-off
        details: Every rule is evaluated, and the kind's precedence picks the winner. Reordering a file never changes a decision.

      - title: Decisions explain themselves
        icon: mdi:message-text-outline
        details: Every decision takes a reason the kind declares, so you can grep for it and count it in metrics. Payloads are typed fields your Go code reads back as structs, and a trace shows every candidate.

      - title: Shared helper libraries
        icon: mdi:library-shelves
        details: Modules share named expressions, and policies with typed params are invoked like functions with each team's values. No text templating, and the guardrails the host requires can't be switched off.

      - title: Parse once, evaluate many
        icon: mdi:lightning-bolt
        details: A compiled policy is immutable and safe for concurrent use, in the spirit of Go's regexp package. An evaluation takes microseconds, and hot reload is a pointer swap.

  - type: VPListCompare
    title: "YAML rule engine vs. Sigil"
    description: "The same rules, expressed two ways."
    left:
      title: "Hand-rolled YAML rules"
      description: "Stringly typed, checked at runtime, if at all"
      items:
        - title: "Typos fail open"
          description: "A misspelled field resolves to null, and the rule that used it quietly stops matching"
        - title: "Order-dependent"
          description: "First match wins, so moving a block changes the outcome"
        - title: "Templated with text"
          description: "Per-team variants come from text/template or YAML anchors, and whitespace breaks them"
        - title: "Opaque outcomes"
          description: "The result is a bare label, without a reason you can alert on"

    right:
      title: "Sigil"
      description: "Typed, halting, and explainable"
      items:
        - title: "Typos fail to compile"
          description: "Every field, value, function and payload key is checked against the kind"
        - title: "Order-independent"
          description: "All rules run; precedence decides between the candidates"
        - title: "Typed params and invocation"
          description: "Teams invoke a shared policy with values, never with text substitution"
        - title: "Reason plus payload"
          description: "Every decision names why it happened and carries the data your program acts on"

  - type: custom

  - type: VPReleases
    repo: SpechtLabs/sigil

  - type: VPContributors
    repo: SpechtLabs/sigil
---

## A policy, start to finish

An alert router asks a policy what to do with each alert: page the on-call, drop it, or post it to a channel. Your Go code defines the contract, a policy decides, and your code acts on a typed result.

::: steps

1. Define the input and the decisions in Go

   ```go
   type Input struct {
   	Alert Alert `policy:"alert"`
   	Team  Team  `policy:"team"`
   }

   var (
   	Page   = policy.NewDecision[PageData]("page", "critical_alert", "sustained")
   	Drop   = policy.NewDecision[policy.None]("drop", "muted", "not_production")
   	Notify = policy.NewDecision[NotifyData]("notify", "routine", "unrouted")
   )

   var Kind = policy.NewKind[Input]("AlertRouting",
   	policy.WithVersion(1),
   	policy.WithEnum(Critical, Warning, Info),
   	policy.WithDecisions(Page, Drop, Notify), // a page beats a drop beats a notification
   	policy.WithDefault(Notify.Reason("unrouted")),
   	// ...
   )
   ```

2. Write the rules

   ```sigil
   policy checkout.alerts: AlertRouting@1

   let in_production = alert.labels["env"] == "production"

   when in_production and alert.severity == critical {
     page(reason: critical_alert, target: team.oncall)
   }

   when in_production and alert.severity == warning {
     when alert.firing_for >= 30m {
       page(reason: sustained, target: team.oncall)
     }

     notify(reason: routine, channel: team.channel)
   }

   when not in_production {
     drop(reason: not_production)
   }
   ```

3. Load once, evaluate per alert, act on the typed result

   ```go
   p, err := Kind.Load(policies, "checkout.alerts")
   // ...
   res, err := p.Eval(ctx, Input{Alert: alert, Team: team})
   // ...
   if page, ok := Page.Match(res); ok {
   	pageOncall(page.Target, res.Reason) // page is a typed PageData
   }
   ```

:::

A warning that has been firing for 45 minutes matches two rules. Both become candidates, and the page wins because the kind ranks it above the notification:

```text
checkout.alerts: page(reason: sustained)
  target = "checkout-primary"

trace: 2 candidates
  * page(reason: sustained)  checkout/alerts.sigil:11:5
      when in_production and alert.severity == warning
       and alert.firing_for >= 30m
      target = "checkout-primary"
    notify(reason: routine)  checkout/alerts.sigil:14:3
      channel = "#checkout-alerts"
```

The [tour](/getting-started/tour/) reads this policy line by line and lets you route alerts through it in the browser.

## Not only alerts

Sigil doesn't know what an alert is. The kind says what the input looks like and which decisions exist, and the same rules, lets and precedence decide feature rollouts, discounts, deploy approvals or the roles someone holds. [What Sigil is](/getting-started/overview/#what-you-can-decide-with-it) shows five of them side by side, and [deploygate](/guides/example-service/) is a complete service that decides deploy approvals and access grants with two kinds.

Teams that share a kind can share rules too: a platform team publishes a library of helpers and policies with typed params, product teams invoke them with their own values, and the host can require the rules no team may switch off. [Share rules across teams](/getting-started/share-rules/) builds that from scratch.

::: info Project status
The language, the Go API, composition and the CLI are implemented, and fuzz tests cover every layer. Not built yet: loading a kind from a file at run time (`policy.LoadKind`), host-ordered types such as versions, static cost budgets, and editor tooling. The [roadmap](/project/roadmap/) tracks what's left.
:::
