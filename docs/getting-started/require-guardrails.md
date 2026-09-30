---
title: 7. Require guardrails
icon: mdi:shield-lock-outline
createTime: 2026/09/30 12:00:00
permalink: /getting-started/require-guardrails/
---

At the end of step 5, `sigil check` warned that payments invokes the platform's page rules under a `when`. Any team can do that, including one that writes `when false { routing() }` and never pages anyone again. In this step the platform moves the rules every team must get into a policy of their own, and your program refuses to load a team policy that doesn't invoke it unconditionally.

## Split out the page rules

Pages are the rules that must always apply: a critical production alert pages the on-call, and so does a warning that won't go away. Create `platform/paging.sigil` with both, and the threshold as a param with bounds on both sides:

```sigil
policy platform.paging: AlertRouting@1

use platform.alerts.{in_production}

param page_after: duration = 30m, min: 5m, max: 1h

when in_production and alert.severity == critical {
  page(reason: critical_alert, target: team.oncall)
}

when in_production and alert.severity == warning and alert.firing_for >= page_after {
  page(reason: sustained, target: team.oncall)
}
```

The `max` matters as much as the `min`: without it, `paging(page_after: 1000h)` would switch sustained paging off as surely as a `when false`.

What's left in `platform/routing.sigil` is the part teams may shape freely:

```sigil
policy platform.routing: AlertRouting@1

use platform.alerts.{in_production}

param muted: list<string> = []

when in_production and alert.severity == warning {
  notify(reason: routine, channel: team.channel)
}

when not in_production {
  drop(reason: not_production)
}

when alert.name in muted {
  drop(reason: muted)
}
```

## Require it

Tell `sigil check` that every team policy must invoke `platform.paging`:

```text
$ sigil check --require platform.paging
checkout/alerts.sigil:5:9: error: policy platform.routing has no param `page_after`
  |
5 | routing(page_after: 10m, muted: ["CheckoutCanaryLatency"])
  |         ^^^^^^^^^^
  = help: platform.routing declares: muted

payments/alerts.sigil:7:11: error: policy platform.routing has no param `page_after`
  |
7 |   routing(page_after: 5m)
  |           ^^^^^^^^^^
  = help: platform.routing declares: muted

✗ checked 6 files, 2 errors
```

The threshold moved, so both team policies need updating. Give `checkout/alerts.sigil` both calls:

```sigil
policy checkout.alerts: AlertRouting@1

use platform.paging
use platform.routing

paging(page_after: 10m)
routing(muted: ["CheckoutCanaryLatency"])
```

And `payments/alerts.sigil`:

```sigil
policy payments.alerts: AlertRouting@1

use platform.alerts.{in_production}
use platform.paging
use platform.routing

paging(page_after: 5m)
routing()

when in_production and alert.severity == info and alert.labels["component"] == "ledger" {
  notify(reason: routine, channel: "#payments-ledger")
}
```

```text
$ sigil check --require platform.paging
✓ checked 6 files, no problems found
```

The `gated-deny` warnings are gone too: `routing` no longer holds page rules, so gating it can't silence a page.

Payments lost something in the move: its ledger-only threshold. A required policy is invoked once, at the top level, so each team gets one `page_after`. That's the trade: the platform can now promise that every team pages for sustained warnings within an hour.

## Try to get around it

Put the payments call back under a condition:

```sigil
when alert.labels["component"] == "ledger" {
  paging(page_after: 5m)
}
```

```text
$ sigil check --require platform.paging
payments/alerts.sigil:8:3: error: platform.paging must be invoked unconditionally
  |
8 |   paging(page_after: 5m)
  |   ^^^^^^^^^^^^^^^^^^^^^^
  = help: the host requires platform.paging for every AlertRouting policy; move the call to the top level

✗ checked 6 files, 1 error
```

Leave it out entirely, and the error names the policy that's missing:

```text
$ sigil check --require platform.paging
checkout/alerts.sigil:1:1: error: checkout.alerts doesn't invoke platform.paging
  |
1 | policy checkout.alerts: AlertRouting@1
  | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
  = help: the host requires platform.paging for every AlertRouting policy; import it with `use platform.paging` and invoke it at the top level
```

And a threshold outside the bounds:

```text
payments/alerts.sigil:8:22: error: page_after: 5h is above the maximum 1h
  |
8 |   paging(page_after: 5h)
  |                      ^^
  = help: platform.paging declares `param page_after: duration = 30m, min: 5m, max: 1h`
```

Put everything back the way it was before moving on.

## Make it stick

Typing `--require` on every run is easy to forget. Put the requirement in `policies/sigil.yaml`, which `check`, `eval`, `explain` and `test` read from the directory they run in:

```yaml
require:
  - policy: platform.paging
    trusted: [platform]
    roots: ["*.alerts"]
```

`trusted` says `platform.paging` must come from `platform/`, so a team can't satisfy the requirement with a `platform.paging` of its own. `roots` names the policies it applies to.

The check that counts is the one in your program, because that's what serves alerts. Change the `Load` call in `cmd/route/main.go`:

```go
p, err := alerting.Kind.Load(alerting.Policies, "checkout.alerts",
	policy.Require("platform.paging"))
```

Now a team policy that skips the call doesn't load:

```text
$ go run ./cmd/route
2026/09/30 17:31:40 policies/checkout/alerts.sigil:1:1: error: checkout.alerts doesn't invoke platform.paging
  |
1 | policy checkout.alerts: AlertRouting@1
  | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
  = help: the host requires platform.paging for every AlertRouting policy; import it with `use platform.paging` and invoke it at the top level
exit status 1
```

With the call back in place:

```text
$ go run ./cmd/route
CheckoutErrorRate      critical production  2m0s  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production 12m0s  → page checkout-primary (sustained)
CheckoutLatencyHigh    warning  production 45m0s  → page checkout-primary (sustained)
CheckoutErrorRate      critical staging     2m0s  → drop (not_production)
CheckoutCanaryLatency  warning  production  5m0s  → drop (muted)
```

The 12-minute warning pages now, because checkout's `page_after` is 10 minutes. And since `page` outranks every other decision, nothing a team adds can beat a page from `platform.paging`: a team can add candidates, never remove them.

## What you built

A Go service that routes alerts through policies it checks against a typed contract; a kind file that lets anyone check, evaluate and test those policies with the CLI; a shared library that teams use with their own values; and guardrails that no team can switch off.

The finished router, as a service with an HTTP API, hot reload, metrics, traces, a Grafana dashboard and load tests, is [`examples/alert-routing`](https://github.com/SpechtLabs/sigil/tree/main/examples/alert-routing). From here:

- [Per-team policies](/guides/team-policies/) and [Policies in a ConfigMap](/guides/configmaps/) take the same ideas into production, including loading team policies from a directory the platform doesn't control, where `policy.From` makes the guardrails come from the platform's own copy.
- [Composition without templating](/understanding/composition/) explains what guarantees composition gives and which it doesn't.
- The [language reference](/reference/policy-files/) is the precise version of everything you used.
