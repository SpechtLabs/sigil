# alertrouter: an alert router built on Sigil

alertrouter takes alerts from Alertmanager and asks the owning team's Sigil policy what to do with each firing one: page the on-call, post to a channel, or drop it. Every answer carries a reason, the target or channel, and a trace of the rules that led there. It's the alert router the [Getting Started path](../../docs/getting-started/overview.md) builds from an empty Go module, grown into a service you can start with one command.

The example is a separate Go module, `github.com/spechtlabs/sigil/examples/alert-routing`, that builds against the Sigil checkout it lives in, so its dependencies never reach the library's `go.mod`. It follows [deploygate](../deploy-gates/README.md)'s layout, libraries and observability stack; read either one first. Start with [Run it](#run-it); [Layout](#layout) and [How it is wired](#how-it-is-wired) map the directories and packages.

## What the example shows

- **A kind defined in Go.** `internal/routing` declares the `AlertRouting` kind with `policy.NewKind`: the alert and team inputs, the `Severity` enum, the `page`, `drop` and `notify` decisions, their precedence and the default. Its runnable `Example` is step 1 of the path. See [Define the input and evaluate a policy](../../docs/getting-started/define-the-input.md).
- **An exported kind file.** The service's own `sigilc` binary writes the kind to `policies/alert_routing.sigil`, and a test fails when that copy is stale. See [Export the kind](../../docs/guides/host-binary.md#export-the-kind).
- **Paging no team can switch off.** The platform's documents are embedded in the binary and passed to `policy.Require("platform.paging", policy.From(policies.Platform))`, so every team policy has to invoke the paging rules at the top level and can't bring its own copy. See [Require guardrails](../../docs/getting-started/require-guardrails.md) and [Trusted sources](../../docs/reference/bundles.md#trusted-sources).
- **Team policies from a directory.** The team bundle comes from a directory, a mounted ConfigMap in a cluster, and reloads in place; a bundle that doesn't compile never replaces the one that serves. See [Reload without an outage](../../docs/guides/configmaps.md#reload-without-an-outage).
- **No alert is lost.** An alert no team owns, one the router can't read and one whose evaluation failed all go to the kind's default, `notify(reason: unrouted)` to `#alerts`, and each is reported with its own status. The route endpoint's HTTP status says whose fault a failure is. See [Tell the failures apart](../../docs/guides/handle-errors.md#tell-the-failures-apart).
- **Typed matching.** The server reads results with `routing.Page.Match` and `routing.Notify.Match` and gets a `PageData` target or a `NotifyData` channel back, not a map. See [Typed matching](../../docs/reference/go-api.md#typed-matching).
- **An observability stack and a k6 suite.** Alloy, Mimir, Tempo, Loki, Pyroscope and one provisioned Grafana dashboard, plus six load-test shapes that check every answer while policies reload underneath.

## Run it

You need [mise](https://mise.jdx.dev/) and Docker. From the repository root:

```bash
cd examples/alert-routing
mise install
mise run up
```

The tasks in [`.mise.toml`](./.mise.toml) run from `examples/alert-routing/` and inherit tool versions from the root config; all commands below run from there too. From the repository root, use `mise -C examples/alert-routing run <task>`.

`up` builds the image and runs `docker compose up --build --wait`, which returns once alertrouter and every backend are ready. alertrouter counts as healthy once every team's policy has loaded: the image has no shell, so compose runs `/alertrouter healthcheck`, which asks the server's own `/readyz`. The stack publishes the same host ports as deploygate's, so run one stack at a time:

| Service | Image | URL | What's there |
| --- | --- | --- | --- |
| alertrouter | built from `Dockerfile` | <http://localhost:8080> | The API, `/healthz`, `/readyz` and `/metrics` |
| Alloy | `grafana/alloy:v1.20.0` | <http://localhost:12345>, OTLP `4317`/`4318` | Scrapes metrics, forwards traces and reads alertrouter's Docker logs |
| Tempo | `grafana/tempo:3.0.3` | <http://localhost:3200> | Trace API; explore traces through Grafana |
| Loki | `grafana/loki:3.7.8` | <http://localhost:3100> | Log API; explore routing logs through Grafana |
| Mimir | `grafana/mimir:3.2.1` | <http://localhost:9009/prometheus> | Prometheus-compatible query API for service and k6 metrics |
| Pyroscope | `grafana/pyroscope:2.3.1` | <http://localhost:4040> | CPU, memory, goroutine, mutex, blocking and leak profiles |
| Grafana | `grafana/grafana:13.2.2` | <http://localhost:3000/d/alertrouter> | The provisioned dashboard, no login needed |
| k6 | `grafana/k6:2.3.0` | No listening port | Optional load generator; runs with `mise run loadtest` |

Each backend runs as one process with filesystem storage in named volumes, and Mimir uses its classic ingestion path, so there's no Kafka and no object store. `mise run down` stops the stack and drops its volumes. The image reports the version `compose`.

### Without Docker

alertrouter runs on its own too. With no `--policies` it serves the team bundle embedded in the binary; pointing the flag at `policies/teams` serves the files in your checkout and reloads them when they change. The team directory, who is on call and which channel a team posts to, comes from the binary unless `--teams-file` names another. Spans are exported only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, so no collector is needed:

```bash
go run ./cmd/alertrouter serve --policies policies/teams --log-format console
```

`go run ./cmd/alertrouter version` prints `alertrouter dev`; release builds get their version from GoReleaser.

### Use the demo CLI

`demo-cli` stands in for Alertmanager and for an engineer asking why an alert went where it did. Named scenarios send the sample requests under [`requests/`](./requests), the same ones the test suites and the load test check, and print the decision:

```bash
mise run demo route checkout-sustained
```

```text
HTTP 200 OK
PAGE: sustained
Team: checkout
Policy: checkout.alerts
Target: checkout-primary
```

A warning that has fired for 12 minutes pages, because checkout lowered the platform's 30-minute threshold to 10. Add `--explain` for the trace:

```text
Trace:
  [winner] page: sustained
    Policy: platform.paging
    checkout/alerts.sigil:7:1 → platform/paging.sigil:12:3
    when in_production and alert.severity == warning and alert.firing_for >= 10m
    with {"target":"checkout-primary"}
  [candidate] notify: routine
    Policy: platform.routing
    checkout/alerts.sigil:9:1 → platform/routing.sigil:8:3
    with {"channel":"#checkout-alerts"}
```

The location is the call chain: the page rule lives in `platform.paging`, and runs because line 7 of the checkout policy invokes it. The notification fired too, but `page` outranks `notify`.

| Command | What it does |
| --- | --- |
| `mise run demo scenario list` | List the built-in route and webhook scenarios |
| `mise run demo route checkout-muted-critical` | Show that muting an alert never silences a critical page |
| `mise run demo route invalid-severity` | Send a severity the kind doesn't declare, and get a `422` |
| `mise run demo webhook webhook-mixed` | Deliver an Alertmanager batch with every kind of result |
| `mise run demo teams` | List the team directory |
| `mise run demo policy list` | List the loaded team policies, the bundle's source and fingerprint |
| `mise run demo policy reload` | Reload the team policies |
| `mise run demo status` | Check readiness |
| `mise run demo metric` | Print Prometheus metrics |

Without a scenario, `route` sends `checkout-critical` and `webhook` sends `webhook-mixed`. To describe an alert on the command line instead:

```bash
mise run demo route --team payments --name PaymentsLatencyHigh \
  --severity warning --label env=production --firing-for 7m
```

```text
HTTP 200 OK
PAGE: sustained
Team: payments
Policy: payments.alerts
Target: payments-primary
```

`--file` sends your own JSON, and `--file -` reads stdin. `--url` overrides `ALERTROUTER_URL`, which defaults to `http://localhost:8080`, and `--timeout` defaults to `10s`. `--json` prints the full response on stdout, error responses included. Run `mise run demo -- --help` for the rest.

The binary exits `0` for any `2xx`, whatever the policy decided: a drop is as much a route as a page. It exits `2` when a `422`, `500` or `503` carries a failed evaluation's fallback, so a script can tell a failed policy, whose alert still went to `#alerts`, from any other failure, which exits `1`. Build with `mise run build` and use `./bin/demo-cli` when you need those codes; `mise run demo` goes through `go run`, which turns any nonzero exit into `1`.

### Route one alert

`POST /api/v1/teams/{team}/route` routes one alert of the team in the path. `firing_for` is a duration string:

```bash
curl -s localhost:8080/api/v1/teams/checkout/route -H 'Content-Type: application/json' \
  -d '{"alert": {"name": "CheckoutLatencyHigh", "severity": "warning", "labels": {"env": "production"}, "firing_for": "4m"}}'
```

The answer is `200 OK`, formatted:

```json
{
  "team": "checkout",
  "policy": "checkout.alerts",
  "decision": "notify",
  "reason": "routine",
  "channel": "#checkout-alerts",
  "trace": [
    {
      "decision": "notify",
      "reason": "routine",
      "policy": "platform.routing",
      "location": "checkout/alerts.sigil:9:1 → platform/routing.sigil:8:3",
      "conditions": ["in_production and alert.severity == warning"],
      "payload": {"channel": "#checkout-alerts"},
      "winner": true
    }
  ]
}
```

`target` is set for a page and `channel` for a notification. Any decision is a `200`, a drop included. Everything else:

| Status | Meaning |
| --- | --- |
| `400 Bad Request` | The body isn't valid JSON, has an unknown field, or has a `firing_for` that doesn't parse |
| `404 Not Found` | The team isn't in the team directory; the advice lists the teams that are |
| `413 Request Entity Too Large` | The body is over 1 MiB |
| `422 Unprocessable Entity` | The alert can't be evaluated: an empty name, a severity the kind doesn't declare, a negative `firing_for`, or a failed input assert. An assert failure carries the fallback decision and `asserts` |
| `500 Internal Server Error` | The policy failed on a valid alert: a conflict, a failed outcome assert or a runtime error. The body holds the fallback, `notify` / `unrouted` to `#alerts`, with `error` and `conflict` or `asserts` |
| `503 Service Unavailable` | The evaluation ran past `--evaluation-timeout`, one second by default, and the body holds the fallback. Also the answer, with only an `error`, before the first bundle has loaded |
| `499` | The client closed the request during the evaluation. Nothing is written or dispatched, and nothing counts as a failure |

As in deploygate, the status says whose fault a failure is. A conflict answered with a `4xx` would never burn alertrouter's error budget, so it's a `500`; a timeout is alertrouter's failure to decide in time, so it's a `503`. The shipped policies declare no asserts, so their `422`s all come from the router's own checks. [Tell the failures apart](../../docs/guides/handle-errors.md#tell-the-failures-apart) explains the classification.

### Receive an Alertmanager webhook

`POST /api/v1/alerts` takes Alertmanager's webhook, version 4. Point a receiver at it:

```yaml
receivers:
  - name: alertrouter
    webhook_configs:
      - url: http://alertrouter:8080/api/v1/alerts
```

Each alert's `team` label picks the team, `alertname` and `severity` fill in the kind's alert, every label is passed through, and `firing_for` is the time since `startsAt`. The mixed scenario shows every result a batch can hold:

```bash
mise run demo webhook webhook-mixed
```

```text
HTTP 200 OK
Received 6, routed 2 by a team's policy

STATUS    ALERT                        TEAM      DECISION              DESTINATION
routed    LedgerReplicationLag         payments  notify: routine       #payments-ledger
routed    PaymentsAuthorizationErrors  payments  drop: not_production  -
unowned   SearchIndexStale             -         notify: unrouted      #alerts
unowned   NodeDiskFilling              -         notify: unrouted      #alerts
invalid   PaymentsWebhookBacklog       payments  notify: unrouted      #alerts
resolved  PaymentsLatencyHigh          -         -                     -

Not routed by a policy:
  SearchIndexStale (9e4c1f73a0b85d2e): team "search" isn't in the team directory
  NodeDiskFilling (b3806e5d2c9f147a): the alert has no team label
  PaymentsWebhookBacklog (f8a2d61c0e7b4935): alert PaymentsWebhookBacklog has the severity "urgent", which the AlertRouting kind doesn't declare
```

A `routed` alert went through its team's policy. An `unowned` alert has no `team` label, or one the directory doesn't list, and an `invalid` one can't be read; both go to the kind's default without an evaluation, and an unowned alert carries no team. A `failed` alert's evaluation failed and it got the fallback. A `resolved` alert is acknowledged and not evaluated. Every firing alert ends in a notification either way.

The same file with curl:

```bash
curl -s localhost:8080/api/v1/alerts -H 'Content-Type: application/json' \
  --data @requests/webhook-mixed.json
```

Excerpt from the response, a routed, an unowned and a resolved alert:

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
      "trace": [{"decision": "notify", "reason": "routine", "policy": "payments.alerts", "location": "payments/alerts.sigil:12:3", "winner": true}]
    },
    {
      "fingerprint": "9e4c1f73a0b85d2e",
      "alertname": "SearchIndexStale",
      "status": "unowned",
      "error": "team \"search\" isn't in the team directory",
      "policy": "",
      "decision": "notify",
      "reason": "unrouted",
      "channel": "#alerts",
      "trace": []
    },
    {"fingerprint": "07c9e4b2d5f31a68", "alertname": "PaymentsLatencyHigh", "status": "resolved"}
  ]
}
```

The webhook answers `200` once the batch is processed, whatever became of its alerts, because Alertmanager retries anything else and a retry would only route them again. It answers `400` for a payload that isn't a version 4 webhook or carries more than 1,000 alerts, `413` over 1 MiB, and `503` before the first bundle has loaded, which Alertmanager retries. Each alert's evaluation has its own deadline, so one slow alert can't use up the rest of the batch's time.

## Watch it reload

The compose stack bind-mounts `policies/teams` read-only as `/etc/alertrouter/policies`, so the policies alertrouter serves are the files in your checkout. It reloads when you ask, on `SIGHUP` (`docker compose kill -s SIGHUP alertrouter`), and whenever a poll finds that the bundle's content changed. Compose sets `ALERTROUTER_RELOAD_INTERVAL` to 5 seconds; the default is 30. The output below comes from the service under [Without Docker](#without-docker), whose source is `policies/teams`; in the stack it's `/etc/alertrouter/policies`.

Raise checkout's threshold from 10 minutes to 15 in `policies/teams/checkout/alerts.sigil`:

```sigil
paging(page_after: 15m)
```

Wait for the next poll or reload right away, then send the same 12-minute warning:

```bash
mise run demo policy reload
mise run demo route checkout-sustained
```

```text
HTTP 200 OK
NOTIFY: routine
Team: checkout
Policy: checkout.alerts
Channel: #checkout-alerts
```

Now try to get around the platform. Put the paging call under a condition:

```sigil
when alert.labels["service"] == "checkout-api" {
  paging(page_after: 10m)
}
```

The reload answers `500 Internal Server Error`, and the CLI prints the compiler's diagnostic from the error's cause:

```text
the AlertRouting policies from policies/teams don't load: checkout.alerts failed to compile, so the previous bundle keeps serving
  fix the diagnostics in the policies and reload
  run `sigilc check` on the policies directory to see the same diagnostics before deploying

checkout/alerts.sigil:8:3: error: platform.paging must be invoked unconditionally
  |
8 |   paging(page_after: 10m)
  |   ^^^^^^^^^^^^^^^^^^^^^^^
  = help: the host requires platform.paging for every AlertRouting policy; move the call to the top level
```

`paging(page_after: 2h)` fails the same way, with `page_after: 2h is above the maximum 1h`: the bounds are the platform's, so a huge threshold can't switch sustained paging off either. A team bundle that brings its own `platform.paging` doesn't help, because the Require reads it from the binary.

alertrouter keeps serving the bundle it loaded last, so `checkout-sustained` still gets `NOTIFY: routine`, and the failure shows up in the metrics and on the dashboard's reload panels:

```bash
mise run demo metric | grep alertrouter_policy_
```

`alertrouter_policy_last_reload_successful` is now `0`, while `alertrouter_policy_last_reload_timestamp_seconds` still holds the time of the bundle that serves, and `alertrouter_policy_reloads_total{result="failure"}` went up. The poller may have tried before you did, so it can be one more than the reloads you asked for. Alert on the gauge, not on the counter's rate: the poller reports a broken bundle once, so the rate goes back to zero while the old bundle still serves, and the gauge stays `0` until a load succeeds.

Put the file back the way it was and the next poll loads it: the gauge returns to `1`, and the fingerprint `mise run demo policy list` reports is the original one again. A bundle loads as a whole, so one team's typo stops every team's reload, never any team's routing.

## Observe it

The compose stack records every request alertrouter answers. Run a few demo commands or a load test, then open [alertrouter · LGTM](http://localhost:3000/d/alertrouter), provisioned from [`deploy/grafana/dashboards/alertrouter.json`](deploy/grafana/dashboards/alertrouter.json).

- **Mimir:** the top row shows service health and reload state. **Alert flow** follows alerts from received to routed, by outcome and destination, with pages, drops, channel notifications, alerts routed by fallback and the webhook batch size. **Sigil evaluation performance** shows evaluations per second, mean latency, percentiles and the share finishing within 100 µs. **Policy outcomes and observability** has decisions, evaluation errors by kind, HTTP status codes and latency, the loaded policies and reloads. **Team** filters the policy panels, and **Load run** the k6 row.
- **Tempo:** every firing alert gets an `alertrouter.route` span with `alert.name`, `alert.severity`, `alert.fingerprint`, `alertrouter.team`, `sigil.policy`, `sigil.decision`, `sigil.reason`, `sigil.candidates` and one `sigil.candidate` event per trace entry. A webhook's spans sit under its request span, and a failed evaluation marks its span as an error.
- **Loki:** JSON logs, with one `alert routed` line and one `notification dispatched` line per firing alert, both carrying the alert's `trace_id`. Expand a line and follow **View trace** to Tempo.
- **Pyroscope:** a CPU flame graph and a runtime profile you pick with **Runtime profile**: allocations, live heap, goroutines, mutex contention, blocking or leaks.

Alloy scrapes every five seconds and profiles upload every fifteen, so give the panels a moment.

### Load test it

The k6 suite in [`test/load/`](./test/load) sends alerts whose right answer it knows: the named cases in `requests/cases.json`, and generated alerts whose outcome `lib/rules.js` derives from the same rules the policies hold, checked against the live team directory at startup. A run passes only if alertrouter is fast enough and routes every alert correctly, while a third scenario keeps reloading the policies underneath.

`TEST_MODE` picks the shape; every mode except smoke runs single-alert routes and webhook batches side by side, splitting `RATE` by `WEBHOOK_SHARE`, plus the reload scenario:

| Mode | Shape | Length |
| --- | --- | --- |
| `smoke` | Every case in `requests/cases.json` once, five generated routes, five generated webhooks and one reload, on one VU | Under a minute |
| `load` | Constant arrival at `RATE` | `DURATION`, default `2m` |
| `stress` | A minute at `RATE`, a ramp to 2× and on to 5× over a minute each, a minute at 5×, and a ramp back to `RATE` | 5 minutes |
| `spike` | One minute at `RATE`, a 10-second jump to 10×, a minute there, 10 seconds back, two minutes at `RATE` to show it recovers | 4 minutes 20 seconds |
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
| `VUS` / `MAX_VUS` | `20` / `100` | Preallocated and maximum virtual users per routing scenario |
| `P95_MS` / `P99_MS` | `250` / `500` | Latency budget of a single-alert route, in milliseconds |
| `WEBHOOK_P95_MS` / `WEBHOOK_P99_MS` | `1000` / `2000` | Latency budget of a webhook batch |
| `SEED` | random | Seeds the alert generator; set it to replay a failing run |
| `RUN_ID` | mode and UTC time | Report filename and Mimir `testid` label |

A run fails when more than one alert in a thousand is routed wrong or fails a check, when 1% or more of requests fail, when a latency budget is breached, or when an iteration is dropped because every VU was still waiting. Smoke allows no wrong answer and no failed request at all. Besides k6's own metrics, the suite records `routing_correct`, `alerts_routed` and `decisions_page`, `decisions_drop` and `decisions_notify`, which the dashboard's k6 row plots next to reloads under load.

[`run.sh`](./test/load/run.sh) runs the pinned k6 image with `--no-deps`, sends its metrics to Mimir and writes `results/<RUN_ID>.json`: the configuration, the seed, the git commit and number of changed files, Docker's CPU and memory, and every threshold result. `results/` is ignored by Git. The numbers cover HTTP, JSON, evaluation, dispatch and telemetry with profiling on, on your machine; they measure this service, not Sigil's maximum throughput.

## Your own sigil binary: sigilc

`cmd/sigilc` is the stock `sigil` CLI with the kind linked in, one line of real code:

```go
cli.Main(cli.WithKind(routing.Kind), cli.WithVersion(version))
```

`sigilc test` decodes each input into `routing.Input`, so its answers are the service's, and `sigilc export` writes the kind file. `mise run generate` runs the `go generate` directive that rewrites `policies/alert_routing.sigil`. The kind has no host functions, so the stock `sigil` binary checks and tests these policies too; the [host binary guide](../../docs/guides/host-binary.md) covers when a host needs its own. `mise run policies` runs the check and the tests the way CI does.

Check every policy with the requirement and lint levels in [`policies/sigil.yaml`](./policies/sigil.yaml): `platform.paging` from `platform/` in every `*.alerts` root, and `gated-deny` and `path-matches-name` as errors:

```bash
mise run sigilc check --config policies/sigil.yaml policies
```

```text
✓ checked 6 files, no problems found
```

Run every test file:

```bash
mise run sigilc test policies
```

```text
ok    policies/teams/checkout/alerts_test.yaml  13 cases
ok    policies/teams/payments/alerts_test.yaml  11 cases
✓ 24 cases passed in 2 files
```

Flatten a team policy into the rules it adds up to, with every param replaced by its value:

```bash
mise run sigilc explain --policy checkout.alerts policies/platform policies/teams
```

```text
checkout.alerts: 6 rules from 3 policies and 1 module

  page(reason: critical_alert)  checkout.alerts:7 → platform.paging:8
    when in_production and alert.severity == critical
    with target = team.oncall

  page(reason: sustained)       checkout.alerts:7 → platform.paging:12
    when in_production and alert.severity == warning and alert.firing_for >= 10m
    with target = team.oncall

  notify(reason: routine)       checkout.alerts:9 → platform.routing:8
    when in_production and alert.severity == warning
    with channel = team.channel

  drop(reason: not_production)  checkout.alerts:9 → platform.routing:12
    when not in_production

  drop(reason: muted)           checkout.alerts:9 → platform.routing:16
    when alert.name in ["CheckoutCanaryLatency"]

  notify(reason: routine)       checkout.alerts:12
    when in_production and alert.severity == info and alert.labels["component"] == "payments"
    with channel = "#checkout-payments"
```

Fail when the checked-in kind file no longer matches the Go definition:

```bash
mise run sigilc export AlertRouting --check --out policies/alert_routing.sigil
```

```text
✓ policies/alert_routing.sigil is up to date
```

The flags are in the [CLI reference](../../docs/reference/cli.md).

## Tests

`mise run test` runs the first three layers with `go test -race ./...` and vets the fourth; none of them needs Docker.

- **Unit tests** are table-driven `testing` tests next to the code in `internal/` and `cmd/demo-cli`, plus one next to the dashboard that checks its layout and that every metric it queries exists. `internal/routing`'s runnable `Example` is the docs' first step.
- **Policy tests** run the `*_test.yaml` files from `go test` with `policytest.Run`, with `platform.paging` required from `policies/platform` as the service requires it, and `policytest.Schema` fails when the kind file is stale. `requests/embed_test.go` routes every sample in `requests/cases.json` through the embedded policies, so the samples the demo, the suites and k6 send can't drift from what the policies decide.
- **Integration tests** in `test/integration` are a Ginkgo suite that wires the real store, server, team directory and telemetry the way `cmd/alertrouter` does and serves them with `httptest`. Spans go to an in-memory exporter, metrics to a fresh registry and notifications to a recorder, so the specs assert exact values. It covers the whole wire contract, reload by polling, `SIGHUP` and request on a private copy of `policies/teams`, last-known-good with a broken bundle, the Require rejecting a team that skips, gates or shadows `platform.paging`, a slow policy's `503` and a departed client's `499`, the log lines, and that the embedded bundle decides like the directory it was built from.
- **End-to-end tests** in `test/e2e` are a Ginkgo suite behind the `e2e` build tag that talks to the compose stack over HTTP only: the API, reload by editing the mounted directory, a checkout policy swapped for one that outlasts the timeout, metrics in Mimir, spans in Tempo, logs in Loki whose trace IDs resolve in Tempo, profiles in Pyroscope and the dashboard in Grafana.

Both suites read their cases from `test/internal/fixture`, which loads `requests/cases.json`, so the in-process server and the container are checked against the same expectations. Run the end-to-end suite, which brings the stack up first, with:

```bash
mise run e2e
```

To point it at a stack running elsewhere, set `ALERTROUTER_URL`, `MIMIR_URL`, `TEMPO_URL`, `LOKI_URL`, `PYROSCOPE_URL` and `GRAFANA_URL`, and run `go test -count=1 -tags e2e ./test/e2e/...`. `-count=1` keeps `go test` from replaying a cached pass. The reload and timeout specs edit the team policies in `ALERTROUTER_POLICIES_DIR`, `policies/teams` by default, and put every byte back afterwards; they skip themselves when that directory isn't writable or the service serves its embedded bundle.

`mise run bench` measures the compiled policies through the store, one alert per decision, serial and in parallel, without HTTP or telemetry. `mise run check` runs every gate the examples CI job runs: lint, tests, the policy checks and the GoReleaser check. `mise run build` builds `alertrouter`, `demo-cli` and `sigilc` into `bin/`, and `mise run snapshot` builds them with the example's own [`.goreleaser.yaml`](./.goreleaser.yaml), which the root release doesn't use.

## Layout

| Path | What it holds |
| --- | --- |
| `cmd/alertrouter/` | The service: `serve`, `healthcheck` and `version` |
| `cmd/demo-cli/` | The CLI, run with `mise run demo` |
| `cmd/sigilc/` | The host's sigil binary, with `AlertRouting` linked in |
| `internal/routing/` | The `AlertRouting` kind |
| `internal/teams/` | The team directory and its embedded default, `teams.yaml` |
| `internal/alertmanager/` | The webhook payload and its conversion to the kind's alert |
| `internal/config/` | Flags, environment variables and their defaults |
| `internal/store/` | The loaded policies and hot reload |
| `internal/server/` | HTTP routes, handlers and the JSON error model |
| `internal/dispatch/` | The notifier each decision goes to |
| `internal/telemetry/` | Tracing, logging, metrics and profiles |
| `policies/` | The exported kind file, the platform's documents and each team's policy and tests; see [`policies/README.md`](./policies/README.md) |
| `requests/` | Sample route requests and webhooks, with their expected outcomes in `cases.json` |
| `test/integration/` | Ginkgo suite against the server in process |
| `test/e2e/` | Ginkgo suite against the running compose stack |
| `test/load/` | The k6 suite and its runner |
| `test/internal/fixture/` | Cases, expectations and a client both suites share |
| `deploy/` | Configuration for the compose services and the Grafana dashboard |
| `Dockerfile`, `docker-compose.yaml` | The image and the stack |

## How it is wired

### routing

`internal/routing` is the contract and nothing else. `Input` holds an `Alert` and a `Team`, with `policy:` tags naming what policies read. `Severity` is a named string registered as `enum Severity`, and `ParseSeverity` is how the router checks one before it evaluates. `Page`, `Drop` and `Notify` are the decision handles, with `PageData` and `NotifyData` payloads, and `Kind` ties them to the precedence `page > drop > notify` and the default `notify(reason: unrouted)`, whose channel defaults to `#alerts`.

### teams and alertmanager

`internal/teams` is the directory an alert's `team` label is looked up in: each team's on-call target and channel, so a rotation change is an edit to `teams.yaml` and not to a policy. Decoding is strict, with a did-you-mean hint for a misspelled key, because a typo would otherwise leave a team with an empty on-call that only shows up when a critical alert can't page anyone.

`internal/alertmanager` holds the version 4 webhook types and `Convert`, which reads the name, severity and team from their labels and derives `firing_for` from `startsAt`.

### store

`internal/store` holds the compiled policies. It's generic over the kind's input, like deploygate's, and `store.NewRouting` builds the one alertrouter runs, with a root per team in the directory:

```go
routing.Kind.Load(teamsFS, team+".alerts",
	policy.Require("platform.paging", policy.From(policies.Platform)))
```

The new set replaces the old with one atomic pointer store only when every root compiles, so in-flight evaluations finish on the bundle they started with. The platform's documents always come from the binary; there's no flag to point them elsewhere, because a guardrail an operator can swap isn't one. A failed first load fails startup, since there's no good bundle to fall back to. `Watch` reloads on `SIGHUP` and at each poll whose fingerprint of the `.sigil` files changed since the last attempt, successful or not, so a broken bundle is reported once.

### server and dispatch

`internal/server` is a gin router with recovery, OpenTelemetry, access log and request metrics middleware, built with `server.New(server.WithStore(st), server.WithDirectory(dir), ...)`. Both alert endpoints go through one path: look up the team, evaluate `<team>.alerts` under its own deadline, match the result, dispatch, count, log and trace. Probes and scrapes are neither traced nor logged. Errors without a decision are humane errors, rendered as `{"error": {"message": ..., "advice": [...], "cause": {...}}}`.

| Route | Does |
| --- | --- |
| `POST /api/v1/alerts` | Routes every firing alert of an Alertmanager webhook |
| `POST /api/v1/teams/{team}/route` | Routes one alert with that team's policy |
| `GET /api/v1/teams` | The team directory |
| `GET /api/v1/policies` | The loaded bundle: kind, version, source, fingerprint, `loaded_at` and one root per team |
| `POST /api/v1/policies/reload` | Reloads now; `500` with the diagnostics when the bundle doesn't compile |
| `GET /healthz`, `GET /readyz` | `200` once the process serves; `200` once the bundle is loaded, `503` before |
| `GET /metrics` | Prometheus text format |

`internal/dispatch` is where a decision leaves the service. The `Notifier` interface takes a `Notification`, and the example's `LogNotifier` writes one `notification dispatched` line per alert, drops included, where a real deployment would call PagerDuty or Slack.

### telemetry

`internal/telemetry` sets up the tracer provider from the standard OpenTelemetry variables, a zap logger that adds trace and span IDs to each line, Pyroscope profiling when `PYROSCOPE_SERVER_ADDRESS` is set, and these metrics on a registry the service owns:

| Metric | Type | Labels |
| --- | --- | --- |
| `alertrouter_alerts_received_total` | counter | `status`: `firing` or `resolved` |
| `alertrouter_alerts_routed_total` | counter | `team`, `outcome`: `routed`, `unowned`, `invalid` or `failed` |
| `alertrouter_decisions_total` | counter | `team`, `policy`, `decision`, `reason` |
| `alertrouter_evaluation_duration_seconds` | histogram | `team` |
| `alertrouter_evaluation_errors_total` | counter | `team`, `kind`: `assertion`, `runtime`, `conflict` or `timeout` |
| `alertrouter_notifications_total` | counter | `decision`, `destination`: the target, the channel, or `-` for a drop |
| `alertrouter_webhook_batch_size` | histogram | none |
| `alertrouter_policy_reloads_total` | counter | `result`: `success` or `failure` |
| `alertrouter_policy_last_reload_timestamp_seconds` | gauge | none; the last successful load |
| `alertrouter_policy_last_reload_successful` | gauge | none; `0` while the latest attempt failed |
| `alertrouter_policy_loaded_info` | gauge, always 1 | `team`, `policy`, `fingerprint`, `source` |
| `alertrouter_requests_total` | counter | `code`, `method`, `route` |
| `alertrouter_request_duration_seconds` | histogram | `method`, `route` |

Every label is bounded: `team` is `-` for an unowned alert, whatever its label said, and `route` is the route template. A failed evaluation made no decision, so it counts in `alertrouter_evaluation_errors_total` and not in `alertrouter_decisions_total`, and a `notify` / `unrouted` there is always one a policy chose.

Reloads run in an `alertrouter.policy.load` span with `alertrouter.reload.trigger` (`startup`, `manual`, `sighup` or `poll`), the first load sits under `alertrouter.startup`, and a graceful shutdown runs in `server.shutdown`.

### config

`internal/config` builds the command tree with cobra: `alertrouter serve`, `alertrouter healthcheck`, which probes `/readyz` on the loopback interface, and `alertrouter version`. Every flag of `serve` can also be set through an `ALERTROUTER_` variable, read with viper:

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `--addr` | `ALERTROUTER_ADDR` | `:8080` | Listen address for the API, health and metrics |
| `--policies` | `ALERTROUTER_POLICIES` | empty | Directory with the team policies; empty serves the bundle embedded in the binary |
| `--teams-file` | `ALERTROUTER_TEAMS_FILE` | empty | The team directory; empty serves the one embedded in the binary. Every team in it needs a `<team>.alerts` policy |
| `--reload-interval` | `ALERTROUTER_RELOAD_INTERVAL` | `30s` | How often to check the policies directory for changes; `0` turns polling off |
| `--shutdown-timeout` | `ALERTROUTER_SHUTDOWN_TIMEOUT` | `15s` | How long a graceful shutdown may take |
| `--evaluation-timeout` | `ALERTROUTER_EVALUATION_TIMEOUT` | `1s` | How long one alert's evaluation may take; past it the alert gets the fallback. Must be positive |
| `--debug` | `ALERTROUTER_DEBUG` | `false` | Debug logging and gin's debug mode |
| `--log-format` | `ALERTROUTER_LOG_FORMAT` | `json` | `json` or `console` |

### demo-cli

`cmd/demo-cli` follows the Sigil CLI's layout: `main.go` runs `command.NewCommand` through `command.Execute`, each subcommand has its own package with `With*` options, and the HTTP client, rendering and scenarios live under `cmd/demo-cli/internal/`. It decodes responses into the server's own types, so the two can't disagree on the wire format.

## Further reading

- [What Sigil is](../../docs/getting-started/overview.md) and the [tour](../../docs/getting-started/tour.md) read the same policies by hand, and the Getting Started steps build this router from scratch.
- [Per-team policies](../../docs/guides/team-policies.md) explains how team policies compose the platform's.
- [Policies in a ConfigMap](../../docs/guides/configmaps.md) shows how to ship the team bundle to a cluster.
- [Handle evaluation errors](../../docs/guides/handle-errors.md) covers the failure classes behind the status codes and metrics.
- [deploygate](../deploy-gates/README.md) is the other example service: deploy approvals, and a second kind that collects every decision.
