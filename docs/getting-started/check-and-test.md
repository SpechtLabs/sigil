---
title: 4. Check, evaluate and test
icon: mdi:check-decagram-outline
createTime: 2026/09/30 12:00:00
permalink: /getting-started/check-and-test/
---

With the kind file exported, policies can be worked on with the `sigil` CLI alone: check them against the contract, evaluate them against an input and see the trace, and pin their behavior with test cases. In this step you do all three, then run the same test cases from `go test`.

Install the CLI if you haven't:

```sh
brew install --cask spechtlabs/tap/sigil
```

Every command below runs in `policies/`. The CLI reads the directory it's given, `.` by default, finds the kind file among the files, and checks every document against the kind its header names.

## Check

```text
$ sigil check
✓ checked 2 files, no problems found
```

`check` parses, type-checks and compiles every document, then runs the [lints](/reference/lints/). It reports every problem at once, with the same messages `Load` gives your program, and exits 1 on an error. That makes it the command to run in CI and in an editor's save hook.

## Evaluate

Put an alert in `checkout/testdata/latency.json`. The input is a JSON or YAML object with one key per `input` the kind declares, durations as strings:

```json
{
  "alert": {
    "name": "CheckoutLatencyHigh",
    "severity": "warning",
    "labels": { "env": "production" },
    "firing_for": "45m"
  },
  "team": { "name": "checkout", "oncall": "checkout-primary", "channel": "#checkout-alerts" }
}
```

```text
$ sigil eval --input checkout/testdata/latency.json
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

`eval` prints the decision with its payload and the whole trace: every candidate, the winner marked `*`, where each came from, and which conditions held. When a policy surprises you, this is where you find out why.

## Test

A test case is an input and the decision you expect for it. Put the cases for a policy next to it, in `checkout/alerts_test.yaml`:

```yaml
policy: checkout.alerts
cases:
  - name: a critical production alert pages the on-call
    input:
      alert: {name: CheckoutErrorRate, severity: critical, labels: {env: production}, firing_for: 2m}
      team: {name: checkout, oncall: checkout-primary, channel: "#checkout-alerts"}
    expect:
      decision: page
      reason: critical_alert
      payload: {target: checkout-primary}
  - name: a warning that keeps firing pages
    input_file: testdata/latency.json
    expect:
      decision: page
      reason: sustained
  - name: staging is dropped, even when it's critical
    input:
      alert: {name: CheckoutErrorRate, severity: critical, labels: {env: staging}}
      team: {name: checkout, oncall: checkout-primary, channel: "#checkout-alerts"}
    expect:
      decision: drop
      reason: not_production
  - name: info alerts fall back to the shared channel
    input:
      alert: {name: CheckoutPodRestarted, severity: info, labels: {env: production}}
      team: {name: checkout, oncall: checkout-primary, channel: "#checkout-alerts"}
    expect:
      decision: notify
      reason: unrouted
      payload: {channel: "#checkout-alerts"}
```

Inputs can be inline or in a file, and `payload` lists only the fields you want to compare. The last case expects the wrong channel on purpose:

```text
$ sigil test
--- FAIL: checkout/alerts_test.yaml:23: info alerts fall back to the shared channel
      want channel = "#checkout-alerts"
      got  channel = "#alerts"
FAIL  checkout/alerts_test.yaml  1 of 4 cases failed
✗ 1 of 4 test cases failed in 1 file
```

No rule covers info alerts, so the kind's default posts them to `#alerts`. Fix the expectation:

```text
$ sigil test -v
--- PASS: checkout/alerts_test.yaml:3: a critical production alert pages the on-call
--- PASS: checkout/alerts_test.yaml:11: a warning that keeps firing pages
--- PASS: checkout/alerts_test.yaml:16: staging is dropped, even when it's critical
--- PASS: checkout/alerts_test.yaml:23: info alerts fall back to the shared channel
ok    checkout/alerts_test.yaml  4 cases
✓ 4 cases passed in 1 file
```

Test files are checked against the kind before anything runs, so a case that expects a reason the kind doesn't declare fails with a did-you-mean, not with a confusing mismatch. The format is in [Test files](/reference/test-files/).

## Run the same cases from `go test`

`policytest.Run` runs every `*_test.yaml` in a directory tree against your Go types. Add it to `kind_test.go`:

```go
func TestPolicies(t *testing.T) {
	policytest.Run(t, alerting.Kind, os.DirFS("policies"))
}
```

```text
$ go test -v -run TestPolicies .
=== RUN   TestPolicies
--- PASS: TestPolicies (0.00s)
    --- PASS: TestPolicies/checkout/alerts_test.yaml (0.00s)
        --- PASS: TestPolicies/checkout/alerts_test.yaml/a_critical_production_alert_pages_the_on-call (0.00s)
        --- PASS: TestPolicies/checkout/alerts_test.yaml/a_warning_that_keeps_firing_pages (0.00s)
        --- PASS: TestPolicies/checkout/alerts_test.yaml/staging_is_dropped,_even_when_it's_critical (0.00s)
        --- PASS: TestPolicies/checkout/alerts_test.yaml/info_alerts_fall_back_to_the_shared_channel (0.00s)
```

The policy author runs `sigil test`, the service's CI runs `go test`, and both run the same cases.

::: tip You can stop here
This is a complete setup: a Go service with a typed contract, policies with test cases, and a CLI to work on them. If one team owns the service and its policies, nothing in the rest of the path is required. [Check policies in CI](/guides/ci/) and [Test your policies](/guides/test-policies/) go further with what you have.

The next three steps are for when several teams write policies against the same kind and want to share rules instead of copying them.
:::

Next: [Share rules across teams](/getting-started/share-rules/).
