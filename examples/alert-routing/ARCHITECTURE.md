# Architecture

alertrouter is one Next.js application: the HTTP API Alertmanager and operators call, and the operator console in the browser. Its only policy engine is `@spechtlabs/sigil`, the Go engine compiled to WebAssembly; no Go runs at run time.

## One composition root

Nothing is created when a module is imported. Every long-lived object (the compiled Sigil module, the engines, telemetry, the team directory, the policy store, the dispatcher, the history) is built in one place and passed to whatever needs it:

```text
src/instrumentation.ts    Next calls register() once per server process
  └─ src/server/boot.ts   reads the environment, builds telemetry, then createService(),
                          installs it in the registry, starts reloads, handles signals
       └─ src/server/service.ts   createService(deps): the composition root proper.
                                  Pure: no process globals, no signals, no timers
                                  it doesn't own. Tests call it directly.
src/server/registry.ts    setService()/getService(): the one handle route files read
src/app/**/route.ts       thin adapters: every method goes to getService().api.fetch(request)
```

Route files only adapt Next to the service: `src/server/routes.ts` sends every method of every API, health and metrics route to `Api.fetch` (`src/lib/http/api.ts`), which routes the request itself, reports it (server span, access log line, request metrics) and calls the framework-free handlers in `src/lib/http/handlers.ts`. The integration tests call the same `fetch` without Next, so they drive exactly the code production runs, down to the 404 for a method a route doesn't serve.

Next bundles `instrumentation.ts` and each route separately, so a module-level variable would exist once per bundle. The registry keeps the service on `globalThis` under `Symbol.for("alertrouter.service")`; that is the only global, and only `boot()` writes it.

## Layout

```text
src/
  instrumentation.ts   Next's startup hook → boot()
  server/              boot (process wiring), service (composition root), registry, routes
  app/                 App Router: the console's pages (ts-ui) and the API route files
  lib/
    config/            ALERTROUTER_* environment → validated Config
    routing/           the AlertRouting kind, defined with the @spechtlabs/sigil builder
    teams/             the team directory (teams.yaml)
    alertmanager/      the Alertmanager webhook payload and its conversion to kind input
    engine/            the Sigil engines: the in-process platform engine and the worker pool
    store/             the policy store: compile, require platform.paging, reload, last known good
    dispatch/          the notifier, the bounded history and its event stream (SSE)
    http/              the route handlers, error rendering, request instrumentation
    telemetry/         tracer, metrics, logger, profiler (types.ts is the contract)
    embedded.ts        generated: the platform documents, default team bundle and team directory
  components/          the console's components (ts-ui), components/ui/ vendored shadcn
policies/              the kind file, the platform's documents and the example team bundle
requests/              example requests, shared by the tests and the load test
scripts/               export-kind, embed, copy-wasm, vendor-sigil (postinstall)
bin/                   the container's entry point and health check, plain Node scripts
test/                  integration and e2e suites (ts-qa)
```

## Two engines

A Sigil instance is one WebAssembly module, and alertrouter keeps two kinds apart, so that nothing a team writes can cost the platform's page or stall the process:

- **The platform engine** (`src/lib/engine/platform.ts`) is an instance in the server's own thread that holds only `platform.paging`, compiled from the platform's documents. No team file ever reaches it. It answers whether the platform pages for an alert, and whom, when the team's evaluation left no trace to read that from: the evaluation failed, timed out or never ran, or the alert couldn't be read. It takes microseconds. /readyz evaluates a canary alert through it; when it fails (the module stopped, or a call ran out of stack in Go code) it reports itself down, /readyz answers 503, and a fresh instance is loaded from the compiled module. After three failed replacements in a row the process exits.
- **The evaluation pool** (`src/lib/engine/pool.ts`) is `ALERTROUTER_WORKERS` worker threads, each running its own instance through the package's `SigilWorker`. Team policies compile in every worker and evaluate in whichever is free, so a slow policy never holds the thread that answers probes, streams the console and delivers notifications. One team may occupy at most half the workers. A worker that fails or doesn't answer shortly after its deadline is replaced, and its policies compile again in the new one by themselves.

Every engine instantiates the same compiled module, which boot reads and compiles once, so replacing one takes milliseconds.

## Why these choices

- **Trusted documents are compiled in.** `platform.paging` is required from the platform documents in `src/lib/embedded.ts`, generated from `policies/platform/` by `bun run generate`, never from the team directory the service reads at run time. They go to the engine as `trustedFiles`, apart from the team files, so trust comes from the list rather than from a path a team could imitate. That's Go's `//go:embed` and `policy.From`, and it keeps the guardrail out of reach of whoever can write to the mounted directory.
- **The kind is TypeScript.** `src/lib/routing/kind.ts` defines AlertRouting with the builder; `bun run export-kind` writes `policies/alert_routing.sigil` from it, and a test fails when the file is stale.
- **The server runs on Node.** Next's standalone output runs `server.js` on Node; `@spechtlabs/sigil` is kept external (`serverExternalPackages`), a plain package in `node_modules` rather than part of a bundle.
- **The package is vendored on install.** bun installs the `file:` dependency on `bindings/typescript` as symlinks back into it, and Turbopack won't follow a symlink out of the project, so `scripts/vendor-sigil.ts` (postinstall) replaces them with copies. Run `bun install --force` after rebuilding the bindings.
- **`sigil.wasm` is resolved from the working directory** through `process.getBuiltinModule`, so the bundler neither links it as a WebAssembly import nor stubs the resolve out; `next.config.ts` traces it into the standalone output.
- **The container runs `bin/alertrouter.mjs`**, which maps `ALERTROUTER_ADDR` to Next's `PORT`/`HOSTNAME` and sets `NEXT_MANUAL_SIG_HANDLE`, so `boot()` owns SIGTERM and SIGINT (drain, release, flush, exit) and SIGHUP (reload). `bin/healthcheck.mjs` is the image's health check: `GET /readyz` on the loopback interface.
- **Biome** lints and formats; nothing in the app needs Next's ESLint plugin. `tsc --noEmit` type-checks; `next build` skips both.

## Configuration

Every setting is an `ALERTROUTER_*` environment variable, with the Go service's names and defaults, checked at startup with advice on what to set (`src/lib/config/config.ts`). Durations use Go's syntax (`30s`, `1m30s`).

| Variable | Default | |
| --- | --- | --- |
| `ALERTROUTER_ADDR` | `:8080` | listen address of the one HTTP port |
| `ALERTROUTER_POLICIES` | empty | team policies directory; empty serves the bundle compiled into the app |
| `ALERTROUTER_TEAMS_FILE` | empty | team directory; empty serves the one compiled into the app |
| `ALERTROUTER_RELOAD_INTERVAL` | `30s` | how often the policies directory is polled; `0` disables polling |
| `ALERTROUTER_SHUTDOWN_TIMEOUT` | `15s` | how long a graceful shutdown waits for requests in flight |
| `ALERTROUTER_EVALUATION_TIMEOUT` | `50ms` | how long one alert's evaluation may take (the Go service had `1s`; evaluations take microseconds, and a slow one holds a worker) |
| `ALERTROUTER_BATCH_TIMEOUT` | `10s` | how long the answer to one webhook may take, from its arrival; evaluations must start in the first half (new) |
| `ALERTROUTER_DEDUP_TTL` | `5m` | how long a delivered notification is remembered by fingerprint; `0` disables deduplication (new) |
| `ALERTROUTER_DEDUP_MAX_ENTRIES` | `100000` | delivered notifications deduplication remembers at most, oldest forgotten first (new) |
| `ALERTROUTER_DISPATCH_CONCURRENCY` | `16` | deliveries in flight at once (new) |
| `ALERTROUTER_WORKERS` | 2 to 4, by CPUs | worker threads that evaluate team policies; one team gets at most half (new) |
| `ALERTROUTER_MAX_EVENT_STREAMS` | `32` | console event streams (SSE) open at once (new) |
| `ALERTROUTER_HISTORY_SIZE` | `500` | routed alerts the console keeps (new) |
| `ALERTROUTER_LOG_FORMAT` | `json` | `json` or `console` |
| `ALERTROUTER_DEBUG` | `false` | debug logging |
| `ALERTROUTER_VERSION` | `dev` | the version on logs, spans and profiles; the image sets it |

## Where the wire differs from the Go service

The Go service's HTTP behavior is the contract, with these deliberate changes from the review of it:

1. **The platform's page always wins.** Every team policy invokes `platform.paging`, so a team's evaluation holds the platform's page candidates, bound to the team's own `page_after` and tagged `platform.paging` (a tag only the trusted platform documents can carry).
   - When the team's evaluation fails (conflict, assert, runtime error, timeout) or never runs (batch deadline, alert limit), there is no trace to read, and `platform.paging` is evaluated on its own in the platform engine, with its params at the platform's defaults. Its page goes out if it pages: status `failed`, decision `page`, the platform's reason and target. Only otherwise does the kind's default, `notify(reason: unrouted)`, apply. The failure is still logged, counted and marked on the span, and the route endpoint keeps its 422/500/503.
   - When the team's evaluation succeeds and `platform.paging` paged in it, but the winner isn't a page to that target (say a higher-ranked page to someone else, which outranks `sustained` without conflicting), the platform's page goes out instead: status `failed`, the route endpoint answers 500, and it counts as a guardrail violation (`alertrouter_guardrail_violations_total{team}`, an error log line and a span event). The check reads the team's own evaluation, so it holds at the team's `page_after`, and it costs no second evaluation.
   - An invalid alert whose team and severity can be read gets the platform's page too, from what could be read. A `startsAt` so far back that the duration overflows is clamped to the longest duration Sigil writes.
   - When the platform engine is needed and can't evaluate (it's being replaced), the alert goes out with the fallback, the error says so, and both endpoints answer 503, so Alertmanager sends it again.
2. **A batch has a deadline.** `ALERTROUTER_BATCH_TIMEOUT` bounds the answer to a webhook, from the moment it arrives: an alert whose evaluation hasn't started in its first half goes the way of item 1 and counts as `evaluation_errors_total{kind="timeout"}`. Every decided alert's delivery starts, and runs to its end; the deadline bounds only how long the answer waits for it. A delivery still under way then gives the alert status `dispatch_failed` and the webhook 503, and the retry is deduplicated once the delivery finishes.
3. **Delivery is at least once, with deduplication.** Deliveries of a batch run concurrently, bounded. A delivery is remembered by fingerprint, decision, reason and destination for `ALERTROUTER_DEDUP_TTL`, so a redelivered webhook doesn't page twice (`alertrouter_notifications_deduplicated_total{decision}`). The table is kept in expiry order and holds at most `ALERTROUTER_DEDUP_MAX_ENTRIES` entries; a full table forgets the oldest first (`alertrouter_notifications_dedup_evicted_total`). It's not exactly once: an alert delivered and then lost with the process, forgotten early, or redelivered after the TTL, goes out again.
4. **A failed delivery is visible and retried.** The alert's status is `dispatch_failed` (not counted in `routed`), `alertrouter_notification_errors_total{decision}` counts it, and the webhook answers 503, with its usual body, when any page or notification failed to go out, so Alertmanager retries. The route endpoint answers as the Go service does: 200 with the error in the body.
5. **No 4xx for one bad alert.** Alertmanager drops a group on a 4xx. An alert with an unknown status, labels that aren't strings or an unreadable `startsAt` is `invalid`; past 1000 alerts, the rest go the way of item 1 without an evaluation, a span, a log line or a history entry each: one warn line and `alertrouter_alerts_truncated_total{by="alertrouter"}` report them; Alertmanager's own `truncatedAlerts` is logged and counted (`{by="alertmanager"}`). Only a body that isn't a webhook at all is a 400. The body cap is 4 MiB instead of 1 MiB.
6. **Bounded destination label.** `notifications_total{destination}` is the target or channel only when the team directory or the kind names it, and `other` otherwise.
7. **`policy` only when a policy ran.** Route and webhook results leave `policy` out for an unowned alert, an invalid one, and one skipped by the batch deadline or the alert limit, where the Go service wrote `""` for an unowned alert and the team's policy for an invalid one. The route span's `sigil.policy` is left out the same way. A failed evaluation still names the policy that failed.
8. **`notification dispatched`** carries team `-` for an unowned alert and leaves `policy` out when no policy ran.
9. **Readiness and shutdown.** `/readyz` answers 503 while the platform engine is down and once shutdown begins, and API requests during the drain get a 503 error. Evaluated alerts finish in the pool, so their log lines, spans and history entries come in the order they finish, not the webhook's; the webhook's results keep its order.
10. **Not the Go service's at all:** Node's process metrics instead of Go's, Node profiles, and paths outside `/api` that no route serves get the console's HTML 404 under Next (the Go JSON 404 everywhere under `/api`, and for any method a route doesn't serve).

New endpoints for the console: `GET /api/v1/policies/files` (exactly what the server compiles, for the in-browser preview, with the last reload error), `GET /api/v1/policies/{team}/explain`, `GET /api/v1/history` and `GET /api/v1/events` (Server-Sent Events of routed alerts and policy loads; at most `ALERTROUTER_MAX_EVENT_STREAMS` at once, and a stream more than 256 events behind is closed so the browser reconnects and replays from the ring buffer).
