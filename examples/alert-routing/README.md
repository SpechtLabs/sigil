# alertrouter: an alert router in TypeScript on Sigil's WebAssembly engine

alertrouter takes alerts from Alertmanager and asks the owning team's Sigil policy what to do with each firing one: page the on-call, post to a channel, or drop it. Every answer carries a reason, the target or channel, and a trace of the rules that led there. It's the alert router the [Getting Started path](../../docs/getting-started/overview.md) builds in Go, grown into a service, and written in TypeScript.

The service is a Next.js application running on Node, with a shadcn/ui console, and its only policy engine is `@spechtlabs/sigil`: Sigil's Go engine compiled to WebAssembly, from [`bindings/typescript`](../../bindings/typescript). Go only builds `sigil.wasm`; nothing written in Go runs at run time. The policies, the decisions and the HTTP API are the ones a Go host would have, which is the point: it's the proof that the bindings carry a real service. Start with [Run it](#run-it); [Layout](#layout) and [How it is wired](#how-it-is-wired) map the code, and [`ARCHITECTURE.md`](./ARCHITECTURE.md) explains the composition root and the choices behind it.

## What the example shows

- **One engine from TypeScript.** `@spechtlabs/sigil` compiles `sigil.wasm` once at startup and runs it twice over: team policies evaluate in a pool of worker threads, each with its own instance, and the platform's page rules in a separate instance on the main thread that no team file reaches. See [Embed Sigil in TypeScript](../../docs/guides/embed-typescript.md), the [WebAssembly module reference](../../docs/reference/wasm.md) and [One engine for every host](../../docs/understanding/one-engine.md).
- **A kind defined in TypeScript.** `src/lib/routing/kind.ts` declares `AlertRouting` with the [kind builder](../../docs/reference/wasm.md#the-kind-builder): the alert and team inputs, the `Severity` enum, the `page`, `drop` and `notify` decisions, their precedence and the default. `bun run export-kind` writes it to `policies/alert_routing.sigil`, byte for byte what the Go host exported. See [The kind in TypeScript](#the-kind-in-typescript).
- **Paging no team can switch off.** The platform's documents are compiled into the app and passed to the engine as trusted files, and every team root is compiled with `require: [{ policy: "platform.paging" }]`. A team bundle that skips, gates or redefines the paging, or passes it a `page_after` out of bounds, doesn't load. See [Require guardrails](../../docs/getting-started/require-guardrails.md) and [Required policies](../../docs/reference/wasm.md#required-policies).
- **Team policies from a directory.** The team bundle comes from a mounted directory, a ConfigMap in a cluster, and reloads in place; a bundle that doesn't compile never replaces the one that serves. See [Reload without an outage](../../docs/guides/configmaps.md#reload-without-an-outage).
- **The platform's page always wins.** alertrouter evaluates `platform.paging` on its own for every alert a team owns. When the team's policy fails, times out, never runs, or decides anything but that page, the platform's page goes out; only when the platform doesn't page does the kind's default, a post to `#alerts`, cover a failure. See [Handle a failed evaluation](../../docs/guides/embed-typescript.md#handle-a-failed-evaluation) and [When things fail](#when-things-fail).
- **At-least-once delivery with deduplication.** A failed delivery makes the webhook answer `503`, so Alertmanager retries, and a redelivery within five minutes doesn't page twice.
- **A slow policy can't stall the service.** Evaluations run in worker threads with a 50 ms deadline, and one team gets at most half of them, so probes, the console and deliveries keep going whatever a team's policy does.
- **No batch rejected for one bad alert.** An unreadable alert, or one past the 1,000th, goes to the fallback; only a body that isn't a webhook at all is a `4xx`.
- **A console that previews in the browser.** The send form evaluates the alert with the same engine in a Web Worker before the server decides it, and shows both answers side by side.
- **An observability stack and a k6 suite.** Alloy, Mimir, Tempo, Loki, Pyroscope and one provisioned Grafana dashboard, plus six load-test shapes that check every answer while the policies reload, and break, underneath.

## Run it

You need [mise](https://mise.jdx.dev/) and Docker. From the repository root:

```bash
cd examples/alert-routing
mise install
mise run up
```

The tasks in [`.mise.toml`](./.mise.toml) run in `examples/alert-routing/` and inherit the tool versions (go, bun, node) from the root config; the commands below run from there too. From the repository root, use `mise run -C examples/alert-routing <task>`.

`up` builds the image and runs `docker compose up --build --wait`, which returns once alertrouter and every backend are ready. The image builds `sigil.wasm` and `@spechtlabs/sigil` itself, so the host needs nothing but Docker. alertrouter counts as healthy once every team's policy has loaded and the platform engine pages a canary alert: the distroless image has no shell, so compose runs `node healthcheck.mjs`, which asks the server's own `/readyz`. The stack publishes the same host ports as [deploygate](../deploy-gates/README.md)'s, so run one stack at a time:

| Service | Image | URL | What's there |
| --- | --- | --- | --- |
| alertrouter | built from `Dockerfile` | <http://localhost:8080> | The console, the API, `/healthz`, `/readyz` and `/metrics` |
| Alloy | `grafana/alloy:v1.20.0` | <http://localhost:12345>, OTLP `4317`/`4318` | Scrapes metrics, forwards traces and reads alertrouter's Docker logs |
| Tempo | `grafana/tempo:3.0.3` | <http://localhost:3200> | Trace API; explore traces through Grafana |
| Loki | `grafana/loki:3.7.8` | <http://localhost:3100> | Log API; explore routing logs through Grafana |
| Mimir | `grafana/mimir:3.2.1` | <http://localhost:9009/prometheus> | Prometheus-compatible query API for service and k6 metrics |
| Pyroscope | `grafana/pyroscope:2.3.1` | <http://localhost:4040> | Node's wall-clock, CPU and heap profiles |
| Grafana | `grafana/grafana:13.2.2` | <http://localhost:3000/d/alertrouter> | The provisioned dashboard, no login needed |
| k6 | `grafana/k6:2.3.0` | No listening port | Optional load generator; runs with `mise run loadtest` |

Each backend runs as one process with filesystem storage in named volumes. `mise run open` opens the console, and `mise run down` stops the stack and drops its volumes. The image reports the version `compose`.

### The console

<http://localhost:8080> serves the operator console next to the API, from the same Next.js process:

- **Overview** is a live feed of every firing alert the router decided, streamed over Server-Sent Events (`GET /api/v1/events`), with counters for pages, notifications, drops, fallbacks and undelivered alerts. Each row opens the alert's detail page, with the trace rendered readably: every candidate, the winner, the call chain and the conditions that held.
- **Send an alert** has a form for team, name, severity, `env` and `component` labels and firing time. As you type, the browser previews the decision: `@spechtlabs/sigil`'s worker helper loads `sigil.wasm` in a Web Worker and compiles the same kind file, team bundle and trusted platform documents the server compiled, fetched from `GET /api/v1/policies/files`. Sending posts the alert to the route endpoint, or as an Alertmanager webhook, and the server's answer, which is authoritative, is shown next to the preview with any difference called out.
- **Notifications** groups what the dispatcher sent by where it went: on-call targets, channels and drops.
- **Policies** shows the bundle that serves (source, fingerprint, load time), each team's policy flattened into its rules with `explain`, a reload button, and the last reload's error.
- **Teams** lists the team directory.

The history behind the feed is in memory and bounded (`ALERTROUTER_HISTORY_SIZE`, 500 by default), so it starts empty after a restart. At most `ALERTROUTER_MAX_EVENT_STREAMS` (32) browsers stream it at once, and a stream more than 256 events behind is closed; the browser reconnects and replays what it missed from the history.

### Without Docker

```bash
mise run dev
```

`dev` builds `sigil.wasm` and the package, installs, and runs Next's dev server on <http://localhost:8080> with hot reload. It serves the team policies in `policies/teams`, polls them every two seconds, logs in console format and exports nothing, so no collector is needed. Port 8080 is the stack's too: stop the stack first, or pass another port with `mise run dev -- --port 8091`. `mise run demo` and the curl commands below work against it the same way.

## Send alerts

`mise run demo` sends every sample under [`requests/`](./requests) to a running alertrouter, the single alerts to their team's route endpoint and the Alertmanager batches to the webhook, and checks each answer against `requests/cases.json`, the same expectations the tests and the load test use:

```text
✓ checkout-critical            200 page(critical_alert) checkout-primary
✓ checkout-warning             200 notify(routine) #checkout-alerts
✓ checkout-sustained           200 page(sustained) checkout-primary
✓ checkout-before-page-after   200 notify(routine) #checkout-alerts
✓ checkout-at-page-after       200 page(sustained) checkout-primary
✓ checkout-muted               200 drop(muted)
✓ checkout-muted-critical      200 page(critical_alert) checkout-primary
✓ checkout-staging             200 drop(not_production)
✓ checkout-no-env              200 page(critical_alert) checkout-primary
✓ checkout-payments-info       200 notify(routine) #checkout-payments
✓ checkout-unrouted            200 notify(unrouted) #alerts
✓ payments-critical            200 page(critical_alert) payments-primary
✓ payments-before-page-after   200 notify(routine) #payments-alerts
✓ payments-sustained           200 page(sustained) payments-primary
✓ payments-ledger              200 notify(routine) #payments-ledger
✓ payments-muted               200 drop(muted)
✓ payments-unknown-env         200 page(critical_alert) payments-primary
✓ unknown-team                 404 team "search" isn't in the team directory
✓ invalid-severity             422 alert.severity "urgent" isn't a severity
✓ webhook-checkout             200 4 received, 3 routed: page(critical_alert) checkout-primary, page(sustained) checkout-primary, notify(unrouted) #alerts, resolved
✓ webhook-mixed                200 6 received, 2 routed: notify(routine) #payments-ledger, drop(not_production), notify(unrouted) #alerts, notify(unrouted) #alerts, notify(unrouted) #alerts, resolved

21 of 21 cases answered as requests/cases.json expects
```

`checkout-no-env` and `payments-unknown-env` page: only an alert labelled `env: staging` or `env: dev` counts as pre-production, so a critical alert with a missing or unknown `env` fails loud rather than silent. `ALERTROUTER_URL` or `--url` points the demo elsewhere; it exits `1` when any answer is wrong.

### Route one alert

`POST /api/v1/teams/{team}/route` routes one alert of the team in the path. `firing_for` is a duration string:

```bash
curl -s localhost:8080/api/v1/teams/checkout/route -H 'Content-Type: application/json' \
  -d '{"alert": {"name": "CheckoutLatencyHigh", "severity": "warning", "labels": {"env": "production"}, "firing_for": "12m"}}'
```

The answer is `200 OK`, formatted:

```json
{
  "team": "checkout",
  "policy": "checkout.alerts",
  "decision": "page",
  "reason": "sustained",
  "target": "checkout-primary",
  "trace": [
    {
      "decision": "page",
      "reason": "sustained",
      "policy": "platform.paging",
      "location": "checkout/alerts.sigil:7:1 → platform/paging.sigil:12:3",
      "conditions": ["not pre_production and alert.severity == warning and alert.firing_for >= 10m"],
      "payload": {"target": "checkout-primary"},
      "winner": true
    },
    {
      "decision": "notify",
      "reason": "routine",
      "policy": "platform.routing",
      "location": "checkout/alerts.sigil:9:1 → platform/routing.sigil:8:3",
      "payload": {"channel": "#checkout-alerts"},
      "winner": false
    }
  ]
}
```

A warning that has fired for 12 minutes pages, because checkout lowered the platform's 30-minute threshold to 10. The location is the call chain: the page rule lives in `platform.paging` and runs because line 7 of the checkout policy invokes it. The notification fired too, but `page` outranks `notify`. `target` is set for a page and `channel` for a notification; any decision is a `200`, a drop included. Everything else:

| Status | Meaning |
| --- | --- |
| `400 Bad Request` | The body isn't valid JSON, has an unknown field, or has a `firing_for` that doesn't parse |
| `404 Not Found` | The team isn't in the team directory; the advice lists the teams that are |
| `413 Content Too Large` | The body is over 4 MiB |
| `422 Unprocessable Entity` | The alert can't be evaluated: an empty name, a severity the kind doesn't declare, a negative `firing_for`, or a failed input assert |
| `500 Internal Server Error` | The policy failed on a valid alert: a conflict, a failed outcome assert or a runtime error. Or it decided something other than the page the platform owes, a guardrail violation. The body holds the platform's page or the fallback, with `error` and `conflict` or `asserts` |
| `503 Service Unavailable` | The evaluation ran past `ALERTROUTER_EVALUATION_TIMEOUT`, 50 ms by default, and the body holds the platform's page or the fallback. Also the answer when the platform engine is being replaced (the alert still goes out, with the team's decision), and, with only an `error`, before the first bundle has loaded and during shutdown |
| `499` | The client closed the request during the evaluation. Nothing is dispatched, and nothing counts as a failure |

The status says whose fault a failure is: a conflict is alertrouter's policy failing, so it's a `500` and burns the error budget, and a timeout is a `503`. [Tell the failures apart](../../docs/guides/handle-errors.md#tell-the-failures-apart) explains the classification.

### Receive an Alertmanager webhook

`POST /api/v1/alerts` takes Alertmanager's webhook, version 4. Point a receiver at it:

```yaml
receivers:
  - name: alertrouter
    webhook_configs:
      - url: http://alertrouter:8080/api/v1/alerts
```

Each alert's `team` label picks the team, `alertname` and `severity` fill in the kind's alert, every label is passed through, and `firing_for` is the time since `startsAt`:

```bash
curl -s localhost:8080/api/v1/alerts -H 'Content-Type: application/json' \
  --data @requests/webhook-mixed.json
```

Excerpt from the `200 OK`, a routed, an unowned, an invalid and a resolved alert:

```json
{
  "received": 6,
  "routed": 2,
  "results": [
    {
      "fingerprint": "1a7f4c2e9b6d3085",
      "alertname": "LedgerReplicationLag",
      "status": "routed",
      "team": "payments",
      "policy": "payments.alerts",
      "decision": "notify",
      "reason": "routine",
      "channel": "#payments-ledger",
      "trace": [{"decision": "notify", "reason": "routine", "policy": "payments.alerts", "location": "payments/alerts.sigil:12:3", "conditions": ["not pre_production and alert.severity == info and alert.labels[\"component\"] == \"ledger\""], "payload": {"channel": "#payments-ledger"}, "winner": true}]
    },
    {
      "fingerprint": "9e4c1f73a0b85d2e",
      "alertname": "SearchIndexStale",
      "status": "unowned",
      "error": "team \"search\" isn't in the team directory",
      "decision": "notify",
      "reason": "unrouted",
      "channel": "#alerts",
      "trace": []
    },
    {
      "fingerprint": "f8a2d61c0e7b4935",
      "alertname": "PaymentsWebhookBacklog",
      "status": "invalid",
      "error": "alert PaymentsWebhookBacklog has the severity \"urgent\", which the AlertRouting kind doesn't declare",
      "team": "payments",
      "decision": "notify",
      "reason": "unrouted",
      "channel": "#alerts",
      "trace": []
    },
    {"fingerprint": "07c9e4b2d5f31a68", "alertname": "PaymentsLatencyHigh", "status": "resolved"}
  ]
}
```

A `routed` alert went through its team's policy, and `routed` counts only those. An `unowned` alert has no `team` label, or one the directory doesn't list, and carries no team; it goes to the kind's default. An `invalid` one can't be read, an unknown `status` included; when its team and severity can be read, it gets the platform's page if the platform pages, and the kind's default otherwise. `PaymentsWebhookBacklog`'s severity, `urgent`, isn't one the kind declares, so it gets the default. Neither carries a `policy`, because none ran. A `failed` alert's evaluation failed, never ran, or decided against the platform's page, and a `dispatch_failed` one was decided but not delivered in time. A `resolved` alert is acknowledged and not evaluated. Every firing alert ends in a notification attempt either way.

The results keep the webhook's order. The alerts themselves finish in the pool in whatever order they finish, so their log lines, spans and console entries come in that order.

The webhook answers `200` once every decision went out, whatever the decisions were. It answers `503` when a page or notification failed to go out or was still under way when the answer was due, when the platform engine was being replaced, before the first bundle has loaded, and during shutdown; Alertmanager retries a `5xx`, and deduplication keeps the retry from paging twice. It answers `400` only for a body that isn't a version 4 webhook, and `413` over 4 MiB.

## Watch it reload

The compose stack bind-mounts `policies/teams` read-only as `/etc/alertrouter/policies`, so the policies alertrouter serves are the files in your checkout. It reloads on `POST /api/v1/policies/reload`, on `SIGHUP` (`docker compose kill -s SIGHUP alertrouter`), and whenever a poll finds the bundle's content changed: every 5 seconds in compose, every 30 by default. The source in the messages below is the stack's mount point; under `mise run dev` it's the absolute path of `policies/teams`.

Raise checkout's threshold from 10 minutes to 15 in `policies/teams/checkout/alerts.sigil`:

```sigil
paging(page_after: 15m)
```

Reload right away instead of waiting for the poll, then send the same 12-minute warning:

```bash
curl -s -X POST localhost:8080/api/v1/policies/reload
curl -s localhost:8080/api/v1/teams/checkout/route -H 'Content-Type: application/json' \
  --data @requests/checkout-sustained.json
```

The reload answers with the new bundle's fingerprint, and the warning now notifies: `"decision": "notify"`, `"reason": "routine"`, `"channel": "#checkout-alerts"`.

Now try to get around the platform. Put the paging call under a condition:

```sigil
when alert.labels["service"] == "checkout-api" {
  paging(page_after: 10m)
}
```

The reload answers `500 Internal Server Error`, with the compiler's diagnostic as the cause:

```json
{
  "error": {
    "message": "the AlertRouting policies from /etc/alertrouter/policies don't load: checkout.alerts failed to compile, so the previous bundle keeps serving",
    "advice": [
      "fix the diagnostics in the policies and reload",
      "run `sigil check --config policies/sigil.yaml policies` on the policies to see the same diagnostics before deploying"
    ],
    "cause": {
      "message": "the policy doesn't compile, so nothing was compiled\ncheckout/alerts.sigil:8:3: platform.paging must be invoked unconditionally (the host requires platform.paging for every AlertRouting policy; move the call to the top level)"
    }
  }
}
```

`paging(page_after: 2h)` fails the same way, with `page_after: 2h is above the maximum 1h`: the bounds are the platform's, so a huge threshold can't switch sustained paging off either. A team file that defines its own `platform.paging` doesn't help, because the required policy must come from the trusted files.

alertrouter keeps serving the bundle it loaded last, so `checkout-sustained` still notifies, and the failure shows in the metrics and on the dashboard's reload panels:

```bash
curl -s localhost:8080/metrics | grep '^alertrouter_policy_'
```

`alertrouter_policy_last_reload_successful` is now `0`, `alertrouter_policy_last_reload_timestamp_seconds` still holds the time of the bundle that serves, and `alertrouter_policy_reloads_total{result="failure"}` went up. Alert on the gauge, not the counter's rate: the poller reports a broken bundle once, so the rate goes back to zero while the old bundle still serves. Put the file back and the next poll loads it: the gauge returns to `1`, and `GET /api/v1/policies` reports the original fingerprint again, since the fingerprint is of the content, not the path. A bundle loads as a whole, so one team's typo stops every team's reload, never any team's routing.

## When things fail

The Go version of this service answered some failures in ways a reviewer showed could lose a page or a batch. This one deliberately differs there; [`ARCHITECTURE.md`](./ARCHITECTURE.md#where-the-wire-differs-from-the-go-service) lists every difference.

- **The platform's page always wins.** For every alert a team owns, the router evaluates `platform.paging` in the platform engine, next to the team's policy in the pool. When the team's evaluation fails (a conflict, an assert, a runtime error, a timeout) or never runs (the batch deadline, the alert limit), the platform's page goes out if the platform pages: status `failed`, the platform's reason and target, the failure still logged, counted and marked on the span. Only when the platform doesn't page does the kind's default apply. A team rule that pages `checkout-secondary` for critical alerts conflicts with the platform's page, and the route endpoint answers `500` with the platform's page in the decision fields. Excerpt, without the trace and the `conflict` that lists both candidates:

  ```json
  {
    "team": "checkout",
    "policy": "checkout.alerts",
    "decision": "page",
    "reason": "critical_alert",
    "target": "checkout-primary",
    "error": {
      "message": "checkout.alerts produced decisions that can't stand together: collect one: 2 candidates at the top rank",
      "advice": [
        "the alert was routed with platform.paging's own page, which the decision fields hold; it pages with the platform's default page_after, not the team's",
        "a conflict is a defect in the policy, not in the alert, such as a team rule that pages someone else than the platform's; tell the policy's owners"
      ]
    }
  }
  ```

  The webhook reports the same alert as `failed` with that page and still answers `200`, because the page went out.

  A team policy that evaluates cleanly but decides anything other than a page to the platform's target doesn't get through either. Say checkout adds a rule that pages `nobody` for its sustained warnings, with the higher-ranked reason `critical_alert`, which outranks the platform's `sustained` page instead of conflicting with it:

  ```sigil
  when alert.severity == warning and alert.firing_for >= 10m {
    page(reason: critical_alert, target: "nobody")
  }
  ```

  The 12-minute warning from `requests/checkout-sustained.json` still pages `checkout-primary`, with a `500`. Excerpt, without the trace, where the team's page is the winner:

  ```json
  {
    "team": "checkout",
    "policy": "checkout.alerts",
    "decision": "page",
    "reason": "sustained",
    "target": "checkout-primary",
    "error": {
      "message": "checkout.alerts decided page(reason: critical_alert, target: nobody) for an alert platform.paging pages checkout-primary for (reason: sustained)",
      "advice": [
        "the alert was routed with platform.paging's page, which the decision fields hold",
        "a team policy may add pages, never replace the platform's; tell the owners of checkout.alerts"
      ]
    }
  }
  ```

  The check reads the platform's page from the team's own trace, so it holds at the team's `page_after`, here 10 minutes. The alert is `failed`, and the violation is counted in `alertrouter_guardrail_violations_total{team}`, logged at error level as `guardrail violated: the platform's page replaced the team's decision` with the team's decision and the platform's reason and target, and added to the span as an `alertrouter.guardrail_violation` event. A team may add pages, never replace the platform's. An `invalid` alert whose team and severity can be read gets the platform's page too.

  The routing path is [`src/lib/http/route.ts`](./src/lib/http/route.ts), and the platform engine [`src/lib/engine/platform.ts`](./src/lib/engine/platform.ts).
- **The platform engine stands apart.** `platform.paging` runs in a Sigil instance of its own on the main thread, compiled from the platform's documents; no team file reaches it, so nothing a team writes can break the page it computes. `/readyz` evaluates a canary alert through it. When the instance fails, it's replaced from the compiled module, and meanwhile `/readyz` answers `503` and both endpoints answer `503` while still delivering with the team's decision, so Alertmanager sends the batch again. After three failed replacements in a row the process exits: a router that can't compute the platform's page mustn't look healthy.
- **A slow policy stays in the pool.** Team policies evaluate in `ALERTROUTER_WORKERS` worker threads ([`src/lib/engine/pool.ts`](./src/lib/engine/pool.ts)), under a 50 ms deadline, and one team may occupy at most half of them. A policy that loops or crawls holds a worker until its deadline, not the thread that answers probes, streams the console and delivers notifications, and a worker that doesn't answer shortly after the deadline is replaced.
- **A batch has a deadline.** `ALERTROUTER_BATCH_TIMEOUT` (10 seconds) bounds the answer to a webhook, counted from its arrival and well inside Alertmanager's receiver timeout. An alert whose evaluation hasn't started in the first half takes the platform's page or the fallback and counts as `evaluation_errors_total{kind="timeout"}`. Every decided alert's delivery starts and runs to its end; the deadline only bounds how long the answer waits. A delivery still under way when the answer is due makes the alert `dispatch_failed` and the webhook `503`, and the retry is deduplicated once the delivery finishes. See `webhook` in [`src/lib/http/handlers.ts`](./src/lib/http/handlers.ts).
- **A failed delivery is retried.** A notifier error gives the alert status `dispatch_failed`, counts in `alertrouter_notification_errors_total{decision}`, and makes the webhook answer `503`, so Alertmanager redelivers the group. The route endpoint answers `200` with the error in the body, as the Go service did.
- **Delivery is at least once, deduplicated.** Deliveries of a batch run concurrently, at most `ALERTROUTER_DISPATCH_CONCURRENCY` at a time. The [dispatcher](./src/lib/dispatch/dispatcher.ts) remembers each delivery by fingerprint, decision, reason and destination for `ALERTROUTER_DEDUP_TTL` (5 minutes), so a redelivered webhook doesn't page twice; send `requests/webhook-checkout.json` twice and `alertrouter_notifications_deduplicated_total` goes up by two pages and one notification. The table holds at most `ALERTROUTER_DEDUP_MAX_ENTRIES` (100,000) deliveries and forgets the oldest first, counted in `alertrouter_notifications_dedup_evicted_total`. It isn't exactly once: an alert delivered and then lost with the process, forgotten early, or redelivered after the TTL, goes out again.
- **No batch is rejected for one bad alert.** Alertmanager drops a group on a `4xx`. An alert with an unknown status, labels that aren't strings or an unreadable `startsAt` is `invalid`, and a `startsAt` so far back that the duration overflows is clamped rather than invalid. Alerts past the 1,000th of a webhook still get the platform's page or the fallback and are delivered, without a span, log line or console entry each: one warn line per batch and `alertrouter_alerts_truncated_total{by="alertrouter"}` report them. Alertmanager's own `truncatedAlerts` is logged and counted as `{by="alertmanager"}`.

## Observe it

The compose stack records every request alertrouter answers. Run the demo or a load test, then open [alertrouter · LGTM](http://localhost:3000/d/alertrouter), provisioned from [`deploy/grafana/dashboards/alertrouter.json`](deploy/grafana/dashboards/alertrouter.json).

- **Mimir:** the top row shows service health and reload state. **Alert flow** follows alerts from received to routed, by outcome and destination, with pages, drops, channel notifications, alerts routed by fallback and the webhook batch size. **Dispatch** shows failed and deduplicated deliveries and alerts cut off by a batch limit. **Guardrail and engine** shows guardrail violations by team, Sigil engine restarts, whether the platform engine is up, deduplication evictions, and console event streams closed for falling behind or over the limit. **Sigil evaluation performance** has evaluations per second, mean latency, percentiles and the share within 100 µs. **Policy outcomes and observability** has decisions, evaluation errors by kind, HTTP status codes and latency, the loaded policies and reloads, plus links into Tempo, Loki and Pyroscope. **Node.js runtime** shows event loop lag, V8 heap and external memory, garbage collection, and active handles and requests. The event loop lag is the main thread's, where HTTP, the platform engine and dispatch run; team evaluations in the workers don't hold it. **Team** filters the policy panels, and **Load run** the k6 row.
- **Tempo:** every firing alert gets an `alertrouter.route` span with `alert.name`, `alert.severity`, `alert.fingerprint`, `alertrouter.team`, `sigil.policy`, `sigil.decision`, `sigil.reason`, `sigil.candidates` and one `sigil.candidate` event per trace entry, under the webhook's HTTP server span. A failed evaluation marks its span as an error, and a guardrail violation adds an `alertrouter.guardrail_violation` event. A batch's route spans, like its log lines, come in the order its alerts finish in the pool, not the webhook's. Reloads run in `alertrouter.policy.load` with `alertrouter.reload.trigger` (`startup`, `manual`, `sighup` or `poll`).
- **Loki:** JSON logs from pino, with one `alert routed` line and one `notification dispatched` line per firing alert, both carrying the alert's `trace_id` and `span_id`. Expand a line and follow **View trace** to Tempo.
- **Pyroscope:** `@pyroscope/nodejs` uploads a wall-clock profile with each sample's CPU time, which the CPU panel shows (`wall:cpu`), and the sampled live heap (`memory:inuse_space` and `memory:inuse_objects`), which **Runtime profile** picks. Node has no goroutine, mutex or block profiles. The profiler samples only the main thread, so team evaluations in the workers don't show in the flame graph; the process CPU and memory panels still count them. The SDK uploads the heap only on its 15-second interval, never on shutdown, so a process that lived less than 15 seconds has no heap profile.

Alloy scrapes every five seconds and profiles upload every fifteen, so give the panels a moment.

## Load test it

The k6 suite in [`test/load/`](./test/load) sends alerts whose right answer it knows: the named cases in `requests/cases.json`, including each team's `page_after − 1s` and `page_after` boundaries, and generated alerts whose outcome `lib/rules.js` derives from the same rules the policies hold, checked against the live team directory at startup. A run passes only if alertrouter is fast enough and routes every alert correctly while a third scenario keeps reloading the policies.

`TEST_MODE` picks the shape; every mode except smoke runs single-alert routes and webhook batches side by side, splitting `RATE` by `WEBHOOK_SHARE`, plus the reload scenario:

| Mode | Shape | Length |
| --- | --- | --- |
| `smoke` | Every case in `requests/cases.json` once, five generated routes, five generated webhooks and one reload, on one VU | Under a minute |
| `load` | Constant arrival at `RATE` | `DURATION`, default `2m` |
| `stress` | A minute at `RATE`, a ramp to 2× and on to 5× over a minute each, a minute at 5×, and a ramp back to `RATE` | 5 minutes |
| `spike` | A minute at `RATE`, a 10-second jump to 10×, a minute there, 10 seconds back, two minutes at `RATE` to show it recovers | 4 minutes 20 seconds |
| `soak` | Constant arrival at `RATE`, for leaks and slow drift | `DURATION`, default `1h` |
| `breakpoint` | A ramp from `RATE` to `BREAKPOINT_MAX_RATE`; from 30 seconds in, the first failing threshold aborts the run, and the rate it reached is the capacity | Up to `DURATION`, default `10m` |

Each mode has a task, which starts the stack first:

```bash
mise run loadtest-smoke
mise run loadtest                  # TEST_MODE, default load
DURATION=15m RATE=200 mise run loadtest
mise run loadtest-stress
mise run loadtest-spike
mise run loadtest-soak
mise run loadtest-breakpoint
```

The stress, spike and breakpoint tasks raise `VUS`/`MAX_VUS` to `50`/`400`, `50`/`600` and `100`/`1000`. To load a stack that's already running without rebuilding it, add `--skip-deps`: `mise run --skip-deps loadtest`.

| Setting | Default | Meaning |
| --- | --- | --- |
| `RATE` | `100` | Requests per second the routing scenarios start at |
| `DURATION` | `2m`, `1h` for soak, `10m` for breakpoint | How long load and soak hold `RATE`, and how long breakpoint climbs |
| `BREAKPOINT_MAX_RATE` | 50 × `RATE` | Where breakpoint ends if nothing gives first |
| `WEBHOOK_SHARE` | `0.3` | Fraction of requests that are webhook batches; the rest route one alert |
| `MAX_BATCH` | `100` | Largest webhook batch; each has 1 to `MAX_BATCH` alerts |
| `RELOADS_PER_MINUTE` / `RELOAD_CONCURRENCY` | `6` / `2` | Reload rounds per minute, and reloads sent at once in each |
| `BAD_BUNDLE_EVERY` | `15`, `0` in smoke | Seconds between breaking the mounted bundle and repairing it; `0` turns it off |
| `VUS` / `MAX_VUS` | `20` / `100` | Preallocated and maximum virtual users per routing scenario |
| `P95_MS` / `P99_MS` | `250` / `500` | Latency budget of a single-alert route, in milliseconds |
| `WEBHOOK_P95_MS` / `WEBHOOK_P99_MS` | `1000` / `2000` | Latency budget of a webhook batch |
| `DISPATCH_FAILURES` | `0` | `1` accepts `dispatch_failed` and the webhook's `503`, for a notifier that fails on purpose; the one alertrouter ships never does |
| `SEED` | random | Seeds the alert generator; set it to replay a failing run |
| `RUN_ID` | mode and UTC time | Report filename and Mimir `testid` label |

Outside smoke, [`run.sh`](./test/load/run.sh) alternates the served bundle between good and broken: every `BAD_BUNDLE_EVERY` seconds it writes `policies/teams/loadtest-broken.sigil`, a document that doesn't parse, into the mounted directory, and as long again later removes it. alertrouter has to reject each broken bundle and keep routing with the last good one, and the run fails unless it saw both accepted and rejected reloads. The file is removed however the run ends; don't edit `policies/teams` during a run.

A run fails when more than one alert in a thousand is routed wrong or fails a check, when 1% or more of requests fail, when a latency budget is breached, or when an iteration is dropped because every VU was still waiting. Smoke allows no wrong answer and no failed request. Besides k6's own metrics, the suite records `routing_correct`, `alerts_routed`, `decisions_page`, `decisions_drop` and `decisions_notify`, which the dashboard's k6 row plots next to reloads under load. `run.sh` runs the pinned k6 image with `--no-deps`, sends its metrics to Mimir and writes `results/<RUN_ID>.json`: the configuration, the seed, the git commit and number of changed files, Docker's CPU and memory, and every threshold result. The numbers cover HTTP, JSON, the WebAssembly boundary, dispatch and telemetry with profiling on, on your machine; they measure this service, not Sigil's maximum throughput.

## The kind in TypeScript

The kind is TypeScript data, built with `@spechtlabs/sigil`'s builder in [`src/lib/routing/kind.ts`](./src/lib/routing/kind.ts):

```ts
export const Page = decision("page", ["critical_alert", "sustained"], { target: t.string });
export const Drop = decision("drop", ["muted", "not_production"]);
export const Notify = decision("notify", ["routine", "unrouted"], { channel: t.string.default(DEFAULT_CHANNEL) });

export const AlertRouting = defineKind("AlertRouting", {
  version: 1,
  inputs: { alert: AlertType, team: TeamType },
  decisions: [Page, Drop, Notify],
  reasonPrecedence: [Page, Drop, Notify],
  default: Unrouted,
});

export type Input = InputOf<typeof AlertRouting>;
```

Decisions are listed in precedence order, so a page beats a drop beats a notification, and muting an alert never silences a page. Reason names and payload fields are literal types: `Page.reason("sustaned")` doesn't type-check, and `Page.match(res)` returns `{ target: string } | undefined`.

`AlertRouting.schema()` is the kind file. `bun run export-kind` writes it to `policies/alert_routing.sigil` for the tools that run without this code: `sigil check`, `sigil test` and the browser preview. `mise run generate` exports it and regenerates `src/lib/embedded.ts`, the platform documents, default team bundle and team directory compiled into the app. `src/lib/routing/kind.test.ts` fails when the file is stale, and because the committed file is the one the Go host's `sigilc export` wrote, it also proves the TypeScript kind is byte for byte the Go kind. The kind has no host functions, so the stock `sigil` CLI checks and tests these policies; there's no `sigilc` here.

## Tests

`mise run test` runs `bun test`, which covers the unit, integration and policy tests while the e2e suite skips itself, and the profiler's upload test under `node --test`, because the Pyroscope SDK's native profiler needs V8 and doesn't load under Bun. None of them needs Docker.

- **Unit tests** are `bun:test` files next to the code in `src/`, table-driven with `test.each`, plus one next to the dashboard that checks its layout and that every `alertrouter_*` series and label it queries exists and every exported metric is on a panel.
- **Integration tests** in `test/integration` wire the real store, handlers, team directory and telemetry the way the composition root does, and serve them over HTTP with `Bun.serve`. Spans go to an in-memory exporter, metrics to a fresh registry, logs to memory and notifications to a recorder, so the specs assert exact values. They cover the whole wire contract, reload by poll, `SIGHUP` and request on a private copy of `policies/teams`, last-known-good with a broken bundle, the guardrail rejecting a team that skips, gates or redefines `platform.paging`, compiled policies released on reload, the platform's page for a conflict, an input assert, an outcome assert, a runtime error, a timeout and a guardrail violation, a team bundle that crashes its engine, the platform engine failing, being replaced and giving up, batch deadlines, dispatch failures and deduplication, and the log lines.
- **Policy tests** in `test/policies` run every `*_test.yaml` through `@spechtlabs/sigil`, with `platform.paging` required from the platform's documents as the service requires it, and check the kind file and `src/lib/embedded.ts` against their sources. `mise run policies` runs the same gates with the stock CLI, so the two engines can't drift apart:

  ```text
  ✓ checked 6 files, no problems found
  ok    policies/teams/checkout/alerts_test.yaml  15 cases
  ok    policies/teams/payments/alerts_test.yaml  13 cases
  ✓ 28 cases passed in 2 files
  ```

- **End-to-end tests** in `test/e2e` talk to the compose stack over HTTP only: the API, reload by editing the mounted directory, a policy that outlasts the timeout, a client that leaves mid-evaluation, metrics in Mimir, spans in Tempo, logs in Loki whose trace IDs resolve in Tempo, profiles in Pyroscope and the dashboard in Grafana. They're part of `bun test` but skip themselves unless `ALERTROUTER_E2E=1`.

The integration and e2e suites read their cases from `test/fixture`, which loads `requests/cases.json`, so the in-process server and the container are checked against the same expectations. Run the e2e suite, which brings the stack up first, with:

```bash
mise run e2e
```

To point it at a stack running elsewhere, set `ALERTROUTER_URL`, `MIMIR_URL`, `TEMPO_URL`, `LOKI_URL`, `PYROSCOPE_URL` and `GRAFANA_URL` with `ALERTROUTER_E2E=1` and run `bun test test/e2e`. The reload and timeout specs edit the team policies in `ALERTROUTER_POLICIES_DIR`, `policies/teams` by default: each edit is written to a dot file and renamed into place, so a reload never reads half a policy, and every byte is put back afterwards. They skip themselves when the directory isn't writable or the service serves its embedded bundle. Because they edit the directory the other suites read, don't run `mise run check` or the integration tests while the e2e suite runs.

The console has a Playwright smoke test in `test/ui`: it sends an alert from the form, checks the preview matches the server, and finds it in the feed, the inbox and its detail page. Install Chromium into the project once with `bun run ui:install`, then run `bun run test:ui`; it starts `next dev` on port 3100 with the embedded policies, or tests the stack when `ALERTROUTER_URL` is set.

`mise run check` runs every gate CI runs on the example: `lint` (Biome), `typecheck` (`tsc --noEmit`), `test`, `policies` and `build`, which writes the standalone server to `.next/standalone/`.

## Layout

| Path | What it holds |
| --- | --- |
| `src/instrumentation.ts` | Next's startup hook, which boots the service once per server process |
| `src/server/` | `boot.ts` (environment, telemetry, signals), `service.ts` (the composition root), the registry and the route adapter |
| `src/app/` | The console's pages and the App Router route files for the API, health and metrics |
| `src/components/` | The console's components; `ui/` holds the vendored shadcn/ui primitives |
| `src/lib/routing/` | The `AlertRouting` kind |
| `src/lib/teams/` | The team directory and its default, `teams.yaml` |
| `src/lib/alertmanager/` | The webhook payload and its conversion to the kind's alert |
| `src/lib/config/` | `ALERTROUTER_*` settings, their defaults and their checks |
| `src/lib/engine/` | The platform engine and the evaluation pool of worker threads |
| `src/lib/store/` | The compiled team policies, with `platform.paging` required, and hot reload |
| `src/lib/http/` | The handlers, the routing path, error rendering and the JSON wire types |
| `src/lib/dispatch/` | The notifier, the deduplicating dispatcher and the console's history |
| `src/lib/telemetry/` | Tracing, logging, metrics and profiles |
| `src/lib/ui/` | The console's client logic: the form, the preview and the history views |
| `src/lib/embedded.ts` | Generated: the platform documents, default team bundle and team directory |
| `policies/` | The exported kind file, the platform's documents and each team's policy and tests; see [`policies/README.md`](./policies/README.md) |
| `requests/` | Sample route requests and webhooks, with their expected outcomes in `cases.json` |
| `scripts/` | `export-kind`, `embed`, `demo`, `copy-wasm` and `vendor-sigil` (postinstall) |
| `bin/` | The container's entry point and health check, plain Node scripts |
| `test/integration/`, `test/e2e/`, `test/policies/`, `test/ui/` | The suites above; `test/fixture/` holds what they share |
| `test/load/` | The k6 suite and its runner |
| `deploy/` | Configuration for the compose services and the Grafana dashboard |
| `Dockerfile`, `docker-compose.yaml` | The image and the stack |

## How it is wired

Nothing is created when a module is imported. Next calls `register()` in `src/instrumentation.ts` once per server process; `boot()` reads the environment, sets up telemetry, compiles `sigil.wasm` once, builds the service with `createService()` and installs it in the registry, the one global. Every route file is a thin adapter that hands the request to `Api.fetch`, which routes it, reports it (server span, access log line, request metrics) and calls the framework-free handlers. The integration tests call the same `fetch`, without Next.

**routing** is the contract and nothing else: the kind, its decision handles and `parseSeverity`, which the router checks before it evaluates.

**teams and alertmanager.** The team directory maps a `team` label to its on-call target and channel, so a rotation change is an edit to `teams.yaml` and not to a policy; decoding is strict, with a did-you-mean hint for a misspelled key. The webhook module reads the name, severity and team from the labels and derives `firing_for` from `startsAt`, per alert, so one unreadable alert is `invalid` and the rest of the batch still routes.

**engine** holds the two kinds of Sigil instance, each instantiated from the one compiled module, so replacing one takes milliseconds. The platform engine runs `platform.paging` alone on the main thread and reports itself up or down (`alertrouter_engine_up{engine="platform"}`). The evaluation pool is `ALERTROUTER_WORKERS` worker threads, each running the package's `SigilWorker` with every team's policy; a task goes to the first free worker, one team may hold at most half of them, and a worker that fails or hangs is replaced and compiles its policies again by itself (`alertrouter_engine_restarts_total{engine}`).

**store** compiles one root per team, `<team>.alerts`, in every worker, with `platform.paging` required from the trusted platform documents, and swaps the new set in only when every root compiles. Requests take a lease on the snapshot they started with; a replaced snapshot's compiled policies are released inside the workers once its last lease is done, so reloads don't leak WebAssembly memory. A failed first load fails startup, and `/readyz` never turns `200`. Polls fingerprint the directory's `.sigil` files and reload only when that changed since the last attempt, so a broken bundle is reported once.

**http and dispatch.** Both alert endpoints go through one path in `route.ts`: look up the team, evaluate `<team>.alerts` in the pool under its deadline and `platform.paging` in the platform engine, let the platform's page win, fall back to the kind's default when neither decided, dispatch, count, log and trace. The dispatcher delivers through a `Notifier`; the example's `LogNotifier` writes one `notification dispatched` line per alert, drops included, where a real deployment would call PagerDuty or Slack. Errors without a decision render as `{"error": {"message": ..., "advice": [...], "cause": {...}}}`.

| Route | Does |
| --- | --- |
| `POST /api/v1/alerts` | Routes every firing alert of an Alertmanager webhook |
| `POST /api/v1/teams/{team}/route` | Routes one alert with that team's policy |
| `GET /api/v1/teams` | The team directory |
| `GET /api/v1/policies` | The loaded bundle: kind, version, source, fingerprint, `loaded_at` and one root per team |
| `POST /api/v1/policies/reload` | Reloads now; `500` with the diagnostics when the bundle doesn't compile |
| `GET /api/v1/policies/files` | What the server compiled, for the browser preview, with the last reload error |
| `GET /api/v1/policies/{team}/explain` | The team's policy flattened into its rules |
| `GET /api/v1/history`, `GET /api/v1/events` | The console's history, and its live stream over Server-Sent Events, at most `ALERTROUTER_MAX_EVENT_STREAMS` at once |
| `GET /healthz`, `GET /readyz` | `200` once the process serves; `200` once the bundle is loaded and the platform engine pages its canary, `503` before, while the engine is replaced, and during shutdown |
| `GET /metrics` | Prometheus text format, with Node's process metrics |

**telemetry** sets up the OpenTelemetry tracer from the standard `OTEL_*` variables (spans are exported only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set), pino with the trace and span IDs on each line, `prom-client` on a registry the service owns, and Pyroscope when `PYROSCOPE_SERVER_ADDRESS` is set. The metric names, labels and buckets are the Go service's, listed in [`src/lib/telemetry/types.ts`](./src/lib/telemetry/types.ts), plus these:

| Metric | Type | Labels |
| --- | --- | --- |
| `alertrouter_guardrail_violations_total` | counter | `team` |
| `alertrouter_notification_errors_total` | counter | `decision` |
| `alertrouter_notifications_deduplicated_total` | counter | `decision` |
| `alertrouter_notifications_dedup_evicted_total` | counter | none |
| `alertrouter_alerts_truncated_total` | counter | `by`: `alertrouter` or `alertmanager` |
| `alertrouter_engine_restarts_total` | counter | `engine`: `platform` or `worker` |
| `alertrouter_engine_up` | gauge | `engine`: `platform` |
| `alertrouter_event_streams_closed_total` | counter | `reason`: `slow` or `limit` |

 Every label is bounded: `team` is `-` for an unowned alert, `route` is the route template, and `destination` is `other` unless the team directory or the kind names it.

**config.** Every setting is an environment variable, checked at startup with advice on what to set. Durations use Go's syntax (`30s`, `1m30s`):

| Variable | Default | Meaning |
| --- | --- | --- |
| `ALERTROUTER_ADDR` | `:8080` | Listen address of the container's one port; `next dev` takes `--port` instead |
| `ALERTROUTER_POLICIES` | empty | Team policies directory; empty serves the bundle compiled into the app |
| `ALERTROUTER_TEAMS_FILE` | empty | Team directory; empty serves the one compiled into the app. Every team needs a `<team>.alerts` policy |
| `ALERTROUTER_RELOAD_INTERVAL` | `30s` | How often the policies directory is polled; `0` turns polling off |
| `ALERTROUTER_SHUTDOWN_TIMEOUT` | `15s` | How long a graceful shutdown waits for requests in flight |
| `ALERTROUTER_EVALUATION_TIMEOUT` | `50ms` | How long one alert's evaluation may take; past it the alert takes the platform's page or the fallback. The Go service's default was `1s`; the policies take microseconds, and a slow one holds a worker |
| `ALERTROUTER_WORKERS` | 2 to 4, by CPUs | Worker threads that evaluate team policies; one team gets at most half |
| `ALERTROUTER_BATCH_TIMEOUT` | `10s` | How long the answer to a webhook may take, from its arrival; evaluations must start in the first half |
| `ALERTROUTER_DEDUP_TTL` | `5m` | How long a delivery is remembered; `0` turns deduplication off |
| `ALERTROUTER_DEDUP_MAX_ENTRIES` | `100000` | Deliveries deduplication remembers at most, oldest forgotten first |
| `ALERTROUTER_DISPATCH_CONCURRENCY` | `16` | Deliveries in flight at once |
| `ALERTROUTER_HISTORY_SIZE` | `500` | Routed alerts the console keeps |
| `ALERTROUTER_MAX_EVENT_STREAMS` | `32` | Console event streams open at once; `0` refuses them all |
| `ALERTROUTER_LOG_FORMAT` | `json` | `json` or `console` |
| `ALERTROUTER_DEBUG` | `false` | Debug logging |

## Differences from a Go host

- **The runtime.** A Go host evaluates on whatever goroutine serves the request. Node runs one event loop, and an evaluation is a synchronous call into a module that can't be preempted, so it enforces its deadline itself by checking the clock as it goes ([why](../../docs/understanding/one-engine.md#why-the-module-enforces-its-own-deadline)). alertrouter therefore evaluates team policies in worker threads, which it can replace from outside when one hangs, and keeps only `platform.paging`, which it controls, on the main thread. The browser preview does the same with a Web Worker.
- **The boundary.** Every evaluation crosses the WebAssembly boundary as JSON: about 36 µs under Node against about 5 µs for the same work in a Go process, and loading the module costs about 33 ms at startup. See [Through WebAssembly](../../docs/reference/performance.md#through-webassembly). HTTP, JSON and telemetry still cost more per request than the evaluation.
- **Handles and instances.** Compiled policies live inside a module until released, where Go's garbage collector would take them; the store releases a snapshot's policies in every worker once no request holds it. A module that stops, on a trap or when Go code runs out of stack, can't be resumed, so the engines replace their instance.
- **Trusted files.** Go separates the platform's documents structurally with `policy.From`; through the module both lists are strings, so trust comes from the list, a path in both fails, and a required policy the trusted files don't define fails instead of falling back to the bundle's copy. See [Why trusted files are stricter than `policy.From`](../../docs/understanding/one-engine.md#why-trusted-files-are-stricter-than-policyfrom).
- **The failure paths.** The platform's page winning over a failed or deviating team policy, the batch deadline, `dispatch_failed` with a `503`, deduplication, and no `4xx` for one bad alert are deliberate changes from the Go service, listed in [`ARCHITECTURE.md`](./ARCHITECTURE.md#where-the-wire-differs-from-the-go-service). The rest of the wire is the Go service's.
- **Profiles and process metrics** are Node's: wall-clock, CPU and heap profiles and V8's heap and event loop, all of the main thread only, where the Go service had goroutine, mutex and block profiles and Go's runtime metrics.

## Further reading

- [Embed Sigil in TypeScript](../../docs/guides/embed-typescript.md) builds the core of this service step by step, and the [WebAssembly module reference](../../docs/reference/wasm.md) lists every export of the package.
- [What Sigil is](../../docs/getting-started/overview.md) and the [tour](../../docs/getting-started/tour.md) read the same policies by hand, and the Getting Started steps build this router in Go.
- [Per-team policies](../../docs/guides/team-policies.md) explains how team policies compose the platform's, and [Policies in a ConfigMap](../../docs/guides/configmaps.md) how to ship the team bundle to a cluster.
- [Handle evaluation errors](../../docs/guides/handle-errors.md) covers the failure classes behind the status codes and metrics.
- [deploygate](../deploy-gates/README.md) is the Go example service: deploy approvals, and a second kind that collects every decision.
