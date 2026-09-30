---
title: 2. Write policies
icon: mdi:file-document-edit-outline
createTime: 2026/09/30 12:00:00
permalink: /getting-started/write-policies/
---

In this step you grow `checkout.alerts` from one rule into the policy the [tour](/getting-started/tour/) reads, one rule at a time. After each change you run `go run ./cmd/route` from [step 1](/getting-started/define-the-input/) and watch the six sample alerts change route. Along the way you'll meet lets, nested rules, precedence and the compile errors that catch mistakes.

You start from the one-rule policy:

```sigil
policy checkout.alerts: AlertRouting@1

when alert.severity == critical {
  page(reason: critical_alert, target: team.oncall)
}
```

## Post warnings to the team

Warnings shouldn't wake anyone up, but they should reach the team. Add a second rule:

```sigil
policy checkout.alerts: AlertRouting@1

when alert.severity == critical {
  page(reason: critical_alert, target: team.oncall)
}

when alert.severity == warning { // [!code ++]
  notify(reason: routine, channel: team.channel) // [!code ++]
} // [!code ++]
```

```text
$ go run ./cmd/route
CheckoutErrorRate      critical production  2m0s  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production 12m0s  → post to #checkout-alerts (routine)
CheckoutLatencyHigh    warning  production 45m0s  → post to #checkout-alerts (routine)
CheckoutErrorRate      critical staging     2m0s  → page checkout-primary (critical_alert)
CheckoutQueueStuck     critical -           3m0s  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production  5m0s  → post to #checkout-alerts (routine)
```

Every warning now goes to `#checkout-alerts` instead of the catch-all `#alerts`. `team.channel` comes from the input, so the same rule works for every team that uses the policy.

## Page for warnings that don't go away

A warning that has been firing for half an hour is a problem someone should look at now. Nest a rule inside the warning rule. A nested `when` only fires when its parent's condition holds too:

```sigil
when alert.severity == warning {
  when alert.firing_for >= 30 { // [!code ++]
    page(reason: sustained, target: team.oncall) // [!code ++]
  } // [!code ++]
 // [!code ++]
  notify(reason: routine, channel: team.channel)
}
```

```text
$ go run ./cmd/route
2026/09/30 17:26:03 policies/checkout/alerts.sigil:8:8: error: `>=` needs operands of the same type, found duration and int
  |
8 |   when alert.firing_for >= 30 {
  |        ^^^^^^^^^^^^^^^^^^^^^^
  = help: a bare number is never a duration; write a literal like `30m`
exit status 1
```

Thirty what? Sigil never converts between types, so a duration compares only with a duration. Write `30m`:

```sigil
  when alert.firing_for >= 30m {
```

```text
$ go run ./cmd/route
CheckoutErrorRate      critical production  2m0s  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production 12m0s  → post to #checkout-alerts (routine)
CheckoutLatencyHigh    warning  production 45m0s  → page checkout-primary (sustained)
CheckoutErrorRate      critical staging     2m0s  → page checkout-primary (critical_alert)
CheckoutQueueStuck     critical -           3m0s  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production  5m0s  → post to #checkout-alerts (routine)
```

The 45-minute warning now matches two rules: the nested one pages, and the outer one still notifies. Both are **candidates**. Nothing stops at the first match; every rule is evaluated, and the kind's precedence, `page > drop > notify`, picks the winner. That's why the order of rules in a file never changes a decision. [Why rule order never matters](/understanding/order-independence/) has the background.

## Drop alerts from staging

Staging alerts shouldn't reach anyone. Add a rule that drops them:

```sigil
when alert.labels["env"] == "staging" { // [!code ++]
  drop(reason: not_production) // [!code ++]
} // [!code ++]
```

```text
$ go run ./cmd/route
...
CheckoutErrorRate      critical staging     2m0s  → page checkout-primary (critical_alert)
...
```

The staging alert still pages. The drop is a candidate, but so is the page, and a page outranks a drop. That's deliberate in this kind: no rule that drops an alert can ever silence a page. To keep staging quiet, the page rules have to leave staging out. Name the environments you drop once, with a `let`, and use the name everywhere:

```sigil
policy checkout.alerts: AlertRouting@1

let pre_production = alert.labels["env"] in ["staging", "dev"] // [!code ++]

when not pre_production and alert.severity == critical { // [!code highlight]
  page(reason: critical_alert, target: team.oncall)
}

when not pre_production and alert.severity == warning { // [!code highlight]
  when alert.firing_for >= 30m {
    page(reason: sustained, target: team.oncall)
  }

  notify(reason: routine, channel: team.channel)
}

when pre_production { // [!code highlight]
  drop(reason: not_production)
}
```

```text
$ go run ./cmd/route
CheckoutErrorRate      critical production  2m0s  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production 12m0s  → post to #checkout-alerts (routine)
CheckoutLatencyHigh    warning  production 45m0s  → page checkout-primary (sustained)
CheckoutErrorRate      critical staging     2m0s  → drop (not_production)
CheckoutQueueStuck     critical -           3m0s  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production  5m0s  → post to #checkout-alerts (routine)
```

`CheckoutQueueStuck` has no `env` label and still pages. A missing label reads as `""`, like a missing key in a Go map, and `""` isn't a pre-production environment. Had the rule been `when alert.labels["env"] != "production"`, the same alert would have been dropped: an alert with a missing or misspelled label would never reach anyone. Name what you drop, and everything you didn't think of still gets through.

A `let` is evaluated against the same input as the rules. There's no `else`: "the other case" is `when not pre_production`, which says the same thing without implying an order.

## Mute a noisy alert

`CheckoutCanaryLatency` fires every time a canary is slow and nobody acts on it. Drop it by name:

```sigil
when alert.name in ["CheckoutCanaryLatency"] { // [!code ++]
  drop(reason: mute) // [!code ++]
} // [!code ++]
```

```text
$ go run ./cmd/route
2026/09/30 17:26:05 policies/checkout/alerts.sigil:22:16: error: decision drop has no reason `mute`
   |
22 |   drop(reason: mute)
   |                ^^^^
   = help: did you mean `muted`? drop declares: muted, not_production
exit status 1
```

Reasons are names the kind declares, not free text, so a typo can't create a reason nobody counts on a dashboard. Fix it to `muted`:

```text
$ go run ./cmd/route
CheckoutErrorRate      critical production  2m0s  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production 12m0s  → post to #checkout-alerts (routine)
CheckoutLatencyHigh    warning  production 45m0s  → page checkout-primary (sustained)
CheckoutErrorRate      critical staging     2m0s  → drop (not_production)
CheckoutQueueStuck     critical -           3m0s  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production  5m0s  → drop (muted)
```

The muted warning is dropped: a drop outranks a notification. Were the canary alert ever critical, it would still page.

## The finished policy

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

You used rules, nested rules, decision constructors with payloads, a `let`, and comparisons that never convert between types. The [policy files reference](/reference/policy-files/) and [expressions reference](/reference/expressions/) have the details, and [Common patterns](/guides/patterns/) has recipes for what comes up next: label sets, optional fields, quantifiers over lists, time.

Next: [Export the kind](/getting-started/export-the-kind/), so the policy can be checked without running your Go program.
