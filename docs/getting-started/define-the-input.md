---
title: 1. Define the input
icon: mdi:language-go
createTime: 2026/09/30 12:00:00
permalink: /getting-started/define-the-input/
---

In this step you describe what a policy gets to see and what it may decide, as ordinary Go types, then load a one-rule policy and act on its decision. By the end, a small Go program routes five sample alerts.

You need Go 1.27 or later. Start a module and add the library:

```sh
mkdir alerting && cd alerting
go mod init example.com/alerting
go get github.com/spechtlabs/sigil@latest
```

By the end of this step, the module looks like this:

::: file-tree

- alerting
  - go.mod
  - kind.go # the input, the decisions and the kind
  - policies.go # embeds policies/
  - policies
    - checkout
      - alerts.sigil # the first policy
  - cmd
    - route
      - main.go # loads the policy and routes sample alerts

:::

## Describe the input

A policy reads the input your program passes to it, and nothing else. Write it as structs; every field tagged `policy:"..."` is visible to policies under that name. Put this in `kind.go`:

```go
package alerting

import (
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// Input is what a policy sees for one alert.
type Input struct {
	Alert Alert `policy:"alert"`
	Team  Team  `policy:"team"`
}

type Alert struct {
	Name      string            `policy:"name"`
	Severity  Severity          `policy:"severity"`
	Labels    map[string]string `policy:"labels"`
	FiringFor time.Duration     `policy:"firing_for"`
}

type Team struct {
	Name    string `policy:"name"`
	Oncall  string `policy:"oncall"`
	Channel string `policy:"channel"`
}

type Severity string

const (
	Critical Severity = "critical"
	Warning  Severity = "warning"
	Info     Severity = "info"
)
```

A policy reads these as `alert.severity`, `alert.labels["env"]` or `team.oncall`. `time.Duration` becomes Sigil's `duration`, maps and slices become maps and lists. [Go type mapping](/reference/go-api/#go-type-mapping) lists the rest.

`Severity` is a named string with a fixed set of values. Registering it as an enum, below, is what makes `alert.severity == critcal` a compile error instead of a comparison that's never true.

## Declare the decisions

A decision is something a policy can conclude, with the **reasons** it may give and a **payload** your program acts on. The alert router can page someone, drop the alert, or post a notification. Add the payloads and the decisions to `kind.go`:

```go
// The payloads: whom to page, and where to post.
type PageData struct {
	Target string `policy:"target"`
}

type NotifyData struct {
	Channel string `policy:"channel,default=\"#alerts\""`
}

// The decisions, each with every reason a rule may give for it.
var (
	Page   = policy.NewDecision[PageData]("page", "critical_alert", "sustained")
	Drop   = policy.NewDecision[policy.None]("drop", "muted", "not_production")
	Notify = policy.NewDecision[NotifyData]("notify", "routine", "unrouted")
)
```

`drop` carries only a reason, so its payload is `policy.None`. `channel` has a default, which matters in a moment. Why reasons are declared names and not free text: [Decisions and reasons](/understanding/decisions/).

## Build the kind

The **kind** ties the input and the decisions together into the contract every policy is checked against:

```go
var Kind = policy.NewKind[Input]("AlertRouting",
	policy.WithVersion(1),
	policy.WithEnum(Critical, Warning, Info),
	policy.WithDecisions(Page, Drop, Notify), // order = precedence
	policy.WithReasonPrecedence(Page.Reason("critical_alert"), Page.Reason("sustained")),
	policy.WithReasonPrecedence(Drop.Reason("muted"), Drop.Reason("not_production")),
	policy.WithReasonPrecedence(Notify.Reason("routine"), Notify.Reason("unrouted")),
	policy.WithDefault(Notify.Reason("unrouted")),
)
```

- `WithDecisions` lists the decisions highest precedence first. When several rules fire, a page beats a drop and a drop beats a notification.
- `WithReasonPrecedence` ranks each decision's reasons, so two pages for different reasons never tie.
- `WithDefault` is the answer when no rule fires. Its payload takes the defaults, so an alert no rule covers is posted to `#alerts` rather than lost.

`NewKind` checks all of this when the program starts and panics with every problem it finds, so a broken contract never reaches a request. Every option is in [Kind options](/reference/go-api/#kind-options).

## Write a policy

Policies are `.sigil` files. Create `policies/checkout/alerts.sigil` with one rule:

```sigil
policy checkout.alerts: AlertRouting@1

when alert.severity == critical {
  page(reason: critical_alert, target: team.oncall)
}
```

The header names the policy and pins the kind it's written against. The `when` block is a rule: when a critical alert comes in, page whoever is on call for the team. The next step is all about writing rules.

Embed the policy files in the binary, in `policies.go`:

```go
package alerting

import "embed"

//go:embed policies
var Policies embed.FS
```

## Load and evaluate

Load the policy once, at startup, and evaluate it for every alert. Create `cmd/route/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"example.com/alerting"
	"github.com/spechtlabs/sigil/pkg/policy"
)

var checkout = alerting.Team{Name: "checkout", Oncall: "checkout-primary", Channel: "#checkout-alerts"}

var alerts = []alerting.Alert{
	{Name: "CheckoutErrorRate", Severity: alerting.Critical, Labels: map[string]string{"env": "production"}, FiringFor: 2 * time.Minute},
	{Name: "CheckoutLatencyHigh", Severity: alerting.Warning, Labels: map[string]string{"env": "production"}, FiringFor: 12 * time.Minute},
	{Name: "CheckoutLatencyHigh", Severity: alerting.Warning, Labels: map[string]string{"env": "production"}, FiringFor: 45 * time.Minute},
	{Name: "CheckoutErrorRate", Severity: alerting.Critical, Labels: map[string]string{"env": "staging"}, FiringFor: 2 * time.Minute},
	{Name: "CheckoutCanaryLatency", Severity: alerting.Warning, Labels: map[string]string{"env": "production"}, FiringFor: 5 * time.Minute},
}

func main() {
	p, err := alerting.Kind.Load(alerting.Policies, "checkout.alerts")
	if err != nil {
		log.Fatal(err)
	}

	for _, a := range alerts {
		res, err := p.Eval(context.Background(), alerting.Input{Alert: a, Team: checkout})
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("%-22s %-8s %-10s %5s  → %s\n", a.Name, a.Severity, a.Labels["env"], a.FiringFor, route(res))
	}
}

func route(res *policy.Result) string {
	if page, ok := alerting.Page.Match(res); ok {
		return fmt.Sprintf("page %s (%s)", page.Target, res.Reason)
	}

	if note, ok := alerting.Notify.Match(res); ok {
		return fmt.Sprintf("post to %s (%s)", note.Channel, res.Reason)
	}

	return fmt.Sprintf("drop (%s)", res.Reason)
}
```

- `Kind.Load` reads every `.sigil` file in the embedded tree, type-checks them against the kind, and compiles the policy named `checkout.alerts`. The compiled policy is immutable and safe to share between goroutines.
- `Eval` takes the input as a Go value and returns the result, with the winning decision and a trace of every candidate.
- `Page.Match` returns the payload as a `PageData` when the decision is a page. No string comparisons, no maps.

Run it:

```text
$ go run ./cmd/route
CheckoutErrorRate      critical production  2m0s  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production 12m0s  → post to #alerts (unrouted)
CheckoutLatencyHigh    warning  production 45m0s  → post to #alerts (unrouted)
CheckoutErrorRate      critical staging     2m0s  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production  5m0s  → post to #alerts (unrouted)
```

The two critical alerts page the on-call. Everything else matches no rule, so the kind's default posts it to `#alerts`.

## Break it on purpose

Misspell a field in the policy, `alert.severty`, and run it again:

```text
$ go run ./cmd/route
2026/09/30 17:20:44 policies/checkout/alerts.sigil:3:12: error: unknown field "severty" on type Alert
  |
3 | when alert.severty == critical {
  |            ^^^^^^^
  = help: did you mean "severity"? Alert declares: name, severity, labels, firing_for
exit status 1
```

`Load` fails with every problem it finds, each with a position and a hint, before a single alert is evaluated. A policy that loads is one where every field, value, decision and payload exists in the kind.

## What you have

A Go program with a contract, one policy and a loop that acts on typed decisions. That's the whole integration; everything from here on is about the policies. The same steps with every option are in [Embed Sigil in a Go service](/guides/embed-go/), and [Handle failed evaluations](/guides/handle-errors/) covers what to do with the `err` from `Eval` in production.

Next: [Write policies](/getting-started/write-policies/).
