---
title: Embed Sigil in Rust
icon: simple-icons:rust
createTime: 2026/10/01 12:00:00
permalink: /guides/embed-rust/
---

By the end of this guide your Rust program defines the `AlertRouting` kind with the kind builder, exports its kind file, compiles a team's policy with the platform's page rules required, and routes alerts through typed payloads. The crate, `spechtlabs-sigil`, runs Sigil's Go engine compiled to WebAssembly on wasmtime, so a policy decides exactly what it decides in a Go host. [WebAssembly module](/reference/wasm/) lists the module's ABI and every item of the crate, and [One engine for every host](/understanding/one-engine/) explains why it's the Go engine and not a port.

What Rust adds over the TypeScript package is control from outside: a call that runs away is killed with epoch interruption, without a worker, and evaluations can be metered in fuel. This guide ends with both, and with a pool of instances for a service.

The policies are the ones [Getting Started](/getting-started/share-rules/) builds: the platform's `platform.alerts` module, `platform.paging` and `platform.routing` from [step 7](/getting-started/require-guardrails/), and checkout's `checkout.alerts`, which invokes both. The program looks like this by the end:

::: file-tree

- alerting
  - Cargo.toml
  - src
    - lib.rs # the modules below
    - kind.rs # the kind, its decisions and types
    - files.rs # reads .sigil files into virtual files
    - team.rs # compiles a team's policy with the platform's rules required
    - host_functions.rs # split, for the kind's host function
    - bin
      - export-kind.rs # writes policies/alert_routing.sigil
      - route.rs # routes sample alerts
      - limits.rs # kills a runaway call, meters fuel
      - pool.rs # evaluates in parallel on a pool
  - tests
    - kind.rs # fails when the kind file is stale
  - policies
    - alert_routing.sigil # generated, committed
    - platform
      - alerts.sigil
      - paging.sigil
      - routing.sigil
    - checkout
      - alerts.sigil

:::

## Add the crate

```sh
cargo add spechtlabs-sigil
cargo add serde --features derive
cargo add serde_json
```

The library is named `sigil`, so the code says `use sigil::...`. The crate's version is the Sigil release it was built from, and it needs Rust 1.98 or later. The default feature `bundled` embeds the WebAssembly module, which ships inside the crate, so the build needs no Go. To load a module of your own instead, turn the feature off with `default-features = false` and use `Module::from_file`. Everything below ran on Rust 1.98.1 against Sigil 0.6.0 with wasmtime 49.

## Define the kind

The kind is the contract between your program and the policies. `Kind::builder` is the Rust twin of Go's `policy.NewKind`, and this one declares the same kind as the Go program in [Define the input](/getting-started/define-the-input/). Put it in `src/kind.rs`:

```rust
use serde_json::json;
use sigil::{Decision, Kind, Type};

pub struct Routing {
    pub kind: Kind,
    pub page: Decision,
    pub drop: Decision,
    pub notify: Decision,
}

pub fn alert_routing() -> Result<Routing, sigil::Error> {
    let severity = Type::enumeration("Severity", ["critical", "warning", "info"]);
    let alert = Type::structure(
        "Alert",
        [
            ("name", Type::string()),
            ("severity", severity),
            ("labels", Type::map(Type::string(), Type::string())),
            ("firing_for", Type::duration()),
        ],
    );
    let team = Type::structure("Team", [("name", Type::string()), ("oncall", Type::string()), ("channel", Type::string())]);

    let page = Decision::new("page", ["critical_alert", "sustained"]).field("target", Type::string());
    let drop = Decision::new("drop", ["muted", "not_production"]);
    let notify = Decision::new("notify", ["routine", "unrouted"]).field_default("channel", Type::string(), json!("#alerts"));

    let kind = Kind::builder("AlertRouting")
        .version(1)
        .input("alert", alert)
        .input("team", team)
        .decisions([&page, &drop, &notify]) // order = precedence
        .rank_reasons(&page) // each decision's reasons, in declared order
        .rank_reasons(&drop)
        .rank_reasons(&notify)
        .default_outcome(notify.reason("unrouted"))
        .build()?;
    Ok(Routing { kind, page, drop, notify })
}
```

- `Type::enumeration`, `Type::structure` and the scalar, list and map constructors build the kind's types, in the order they're written. A `Type::duration()` value is a string in Sigil's syntax, like `"1h30m"`.
- `Decision::new` declares a decision with every reason a rule may give for it. `field` adds a payload field, and `field_default` gives it a default, which the kind's `default_outcome` needs.
- `decisions` lists the decisions highest precedence first. `rank_reasons` ranks a decision's reasons in the order `Decision::new` declared them.
- `build` returns an error listing every problem when the kind breaks a [validity rule](/reference/kind-files/#validity-rules), so a broken contract fails at startup, not at the first alert.

The handles are checked when the kind is defined. `Decision::reason` panics on a reason the decision doesn't declare, with a did-you-mean, because a kind is defined once at startup, where a typo should stop the program:

```text
thread 'main' panicked at src/bin/typo.rs:6:10:
decision page has no reason "critcal_alert"
  help: did you mean "critical_alert"? page declares: critical_alert, sustained
```

`Decision::try_reason` returns the same error as a value. Every call of the builder is in [The kind builder](/reference/wasm/#defining-a-kind-in-rust).

## Export the kind file

The `sigil` CLI and other hosts read the kind as a kind file. `schema()` writes it byte for byte as Go's `Kind.Schema` writes the same kind. Add `src/bin/export-kind.rs`:

```rust
use alerting::kind::alert_routing;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    std::fs::write("policies/alert_routing.sigil", alert_routing()?.kind.schema())?;
    println!("wrote policies/alert_routing.sigil");
    Ok(())
}
```

```text
$ cargo run -q --bin export-kind
wrote policies/alert_routing.sigil
```

Commit the file next to the policies, and fail the tests when it's stale, in `tests/kind.rs`:

```rust
use alerting::kind::alert_routing;

#[test]
fn policies_alert_routing_sigil_is_current() {
    let committed = std::fs::read_to_string("policies/alert_routing.sigil").unwrap();
    assert_eq!(committed, alert_routing().unwrap().kind.schema());
}
```

```text
$ cargo test --test kind
running 1 test
test policies_alert_routing_sigil_is_current ... ok

test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s
```

A stale kind file can't take effect in the program either. When the files you compile hold a kind file for `AlertRouting` that isn't `schema()`, `Kind::compile` fails with `platform/alert_routing.sigil declares kind AlertRouting, but not as this program defines it`.

## Load the module

Load the module once, at startup, and make an instance from it:

```rust
let sigil = Sigil::bundled()?;
```

`Sigil::bundled` compiles the 11 MB module with wasmtime's compiler, which takes a few seconds, and starts the Go runtime in a fresh instance. A program with more than one instance compiles once and shares the result: `Module::bundled()` returns a `Module`, which is cheap to clone, and `Sigil::new(&module)` makes an instance from it in milliseconds. [Evaluate in parallel](#evaluate-in-parallel) does that.

An instance runs one call at a time, on the calling thread. A `Sigil` is `Send` and `Sync`, and concurrent calls on one wait for each other.

## Compile a team's policy

The engine has no filesystem. It reads virtual files, `SourceFile { path, source }`, and names them in positions by their paths. Read a directory of `.sigil` files into them with `src/files.rs`:

```rust
use std::path::Path;
use std::{fs, io};

use sigil::SourceFile;

/// Every .sigil file below `dir`, with its path relative to `root`, as the CLI names it.
pub fn sigil_files(root: &str, dir: &str) -> io::Result<Vec<SourceFile>> {
    let mut files = Vec::new();
    collect(Path::new(root), Path::new(dir), &mut files)?;
    files.sort_by(|a, b| a.path.cmp(&b.path));
    Ok(files)
}

fn collect(root: &Path, dir: &Path, out: &mut Vec<SourceFile>) -> io::Result<()> {
    for entry in fs::read_dir(dir)? {
        let path = entry?.path();
        if path.is_dir() {
            collect(root, &path, out)?;
        } else if path.extension().is_some_and(|e| e == "sigil") {
            let relative = path.strip_prefix(root).unwrap_or(&path).to_string_lossy().replace('\\', "/");
            out.push(SourceFile::new(relative, fs::read_to_string(&path)?));
        }
    }
    Ok(())
}
```

Compile the team's files with the kind, and require the platform's page rules the way a Go host does with `policy.Require("platform.paging", policy.From(platform))`. Put it in `src/team.rs`:

```rust
use sigil::{CompileOptions, CompileRequirement, Error, KindCompileOptions, Policy, Sigil};

use crate::files::sigil_files;
use crate::kind::Routing;

/// Compiles a team's policy, requiring the platform's page rules from the
/// platform's own documents.
pub fn compile_team(sigil: &Sigil, routing: &Routing, team: &str) -> Result<Policy, Error> {
    let files = sigil_files("policies", &format!("policies/{team}")).expect("reading the team's policies");
    let platform = sigil_files("policies", "policies/platform").expect("reading the platform's policies");
    let options = KindCompileOptions {
        compile: CompileOptions {
            policy: Some(format!("{team}.alerts")),
            require: vec![CompileRequirement::new("platform.paging")],
            trusted_files: platform,
            ..Default::default()
        },
        kind_file: None,
    };
    routing.kind.compile(sigil, &files, options)
}
```

- `Kind::compile` adds the kind file when the files don't hold it, and passes the kind's host functions along. It returns a `Policy`.
- `trusted_files` are your own documents, never read from the team's directory. With them, `platform.paging` must come from them: a team file that defines its own fails the compile, and so does a path that's in both lists. Trust comes from the list a file is in, not from its path.
- `require` makes the compile fail unless `checkout.alerts` invokes `platform.paging` at its top level, with arguments inside the param's bounds.

A compile that fails returns an `Error::Sigil` whose `diagnostics` are the records `sigil check -o json` prints, and whose `Display` lists them. A checkout policy that drops the `paging(...)` call:

```text
$ cargo run -q --bin route
the policy doesn't compile, so nothing was compiled
  help: fix the errors in the diagnostics; the check op reports every problem in a bundle at once
  checkout/alerts.sigil:1:1: error: checkout.alerts doesn't invoke platform.paging (help: the host requires platform.paging for every AlertRouting policy; import it with `use platform.paging` and invoke it at the top level)
```

One that invokes it under `when alert.labels["env"] == "production"`:

```text
$ cargo run -q --bin route
the policy doesn't compile, so nothing was compiled
  help: fix the errors in the diagnostics; the check op reports every problem in a bundle at once
  checkout/alerts.sigil:7:3: error: platform.paging must be invoked unconditionally (help: the host requires platform.paging for every AlertRouting policy; move the call to the top level)
```

And a `checkout/paging.sigil` that defines a `platform.paging` of its own:

```text
$ cargo run -q --bin route
the bundle doesn't check, so nothing was compiled
  help: fix the errors in the diagnostics; the check op reports every problem in a bundle at once
  checkout/paging.sigil:1:8: error: policy platform.paging is defined twice (help: the name belongs to the trusted source, defined at platform/paging.sigil:1:1; documents resolve by name, so each name has one definition)
```

The rules are in [Required policies](/reference/wasm/#required-policies), and why the host needs its own copy in [Why required policies need a trusted source](/understanding/bundles/#why-required-policies-need-a-trusted-source).

## Evaluate and act on the result

Compile once and evaluate for every alert. An input is anything serde can serialize to the kind's inputs, a struct or a `serde_json::Value`. Here's `src/bin/route.rs`:

```rust
use std::collections::BTreeMap;

use alerting::kind::{Routing, alert_routing};
use alerting::team::compile_team;
use serde::{Deserialize, Serialize};
use sigil::{Error, EvalResult, Sigil};

#[derive(Serialize)]
struct Alert {
    name: &'static str,
    severity: &'static str,
    labels: BTreeMap<&'static str, &'static str>,
    firing_for: &'static str,
}

#[derive(Serialize)]
struct Team {
    name: &'static str,
    oncall: &'static str,
    channel: &'static str,
}

#[derive(Serialize)]
struct Input<'a> {
    alert: &'a Alert,
    team: &'a Team,
}

#[derive(Deserialize)]
struct PageData {
    target: String,
}

#[derive(Deserialize)]
struct NotifyData {
    channel: String,
}

fn alert(name: &'static str, severity: &'static str, labels: &[(&'static str, &'static str)], firing_for: &'static str) -> Alert {
    Alert { name, severity, labels: labels.iter().copied().collect(), firing_for }
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let routing = alert_routing()?;
    let sigil = Sigil::bundled()?;
    let policy = compile_team(&sigil, &routing, "checkout").inspect_err(|err| eprintln!("{err}"))?;

    let checkout = Team { name: "checkout", oncall: "checkout-primary", channel: "#checkout-alerts" };
    let alerts = [
        alert("CheckoutErrorRate", "critical", &[("env", "production")], "2m"),
        alert("CheckoutLatencyHigh", "warning", &[("env", "production")], "12m"),
        alert("CheckoutLatencyHigh", "warning", &[("env", "production")], "45m"),
        alert("CheckoutErrorRate", "critical", &[("env", "staging")], "2m"),
        alert("CheckoutQueueStuck", "critical", &[], "3m"),
        alert("CheckoutCanaryLatency", "warning", &[("env", "production")], "5m"),
    ];

    for a in &alerts {
        let res = policy.eval(&Input { alert: a, team: &checkout })?;
        let env = a.labels.get("env").copied().unwrap_or("-");
        println!("{:<22} {:<8} {:<10} {:>4}  → {}", a.name, a.severity, env, a.firing_for, route(&routing, &res)?);
    }
    Ok(())
}

fn route(routing: &Routing, res: &EvalResult) -> Result<String, Error> {
    if let Some(page) = routing.page.matches::<PageData>(res)? {
        return Ok(format!("page {} ({})", page.target, res.reason.as_deref().unwrap_or("?")));
    }
    if let Some(note) = routing.notify.matches::<NotifyData>(res)? {
        let why = if routing.notify.reason("unrouted").is(res)? { "no rule covers it" } else { res.reason.as_deref().unwrap_or("?") };
        return Ok(format!("post to {} ({why})", note.channel));
    }
    Ok(format!("drop ({})", res.reason.as_deref().unwrap_or("?")))
}
```

```text
$ cargo run -q --bin route
CheckoutErrorRate      critical production   2m  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production  12m  → page checkout-primary (sustained)
CheckoutLatencyHigh    warning  production  45m  → page checkout-primary (sustained)
CheckoutErrorRate      critical staging      2m  → drop (not_production)
CheckoutQueueStuck     critical -            3m  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production   5m  → drop (muted)
```

That's the Go program's output from [step 7](/getting-started/require-guardrails/#make-it-stick), decision for decision.

- `page.matches::<PageData>(res)` returns the payload, deserialized into the type you ask for, when the outcome is exactly one `page`, and `None` otherwise. A type that doesn't fit the payload's fields is an error that names the decision.
- `notify.reason("unrouted").is(res)` checks the decision and the reason. Use a handle instead of comparing `res.reason` with a string, which compiles with a typo in it.
- A `Policy` releases its compiled form inside the module when it's dropped. `policy.release()` does it explicitly and reports a failure.
- An input that doesn't fit the kind, a `labels` written as a list say, fails the call with an `Error::Sigil`: `the input: alert.labels: expected a map<string, string>, found a list`. The instance works after it.

A collecting kind can grant a decision more than once, so read it with `match_all::<P>`, which returns every entry of that decision with its typed payload. `matches` and `is` return an error on a collecting kind without precedence.

## Call host functions

A host function answers what a policy can't compute itself. Alert labels are strings, so shared infrastructure that names every team an alert affects writes `affects="payments,checkout"`, and a policy needs to split it. Put the implementation in `src/host_functions.rs`:

```rust
use serde_json::{Value, json};

/// `fn split(string, string) -> list<string>`, for labels that hold a list.
pub fn split(args: Vec<Value>) -> Result<Value, String> {
    let (Some(s), Some(sep)) = (args.first().and_then(Value::as_str), args.get(1).and_then(Value::as_str)) else {
        return Err("split takes two strings".into());
    };
    if s.is_empty() {
        return Ok(json!([]));
    }
    Ok(json!(s.split(sep).collect::<Vec<_>>()))
}
```

Declare the function in the kind with `FnDecl`, and bump the version, since every change to the kind does. In `src/kind.rs`, add `FnDecl` and `host_fn` to the `use sigil::...` line and the builder gets one call:

```rust
    let kind = Kind::builder("AlertRouting")
        .version(2)
        .input("alert", alert)
        .input("team", team)
        .function("split", FnDecl::new([Type::string(), Type::string()], Type::list(Type::string())).implement(host_fn(split)))
        .decisions([&page, &drop, &notify]) // order = precedence
        // rank_reasons and default_outcome as before
```

`FnDecl` declares `fn split(string, string) -> list<string>` in the kind file. Export the kind again, and checkout can use it, pinned to the version that has it:

```sigil
policy checkout.alerts: AlertRouting@2

use platform.paging
use platform.routing

paging(page_after: 10m)
routing(muted: ["CheckoutCanaryLatency"])

// Shared infrastructure names the teams an alert affects in a label.
when alert.severity == info and team.name in split(alert.labels["affects"], ",") {
  notify(reason: routine, channel: team.channel)
}
```

Add two info alerts to the list in `src/bin/route.rs`:

```rust
        alert("PaymentsReplicaLag", "info", &[("env", "production"), ("affects", "payments,checkout")], "8m"),
        alert("EdgeCertExpiring", "info", &[("env", "production"), ("affects", "search")], "1h"),
```

```text
$ cargo run -q --bin export-kind && cargo run -q --bin route
wrote policies/alert_routing.sigil
CheckoutErrorRate      critical production   2m  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production  12m  → page checkout-primary (sustained)
CheckoutLatencyHigh    warning  production  45m  → page checkout-primary (sustained)
CheckoutErrorRate      critical staging      2m  → drop (not_production)
CheckoutQueueStuck     critical -            3m  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production   5m  → drop (muted)
PaymentsReplicaLag     info     production   8m  → post to #checkout-alerts (routine)
EdgeCertExpiring       info     production   1h  → post to #alerts (no rule covers it)
```

- A host function is an `Arc<dyn Fn(Vec<Value>) -> Result<Value, String> + Send + Sync>`, and `host_fn` builds one from a closure. It runs synchronously, in the middle of the evaluation, on the thread that called `eval`, and it can't call back into the `Sigil` instance that's running it: that call fails with an error instead of deadlocking.
- Arguments and results take the JSON form of inputs: durations as strings like `"2h30m"`, timestamps as RFC 3339 strings, enum values as their names.
- A function that returns `Err`, or panics, fails the evaluation with a runtime error that quotes the message: `host function split failed: label too long`. The panic never unwinds through the module.
- Like a Go host's, it must be pure and terminate. Epoch interruption can't stop native code while it runs; see [Bound a call](#bound-a-call).

To stand in for a function without running it, pass `stubs` in `CompileOptions`, in the format of a test file's [`stubs:`](/reference/test-files/#stubs). A stub replaces an implementation of the same name.

## Handle a failed evaluation

A failed evaluation isn't an `Err`. A runtime error, a conflict, a failing assert or a deadline returns a result whose `error` says why, with the kind's default as the outcome, which for `AlertRouting` is a post to `#alerts`. So check `error` before you match: a page that failed to evaluate comes back as a post.

The fix [step 7](/getting-started/require-guardrails/#what-a-guardrail-can-t-stop) suggests is to evaluate the platform's page rules on their own when a team's policy fails. Compile `platform.paging` from your own files next to the team's policy, give every evaluation a deadline, and call `decide` where the loop called `policy.eval`:

```rust
use std::time::Duration;

use sigil::{CompileOptions, EvalOptions, KindCompileOptions, Policy};

// in main, after compiling the team's policy
let platform = sigil_files("policies", "policies/platform")?;
let options = KindCompileOptions { compile: CompileOptions { policy: Some("platform.paging".into()), ..Default::default() }, kind_file: None };
let paging = routing.kind.compile(&sigil, &platform, options)?;

fn decide(policy: &Policy, paging: &Policy, input: &Input) -> Result<EvalResult, Error> {
    let options = EvalOptions::timeout(Duration::from_millis(100));
    let res = policy.eval_with(input, &options)?;
    let Some(error) = &res.error else { return Ok(res) };

    eprintln!("{}: {:?}: {}", policy.name(), error.kind, error.message);
    // The team's policy failed, and res holds the kind's default. Page anyway
    // if the platform's page rules say so.
    let fallback = paging.eval_with(input, &options)?;
    Ok(if fallback.error.is_none() { fallback } else { res })
}
```

`EvalOptions::timeout` bounds one evaluation. Past it, the module stops the evaluation at its next check, and `error.kind` is `FailureKind::Canceled`. Add an alert whose `affects` label names a hundred thousand teams:

```rust
let noise: &'static str = Box::leak("team,".repeat(100_000).into_boxed_str());
// ...
        alert("FleetWideNoise", "info", &[("env", "production"), ("affects", noise)], "1m"),
```

Then give checkout a second page for the same reason as the platform's:

```sigil
when alert.labels["env"] == "production" and alert.severity == critical {
  page(reason: critical_alert, target: "nobody")
}
```

Checkout's policy now conflicts on the production error rate alert, and the noisy alert runs past its deadline. The platform's page still goes out:

```text
$ cargo run -q --bin route
checkout.alerts: Conflict: collect one: 2 candidates at the top rank
CheckoutErrorRate      critical production   2m  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production  12m  → page checkout-primary (sustained)
CheckoutLatencyHigh    warning  production  45m  → page checkout-primary (sustained)
CheckoutErrorRate      critical staging      2m  → drop (not_production)
CheckoutQueueStuck     critical -            3m  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production   5m  → drop (muted)
PaymentsReplicaLag     info     production   8m  → post to #checkout-alerts (routine)
EdgeCertExpiring       info     production   1h  → post to #alerts (no rule covers it)
checkout.alerts: Canceled: the evaluation was stopped: context deadline exceeded
FleetWideNoise         info     production   1m  → post to #alerts (no rule covers it)
```

Remove the second page again before moving on.

The module checks the deadline before every rule, after every host function call and every few hundred elements of a list it goes through. It can't stop what runs between two checks, which is what the next section is for.

| `error.kind` | Why | What to do |
| --- | --- | --- |
| `Runtime` | An expression failed, such as a host function that returned `Err` | Fix the policy or the input it read |
| `Conflict` | Two candidates the kind doesn't rank reached the top | Fix the policy; the fallback decides meanwhile |
| `Assertion` | An `assert` failed; `phase` says whether on the input or the outcome | Reject the input, or fix the policy's assumption |
| `Canceled` | The evaluation ran past its timeout | Not a policy bug; retry, or fall back |

[Handle failed evaluations](/guides/handle-errors/) covers the same failures in a Go host, and the fields of `error` are in the [eval record](/reference/cli/#records).

## Bound a call

The module's own deadline is polite: it stops at the next check. A host that must bound time strictly needs to kill a call from outside, which a Go host does with a context and the TypeScript package does with a worker. In Rust runaway engine work is stopped where it runs, with wasmtime's epoch interruption, at the next epoch check. There are two bounds, and they stack:

- **The timeout**, `EvalOptions::timeout`: the module's own deadline, as above. The result is a `Canceled` failure and the instance lives.
- **The hard deadline**: the timeout plus `grace`, 500 ms unless `EvalOptions::grace` or `Limits::grace` says otherwise. A call that runs past it traps, returns `Error::Timeout`, and **stops the instance**: Go can't resume after a call is cut off mid-function, so every later call on it returns the same `Error::Stopped`. Build a new instance, or use a [pool](#evaluate-in-parallel), which does.

Optionally, **fuel** bounds a call by the work it does rather than the time it takes: build the module with `ModuleConfig { fuel: true }` and set `EvalOptions::fuel`. A call that uses it up returns `Error::OutOfFuel` and stops the instance the same way.

Here's `src/bin/limits.rs`, which evaluates a flood alert naming two million teams, which no 100 ms timeout can stop in time:

```rust
use std::time::{Duration, Instant};

use alerting::kind::alert_routing;
use alerting::team::compile_team;
use serde_json::{Value, json};
use sigil::{Error, EvalOptions, Limits, Module, ModuleConfig, Sigil};

fn input(affects: &str) -> Value {
    json!({
        "alert": { "name": "FleetWideFlood", "severity": "info", "labels": { "env": "production", "affects": affects }, "firing_for": "1m" },
        "team": { "name": "checkout", "oncall": "checkout-primary", "channel": "#checkout-alerts" },
    })
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let routing = alert_routing()?;
    let module = Module::bundled_with(ModuleConfig { fuel: true })?;
    let flood = input(&"team,".repeat(2_000_000));
    let small = input("payments");

    // Epoch interruption: the evaluation may take 100 ms, and 200 ms more before it is killed.
    let limits = Limits { grace: Duration::from_millis(200), ..Limits::default() };
    let sigil = Sigil::with_limits(&module, limits)?;
    let policy = compile_team(&sigil, &routing, "checkout")?;
    let started = Instant::now();
    match policy.eval_with(&flood, &EvalOptions::timeout(Duration::from_millis(100))) {
        Err(err @ Error::Timeout(_)) => println!("killed after {} ms: {err}", started.elapsed().as_millis()),
        other => println!("unexpected: {:?}", other.map(|r| r.decision)),
    }
    println!("stopped: {}", sigil.stopped().is_some());
    println!("next call: {}", policy.eval(&small).unwrap_err().to_string().lines().next().unwrap_or_default());

    // Fuel: bound the work instead of the time.
    let sigil = Sigil::new(&module)?;
    let policy = compile_team(&sigil, &routing, "checkout")?;
    let fuel = EvalOptions { fuel: Some(200_000_000), ..Default::default() };
    let res = policy.eval_with(&small, &fuel)?;
    println!("small alert with fuel: {}", res.decision.as_deref().unwrap_or("?"));
    match policy.eval_with(&flood, &fuel) {
        Err(Error::OutOfFuel) => println!("flood alert: out of fuel, stopped: {}", sigil.stopped().is_some()),
        other => println!("unexpected: {:?}", other.map(|r| r.decision)),
    }
    Ok(())
}
```

```text
$ cargo run -q --release --bin limits
killed after 493 ms: the call ran past its hard deadline of 300ms and was killed; the Sigil instance is stopped
  help: raise the timeout, or look for a policy loop over a large input or a host function that blocks; build a new instance to go on
stopped: true
next call: the Sigil module stopped earlier: a call ran past its hard deadline and was killed
small alert with fuel: notify
flood alert: out of fuel, stopped: true
```

The wall time, 493 ms for a 300 ms deadline, includes encoding a 10 MB input and running the `split` host function on it before the module sees anything, and a deadline can't interrupt either: epoch interruption stops WebAssembly, not your Rust. A host function that may block needs its own I/O timeout, or the evaluation runs on `spawn_blocking` and the instance is abandoned when it hangs. The deadline is a bound on engine work, not on your code. The kill lands the moment execution is back in the module. A ticker thread advances the engine's clock every 2 ms, and only while a call with a deadline runs, so an idle process has no timer waking it.

Two more bounds belong to the instance: `Limits::op_deadline` (60 s) is the hard deadline of every op without a timeout of its own, and `Limits::max_memory` caps linear memory. The module needs about 8 MiB to start; an input that needs more than the cap makes Go's runtime die with `fatal error: out of memory`, which stops the instance with that line in its error.

## Evaluate in parallel

A `Sigil` runs one call at a time, so a service that evaluates in parallel runs several. `Pool` holds them, gives a free one to each call, and replaces one that stopped. The API blocks, so from async code call it on a blocking thread. Here's `src/bin/pool.rs` with tokio:

```rust
use std::sync::Arc;
use std::time::Duration;

use alerting::kind::alert_routing;
use alerting::team::compile_team;
use serde_json::{Value, json};
use sigil::{EvalOptions, Limits, Module, Pool, PoolOptions};

fn input(name: &str, affects: &str) -> Value {
    json!({
        "alert": { "name": name, "severity": "info", "labels": { "env": "production", "affects": affects }, "firing_for": "1m" },
        "team": { "name": "checkout", "oncall": "checkout-primary", "channel": "#checkout-alerts" },
    })
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let routing = Arc::new(alert_routing()?);
    let module = Module::bundled()?; // compile once: it takes seconds
    let options = PoolOptions { size: 4, limits: Limits { grace: Duration::from_millis(200), ..Limits::default() }, acquire_timeout: None };
    let pool = Arc::new(Pool::with_options(&module, options)?);

    // The recipe runs once in every instance, and again in every replacement.
    let kind = Arc::clone(&routing);
    pool.install("checkout", move |sigil| compile_team(sigil, &kind, "checkout"))?;

    // 200 evaluations, from as many tasks as the runtime likes; each blocks a blocking thread.
    let mut tasks = Vec::new();
    for i in 0..200 {
        let pool = Arc::clone(&pool);
        tasks.push(tokio::task::spawn_blocking(move || {
            let affects = if i % 2 == 0 { "payments,checkout" } else { "search" };
            pool.evaluate("checkout", &input("PaymentsReplicaLag", affects), &EvalOptions::timeout(Duration::from_millis(100)))
        }));
    }
    let mut routine = 0;
    for task in tasks {
        let res = task.await??;
        routine += usize::from(res.reason.as_deref() == Some("routine"));
    }
    println!("200 evaluations, {routine} routed to the team's channel");
    println!("{:?}", pool.stats());

    // One call that outlives its deadline is killed. Its caller gets the error, the pool a fresh instance.
    let flood = input("FleetWideFlood", &"team,".repeat(2_000_000));
    let p = Arc::clone(&pool);
    let err = tokio::task::spawn_blocking(move || p.evaluate("checkout", &flood, &EvalOptions::timeout(Duration::from_millis(100)))).await?.unwrap_err();
    println!("flood: {}", err.to_string().lines().next().unwrap_or_default());
    println!("{:?}", pool.stats());

    let p = Arc::clone(&pool);
    let res = tokio::task::spawn_blocking(move || p.evaluate("checkout", &input("PaymentsReplicaLag", "payments,checkout"), &EvalOptions::default())).await??;
    println!("next alert: {} {}", res.decision.as_deref().unwrap_or("?"), res.reason.as_deref().unwrap_or("?"));
    Ok(())
}
```

```text
$ cargo run -q --release --bin pool
200 evaluations, 100 routed to the team's channel
PoolStats { size: 4, idle: 4, installed: 1, replaced: 0 }
flood: the call ran past its hard deadline of 300ms and was killed; the Sigil instance is stopped
PoolStats { size: 4, idle: 4, installed: 1, replaced: 1 }
next alert: notify routine
```

- `install` takes a recipe that compiles the policy in an instance. It runs once in each instance and again in every replacement, so it's where `Kind::compile` goes. The first instance validates it: a recipe that fails leaves the pool as it was. `pool.compile(name, &files, options)` is `install` with `Sigil::compile`.
- A reload is another `install` under the same name. The other instances follow one at a time while the rest keep serving, so for a moment some evaluations see the old policy and some the new, never a half-installed one.
- A call that stops its instance gets it replaced, with every policy compiled again, before `evaluate` returns. The error is that caller's, and `PoolStats::replaced` counts it.
- `PoolOptions::acquire_timeout` bounds the wait for a free instance and fails with `Error::Busy`, so a saturated service sheds load instead of queueing.
- Each instance holds its own copy of every policy and the module's memory, so size the pool for the cores the service has.
- Called on an async worker thread directly, a pool or a `Sigil` still works, but the call blocks that thread, and the crate moves it to a thread of its own first because wasmtime's WASI can't block inside a runtime. `spawn_blocking` avoids both.

## Next steps

- [WebAssembly module](/reference/wasm/): every item of the crate, and the ABI for hosts in other languages.
- [Per-team policies](/guides/team-policies/) and [Test your policies](/guides/test-policies/): the policy side, which doesn't change with the host's language.
- [Check policies in CI](/guides/ci/): check the team's policies against the exported kind file before your program ever loads them.
