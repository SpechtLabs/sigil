---
title: A tour of the language
icon: mdi:map-marker-path
createTime: 2026/09/24 22:30:00
permalink: /getting-started/tour/
---

This page reads one complete policy: the alert router's policy for the checkout team. An alert fires, the router asks the policy what to do with it, and the policy decides whether to page the on-call engineer, post to the team's channel, or drop the alert. You'll read the contract the policy is written against, then the policy itself, and then route a few alerts through it.

## The kind

Every policy is written against a **kind**, the contract between the Go program that evaluates policies and the policies themselves. The Go program defines it and exports it as a file. You read it the way you'd read an API spec; nobody edits it by hand. This one is `alert_routing.sigil`:

```sigil
kind AlertRouting version 1

enum Severity: critical | warning | info

type Alert {
  name: string
  severity: Severity
  labels: map<string, string>
  firing_for: duration
}

type Team {
  name: string
  oncall: string
  channel: string
}

input alert: Alert
input team: Team

decision page {
  reason: critical_alert | sustained
  target: string
}

decision drop {
  reason: muted | not_production
}

decision notify {
  reason: routine | unrouted
  channel: string = "#alerts"
}

collect one
precedence page > drop > notify
precedence page: critical_alert > sustained
precedence drop: muted > not_production
precedence notify: routine > unrouted

default notify(reason: unrouted)
```

Reading it top to bottom:

- `enum Severity` is a closed set of values. A policy writes them as bare names, `alert.severity == critical`, and a misspelled one doesn't compile.
- `type` declares structs, and `input` names what a policy can read: the `alert` and the `team` that owns it. `duration` is built in, so `alert.firing_for >= 30m` needs no parsing on your side.
- Each `decision` lists its **reasons** and its **payload** fields. `page` needs a `target`; `notify` has a `channel`, which defaults to `#alerts`.
- `collect one` means the program gets one decision back. `precedence page > drop > notify` says which one wins when several rules fire: a page beats a drop, so muting an alert can never silence a page.
- `default` applies when no rule fires. Here that's a post to `#alerts`, so an alert nobody wrote a rule for still reaches a human.

## The policy

The checkout team's policy, `checkout/alerts.sigil`:

```sigil
policy checkout.alerts: AlertRouting@1

let pre_production = alert.labels["env"] in ["staging", "dev"]

when not pre_production and alert.severity == critical {
  page(reason: critical_alert, target: team.oncall)
}

when not pre_production and alert.severity == warning {
  when alert.firing_for >= 30m {
    page(reason: sustained, target: team.oncall)
  }

  notify(reason: routine, channel: team.channel)
}

when pre_production {
  drop(reason: not_production)
}

when alert.name in ["CheckoutCanaryLatency"] {
  drop(reason: muted)
}
```

- The header names the policy, `checkout.alerts`, and pins the kind and its version, `AlertRouting@1`.
- `let` names an expression, so the rules can say `pre_production` instead of repeating the label lookup. Label keys go in brackets because they're map keys.
- A `when` block is a rule. When its condition holds, every decision inside it becomes a **candidate**. A nested `when` only fires if its parent holds too: nesting means "and".
- `page(reason: critical_alert, target: team.oncall)` is a decision constructor. Every argument is named, the reason is one the kind declares, and the payload fields are type-checked. It doesn't stop evaluation or return anything. It adds a candidate.
- There's no `else` and no rule order. Every rule is evaluated, and the kind's precedence picks the winner, so moving a block never changes a decision.

## Route an alert

To get a decision, the evaluator collects every candidate whose conditions hold and picks the one the kind's precedence ranks highest. With no candidates, the kind's `default` applies. Change the alert below and watch which rules fire:

<DecisionPlayground />

A few alerts through the real evaluator, with `sigil eval`. A warning that has been firing for twelve minutes goes to the team's channel:

```text
$ sigil eval --input latency.json
checkout.alerts: notify(reason: routine)
  channel = "#checkout-alerts"

trace: 1 candidate
  * notify(reason: routine)  checkout/alerts.sigil:14:3
      when not pre_production and alert.severity == warning
      channel = "#checkout-alerts"
```

After 45 minutes the nested rule fires too, and the page outranks the notification:

```text
checkout.alerts: page(reason: sustained)
  target = "checkout-primary"

trace: 2 candidates
  * page(reason: sustained)  checkout/alerts.sigil:11:5
      when not pre_production and alert.severity == warning
       and alert.firing_for >= 30m
      target = "checkout-primary"
    notify(reason: routine)  checkout/alerts.sigil:14:3
      channel = "#checkout-alerts"
```

The trace lists every candidate, marks the winner with `*`, and shows where it came from and which conditions held. The reason is a name the kind declares, so a dashboard can count decisions by it.

## What the compiler catches

Policies are checked against the kind before they ever run. Misspell a severity and the policy doesn't compile:

```text
$ sigil check
checkout/alerts.sigil:9:42: error: Severity has no value `warnign`
  |
9 | when not pre_production and alert.severity == warnign {
  |                                          ^^^^^^^
  = help: did you mean `warning`? Severity declares: critical, warning, info

✗ checked 2 files, 1 error
```

The same goes for a field that doesn't exist, a string compared with a duration, a reason the decision doesn't declare, or a payload field that's missing. In a YAML rule list, each of those would be a rule that silently never matches. [Strict schema, forgiving data](/understanding/strictness/) explains where Sigil draws the line.

## Next

[Define the input and evaluate a policy](/getting-started/define-the-input/) starts the step-by-step path: you'll build this router from an empty Go module, one idea at a time.
