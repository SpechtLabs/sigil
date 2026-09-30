---
title: 6. See what a policy adds up to
icon: mdi:file-tree-outline
createTime: 2026/09/30 12:00:00
permalink: /getting-started/explain/
---

After step 5, `checkout/alerts.sigil` is five lines long and decides exactly what it decided before. That's the point of sharing rules, and it's also a problem for anyone reviewing the file: the rules it can fire live somewhere else, with parameters filled in from the call. `sigil explain` puts them back together.

## Flatten a policy

```text
$ sigil explain --policy checkout.alerts
checkout.alerts: 5 rules from 2 policies

  page(reason: critical_alert)  checkout.alerts:5 → platform.routing:9
    when not pre_production and alert.severity == critical
    with target = team.oncall

  page(reason: sustained)       checkout.alerts:5 → platform.routing:14
    when not pre_production and alert.severity == warning
     and alert.firing_for >= 10m
    with target = team.oncall

  notify(reason: routine)       checkout.alerts:5 → platform.routing:17
    when not pre_production and alert.severity == warning
    with channel = team.channel

  drop(reason: not_production)  checkout.alerts:5 → platform.routing:21
    when pre_production

  drop(reason: muted)           checkout.alerts:5 → platform.routing:25
    when alert.name in ["CheckoutCanaryLatency"]
```

Each entry is one decision the policy can make, with everything that has to hold for it:

- The **chain** says where it comes from: the invocation on line 5 of `checkout.alerts`, then line 14 of `platform.routing`.
- The **conditions** are every enclosing `when`, outermost first, nested rules joined with `and`.
- **Params show as their bound values**: `10m` and the muted list, not `page_after` and `muted`.
- Payload fields show the expression that computes them, `target = team.oncall`.

It's the same policy as the one you wrote by hand in step 2, with the team's own threshold. A reviewer can read this and know what the file does without opening `platform/`.

## Conditions pushed down

Payments invokes the platform's policy twice, under two conditions. `explain` pushes each call's condition into every rule it brings in:

```text
$ sigil explain --policy payments.alerts
payments.alerts: 11 rules from 2 policies and 1 module

  page(reason: critical_alert)  payments.alerts:7 → platform.routing:9
    when alert.labels["component"] == "ledger"
     and not pre_production and alert.severity == critical
    with target = team.oncall

  page(reason: sustained)       payments.alerts:7 → platform.routing:14
    when alert.labels["component"] == "ledger"
     and not pre_production and alert.severity == warning
     and alert.firing_for >= 5m
    with target = team.oncall

  ...

  drop(reason: muted)           payments.alerts:11 → platform.routing:25
    when alert.labels["component"] != "ledger"
     and alert.name in []

  notify(reason: routine)       payments.alerts:15
    when not pre_production and alert.severity == info and alert.labels["component"] == "ledger"
    with channel = "#payments-ledger"
```

Two things stand out that the source doesn't show directly. Every page rule now depends on the `component` label, which is what the `gated-deny` warning from step 5 was about. And `alert.name in []` can never be true: payments didn't pass `muted`, so the default, an empty list, makes that rule dead. Neither is wrong here, but both are things you want to see before a policy ships.

Without `--policy`, `explain` explains every policy in the bundle, one after another.

Next: [Require guardrails](/getting-started/require-guardrails/), so the rules that must always apply can't be gated off.
