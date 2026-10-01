# spechtlabs-sigil

Sigil for Rust: the Go engine compiled to WebAssembly and run on [wasmtime](https://wasmtime.dev), behind a typed API. It is the twin of the TypeScript package `@spechtlabs/sigil`: the same ops, the same error model, the same kind builder, in Rust idiom.

The module is the stock `sigil` CLI's engine without a filesystem or a terminal. For the same files and input, its answers are the CLI's `-o json` records: the test suite compares them field for field. Regexes, Unicode comparison, duration arithmetic and integer overflow all behave exactly as they do in Go, because it is the same Go code.

What Rust adds over the other hosts is control from outside. A runaway evaluation, a slow host function or a loop in the engine is killed with wasmtime's epoch interruption, without a worker thread, and a [`Pool`](#parallelism-and-the-pool) replaces the instance it killed.

## Install

```sh
cargo add spechtlabs-sigil
```

and `use sigil::...`: the library is named `sigil`. The crate is versioned with Sigil: `spechtlabs-sigil` 0.7.0 runs the engine of Sigil 0.7.0.

The default feature `bundled` embeds `sigil.wasm` in the crate. The package ships the module in `module/sigil.wasm` (the release builds it from the tag and copies it there before `cargo publish`), so installing the crate needs no Go. `build.rs` reads `$SIGIL_WASM` first, so a build can swap in another module, and in a checkout of the repository it falls back to `../../dist/wasm/sigil.wasm`, which `mise run wasm-build` writes. Without the feature, load the module yourself with `Module::from_file` or `Module::from_bytes`.

## Build from the repository

From the repository root:

```sh
mise run wasm-build   # dist/wasm/sigil.wasm
mise run rust-build   # cargo build, with the module bundled
mise run rust-test    # cargo test against the real module and the sigil CLI
mise run rust-lint    # cargo fmt --check and cargo clippy -D warnings
mise run rust-package # cargo package with the module inside, as the release publishes it
```

The tests build the `sigil` CLI and run the Go kind generator with `go`, to compare their output with this crate's, so `go` must be on `PATH` too.

## Example

```rust
use serde_json::json;
use sigil::{CompileOptions, EvalOptions, Sigil, SourceFile, host_fn};
use std::time::Duration;

let sigil = Sigil::bundled()?;
let files = [
    SourceFile::new("deploy_approval.sigil", kind_source),
    SourceFile::new("teams/payments/production.sigil", policy_source),
];

for d in sigil.check(&files, &Default::default())? {
    println!("{}", d.render());
}

let policy = sigil.compile(
    &files,
    CompileOptions {
        policy: Some("payments.production".into()),
        functions: [("split".to_string(), host_fn(|args| {
            let (Some(s), Some(sep)) = (args[0].as_str(), args[1].as_str()) else { return Err("split takes two strings".into()) };
            Ok(json!(s.split(sep).collect::<Vec<_>>()))
        }))].into(),
        ..Default::default()
    },
)?;
let result = policy.eval_with(&input, &EvalOptions::timeout(Duration::from_millis(50)))?;
println!("{:?} {:?} {:?}", result.decision, result.reason, result.payload);
```

`input` is anything serde can serialize to the kind's inputs: a struct or a `serde_json::Value`.

## API

| Call | Returns | Like |
| --- | --- | --- |
| `Module::bundled()`, `from_file(path)`, `from_bytes(&[u8])` | `Module`, compiled once and cheap to clone | |
| `Sigil::new(&Module)`, `Sigil::bundled()` | `Sigil`, one instance | |
| `sigil.version()` | `VersionInfo` | `sigil version -o json` |
| `sigil.check(&files, &CheckOptions)` | `Vec<Diagnostic>`, errors and warnings | `sigil check -o json` |
| `sigil.compile(&files, CompileOptions)` | `Policy`, or an `Error::Sigil` with diagnostics | |
| `sigil.explain(&files, &ExplainOptions)` | `Vec<Explanation>` | `sigil explain -o json` |
| `sigil.format(source, &FormatOptions)` | the canonical source | `sigil fmt` |
| `policy.eval(&input)`, `eval_with(&input, &EvalOptions)` | `EvalResult` | `sigil eval -o json` |
| `policy.explain()` | `Explanation` | |
| `policy.release()` | frees the handle; dropping a `Policy` does it too | |
| `sigil.stopped()` | `Option<StoppedError>` | |

The record types (`Diagnostic`, `EvalResult`, `EvalEntry`, `EvalFailure`, `FailedAssert`, `Explanation`, `ExplainEntry`, `VersionInfo`) implement `Serialize` and `Deserialize` and match the CLI's JSON field for field. A field the CLI leaves out when it's empty is an `Option` or an empty `Vec`.

A `Policy` holds its instance alive, so it may outlive the `Sigil` it came from, and is `Send + Sync`. A `Sigil` runs one call at a time: concurrent calls wait for each other. A host function that calls back into the instance that runs it gets an error, not a deadlock.

### Options

| Option | Of | Is |
| --- | --- | --- |
| `policies`, `require`, `lints`, `trusted_files` | `CheckOptions` | the `check` op's fields of those names |
| `policy`, `require`, `trusted_files`, `stubs`, `functions` | `CompileOptions` | the `compile` op's fields; `functions` holds the implementations |
| `policy`, `trusted_files` | `ExplainOptions` | the `explain` op's fields |
| `timeout`, `grace`, `fuel` | `EvalOptions` | see [Deadlines](#deadlines-and-cost-bounds) |
| `grace`, `op_deadline`, `max_memory` | `Limits` | per instance, `Sigil::with_limits` |
| `fuel` | `ModuleConfig` | meters fuel, `Module::from_bytes_with` |

### Host functions

A host function is an `Arc<dyn Fn(Vec<Value>) -> Result<Value, String> + Send + Sync>`; `host_fn` builds one from a closure. Arguments are the Sigil ones as JSON: durations as `1h30m`, timestamps as RFC 3339 strings, enum values as their names, structs as objects. An `Err` fails the policy's call with a runtime error that quotes the message, and so does a panic, which is caught so it never unwinds through the module. `stubs` replace an implementation of the same name, as a test file's `stubs:` do.

A host function runs synchronously, on the thread that called `eval`. Epoch interruption can't stop native code: a function that blocks holds the call until it returns, and the deadline bites at the next instruction of the module. Give a function that may block its own timeout.

### Required policies

`CompileOptions::require` and `trusted_files` are the module's `compile` fields of those names: the platform's guardrails come from the host's own documents, and a team's bundle can't omit, gate, redefine or loosen them. `compile` fails with the diagnostics when it does.

## Errors

| Variant | For | The instance |
| --- | --- | --- |
| `Error::Sigil(SigilError { message, help, diagnostics })` | an op with `ok: false`, a kind that breaks a rule, a request that can't be encoded | works |
| `Error::Stopped(StoppedError)` | Go's runtime exited or panicked, the module trapped, or a call after one did | dead |
| `Error::Timeout(Duration)` | a call killed at its hard deadline | dead |
| `Error::OutOfFuel` | a call killed for using its fuel | dead |
| `Error::NoPolicy(name)`, `Error::Busy(Duration)` | a `Pool` without that policy, or saturated past its `acquire_timeout` | works |

`error.is_stopped()` tells a dead instance apart. A failed evaluation is not an error: its `EvalResult::error` says why (`FailureKind::Runtime`, `Conflict`, `Assertion` or `Canceled`), and its outcome is the kind's fallback, as with the CLI.

An instance that stops is dead for good, because Go can't resume after a trap: every later call returns the same `StoppedError`, whose message quotes the first and last lines of the module's standard error (Go's `fatal error: ...` and where it happened). Build a new `Sigil`, or use a `Pool`, which does it for you.

## Deadlines and cost bounds

Two mechanisms, and they stack:

1. **The ABI's timeout**, `EvalOptions::timeout`. The module checks it while it evaluates and answers with a `canceled` failure: a normal `EvalResult`, and the instance lives. It checks before every rule and assert, after every host function and every few hundred elements of a quantifier.
2. **Epoch interruption**, the hard deadline. A ticker thread advances the engine's clock every 2 ms, on an absolute schedule, while a call with a deadline runs (and sleeps otherwise), and the call traps when its deadline passes: the `timeout` plus `grace` (500 ms by default), or `Limits::op_deadline` (60 s) for an evaluation without a timeout and for every other op. The call returns `Error::Timeout`, and the instance is stopped, because it was killed mid-call. This is what catches an engine loop the ABI's checks don't reach, and a host function that returned late.

Optionally, **fuel**: compile the module with `ModuleConfig { fuel: true }` and set `EvalOptions::fuel` to bound a call by work done rather than time. It costs a few percent of speed, so it's off by default. Running out of fuel stops the instance like a killed call.

`Limits::max_memory` caps an instance's linear memory. The module needs about 8 MiB to start; an input that needs more than the cap makes Go's runtime die with `fatal error: out of memory`, which is a stopped instance with that message.

## Parallelism and the pool

```rust
use sigil::{CompileOptions, EvalOptions, Module, Pool};
use std::sync::Arc;

let module = Module::bundled()?;           // compile once: it takes seconds
let pool = Arc::new(Pool::new(&module, 4)?);
pool.compile("checkout", &files, CompileOptions { policy: Some("checkout.alerts".into()), ..Default::default() })?;

let result = pool.evaluate("checkout", &input, &EvalOptions::timeout(std::time::Duration::from_millis(50)))?;
```

| Call | Does |
| --- | --- |
| `Pool::new(&Module, size)`, `Pool::with_options(&Module, PoolOptions)` | builds `size` instances |
| `pool.install(name, recipe)` | `recipe: Fn(&Sigil) -> Result<Policy, Error>` runs once in each instance, and again in every replacement; use it with `Kind::compile` |
| `pool.compile(name, &files, CompileOptions)` | `install` with `Sigil::compile` |
| `pool.remove(name)`, `pool.names()` | |
| `pool.evaluate(name, &input, &EvalOptions)` | evaluates on a free instance, waiting for one |
| `pool.explain(name)`, `pool.with_policy(name, f)` | a free instance's policy |
| `pool.stats()` | `PoolStats { size, idle, installed, replaced }` |

- An install validates on the first instance, so a recipe that fails leaves the pool as it was. The other instances follow one at a time while the rest serve: for a moment some evaluations see the old policy and some the new, never a half-installed one.
- A call that stops its instance (an `Error::Timeout`, `OutOfFuel` or `Stopped`) gets the instance replaced before `evaluate` returns, with every policy compiled again. The error is that caller's, and the next call runs on a fresh instance. `PoolStats::replaced` counts them.
- `PoolOptions::acquire_timeout` bounds the wait for a free instance and fails with `Error::Busy`, so a saturated service sheds load instead of queueing forever.
- The API blocks. Called directly on a Tokio worker thread it still works, because the crate moves the call to a thread of its own (wasmtime-wasi can't block inside a runtime), but it blocks that worker and costs a thread spawn. From async code, call it on a blocking thread:

```rust
let pool = Arc::clone(&pool);
let result = tokio::task::spawn_blocking(move || pool.evaluate("checkout", &input, &Default::default())).await??;
```

Each instance holds its own copy of every policy and the module's memory, so size the pool for the cores the service has.

## Defining a kind

`Kind` is the twin of Go's `policy.NewKind` and TypeScript's `defineKind`. A builder with no macro: every rule of the model is checked when it builds, and `build()` returns an error listing every problem at once.

```rust
use serde_json::json;
use sigil::{Decision, Kind, Type};

let severity = Type::enumeration("Severity", ["critical", "warning", "info"]);
let alert = Type::structure("Alert", [
    ("name", Type::string()),
    ("severity", severity),
    ("labels", Type::map(Type::string(), Type::string())),
    ("firing_for", Type::duration()),
]);
let page = Decision::new("page", ["critical_alert", "sustained"]).field("target", Type::string());
let notify = Decision::new("notify", ["routine", "unrouted"]).field_default("channel", Type::string(), json!("#alerts"));

let kind = Kind::builder("AlertRouting")
    .version(1)
    .input("alert", alert)
    .decisions([&page, &notify])            // `collect one`, highest precedence first
    .rank_reasons(&page)
    .rank_reasons(&notify)
    .default_outcome(notify.reason("unrouted"))
    .build()?;

std::fs::write("policies/alert_routing.sigil", kind.schema())?;   // check it in
let policy = kind.compile(&sigil, &files, Default::default())?;    // adds the kind file, passes the kind's functions
```

`kind.schema()` is byte for byte what Go's `Kind.Schema` writes for the same kind, and what the example services' Go hosts exported: the tests run the same generator the TypeScript tests use and compare. A repository can check the file in, and a test can assert that the file is not stale with `assert_eq!(kind.schema(), std::fs::read_to_string(path)?)`. `Kind::compile` also fails on a kind file among the files that isn't `schema()`, which catches a stale export.

| Builder call | Kind file |
| --- | --- |
| `.version(n)`, `.accepts(n)` | `kind K version n, accepts: m` |
| `.input(name, Type)` | `input alert: Alert` |
| `.enumeration(Type)` | an enum nothing reaches |
| `.function(name, FnDecl::new(params, result).implement(host_fn(..)))` | `fn split(string, string) -> list<string>` |
| `.decisions([&d, ..])`, `.collect_all([&d, ..])` | `collect one` with decisions highest first, or `collect all` |
| `.precedence([&d, ..])` | `precedence a > b` for a `collect all` kind |
| `.rank_reasons(&d)`, `.rank_outcomes([d.reason("x"), ..])` | `precedence page: critical_alert > sustained` |
| `.exclusive([&a, &b])`, `.exclusive([OutcomeRef::from(a.reason("x")), ..])` | `exclusive a, b` |
| `.default_outcome(d.reason("x"))`, `.conflict(..)` | `default notify(reason: unrouted)` |

Types: `Type::string()`, `bool()`, `int()`, `float()`, `duration()`, `timestamp()`, `list(T)`, `map(K, V)`, `optional(T)`, `enumeration(name, values)` and `structure(name, fields)`. A payload field may carry a default, given as the JSON value a host would pass: `field_default("ttl", Type::duration(), json!("90m"))` writes `ttl: duration = 1h30m`. String constants are quoted as Go's `%q` does, using Unicode tables that may be a version away from Go's for characters assigned in between.

### Reading results

```rust
#[derive(serde::Deserialize)]
struct PageData { target: String }

if let Some(data) = page.matches::<PageData>(&result)? {      // Some only when the outcome is exactly one entry of `page`
    println!("page {}", data.target);
}
if notify.reason("unrouted").is(&result)? { /* ... */ }
for granted in admin.match_all::<Ttl>(&result)? { /* collecting kinds: every entry */ }
```

`Decision::reason` panics on a reason the decision doesn't declare, with a did-you-mean: a kind is defined once at startup, where a typo should stop the program. `try_reason` returns an error instead. `matches` and `is` fail on the result of a `collect all` kind without precedence, which has no single outcome; `match_all` reads those.

## Testing

`cargo test` runs, against the real module and the stock CLI:

- every op, with CLI parity: `check`, `eval`, `explain` and the records equal `sigil ... -o json` for the deploy-gates and alert-routing policies
- host functions, stubs, panics, wrong result types and re-entrance
- required policies: omitted, gated, redefined, out of bounds, and a path in both lists
- deadlines: the ABI's timeout, epoch kills (engine loop, slow host function), fuel, out-of-memory
- stopped instances: every later call repeats the error, and dropping their policies is quiet
- the pool: parallel evaluation, rolling installs, replacement after a kill, saturation
- no memory growth over 10,000 evaluations
- kind builder parity with Go's `Kind.Schema` for six kinds, and every builder error

## Limitations

- A host function can't be interrupted while it runs, see [Host functions](#host-functions).
- Integers are exact: `serde_json` numbers carry `i64` and `f64`.
- The instance is single-threaded, like the module: one call at a time.
- Nothing runs in `no_std` or in the browser; the crate needs wasmtime's JIT and OS threads. For a browser, use the TypeScript package.
