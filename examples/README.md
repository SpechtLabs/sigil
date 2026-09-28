# deploygate: a Go service built on Sigil

deploygate is a small deploy approval API. A client posts the release it wants to ship, and deploygate evaluates the team's Sigil policy and answers with a decision: approve, review or deny, each with a reason, a typed payload and a trace of how the policy got there. It's the running example from the [documentation](../docs/getting-started/tour.md), turned into a service you can start with one command.

The example is a separate Go module, `github.com/spechtlabs/sigil/examples`, that builds against the Sigil checkout it lives in. Its dependencies never reach the library's `go.mod`.

## What the example shows

- **A kind defined in Go.** `internal/deploy` declares the `DeployApproval` kind with `policy.NewKind`: the input structs, the decisions and their reasons, the precedence, the default and the `split` host function. See the [Go API](../docs/reference/go-api.md#defining-a-kind).
- **An exported kind file.** The service's own `sigilc` binary writes the kind to `policies/deploy_approval.sigil`, and a test fails when that copy is stale. See [Exporting the kind](../docs/reference/go-api.md#exporting-the-kind).
- **Guardrails no team can remove.** The platform's documents are embedded in the binary and passed to `policy.Require("deploy.guardrails", policy.From(platformFS))`, so a team policy has to invoke the guardrails unconditionally and can't redefine them. See [Where required policies come from](../docs/reference/go-api.md#where-required-policies-come-from).
- **Team policies loaded from a directory.** Each team's policy comes from a directory, a mounted ConfigMap in a cluster, and reloads in place. A bundle that doesn't compile never replaces the one that serves. See [Policies in a ConfigMap](../docs/guides/configmaps.md) and [Hot reload](../docs/reference/go-api.md#hot-reload).
- **Typed matching.** The handler matches the result with `deploy.Review.Match` and `deploy.Approve.Match` and gets `ReviewData` and `ApproveData` back, not a map. See [Typed matching](../docs/reference/go-api.md#typed-matching).
- **Metrics and traces per decision.** Every evaluation counts its decision and reason in Prometheus and records a span with the decision and one event per trace candidate, so you can see in Grafana and Jaeger why a deploy was held.

## Layout

```text
examples/
  README.md                  this walkthrough
  go.mod, go.sum             the examples module
  .goreleaser.yaml           builds deploygate and sigilc; not part of the root release
  Dockerfile                 builds the deploygate image
  docker-compose.yaml        deploygate, OpenTelemetry Collector, Jaeger, Prometheus, Grafana
  deploy/                    configuration for the compose services
  cmd/
    deploygate/              the service: `serve`, `healthcheck` and `version`
    sigilc/                  the host's sigil binary, with DeployApproval linked in
  internal/
    config/                  flags, environment variables and their defaults
    deploy/                  the DeployApproval kind
    server/                  HTTP routes, handlers and the JSON error model
    store/                   the loaded policies and hot reload
    telemetry/               tracing, logging and Prometheus metrics
  policies/
    deploy_approval.sigil    the exported kind file, generated
    embed.go                 embeds platform/ and teams/ into the binary
    platform/deploy/         the platform's trusted documents
    teams/<team>/            each team's policy, its test cases and their inputs
  test/
    integration/             Ginkgo suite against the server in-process
    e2e/                     Ginkgo suite against the running compose stack
    internal/fixture/        requests, expected decisions and helpers both suites share
```

[`policies/README.md`](./policies/README.md) describes the policy tree in detail: who owns which file, and how to add a team.

## Run it

You need [mise](https://mise.jdx.dev/) and Docker. From the repository root:

```bash
mise install
mise run examples-up
```

`examples-up` builds the image and runs `docker compose up --build --wait`, which returns once every service is up. deploygate counts as healthy once its team bundle has loaded: the image has no shell, so compose runs `/deploygate healthcheck`, which asks the server's own `/readyz` and exits 0 on `200`. The stack publishes these ports:

| Service | Image | URL | What's there |
| --- | --- | --- | --- |
| deploygate | built from `Dockerfile` | <http://localhost:8080> | The API, `/healthz`, `/readyz` and `/metrics` |
| OpenTelemetry Collector | `otel/opentelemetry-collector-contrib:0.161.0` | `localhost:4317`, `localhost:4318` | OTLP over gRPC and HTTP; forwards the traces to Jaeger |
| Jaeger | `jaegertracing/jaeger:2.20.0` | <http://localhost:16686> | Traces, one per request, with a `deploygate.evaluate` span |
| Prometheus | `prom/prometheus:v3.15.0` | <http://localhost:9090> | The scraped `deploygate_*` metrics |
| Grafana | `grafana/grafana:13.2.2` | <http://localhost:3000> | The `deploygate` dashboard, no login needed |

`mise run examples-down` stops the stack and drops its volumes.

The compose image is built with the version `compose`:

```bash
docker compose exec deploygate /deploygate version
```

```text
deploygate compose
```

### Without Docker

deploygate runs on its own too. With no `--policies` it serves the team bundle embedded in the binary; pointing it at `policies/teams` serves the files in your checkout and reloads them when they change. Turn tracing off when no collector is listening:

```bash
OTEL_TRACES_EXPORTER=none go run ./cmd/deploygate serve \
  --policies policies/teams --log-format console
```

`go run ./cmd/deploygate version` prints `deploygate dev`; release builds get their version from GoReleaser.

The requests below run from `examples/` and post the inputs the policy tests use, so you can compare the answers with `policies/teams/*/production_test.yaml`.

### A review

A payments engineer ships a PCI-scoped service their team owns:

```bash
curl -si -X POST localhost:8080/api/v1/teams/payments/deployments \
  -H 'Content-Type: application/json' \
  -d @policies/teams/payments/testdata/owner.json
```

deploygate answers `202 Accepted`. The body, formatted:

```json
{
  "team": "payments",
  "policy": "payments.production",
  "decision": "review",
  "reason": "service_owner",
  "payload": {"approvers": ["payments-leads", "security-leads"]},
  "trace": [
    {
      "decision": "review",
      "reason": "service_owner",
      "policy": "deploy.production",
      "location": "payments/production.sigil:10:3 → deploy/production.sigil:16:5",
      "conditions": [
        "service.labels[\"compliance\"] == \"pci\"",
        "cleared",
        "service.tier in [\"standard\", \"internal\"] and owns_service"
      ],
      "payload": {"approvers": ["payments-leads", "security-leads"]},
      "winner": true
    }
  ]
}
```

The review comes from the platform's `deploy.production`, reached through the call on line 10 of the payments policy, and `conditions` lists every `when` that held on the way. Durations on the wire are strings in Sigil syntax, such as `"6h"` or `"15m"`, in requests and payloads alike.

The HTTP status encodes the decision, so a client can act on the status alone:

| Status | Meaning |
| --- | --- |
| `200 OK` | `approve`, with the `bake` time in the payload |
| `202 Accepted` | `review`, with the `approvers` in the payload |
| `403 Forbidden` | `deny` |
| `422 Unprocessable Entity` | The evaluation failed. The decision fields hold the kind's default, `deny` / `no_rule_matched`, and `error` says what went wrong |
| `400 Bad Request` | The body isn't valid JSON, has an unknown field, or has a duration that doesn't parse, is too long for a Go duration or, for `release.soak`, is negative |
| `404 Not Found` | deploygate doesn't serve that team |
| `503 Service Unavailable` | No bundle is loaded yet |

### A deny

The same engineer ships a release that soaked for two hours. The payments policy lowers the platform's minimum soak to four hours, so the guardrail denies it:

```bash
curl -s -X POST localhost:8080/api/v1/teams/payments/deployments \
  -H 'Content-Type: application/json' \
  -d @policies/teams/payments/testdata/short-soak.json \
  | jq '{decision, reason, trace: [.trace[] | {decision, reason, policy, winner}]}'
```

The status is `403 Forbidden`:

```json
{
  "decision": "deny",
  "reason": "soak_too_short",
  "trace": [
    {"decision": "deny", "reason": "soak_too_short", "policy": "deploy.guardrails", "winner": true},
    {"decision": "review", "reason": "service_owner", "policy": "deploy.production", "winner": false}
  ]
}
```

The review still fired, and the trace shows it, but the kind ranks `deny` above `review`, so the guardrail wins.

### A failed assert

The checkout policy asserts that every request names its actor. A request with an empty `actor.name` doesn't get a decision from the rules at all:

```bash
curl -s -X POST localhost:8080/api/v1/teams/checkout/deployments \
  -H 'Content-Type: application/json' \
  -d @policies/teams/checkout/testdata/unnamed-actor.json \
  | jq '{decision, reason, asserts}'
```

The status is `422 Unprocessable Entity`, and the decision is the kind's default, which is what a host that fails closed acts on:

```json
{
  "decision": "deny",
  "reason": "no_rule_matched",
  "asserts": [
    {"reason": "named_actor", "policy": "checkout.production", "location": "checkout/production.sigil:6:1"}
  ]
}
```

The full body also carries an `error` object, in the same shape as every other error the API returns:

```json
{
  "message": "the request fails checkout.production's asserts: named_actor",
  "advice": [
    "the decision fields hold the fallback decision; act on it",
    "fix the request so the asserts listed in asserts hold, then ask again"
  ],
  "cause": {"message": "assertion \"named_actor\" failed at checkout/production.sigil:6:1"}
}
```

## Watch it reload

The compose stack bind-mounts `policies/teams` into the container as `/etc/deploygate/policies`, so the team policies deploygate serves are the files in your checkout. It reloads them when you ask, on `SIGHUP`, and whenever a poll finds that their content changed. Compose sets `DEPLOYGATE_RELOAD_INTERVAL` to 5 seconds so you can watch it happen; the default is 30.

Change the payments SRE bake from 15 minutes to 30 in `policies/teams/payments/production.sigil`:

```sigil
when cleared and "payments-sre" in actor.teams {
  approve(payments_sre, bake: 30m)
}
```

Wait for the next poll or reload right away, then ask for an SRE deploy:

```bash
curl -s -X POST localhost:8080/api/v1/policies/reload | jq .loaded_at

curl -s -X POST localhost:8080/api/v1/teams/payments/deployments \
  -H 'Content-Type: application/json' \
  -d @policies/teams/payments/testdata/sre.json | jq .payload
```

```json
{"bake": "30m"}
```

Now break the file. Delete the closing `}` of the last `when`, and reload again:

```bash
curl -s -X POST localhost:8080/api/v1/policies/reload | jq -r '.error.message, .error.cause.message'
```

The reload answers `500 Internal Server Error`. The error's message says what happened, and its cause holds the compiler's diagnostics, pointing at the file, line and column:

```text
the team policies from /etc/deploygate/policies don't load: payments.production failed to compile, so the previous bundle keeps serving
```

deploygate keeps serving the bundle it loaded last, so the SRE deploy above still answers `{"bake": "30m"}`, and `GET /api/v1/policies` still reports the earlier `loaded_at`. The failure shows up in the metrics, and on the dashboard's "Failed reloads" and "Policy reloads" panels:

```bash
curl -s localhost:8080/metrics | grep deploygate_policy_reloads_total
```

The `result="failure"` series has gone up. The poller may have tried before you did, so it can be one more than the reloads you asked for; it reports a broken bundle once, though, not at every poll. Alert on that counter: while it's rising, the running policy is stale. Put the `}` back, set the bake to `15m` again and reload, and the success counter moves instead. The same thing happens when a whole broken document lands in the directory, because a bundle loads as a whole: one team's typo stops every team's reload, never every team's deploys.

## Your own sigil binary: sigilc

The stock `sigil` CLI checks policies against the exported kind file, but it can't run Go code, so it can't evaluate a rule that calls `split`. A host builds its own binary with the [`cli` package](../docs/reference/cli.md#host-functions-and-host-binaries) and links its kinds in. `cmd/sigilc` is one line of real code:

```go
cli.Main(cli.WithKind(deploy.Kind), cli.WithVersion(version))
```

`sigilc` then decodes test inputs into `deploy.Input` and calls the real `strings.Split`, so its answers are the service's answers. It also writes the kind file. `cmd/sigilc/main.go` carries the `go generate` directive, and `mise run examples-generate` runs it:

```bash
go generate ./cmd/sigilc
```

Run these from `examples/`. `mise run examples-policies` runs the check and the tests, the way the examples CI job does.

Check every team policy against the kind, with the platform's documents as the trusted source, the same `Require` the service makes, and the lint levels from `policies/sigil.yaml`:

```bash
go run ./cmd/sigilc check --config policies/sigil.yaml \
  --require deploy.guardrails --trusted policies/platform \
  --policy 'payments.*' --policy 'checkout.*' -R policies/teams
```

Run every `*_test.yaml` under `policies/`:

```bash
go run ./cmd/sigilc test policies
```

```text
ok    policies/teams/checkout/production_test.yaml  9 cases
ok    policies/teams/payments/production_test.yaml  16 cases
```

Flatten a team policy into the rules it adds up to, with each invocation's conditions pushed into the rules and every param replaced by its value:

```bash
go run ./cmd/sigilc explain --policy payments.production -R policies
```

```text
payments.production: 7 rules from 4 policies

deny     not_eligible      payments.production:7 → guardrails:8
         not eligible

deny     soak_too_short    payments.production:7 → guardrails:12
         release.soak < 4h and not release.hotfix

approve  release_manager   payments.production:10 → deploy.production:11
         service.labels["compliance"] == "pci"
         and cleared
         and service.tier == "critical" and "release_manager" in actor.roles

review   service_owner     payments.production:10 → deploy.production:16
         service.labels["compliance"] == "pci"
         and cleared
         and service.tier in ["standard", "internal"] and owns_service
         approvers = ["payments-leads", "security-leads"]

approve  release_manager   payments.production:14 → deploy.production:11
         service.labels["compliance"] != "pci"
         and cleared
         and service.tier == "critical" and "release_manager" in actor.roles

review   service_owner     payments.production:14 → deploy.production:16
         service.labels["compliance"] != "pci"
         and cleared
         and service.tier in ["standard", "internal"] and owns_service
         approvers = ["payments-leads"]

approve  payments_sre      payments.production:18
         cleared and "payments-sre" in actor.teams
         bake = 15m
```

Fail when the checked-in kind file no longer matches the Go definition:

```bash
go run ./cmd/sigilc export --check --out policies/deploy_approval.sigil
```

The flags are described in the [CLI reference](../docs/reference/cli.md).

## Tests

There are four layers, from fastest to slowest. `mise run examples-test` runs the first three with `go test -race ./...`, and vets the fourth; none of them needs Docker.

- **Unit tests** are table-driven `testing` tests next to the code in `internal/`.
- **Policy tests** run the same `*_test.yaml` files `sigilc test` runs, from `go test`, with `policytest.Run` and the real kind. `internal/deploy/kind_test.go` loads them the way the service does, with the guardrails required from `policies/platform`, and `policytest.Schema` fails when the exported kind file is stale.
- **Integration tests** in `test/integration` are a Ginkgo suite that wires the real store and server the way `cmd/deploygate` does and serves them with `httptest`. Spans go to an in-memory exporter and metrics to a fresh registry per spec, so the specs assert exact values: every span attribute and candidate event, each counter after a known set of requests. A fake clock pins `loaded_at` and fires polls by hand, so the reload specs cover polling, `SIGHUP` and last-known-good without waiting, on a private copy of `policies/teams`. The suite also checks that the embedded teams bundle decides every case the same way as the directory it was built from.
- **End-to-end tests** in `test/e2e` are a Ginkgo suite behind the `e2e` build tag that talks to the compose stack over HTTP only, and checks that Prometheus scraped deploygate and that Jaeger stored its spans.

Both suites run the same table of requests and expected decisions from `test/internal/fixture`, so the in-process server and the container can't drift apart. Run the end-to-end suite with:

```bash
mise run examples-e2e
```

It brings the stack up first. To point the suite at a stack that's already running elsewhere, set `DEPLOYGATE_URL`, `JAEGER_URL` and `PROMETHEUS_URL`, and run this from `examples/`:

```bash
go test -count=1 -tags e2e ./test/e2e/...
```

`-count=1` matters: the suite checks the stack, not the code, and without it `go test` would replay a cached pass from an earlier run. The hot reload specs edit the team policies in `DEPLOYGATE_POLICIES_DIR`, which defaults to `policies/teams`, and put every byte back afterwards, even when a spec fails. They skip themselves when the directory isn't writable or deploygate serves its embedded bundle.

## How it is wired

### deploy

`internal/deploy` is the contract, and nothing else. The `Input` struct and its nested `Release`, `Service` and `Actor` types carry `policy:` tags that name the inputs policies read. `Deny`, `Review` and `Approve` are the decision handles, and `Kind` ties them together with the precedence, the default `deny(no_rule_matched)` and the `split` function. Every other package imports this one; the kind file in `policies/` is generated from it.

### store

`internal/store` holds the compiled policies. `Load` compiles every served team's root, `<team>.production`, from the team bundle:

```go
deploy.Kind.Load(teamsFS, team+".production",
	policy.Require("deploy.guardrails", policy.From(policies.Platform)))
```

It swaps the new set in with one atomic pointer store only when every team compiles, so in-flight requests finish on the bundle they started with, and a broken bundle never serves. The platform documents always come from `policies.Platform`, embedded in the binary; there's no flag to point them elsewhere, because a guardrail an operator can swap isn't a guardrail.

At startup `cmd/deploygate` calls `InitialLoad`, which loads the same way but records the trigger `startup` and leaves the failure to its caller. There's no last good bundle yet, so `main` prints the diagnostics once and exits; on Kubernetes that holds a rollout at the old pods instead of serving without a policy.

`Watch` runs the reload loop. `SIGHUP` always reloads. A poll fingerprints the directory's content and reloads only when it changed since the last load, successful or not, so a broken bundle is reported once rather than every few seconds. Loads are serialized, and each runs in a span that records what triggered it. The store is built with options, `store.New(deploy.Kind, store.WithTeams(...), store.WithTeamsDir(dir), store.WithMetrics(m), ...)`, and `store.WithClock` lets the tests control time.

### server

`internal/server` is a gin router with recovery, OpenTelemetry, access log and request metrics middleware. It's built with functional options, `server.New(server.WithStore(st), server.WithMetrics(m), server.WithTracerProvider(tp))`, which returns the server or a humane error, and `Handler()` hands out the router for tests. Probes and scrapes are neither traced nor logged, so they don't bury the requests worth reading. The deployments handler decodes the request with unknown fields disallowed, evaluates the team's policy, matches the result with the typed decision handles, and maps the decision to the HTTP status. Every error the service creates is a humane error, rendered as:

```json
{"error": {"message": "...", "advice": ["..."], "cause": {"message": "..."}}}
```

| Route | Does |
| --- | --- |
| `POST /api/v1/teams/{team}/deployments` | Evaluates `<team>.production` |
| `GET /api/v1/policies` | The kind, its version, `loaded_at`, the source and the served policies |
| `POST /api/v1/policies/reload` | Reloads the team bundle now; `500` with the diagnostics when it doesn't compile |
| `GET /healthz` | `200` once the process serves |
| `GET /readyz` | `200` once a bundle is loaded, `503` before |
| `GET /metrics` | Prometheus text format |

Any other path answers `404` with the same error model. The server shuts down gracefully on `SIGINT` and `SIGTERM`, within the shutdown timeout.

### telemetry

`internal/telemetry` sets up the tracer provider from the standard OpenTelemetry environment variables, a zap logger that adds the trace and span IDs to each line, and the Prometheus metrics on a registry the service owns, next to the Go runtime and process collectors. `/metrics` serves that registry and nothing else:

| Metric | Type | Labels |
| --- | --- | --- |
| `deploygate_decisions_total` | counter | `team`, `policy`, `decision`, `reason` |
| `deploygate_evaluation_duration_seconds` | histogram | `team` |
| `deploygate_evaluation_errors_total` | counter | `team`, `kind`: `assertion`, `runtime` or `conflict` |
| `deploygate_policy_reloads_total` | counter | `result`: `success` or `failure` |
| `deploygate_policy_last_reload_timestamp_seconds` | gauge | none; the time of the last successful load |
| `deploygate_policy_loaded_info` | gauge, always 1 | `team`, `policy`, `source` |

The server's middleware adds the HTTP request metrics to the same registry:

| Metric | Type | Labels |
| --- | --- | --- |
| `deploygate_requests_total` | counter | `code`, `method`, `url` |
| `deploygate_request_duration_seconds` | histogram | `code`, `method`, `url` |

Every label is bounded. `url` is the route template, `/api/v1/teams/:team/deployments`, or `unmatched` for a path without a route, so made-up team names and scanned paths can't create new series, and a method outside the standard set counts as `other`. Scrapes of `/metrics` aren't counted, because they would dominate the request rate.

Each deployment request gets the gin server span and, below it, a `deploygate.evaluate` span with the attributes `sigil.kind`, `sigil.policy`, `sigil.team`, `sigil.decision`, `sigil.reason` and `sigil.candidates`, plus one `sigil.candidate` event per trace entry. A failed evaluation sets the span's status to error. Reloads run in a `deploygate.policies.reload` span with `sigil.source`, `sigil.kind`, `deploygate.reload.trigger` (`startup`, `manual`, `sighup` or `poll`) and, on success, `sigil.policies`; a rejected bundle sets the status to error and records the diagnostics.

### config

`internal/config` builds the command tree with cobra: `deploygate serve`, `deploygate healthcheck`, which probes `/readyz` on the loopback interface and reads `--addr` the same way, and `deploygate version`. Each flag of `serve` can also be set through a `DEPLOYGATE_` environment variable, read with viper:

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `--addr` | `DEPLOYGATE_ADDR` | `:8080` | Listen address for the API, health and metrics |
| `--policies` | `DEPLOYGATE_POLICIES` | empty | Directory with the team policies; empty serves the teams embedded in the binary |
| `--team` | `DEPLOYGATE_TEAMS` | `payments,checkout` | Teams to serve, repeatable or comma-separated; team `t` evaluates `t.production` |
| `--reload-interval` | `DEPLOYGATE_RELOAD_INTERVAL` | `30s` | How often to check the policies directory for changes; `0` turns polling off |
| `--shutdown-timeout` | `DEPLOYGATE_SHUTDOWN_TIMEOUT` | `15s` | How long a graceful shutdown may take |
| `--debug` | `DEPLOYGATE_DEBUG` | `false` | Debug logging and gin's debug mode |
| `--log-format` | `DEPLOYGATE_LOG_FORMAT` | `json` | `json` or `console` |

Tracing takes the standard variables: `OTEL_EXPORTER_OTLP_ENDPOINT` for the collector, `OTEL_SERVICE_NAME`, which defaults to `deploygate`, and `OTEL_TRACES_EXPORTER=none` to turn tracing off.

## Building it separately

The examples have their own [`.goreleaser.yaml`](./.goreleaser.yaml), which builds `deploygate` and `sigilc`. The root release doesn't use it, so nothing in `examples/` ships with Sigil itself. To build snapshot binaries locally:

```bash
mise run examples-snapshot
```

`mise run examples-build` builds plain binaries into `examples/bin/` instead. `mise run examples-release-check` validates the GoReleaser configuration, and `mise run examples-check` runs every gate the examples CI job runs: lint, tests, the policy checks and the release check.

## Further reading

- [A tour of the language](../docs/getting-started/tour.md) walks through the same policies by hand.
- [Per-team policies](../docs/guides/team-policies.md) explains how the team policies compose the platform's.
- [Policies in a ConfigMap](../docs/guides/configmaps.md) shows how to ship the team bundle to a cluster.
- [Go API](../docs/reference/go-api.md) is the reference for everything `internal/store` and `internal/server` call.
