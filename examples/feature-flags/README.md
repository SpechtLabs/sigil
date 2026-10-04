# featuregate: a feature-flag service in Rust on Sigil's WebAssembly engine

featuregate answers the [OpenFeature Remote Evaluation Protocol (OFREP)](https://github.com/open-feature/protocol), so any OpenFeature SDK with an OFREP provider can use it. What a flag does for a user is not configuration in a database: every flag's rollout is a Sigil policy, written by the product team that owns the flag, reviewed in a pull request, tested with `sigil test`, and reloaded from a mounted directory without a restart. Every answer names the rule that decided it.

The service is [axum](https://github.com/tokio-rs/axum) on tokio. Its only policy engine is the [`spechtlabs-sigil`](../../bindings/rust) crate: Sigil's Go engine compiled to WebAssembly and run on wasmtime. Go only builds `sigil.wasm`; nothing written in Go runs at run time. It's the third service that proves the bindings carry a real workload, next to [deploygate](../deploy-gates/README.md) (Go) and [alertrouter](../alert-routing/README.md) (TypeScript). Start with [Run it](#run-it); [`ARCHITECTURE.md`](./ARCHITECTURE.md) explains the composition root and the choices behind the design.

## What the example shows

- **A kind defined in Rust.** `src/kind.rs` declares `FeatureRollout` with the crate's [kind builder](../../bindings/rust/README.md): the `flag`, `user`, `bucket` and `killed` inputs, the `enable` and `disable` decisions, their precedence and the default. `featuregate export-kind` writes it to `policies/feature_rollout.sigil`, and a test fails when that copy is stale.
- **One policy per flag.** `flags.new_checkout` serves the flag `new-checkout`; its file is `policies/flags/new_checkout.sigil` and its test file is next to it. Product teams own these files; the platform team owns everything under `policies/platform/`.
- **Guardrails no flag can switch off.** `platform.guardrails` is compiled into the binary and required of every flag policy as a trusted document. A flag policy that skips it, gates it behind a `when`, or defines its own `platform.guardrails` doesn't load. And because `disable` outranks `enable` in the kind, a flag policy can't outvote it by ranking its own rules higher. See [Guardrails](#guardrails).
- **Reload with a last known good.** The directory is polled, `SIGHUP` reloads it, and `POST /api/v1/policies/reload` forces it. A bundle that doesn't compile never replaces the one that serves; the error is on `GET /api/v1/policies`, in the log and in a metric.
- **Fail closed.** An evaluation that fails (a conflict, an assertion, a timeout, a stopped instance) answers the flag off with reason `ERROR`, is logged, traced and counted by kind, and never fails the request.
- **A pool with deadlines.** Evaluations run on a `sigil::Pool` of instances, off the async threads, with a per-evaluation deadline that the binding enforces twice: the ABI's timeout, and epoch interruption from outside if the engine ignores it. A killed instance is replaced automatically.
- **Observability.** Structured JSON logs with trace and span ids, OpenTelemetry traces over OTLP, Prometheus metrics with bounded labels, Pyroscope CPU profiles, Alloy, Mimir, Tempo, Loki, Grafana and a k6 suite, the same stack as alertrouter.

## Run it

You need [mise](https://mise.jdx.dev/); the compose stack also needs Docker. From the repository root:

```bash
cd examples/feature-flags
mise install
mise run up      # featuregate, Alloy and the Grafana LGTM stack in Docker
mise run demo    # sends every request of requests/cases.json and checks each answer
```

The tasks in [`.mise.toml`](./.mise.toml) run in `examples/feature-flags/`; from the repository root, use `mise run -C examples/feature-flags <task>`. The image builds `sigil.wasm` and the crate itself, so `up` needs nothing on the host but Docker. The stack publishes the same host ports as the other examples, so run one stack at a time. The dashboard is at <http://localhost:3000/d/featuregate>.

Without Docker, `mise run dev` serves featuregate on `localhost:8080` with the sample flags, reloading edits to `policies/flags/` every two seconds, and logs in a readable format without exporting telemetry. Starting takes tens of milliseconds: the crate's `precompiled` feature compiles the 11 MB module to native code at build time, so startup loads it instead of compiling it (seconds of CPU at every start otherwise), and then every flag compiles in every pool instance. The price is a longer `cargo build` and a larger binary.

| Task | What it does |
| --- | --- |
| `dev` | Serve on `localhost:8080`, no Docker |
| `up`, `down` | Start and stop the compose stack |
| `demo` | Send the fixtures to a running service and check the answers |
| `test` | Unit, integration, policy and dashboard tests |
| `lint` | `cargo fmt --check` and `cargo clippy -D warnings` |
| `policies` | `sigil check` and `sigil test` on `policies/` with the stock CLI |
| `generate` | Export `policies/feature_rollout.sigil` from the Rust kind |
| `e2e` | Start the stack and run the black-box suite against it |
| `loadtest` | k6 load test in the stack; `loadtest-smoke`, `-stress`, `-spike`, `-soak` and `-breakpoint` are its other shapes |
| `check` | Every gate CI runs |

## Evaluate a flag

```bash
curl -s localhost:8080/ofrep/v1/evaluate/flags/search-v2 \
  -d '{"context": {"targetingKey": "user-1", "plan": "enterprise", "region": "eu-1"}}'
```

```json
{
  "key": "search-v2",
  "value": "semantic",
  "reason": "TARGETING_MATCH",
  "variant": "semantic",
  "metadata": {
    "sigil.policy": "flags.search_v2",
    "sigil.decision": "enable",
    "sigil.reason": "enterprise"
  }
}
```

An OpenFeature SDK does the same through its OFREP provider, pointed at `http://localhost:8080`. The endpoints:

| Endpoint | What it does |
| --- | --- |
| `POST /ofrep/v1/evaluate/flags/{key}` | Evaluate one flag for a context |
| `POST /ofrep/v1/evaluate/flags` | Evaluate every flag for a context, sorted by key. The answer has an `ETag`; send it back as `If-None-Match` and an unchanged answer is an empty `304` |
| `GET /api/v1/flags` | The flags being served and their policies |
| `GET /api/v1/policies` | The bundle being served: source, content digest, load time, flags, and the last reload error |
| `POST /api/v1/policies/reload` | Compile the documents again now: `200` when it loaded, `422` with the error when it didn't (the old bundle keeps serving) |
| `GET /healthz` | Liveness |
| `GET /readyz` | Readiness: `503` once shutdown begins |
| `GET /metrics` | Prometheus metrics |

### The context

OFREP sends an evaluation context, and featuregate reads these keys from it into the kind's `user` input:

| Key | Type | Becomes | If missing |
| --- | --- | --- | --- |
| `targetingKey` | string | `user.id`, and what the rollout bucket hashes | `400 TARGETING_KEY_MISSING` |
| `plan` | `free`, `pro` or `enterprise` | `user.plan` | `free` |
| `region` | string | `user.region` | `unknown`, which is in no ready region |
| `beta` | boolean | `user.beta` | `false` |
| anything else | string, number or boolean | `user.attributes[name]`, as a string | |

An attribute that is an object, an array or `null` is `INVALID_CONTEXT`: a policy can't read it.

The kind's `bucket` input is host-computed: 0 to 99, from a SHA-256 of the flag key and the targeting key, so a user keeps their place in a rollout across requests and instances, and isn't in the first 10% of every flag at once. `killed` is true for the flags in `FEATUREGATE_KILLED_FLAGS`.

### The answer

The decision the policy returns maps to OFREP like this:

| Sigil decision | `reason` | `value` | `variant` |
| --- | --- | --- | --- |
| `enable(rollout)` | `SPLIT` | `true` for a boolean flag, the variant for a string flag | the variant |
| `enable(enterprise \| beta_tester \| targeted)` | `TARGETING_MATCH` | `true` for a boolean flag, the variant for a string flag | the variant |
| `disable(kill_switch)` | `DISABLED` | the flag's off value | the off variant |
| `disable(region_not_ready)` | `TARGETING_MATCH` | the flag's off value | the off variant |
| `disable(not_rolled_out)` | `DEFAULT` | the flag's off value | the off variant |
| the evaluation failed | `ERROR` | the flag's off value | the off variant |

Every flag answers in one value type, so a typed client (`getStringValue`, `getBooleanValue`) never sees a type mismatch. The type comes from [`policies/flags/flags.yaml`](./policies/flags/flags.yaml), the flag manifest: a flag with no entry is boolean, with `true` when enabled and `false` (variant `off`) when not. A `type: string` entry needs an `off:` value, and that flag answers its variant when enabled and the `off` value, as value and variant, when disabled, killed or failed. `search-v2` is `type: string, off: control`, so a user outside the experiment gets `"control"`, in the type the client asked for. A manifest entry for a flag no policy serves, or one that doesn't parse, fails the reload like any other broken document.

Sigil's own decision, reason and policy are in `metadata` (`sigil.decision`, `sigil.reason`, `sigil.policy`), so a provider hook or a support engineer can see why without reading the policy. A failed evaluation leaves the kind of failure in `metadata["sigil.error"]` instead; the details are in the logs.

Errors use OFREP's shapes, `{"key": ..., "errorCode": ..., "errorDetails": ...}`:

| Status | `errorCode` | When |
| --- | --- | --- |
| `404` | `FLAG_NOT_FOUND` | no policy serves the key |
| `400` | `TARGETING_KEY_MISSING` | the context has no `targetingKey` |
| `400` | `INVALID_CONTEXT` | a `plan` that isn't one of the three, a `beta` that isn't a boolean, a nested attribute |
| `400` / `413` | `PARSE_ERROR` | the body isn't JSON, or is past 64 KiB |

An unknown route is `404` with `{"error": ...}`, and a wrong method is `405`.

## The flags

| Flag | Policy | What it does |
| --- | --- | --- |
| `new-checkout` | `flags.new_checkout` | On for enterprise, beta testers, the `checkout-pilot` cohort (an attribute) and 25% of everyone else |
| `dark-mode` | `flags.dark_mode` | On for everyone in a ready region |
| `search-v2` | `flags.search_v2` | The `semantic` variant for enterprise and beta testers, `hybrid` for pro and for 10% of everyone else |
| `beta-api` | `flags.beta_api` | Beta testers and the partners `acme` and `globex`, no percentage |

A flag is a boolean unless `policies/flags/flags.yaml` declares it a string flag with an off value. A flag key is lowercase letters, digits and single hyphens, starting with a letter. Its policy is `flags.` plus the key with hyphens written as underscores, in a file named `<that>.sigil`. [`policies/README.md`](./policies/README.md) has the layout and the rules a flag policy lives by.

## Guardrails

`platform.guardrails` belongs to the platform team. It's compiled into the binary from `policies/platform/` and passed to the engine as a trusted document, apart from the directory the flag policies come from, so whoever can write to the mount can't replace it. It does two things:

- **Data residency.** A user outside `eu-1`, `eu-2`, `us-1` and `us-2` gets `disable(reason: region_not_ready)`, whatever the flag says.
- **The kill switch.** A flag in `FEATUREGATE_KILLED_FLAGS` gets `disable(reason: kill_switch)` for everyone.

Every flag policy is compiled with `require: platform.guardrails`, so one that doesn't invoke it at the top level, invokes it under a `when`, or defines its own `platform.guardrails` fails to compile, and with it the whole bundle. On top of that, the kind makes the guardrails impossible to outvote: `disable` ranks above `enable`, so a flag policy that enables a flag in a region that isn't ready, however high it ranks that rule, still gets `disable`; and `kill_switch` is the first reason of `disable`, so a killed flag reports the kill switch even where its region isn't ready either. `tests/reload.rs` exercises each of these against the real engine.

## Trust and deployment

The context is whatever the caller sends. `plan`, `region` and `beta` are **asserted by the caller**, and featuregate has no way to check them, so data residency (`platform.guardrails`) is enforced on the context as asserted: a client that lies about its region gets the flags of the region it names. The service has no authentication either. Run it behind a backend-for-frontend or a trusted proxy that authenticates the user and sets `targetingKey`, `plan`, `region` and `beta` from its own records, and don't expose the OFREP endpoints to browsers or mobile apps directly. The operator endpoints (`/api/v1/*`, including the reload) belong on an internal network for the same reason.

## Configuration

Every setting is a `FEATUREGATE_*` environment variable, checked at startup with advice on what to set; all the problems are reported at once. Durations use Go's syntax (`30s`, `1m30s`).

| Variable | Default | |
| --- | --- | --- |
| `FEATUREGATE_ADDR` | `:8080` | listen address of the one HTTP port |
| `FEATUREGATE_POLICIES` | empty | flag policies directory, read recursively for `*.sigil`; empty serves the sample flags compiled into the binary |
| `FEATUREGATE_RELOAD_INTERVAL` | `30s` | how often the directory is polled; `0` disables polling |
| `FEATUREGATE_SHUTDOWN_TIMEOUT` | `15s` | how long a graceful shutdown waits for requests in flight |
| `FEATUREGATE_EVALUATION_TIMEOUT` | `50ms` | how long one evaluation may run, and how long it may wait for a free instance |
| `FEATUREGATE_WORKERS` | 2 to 4, by CPUs | Sigil instances in the evaluation pool |
| `FEATUREGATE_EVALUATION_FUEL` | empty | optional fuel bound per evaluation, on top of the deadline; a call that runs out is killed like one past its hard deadline |
| `FEATUREGATE_KILLED_FLAGS` | empty | comma-separated flag keys the platform kill switch turns off; a name no loaded flag has fails startup, and after a reload it is a warning and `featuregate_killed_flags_unmatched` |
| `FEATUREGATE_LOG_FORMAT` | `json` | `json` or `console` |
| `FEATUREGATE_DEBUG` | `false` | debug logging |
| `FEATUREGATE_VERSION` | `dev` | the version on logs, spans and profiles; the image sets it |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | OTLP/gRPC endpoint for traces, such as `http://alloy:4317`; empty leaves trace export off |
| `PYROSCOPE_SERVER_ADDRESS` | empty | Pyroscope server for continuous CPU profiling; empty leaves it off |

The binary has four subcommands: `featuregate serve`, `healthcheck` (exit 0 when the local `/readyz` answers 200; the distroless image has no shell or HTTP client), `export-kind` (prints the kind file, or writes it with `--out`) and `version`.

## Reloading

The store reads every `*.sigil` file under the directory (skipping names that start with a dot, which is how a Kubernetes ConfigMap mount keeps its bookkeeping), compiles every flag into a new pool and swaps it in, all or nothing:

- A reload builds a whole new bundle with its own pool, so a request sees the old bundle or the new one, never a mix, and a flag that's added or removed appears or goes in the same step as the flag it was edited beside.
- If anything fails to parse, type-check or satisfy the guardrail requirement, or the directory is empty or unreadable, the serving bundle stays. The error is in `lastReloadError` of `GET /api/v1/policies`, in a `warn` log line, and in `featuregate_reloads_total{result="failed"}`. The next good reload clears it.
- The poll compares a digest of the files, so an unchanged directory costs a read and no compile. `SIGHUP` and the endpoint always compile.
- At startup a bundle that doesn't load is fatal: there's no last known good to serve, and the message says what to fix.

## Observability

- **Logs** are one JSON object per line on stdout: `time`, `level`, `msg`, the event's own fields, and `trace_id` and `span_id` once. Each evaluation logs `flag evaluated` with `flag`, `policy`, `decision`, `reason`, `ofrep_reason` and `duration_ms`; each request logs `request` with `method`, `route`, `status` and `duration_ms`. Probes and scrapes log at debug. A failed evaluation is an `error` line with the failure's kind and message.
- **Traces** go over OTLP/gRPC when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. Each request is an `http request` server span that continues the caller's `traceparent`, with `http.route`, `http.request.method` and `http.response.status_code`; each evaluation is an `evaluate flag` child with `flag`, `sigil.policy`, `sigil.decision`, `sigil.reason` and `ofrep.reason` (and `sigil.error` when it failed).
- **Metrics** at `/metrics`, all `featuregate_*` with bounded labels (a flag label only holds keys that have a policy, and routes are templates):

  | Metric | Labels |
  | --- | --- |
  | `featuregate_evaluations_total` | `flag`, `decision`, `reason` (`error` for a failed evaluation) |
  | `featuregate_evaluation_duration_seconds` | `flag` |
  | `featuregate_evaluation_errors_total` | `kind`: `timeout`, `conflict`, `assertion`, `runtime`, `stopped`, `busy`, `internal` |
  | `featuregate_reloads_total` | `result`: `loaded`, `unchanged`, `failed` |
  | `featuregate_loaded_info` | `source`, `digest`; 1 for the bundle serving |
  | `featuregate_flags_loaded`, `featuregate_ready` | |
  | `featuregate_module_precompiled` | 1 when the Sigil module loaded precompiled, 0 when each start compiles it (fuel metering, or a build without the feature) |
  | `featuregate_killed_flags_unmatched` | kill-switch names no loaded flag has; alert on above 0 |
  | `featuregate_pool_replacements_total` | instances replaced after a kill or trap |
  | `featuregate_pool_rebuilding` | instances being rebuilt in the background after a kill; the pool serves with that many fewer meanwhile |
  | `featuregate_http_requests_total` | `method` (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS or OTHER), `route`, `status` |
  | `featuregate_http_request_duration_seconds` | `method`, `route` |
  | `featuregate_http_requests_in_flight`, `featuregate_build_info{version}` | |

  On Linux the process metrics (`process_cpu_seconds_total`, `process_resident_memory_bytes`, open file descriptors) come with them.
- **Profiles** are pushed to Pyroscope at 100 Hz when `PYROSCOPE_SERVER_ADDRESS` is set, tagged with the version. Profiling is diagnostics: if the agent can't start, featuregate logs a warning and serves.

## Tests

`mise run test` runs all of them against the real engine (`sigil.wasm` built from this repository); none needs Docker.

| Suite | What it covers |
| --- | --- |
| unit (`src/**`) | config parsing and validation, flag keys and buckets, the OFREP context and answer mapping, the failure classification, the metrics registry |
| `tests/api.rs` | every request of `requests/cases.json` through the real router, OFREP errors, ETags, the kill switch, probes, concurrent evaluation |
| `tests/reload.rs` | edited, added and removed flags; broken reloads and last known good; the guardrail violations; fail-closed evaluations |
| `tests/policies.rs` | every `*_test.yaml` case against the policies compiled as the service compiles them; every flag has a test file that reaches both decisions; the exported kind file isn't stale |
| `tests/serve.rs` | polling, `SIGHUP`, graceful shutdown over a real socket; the `healthcheck` and `export-kind` subcommands |
| `tests/metrics.rs`, `tests/telemetry.rs` | the metric contract and its label bounds; spans through an in-memory exporter; log lines |
| `tests/dashboard.rs` | the Grafana dashboard against the metric contract |
| `tests/e2e.rs` | `mise run e2e`: the compose stack from the outside, including hot reload of the mounted files and Mimir, Loki, Tempo, Pyroscope and Grafana |

`mise run policies` runs the same policies and test files with the stock `sigil` CLI, so the two engines can't drift apart.

## Layout

```text
Cargo.toml          the featuregate crate; depends on ../../bindings/rust by path
src/
  main.rs           serve, healthcheck, export-kind, version
  lib.rs            the modules below
  service.rs        the composition root: builds and runs everything
  config.rs         FEATUREGATE_* → validated Config
  kind.rs           the FeatureRollout kind, from the builder
  store.rs          read the documents, compile a bundle, swap it in
  reload.rs         one place that reloads, counts and logs
  engine.rs         one evaluation: bucket, pool, result → OFREP answer
  ofrep.rs          OFREP request and answer types, the context and decision mapping
  verdict.rs        the kind's input and outcome as Rust types
  flags.rs          flag keys and the rollout bucket
  api.rs            the axum router, handlers and request instrumentation
  metrics.rs        the Prometheus registry
  telemetry/        logs, traces and the Pyroscope profiler
  embedded.rs       the platform documents and sample flags compiled in
  cases.rs          requests/cases.json as a type, shared by the tests and the demo
  bin/demo.rs       the demo client
policies/           the kind file, platform and flag policies, and their tests
requests/           cases.json, shared by the tests, the demo and the load test
tests/              integration, e2e and dashboard tests
deploy/             Alloy, Tempo, Loki, Mimir, Pyroscope and Grafana configuration
test/load/          the k6 suite
```
