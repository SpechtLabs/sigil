# Architecture

featuregate is one Rust process: an axum HTTP server that speaks OFREP, and a pool of Sigil instances that decides every flag. Its only policy engine is the `spechtlabs-sigil` crate, the Go engine compiled to WebAssembly and run on wasmtime; no Go runs at run time.

## One composition root

Nothing is created when a module is imported, and there are no global singletons: no `static` registry, no `lazy_static`, no `init()`. Every long-lived object is built in one place and handed to whatever needs it:

```text
src/main.rs            reads the environment, builds telemetry, installs the logger (the one
                       process global, tracing's dispatcher), loads the Sigil module,
                       then calls Service::new and Service::serve
  └─ src/service.rs    Service::new(config, module, metrics): the composition root proper.
                       Pure: no signals, no timers, no global logger. Tests call it directly.
                       Service::serve runs the poll timer, SIGHUP and the graceful shutdown.
       ├─ store.rs     the policy store (documents → compiled bundle, swap on success)
       ├─ reload.rs    the one place a reload is run, counted and logged
       ├─ engine.rs    one evaluation, on the serving bundle's pool
       └─ api.rs       router(AppState): handlers and request instrumentation
```

`api::router` takes an `AppState` (the engine, metrics, reloader, a draining flag) and returns an `axum::Router`. The integration tests call that router with `tower::ServiceExt::oneshot`, so they run exactly the code production runs, down to the `405` for a method a route doesn't serve. `Metrics` is an owned registry rather than the `prometheus` crate's default one, so two services in one test process don't share counters.

The logger is the exception: `tracing` has one global dispatcher. `telemetry::Telemetry::new` builds the subscriber as a value (a filter, the JSON log layer, the OpenTelemetry layer over a tracer provider) and `main` installs it; the tests that read spans and log lines build their own and install it once per test binary.

## The evaluation path

One request, end to end:

```text
POST /ofrep/v1/evaluate/flags/{key}
  api.rs         server span "http request" (continues the caller's traceparent); parse the body
  ofrep.rs       the OFREP context → the kind's User (targetingKey, plan, region, beta, attributes)
  engine.rs      span "evaluate flag"; bucket = SHA-256(flag, targetingKey) mod 100; killed = config
                 pool.evaluate_async("flags.<key>", RolloutInput, deadline)
  (sigil crate)  a free instance runs the policy in wasmtime; the ABI's timeout_ms and, past a
                 grace period, epoch interruption bound it
  engine.rs      EvalResult → Verdict, or a Failure classified by kind
  ofrep.rs       Verdict → the OFREP answer (value, reason, variant, metadata)
```

The pool's API blocks (it waits for a free instance, then for the evaluation), so the engine uses `Pool::evaluate_async` (the crate's `tokio` feature), which runs it on tokio's blocking threads and never on the threads that accept connections. A panic inside it comes back as an error, which counts as a failed evaluation of kind `internal`. A bulk request evaluates every flag of one bundle concurrently with a `JoinSet`, so the pool's instances work in parallel and a reload during the request can't mix two bundle versions in one answer. A `Sigil` instance is `Send` but not `Sync`; the pool hands each caller one instance at a time.

## Startup

The crate's `precompiled` feature compiles `sigil.wasm` to native code in `build.rs`, and `Module::bundled()` loads that: the service answers `/readyz` about 50 ms after it starts, where compiling the module at every start cost about 4.4 s of CPU time (0.4 to 1.4 s of wall time on 12 cores, more on a small container). The price is a longer `cargo build` and a binary about twice the size. Fuel metering (`FEATUREGATE_EVALUATION_FUEL`) isn't in the artifact, so enabling it compiles the module at startup. Building instances no longer touches a runtime of its own, so startup, reloads and tests build them wherever they are; reloads still run on `spawn_blocking` because they compile every flag in every instance.

## Two decisions, both closed

The kind has two decisions, `disable` above `enable` (`src/kind.rs`). That ranking is the design's load-bearing wall:

- **The guardrails can't be outvoted.** `platform.guardrails` only ever produces `disable`. Since a disable beats every enable, no flag policy can turn a flag on where the platform turned it off, however it ranks its own rules, and nothing in a flag's policy can even express "override the guardrail". Within the disables, `kill_switch` is ranked first, so an operator who kills a flag sees `kill_switch` in every region.
- **Nothing decided means off.** The kind's default is `disable(not_rolled_out)`: a user no rule enabled gets the flag off, which is what a percentage rollout means.

The `require` on `platform.guardrails` closes the other door: a flag policy that never invokes the guardrail (or invokes it under a `when`, or defines its own) doesn't compile, so there's no way to be in the bundle without it. See [Guardrails](./README.md#guardrails).

## Why these choices

- **Trusted documents are compiled in.** `platform.guardrails` is required from the documents in `src/embedded.rs` (the files under `policies/platform/`, pulled in with `include_str!`), never from the directory the service reads at run time. They go to the engine as `trusted_files`, apart from the flag files, so trust comes from the list rather than a path someone could imitate. That's Go's `//go:embed` with `policy.From`, and it keeps the guardrail out of reach of whoever can write to the mount.
- **The kind is Rust.** `src/kind.rs` defines `FeatureRollout` with the builder; `featuregate export-kind` writes `policies/feature_rollout.sigil`, and a test fails when the file is stale. The service compiles against `kind::schema()`, never the file on disk, so the file exists for the CLI and the editor and can't be tampered into something the service trusts.
- **A reload builds a new pool.** A bundle is a pool with every flag compiled in every instance. Building a new one and swapping an `Arc` is what makes a reload atomic: no request sees half a bundle, and a failure anywhere leaves the old pool untouched. It costs the compile time of every flag in every instance and the memory of a second pool until the old one's last request finishes.
- **One bundle, all or nothing.** All flag files are compiled together, so one broken file keeps every flag at its last known good version, including the ones that didn't change. That's the stricter choice over serving the flags that compiled: a half-applied change to two flags that were meant to ship together is worse than a delayed one, and the broken file is named in the error.
- **The bucket is host-computed.** A policy can't hash, and shouldn't: the same user must land in the same place whichever instance and whichever version evaluates, and the hash is the one piece of the rollout that must never change. It's in Rust with golden values in its tests. The flag key is part of the hash so one user isn't first in line for every rollout.
- **Fail closed is an answer, not an error.** An evaluation that fails answers `200` with the flag off and reason `ERROR`, not a `5xx`. A client that gets a `5xx` uses its own default, which could be "on"; featuregate picks off on the flag owners' behalf, and the failure still shows in the log, the span, `featuregate_evaluation_errors_total{kind}` and `metadata["sigil.error"]`.
- **Deadlines are in the engine.** The evaluation's timeout is the ABI's `timeout_ms` (the engine stops itself at its next check, instance intact) backed by epoch interruption in the binding (a loop that never checks is killed from outside, which stops the instance; the pool replaces it). `featuregate_pool_replacements_total` counts the replacements. Waiting for a free instance is bounded by the same duration: a pool that's too small for its load fails evaluations with `busy`, visibly, instead of queuing them without bound.
- **`healthcheck` is a subcommand.** The runtime image is distroless with no shell or HTTP client, so the binary probes its own `/readyz` over a socket. It needs no HTTP client dependency.
- **Bodies are bounded.** A context is a handful of attributes; bodies over 64 KiB are refused, and every attribute value is a string in the kind, so a policy can't be made to walk a structure a caller built.

## The context is asserted, not verified

The kind's `user.plan`, `user.region` and `user.beta` come from the OFREP context the caller sends. Residency is therefore enforced on asserted context: the guardrail is exactly as strong as the service in front of it, which has to authenticate the user and set those keys itself (an authenticated BFF or a trusted proxy). featuregate doesn't try to verify them, and the README says so where an operator will read it. See [Trust and deployment](./README.md#trust-and-deployment).

## Value types

A policy decides whether a flag is on and with which variant; `policies/flags/flags.yaml` says what the flag's values are (`manifest.rs`). The mapping to OFREP (`ofrep::evaluation`) answers in that type for enable, disable and ERROR alike, because a typed client that gets `false` for a string flag reports a type mismatch and discards the answer. The manifest is read with the flag policies, is part of the bundle's digest, and is validated with them at load, so a reload that changes a flag's type is as atomic as one that changes its rules.

## Lifecycle details

- A retired bundle's pool is torn down on a blocking thread (`store::Retired`): the last reference is often a request on a tokio worker, and releasing every policy in every instance blocks.
- When a call kills an instance (the deadline, fuel), the pool rebuilds it on a background thread while serving with one fewer; `featuregate_pool_rebuilding` shows how many are in flight and `featuregate_pool_replacements_total` how many were replaced.
- A reload thread that panics is a failed reload (counted, logged, last known good kept), not a dead poll task; a panic on an evaluation thread is a failed evaluation.
- The policies directory is walked with symlinks followed only to places under its root, with a depth cap and a visited set, so a link or a cycle in a mount can't pull in other files or loop. A Kubernetes ConfigMap mount (`..data` and links through it) is read as is.
- A kill-switch name no flag has fails startup; after a reload removes the flag, it is a warning and `featuregate_killed_flags_unmatched`.

## Where featuregate goes beyond OFREP

The wire contract is OFREP's; these are the deliberate choices:

1. **`ERROR` in a `200`.** A failed evaluation answers `200` with `reason: "ERROR"` and the flag's off value and variant (`false` and `off` for a boolean flag), as above, rather than an error status. `errorCode` is left out, so an SDK sees a resolved, off flag.
2. **`TARGETING_KEY_MISSING`.** A context without a `targetingKey` is `400 TARGETING_KEY_MISSING`, OFREP's own code, rather than the generic `INVALID_CONTEXT`: a percentage rollout can't bucket an anonymous user, so the provider is told exactly that.
3. **Safe defaults.** A missing `plan` is `free`, a missing `beta` is `false`, and a missing `region` is `unknown`, which no guardrail accepts: a context that doesn't say where it is gets the flag off, never on.
4. **No unknown flags in bulk.** The bulk answer lists the flags that have a policy, sorted by key, and carries an `ETag` over its body; it doesn't list errors for keys it doesn't serve.
5. **Sigil's reason in `metadata`.** `sigil.policy`, `sigil.decision` and `sigil.reason` ride along as flat string metadata, the form OFREP allows.
