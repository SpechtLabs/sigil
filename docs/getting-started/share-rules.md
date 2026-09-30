---
title: 5. Share rules across teams
icon: mdi:library-shelves
createTime: 2026/09/30 12:00:00
permalink: /getting-started/share-rules/
---

The payments team wants the same routing as checkout: page for critical alerts, page for warnings that don't go away, drop staging, mute what's noisy. Copying `checkout/alerts.sigil` would work until the first fix lands in one copy and not the other. In this step the rules move into a small shared library, and each team's policy becomes a few lines that use it with the team's own values.

Sigil has two kinds of shared file for this:

| File | Holds | Using it |
| --- | --- | --- |
| **Module** | `let`s marked `pub`, and imports | `use platform.alerts.{in_production}` brings in a name; it can't change a decision by itself |
| **Policy with params** | Rules, plus `param`s for the values a team may choose | `routing(page_after: 10m)` adds all its rules, with the params bound |

## A module of shared helpers

A module is a library of named expressions. Create `platform/alerts.sigil`:

```sigil
module platform.alerts: AlertRouting@1

pub let in_production = alert.labels["env"] == "production"
```

`pub` makes the `let` importable; without it, a `let` stays private to its file. A module holds only imports and `let`s, never rules, so importing from it never adds or removes a decision.

## A policy other policies invoke

Move checkout's rules into `platform/routing.sigil`, and turn the two values teams will want to choose into params:

```sigil
policy platform.routing: AlertRouting@1

use platform.alerts.{in_production}

param page_after: duration = 30m, min: 5m
param muted: list<string> = []

when in_production and alert.severity == critical {
  page(reason: critical_alert, target: team.oncall)
}

when in_production and alert.severity == warning {
  when alert.firing_for >= page_after {
    page(reason: sustained, target: team.oncall)
  }

  notify(reason: routine, channel: team.channel)
}

when not in_production {
  drop(reason: not_production)
}

when alert.name in muted {
  drop(reason: muted)
}
```

- `use platform.alerts.{in_production}` imports one name. Every name a file uses is either defined in it or listed in a `use`, so you can always tell where it comes from.
- `param page_after: duration = 30m, min: 5m` is typed, has a default, and has a lower bound: no team can page for sustained warnings after less than five minutes.
- `param muted: list<string> = []` defaults to muting nothing.

Checkout's policy shrinks to a single invocation:

```sigil
policy checkout.alerts: AlertRouting@1

use platform.routing

routing(page_after: 10m, muted: ["CheckoutCanaryLatency"])
```

`use platform.routing` binds the name `routing`, and nothing else. The call is what adds the rules: an invocation looks like a decision constructor and binds the invoked policy's params by name. Arguments are type-checked like everything else:

```text
$ sigil check
checkout/alerts.sigil:5:21: error: expected duration, found string
  |
5 | routing(page_after: "10m", muted: ["CheckoutCanaryLatency"])
  |                     ^^^^^

✗ checked 4 files, 1 error
```

With `10m`, the test cases from step 4 still pass, and the trace now shows the call chain: the invocation on line 5 of the team's file, then the rule in the platform's:

```text
$ sigil eval --policy checkout.alerts --input checkout/testdata/latency.json
checkout.alerts: page(reason: sustained)
  target = "checkout-primary"

trace: 2 candidates
  * page(reason: sustained)  checkout/alerts.sigil:5:1 → platform/routing.sigil:14:5
      when in_production and alert.severity == warning
       and alert.firing_for >= 10m
      target = "checkout-primary"
    notify(reason: routine)  checkout/alerts.sigil:5:1 → platform/routing.sigil:17:3
      channel = "#checkout-alerts"
```

The bundle now holds two policies, so `--policy` says which one to evaluate. Where `page_after` appeared, the trace shows the bound value, `10m`.

## Invoke it under conditions

The payments team pages faster for the ledger than for everything else, and posts the ledger's info alerts to a channel of its own. An invocation can sit inside a `when`, and then the block's condition is added to every rule it brings in. Create `payments/alerts.sigil`:

```sigil
policy payments.alerts: AlertRouting@1

use platform.alerts.{in_production}
use platform.routing

when alert.labels["component"] == "ledger" {
  routing(page_after: 2m)
}

when alert.labels["component"] != "ledger" {
  routing()
}

when in_production and alert.severity == info and alert.labels["component"] == "ledger" {
  notify(reason: routine, channel: "#payments-ledger")
}
```

The platform's bound catches the first call:

```text
$ sigil check
payments/alerts.sigil:7:23: error: page_after: 2m is below the minimum 5m
  |
7 |   routing(page_after: 2m)
  |                       ^^
  = help: platform.routing declares `param page_after: duration = 30m, min: 5m`
```

With `5m`, a ledger warning that has fired for seven minutes pages, and the trace shows the condition the invocation added:

```text
$ sigil eval --policy payments.alerts --input payments/testdata/ledger-lag.json
payments.alerts: page(reason: sustained)
  target = "payments-primary"

trace: 2 candidates
  * page(reason: sustained)  payments/alerts.sigil:7:3 → platform/routing.sigil:14:5
      when alert.labels["component"] == "ledger"
       and in_production and alert.severity == warning
       and alert.firing_for >= 5m
      target = "payments-primary"
    notify(reason: routine)  payments/alerts.sigil:7:3 → platform/routing.sigil:17:3
      channel = "#payments-alerts"
```

`routing()` takes every default; the parentheses are required. The team's own rule imports `in_production` from the module instead of spelling the label lookup out again.

## A warning worth reading

`check` passes now, but it has something to say:

```text
$ sigil check
payments/alerts.sigil:7:3: warning: platform.routing holds page rules and is invoked under `when` [gated-deny]
  |
7 |   routing(page_after: 5m)
  |   ^^^^^^^^^^^^^^^^^^^^^^^
  = help: its page rules only fire while the condition holds; invoke it at the top level, or have the host require it

payments/alerts.sigil:11:3: warning: platform.routing holds page rules and is invoked under `when` [gated-deny]
   |
11 |   routing()
   |   ^^^^^^^^^
   = help: its page rules only fire while the condition holds; invoke it at the top level, or have the host require it

! checked 5 files, 2 warnings
```

`page` is the kind's highest-ranked decision, the one nothing can override. The payments policy happens to cover every alert, since a component either is the ledger or isn't. But the same mechanism that lets a team add conditions would let it write `when false { routing() }` and never page anyone. [Step 7](/getting-started/require-guardrails/) closes that hole. First, [step 6](/getting-started/explain/) shows how to see everything a composed policy can decide.

::: file-tree

- policies
  - alert_routing.sigil
  - platform
    - ++ alerts.sigil # module platform.alerts
    - ++ routing.sigil # policy platform.routing
  - checkout
    - alerts.sigil # one invocation
    - alerts_test.yaml
    - testdata
      - latency.json
  - ++ payments
    - ++ alerts.sigil
    - ++ testdata
      - ++ ledger-lag.json

:::

Sigil finds documents by the names in their headers, not their paths, so the file layout is a convention for people.
