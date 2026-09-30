---
title: WebAssembly module
icon: mdi:cube-outline
createTime: 2026/09/30 12:00:00
permalink: /reference/wasm/
---

The WebAssembly module `sigil.wasm`, built from `cmd/sigil-wasm`, and the TypeScript package `@spechtlabs/sigil` that loads it: the module's ABI, its requests and responses, and every export of the package.

To embed Sigil in TypeScript step by step, see [Embed Sigil in TypeScript](/guides/embed-typescript/). Why: [One engine for every host](/understanding/one-engine/).

::: warning Unpublished
The package isn't on npm yet; build it from the repository. Each [release](https://github.com/SpechtLabs/sigil/releases) attaches the module as the archive `sigil_<version>_wasip1_wasm`, and [Building](#building) shows how to build it yourself.
:::

## Building

```sh
mise run wasm-build   # dist/wasm/sigil.wasm
mise run ts-build     # bindings/typescript/dist, with sigil.wasm copied in
```

`wasm-build` runs:

```sh
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -trimpath -ldflags='-s -w' -o dist/wasm/sigil.wasm ./cmd/sigil-wasm
```

| Property | Value |
| --- | --- |
| Target | WASI preview 1 (`wasip1`), 32-bit linear memory |
| Shape | Reactor: exports `_initialize`, no `_start` |
| Size | 10.9 MB, 2.9 MB gzipped |
| Engine | The stock `sigil` CLI's, package `internal/engine`, without a filesystem or a terminal |
| ABI version | 1 |

## Exports

| Export | Signature | Does |
| --- | --- | --- |
| `_initialize` | `() -> ()` | Starts the Go runtime. Call it once, after instantiating, before any other export |
| `memory` | memory | The module's linear memory. Requests and responses live in it |
| `sigil_abi_version` | `() -> i32` | Returns `1`. Changes only when these signatures or the rules on this page do |
| `sigil_alloc` | `(size i32) -> i32` | Allocates `size` bytes and returns their address |
| `sigil_free` | `(ptr i32, size i32) -> ()` | Releases a buffer from `sigil_alloc`, or a response |
| `sigil_call` | `(ptr i32, len i32) -> i64` | Handles one request. Returns the response as `(respPtr << 32) \| respLen` |

## Imports

| Import | Signature | Does |
| --- | --- | --- |
| `sigil.host_call` | `(reqPtr i32, reqLen i32) -> i64` | Runs one [host function](#host-functions) for the module. Returns the response as `(ptr << 32) \| len` |
| `wasi_snapshot_preview1.*` | WASI preview 1 | The Go runtime's system calls |

The module imports these WASI functions: `args_get`, `args_sizes_get`, `clock_time_get`, `environ_get`, `environ_sizes_get`, `fd_close`, `fd_fdstat_get`, `fd_fdstat_set_flags`, `fd_prestat_dir_name`, `fd_prestat_get`, `fd_read`, `fd_write`, `path_filestat_get`, `path_open`, `poll_oneoff`, `proc_exit`, `random_get` and `sched_yield`.

- `clock_time_get` must tell the time: [deadlines](#deadlines) read it.
- `fd_write` on standard error carries the Go runtime's message when the module stops.
- The module opens no files and reads no environment. No preopened directories are needed, and the filesystem calls may fail.

## Calls

One call, from the host's side:

1. Allocate `len` bytes with `sigil_alloc` and write the request there.
2. Call `sigil_call(ptr, len)`.
3. Read `respLen` bytes at `respPtr`.
4. Free the request with `sigil_free(ptr, len)` and the response with `sigil_free(respPtr, respLen)`.

- A request is one JSON object, and so is its response.
- A request that doesn't start with `{` is reserved for another encoding.
- `sigil_call` never traps on a bad request: a request that isn't in memory from `sigil_alloc`, or isn't JSON, gets a response with `ok: false`.

### Memory

- Every buffer the host sees, a request, a response, or a host function's request or response, stays where it is until it's freed.
- A buffer that isn't freed stays for the life of the instance.
- Freeing a buffer twice, or an address `sigil_alloc` didn't return, does nothing.
- `sigil_free`'s `size` is ignored; pass the size you allocated.
- Compiled policies live in the module, as [handles](#handles), until the `release` op.

### Concurrency

- An instance handles one call at a time, on one thread.
- A host function must not call `sigil_call` on the instance that's running it.
- Hosts that want parallelism run several instances. Handles belong to the instance that returned them.

## Envelope

Every request has an `op` and may have an `id`. Every response has `ok`, and echoes the `id`:

```json
{ "op": "compile", "id": 2, "files": [...], "policy": "checkout.alerts" }
```

```json
{ "id": 2, "ok": true, "policy": "checkout.alerts", "diagnostics": [], "handle": 1 }
```

| Request field | Type | Rules |
| --- | --- | --- |
| `op` | string | One of the [ops](#ops) |
| `id` | number | Optional. Echoed in the response |
| every other field | | The op's. A field no op knows fails the request, so a misspelled field never passes for a default |

A failed op answers with `ok: false`, an `error`, and the diagnostics that stopped it when there are any:

| Response field | Type | Holds |
| --- | --- | --- |
| `ok` | bool | Whether the op succeeded |
| `id` | number | The request's `id`. Left out when the request isn't JSON, or its `id` isn't a number |
| `error.message` | string | What failed |
| `error.help` | string | How to fix it, when known |
| `diagnostics` | list | The [diagnostic records](/reference/cli/#sigil-check) that stopped the op |

```json
{ "id": 7, "ok": false, "error": { "message": "no compiled policy has handle 1", "help": "pass a handle compile returned, before releasing it" } }
```

A request with a field no op knows, `timeoutMs` for `timeout_ms`:

```json
{ "ok": false, "error": { "message": "the request isn't a JSON object of the fields an op takes: json: unknown field \"timeoutMs\"", "help": "send one JSON object, such as {\"op\": \"version\"}" } }
```

## Ops

| Op | Request | Response | Like |
| --- | --- | --- | --- |
| `version` | | The build | `sigil version -o json` |
| `check` | `files`, `trusted_files?`, `policies?`, `require?`, `lints?` | `diagnostics` | `sigil check -o json` |
| `compile` | `files`, `trusted_files?`, `policy?`, `require?`, `stubs?`, `functions?` | `handle`, `policy`, `diagnostics` | |
| `eval` | `handle`, `input`, `timeout_ms?` | The eval record | `sigil eval -o json` |
| `explain` | `handle`, or `files`, `trusted_files?`, `policy?` | `explanations` | `sigil explain -o json` |
| `format` | `source`, `path?` | `source`, `formatted` | `sigil fmt` |
| `release` | `handle` | nothing | |

### Files

```json
{ "path": "checkout/alerts.sigil", "source": "policy checkout.alerts: AlertRouting@1\n..." }
```

- `files` play the part of the CLI's paths, in the order given, and hold the kind documents too.
- A path appears in positions as the CLI prints it for the same relative path. It's cleaned first: `./a//b.sigil` is `a/b.sigil`.
- A file given twice with the same source is read once. The same path with two sources fails the request.
- `trusted_files` are the host's own documents. They load as the trusted source [`policy.From`](/reference/bundles/#trusted-sources) reads, so no other document may take one of their names.
- A path among both `files` and `trusted_files` fails the request, whatever the sources.

### `version`

Returns `version`, `commit`, `commitTime`, `dirty`, `goVersion` and `platform`, which is `wasip1/wasm`.

```json
{ "id": 1, "ok": true, "version": "v0.5.3-0.20260930162129-3fd9682288ff+dirty", "commit": "3fd9682288ffffb121df955a8a1a13fb0f62f401", "commitTime": "2026-09-30T16:21:29Z", "dirty": true, "goVersion": "go1.27.1", "platform": "wasip1/wasm" }
```

### `check`

| Field | Type | Holds |
| --- | --- | --- |
| `files` | list of files | Required |
| `trusted_files` | list of files | The host's documents |
| `policies` | list of strings | Name patterns of the policies to check; every policy without it |
| `require` | list of `{policy, trusted?, roots?}` | A [configuration file's `require`](/reference/config/), with `trusted` paths among all the files, trusted or not |
| `lints` | map of lint name to `off`, `warn` or `error` | A configuration file's `lints` |

- Returns `diagnostics`, the records `sigil check -o json` prints for the same files, errors and warnings alike. A check that finds errors still has `ok: true`.
- The check is strict: the request holds every file a requirement can name.
- An unknown lint or level fails the request, as in a configuration file.

```json
{ "ok": true, "diagnostics": [{ "severity": "error", "file": "a.sigil", "document": "a.b", "message": "unknown field \"sevrity\" on type Alert", "help": "did you mean \"severity\"? Alert declares: name, severity, labels, firing_for", "line": 2, "column": 12 }] }
```

### `compile`

| Field | Type | Holds |
| --- | --- | --- |
| `files` | list of files | Required |
| `trusted_files` | list of files | The host's documents, such as its guardrails |
| `policy` | string | The policy to compile. May be left out when the files hold exactly one |
| `require` | list of `{policy}` | The policies the compiled one must invoke; see [Required policies](#required-policies) |
| `stubs` | map of function name to stub | Stand-ins for host functions, in the format of a test file's [`stubs:`](/reference/test-files/#stubs) |
| `functions` | list of strings | The host functions the host implements through [`host_call`](#host-functions) |

- Compiles the policy the way `sigil eval` compiles its root: in a bundle of the policy and what it uses, so an error in another document doesn't stop it.
- Returns `handle`, the compiled policy's [handle](#handles), `policy`, its name, and `diagnostics`, the problems in documents the policy doesn't use.
- A policy that doesn't compile fails the op, with the diagnostics.
- A stub replaces a host function of the same name. A call to a function neither implements fails the way it does in the stock `sigil` binary.
- A name in `functions` the kind doesn't declare fails the request:

```json
{ "ok": false, "error": { "message": "functions: the kind AlertRouting has no host function splt", "help": "did you mean \"split\"? the kind declares: split" } }
```

### `eval`

| Field | Type | Holds |
| --- | --- | --- |
| `handle` | number | Required |
| `input` | object | Required. One key per input the kind declares, as [`sigil eval`](/reference/cli/#input-documents) reads it |
| `timeout_ms` | number | The evaluation's [deadline](#deadlines) in milliseconds. None when left out, zero or negative |

- Returns the [record `sigil eval -o json` prints](/reference/cli/#records), in the envelope.
- A failed evaluation has `ok: true`. Its record's `error` says why, and its outcome is the kind's fallback. `error.kind` is `runtime`, `conflict`, `assertion` or `canceled`.
- An input that doesn't fit the kind fails the op: `the input: alert.severity: "critcal" is not a value of Severity`.

```json
{ "id": 3, "ok": true, "payload": { "channel": "#checkout-alerts" }, "policy": "checkout.alerts", "decision": "notify", "reason": "routine", "outcome": [{ "payload": { "channel": "#checkout-alerts" }, "decision": "notify", "reason": "routine", "policy": "checkout.alerts", "position": "checkout/alerts.sigil:11:3" }], "trace": [...] }
```

### `explain`

| Field | Type | Holds |
| --- | --- | --- |
| `handle` | number | A compiled policy to explain |
| `files`, `trusted_files` | lists of files | Files whose policies to explain, instead of a handle |
| `policy` | string | With `files`: a name pattern of the policies to explain; every policy without it |

- Returns `explanations`, the records `sigil explain -o json` prints. With a handle, it holds one.
- A `handle` and `files` in one request fail it.

### `format`

| Field | Type | Holds |
| --- | --- | --- |
| `source` | string | Required. The source to format |
| `path` | string | The file's path, for diagnostics. `<stdin>` when left out |

- Returns `source`, in `sigil fmt`'s canonical style, and `formatted`, `true` when the source already was.
- A source that doesn't parse fails the op, with the syntax errors as diagnostics.

### `release`

| Field | Type | Holds |
| --- | --- | --- |
| `handle` | number | Required |

Drops the compiled policy. The handle is invalid from then on, and releasing it again fails.

## Handles

- A handle is an unsigned 32-bit number, never 0.
- A released handle isn't handed out again until the numbers wrap.
- `eval` and `explain` take a handle, so per evaluation only the input and the result cross the boundary.
- An evaluation doesn't change its compiled policy.

## Required policies

`compile`'s `require` is the host's `policy.Require`, and `trusted_files` its `policy.From`:

```json
{ "op": "compile", "files": [...], "trusted_files": [...], "policy": "checkout.alerts", "require": [{ "policy": "platform.paging" }] }
```

- The compiled policy must invoke each required policy at its top level, with arguments within its params' bounds, and no document may redefine it. The diagnostics are the Go API's; see [Required policies](/reference/evaluation/#required-policies).
- With `trusted_files`, each required policy must be defined in them. One the bundle defines instead fails the compile, which Go's `policy.From` would accept.
- Without `trusted_files`, a policy among `files` satisfies the requirement.
- A requirement takes only `policy`. `trusted` or `roots` fail the request: `require[0]: compile's requirements take only a policy`.
- A required policy of another kind doesn't apply to the compiled one.

A required policy the bundle defines and the trusted files don't:

```json
{ "ok": false, "error": { "message": "require[0]: platform.paging isn't among the trusted files", "help": "fix the errors in the diagnostics; the check op reports every problem in a bundle at once" }, "diagnostics": [{ "severity": "error", "file": "platform/paging.sigil", "message": "platform.paging must come from the trusted files, but it's defined here", "help": "the host reads a required policy only from its trusted source, as policy.From does; define it there, and remove this definition", "line": 1, "column": 1 }] }
```

A path among both lists:

```text
platform/paging.sigil is among both the files and the trusted files
  help: a trusted file's path belongs to it; give the other file another path
```

## Host functions

While `sigil_call` runs an evaluation, a policy may call a host function named in `compile`'s `functions`. The module then calls `sigil.host_call` with a request:

```json
{ "function": "split", "args": ["payments,checkout", ","] }
```

and the host answers with one of:

```json
{ "result": ["payments", "checkout"] }
```

```json
{ "error": "label too long" }
```

1. The host reads the request at `reqPtr`, `reqLen` bytes long. The module owns it and frees it once `host_call` returns.
2. The host allocates the response with `sigil_alloc`, from inside `host_call`, and writes it there.
3. The host returns `(ptr << 32) | len`. The module owns the response from then on.

- Args are plain values, as the eval record prints them: durations in Sigil's syntax, `1h30m`, timestamps as RFC 3339 strings, enum values as their names, structs as objects.
- The result is read as an input's value of the function's result type. A `null` result is `{"result": null}`.
- An `error` fails the call with a runtime error that quotes it: `checkout/alerts.sigil:10:46: host function split failed: label too long`.
- An answer that isn't a JSON object, has neither `result` nor `error`, has a result of the wrong type, or wasn't allocated with `sigil_alloc` fails the call with a runtime error that says so:

```text
checkout/alerts.sigil:10:46: host function split failed: the host returned result: expected a list<string>, found a number
  help: the kind declares `fn split(string, string) -> list<string>`; return a value of its result type
```

## Deadlines

`eval`'s `timeout_ms` is checked while the evaluation runs, from `clock_time_get`:

- before every rule and assert,
- after every host function call, and
- every few hundred elements a quantifier, filter, membership test, list operator or `has` goes through.

Past the deadline, the evaluation stops at the next check, and the record's `error.kind` is `canceled`:

```json
{ "ok": true, "payload": { "channel": "#alerts" }, "error": { "kind": "canceled", "message": "the evaluation was stopped: context deadline exceeded", "help": "the evaluation ran past its deadline or was canceled, so nothing it found counts; give it more time, or look for a loop over a large input" }, "policy": "checkout.alerts", "decision": "notify", "reason": "unrouted", "outcome": [{ "payload": { "channel": "#alerts" }, "decision": "notify", "reason": "unrouted" }], "trace": [] }
```

A host function, or one long step between two checks, runs to its end. To bound time strictly, run the module where the host can terminate it, such as a Web Worker.

## Limits

| Limit | Value |
| --- | --- |
| Memory | 4 GiB of linear memory, requests and responses included |
| Calls per instance | One at a time |
| Handles per instance | 2³² − 1 at once |
| A panic while handling a request | A response with `ok: false` that says it's a bug in Sigil. The instance keeps working |
| A fatal error of the Go runtime, such as running out of memory | Stops the instance: every later call fails. Load a new one |
| Integers | Exact in the module. JavaScript numbers lose precision beyond ±2⁵³ |

## TypeScript package

`@spechtlabs/sigil` loads the module and speaks its ABI. It runs on Node 20 and later, Bun, Deno and browsers, and has no runtime dependencies.

| Import | Holds |
| --- | --- |
| `@spechtlabs/sigil` | `Sigil`, `Policy`, the kind builder, decisions, durations, errors and the record types |
| `@spechtlabs/sigil/worker` | `SigilWorker` and `WorkerPolicy`, the same API in a worker |
| `@spechtlabs/sigil/worker-entry` | The script the worker runs |
| `@spechtlabs/sigil/sigil.wasm` | The module |

### `Sigil` and `Policy`

Every method is synchronous and runs on the calling thread.

| Member | Signature | Does |
| --- | --- | --- |
| `Sigil.load` | `(source: WasmSource, options?: LoadOptions) => Promise<Sigil>` | Compiles, instantiates and initializes the module |
| `sigil.version` | `() => VersionInfo` | The [`version`](#version) op |
| `sigil.check` | `(files: SourceFile[], options?: CheckOptions) => Diagnostic[]` | The [`check`](#check) op |
| `sigil.compile` | `(files: SourceFile[], options?: CompileOptions) => Policy` | The [`compile`](#compile) op. Throws a `SigilError` with the diagnostics |
| `sigil.explain` | `(files: SourceFile[], options?: ExplainOptions) => Explanation[]` | The [`explain`](#explain) op on files |
| `sigil.format` | `(source: string, options?: FormatOptions) => string` | The [`format`](#format) op. Throws a `SigilError` with the syntax errors |
| `policy.name` | `string` | The compiled policy's name |
| `policy.diagnostics` | `Diagnostic[]` | Problems in documents the policy doesn't use |
| `policy.handle` | `number \| undefined` | The module's handle; `undefined` once released |
| `policy.eval` | `(input: I, options?: EvalOptions) => EvalResult` | The [`eval`](#eval) op. Throws a `SigilError` for an input that doesn't fit the kind |
| `policy.explain` | `() => Explanation` | The [`explain`](#explain) op on the handle |
| `policy.release` | `() => void` | The [`release`](#release) op. Calling it again does nothing |
| `policy[Symbol.dispose]` | `() => void` | `release`, for `using` |

| Option | Of | Is |
| --- | --- | --- |
| `output` | `LoadOptions` | `(stream: "stdout" \| "stderr", line: string) => void`. The module's output a line at a time. Default: standard error to `console.error` |
| `policies`, `require`, `lints`, `trustedFiles` | `CheckOptions` | The `check` op's `policies`, `require`, `lints` and `trusted_files` |
| `policy`, `require`, `trustedFiles`, `stubs` | `CompileOptions` | The `compile` op's fields of those names; `trustedFiles` is `trusted_files` |
| `functions` | `CompileOptions` | `Record<string, HostFunction>`. Implementations; their names become the op's `functions` |
| `timeoutMs` | `EvalOptions` | The `eval` op's `timeout_ms` |
| `policy`, `trustedFiles` | `ExplainOptions` | The `explain` op's `policy` and `trusted_files` |
| `path` | `FormatOptions` | The `format` op's `path` |

- `WasmSource` is a `URL` or URL string (`file:` URLs on Node, Bun and Deno), a `Response` or a promise of one, the module's bytes, or a compiled `WebAssembly.Module`.
- A host function runs synchronously, inside `eval`. A function that returns a promise, or calls back into its `Sigil`, fails the call with a runtime error.
- A `Policy` that's garbage collected without `release()` is released eventually.
- The record types, `Diagnostic`, `EvalResult`, `EvalEntry`, `EvalFailure`, `FailedAssert`, `Explanation`, `ExplainEntry` and `VersionInfo`, match the CLI's JSON field for field. `SourceFile`, `Requirement`, `CompileRequirement`, `Stub`, `StubCall`, `LintLevel`, `HostFunction` and `JsonValue` type the options.

### Errors

| Class | Thrown for | Fields |
| --- | --- | --- |
| `SigilError` | An op with `ok: false`, a kind that breaks a rule, a released policy, a module that stopped | `message`, `help`, `diagnostics` |
| `SigilTimeoutError` | A worker call past its deadline. Extends `SigilError` | as `SigilError` |

A failed evaluation doesn't throw; its result's `error` says why.

### Worker

`@spechtlabs/sigil/worker` runs the module in a Web Worker, or a `worker_threads` worker on Node.

| Member | Signature | Does |
| --- | --- | --- |
| `new SigilWorker` | `(options: SigilWorkerOptions)` | Starts the worker on the first call |
| `version`, `check`, `explain`, `format` | as on `Sigil`, plus `call?: CallOptions`, returning promises | The ops, in the worker |
| `compile` | `(files, options?: WorkerCompileOptions, call?: CallOptions) => Promise<WorkerPolicy>` | The `compile` op. `functions` names exports of the functions module |
| `terminate` | `(reason?: Error) => void` | Stops the worker and fails the calls waiting on it |
| `workerPolicy.eval` | `(input: I, options?: EvalOptions) => Promise<EvalResult>` | The `eval` op. Waits `timeoutMs` plus 500 ms before it terminates the worker |
| `workerPolicy.explain` | `(options?: CallOptions) => Promise<Explanation>` | The `explain` op on the handle |
| `workerPolicy.release` | `() => Promise<void>` | The `release` op |

| `SigilWorkerOptions` | Is |
| --- | --- |
| `wasm` | `URL \| string \| ArrayBuffer \| WebAssembly.Module`. Where the worker loads the module from |
| `functions` | `URL \| string`. An ES module whose named exports are host functions; the worker imports it |
| `timeoutMs` | Each call's deadline. Default 10,000 |
| `startTimeoutMs` | The deadline for starting the worker and loading the module. Default 30,000 |
| `worker` | `() => WorkerLike \| Promise<WorkerLike>`. Starts the worker. Default: `worker-entry.js` next to the helper, as a module worker |

- A call past its deadline terminates the worker and rejects with a `SigilTimeoutError`. The next call starts a new worker, and a `WorkerPolicy` compiles again in it by itself.
- `CallOptions.timeoutMs` overrides the helper's deadline for one call.

### The kind builder

| Export | Signature | Does |
| --- | --- | --- |
| `defineKind` | `(name: string, spec: KindSpec) => Kind` | A kind, as Go's `policy.NewKind`. Throws a `SigilError` listing every problem |
| `decision` | `(name: string, reasons: string[], payload?: PayloadSpec) => Decision` | A decision, as Go's `policy.NewDecision` |
| `enumType` | `(name: string, values: string[]) => EnumType` | An enum, as Go's `policy.WithEnum` |
| `struct` | `(name: string, fields: Fields) => StructType` | A struct type. Field order is declaration order |
| `fn` | `(params: AnyType[], result: AnyType, impl?: (...args) => In<R>) => Fn` | A host function's signature, and optionally its implementation |
| `t.string`, `t.bool`, `t.int`, `t.float`, `t.duration`, `t.timestamp` | `BasicType` | The scalar types |
| `t.list`, `t.map`, `t.optional` | `(elem) => ListType`, `(key, value) => MapType`, `(elem) => OptionalType` | `list<T>`, `map<K, V>`, `?T` |
| `type.default` | `(value: In<T>) => Defaulted<T>` | A payload field with a default |

| `KindSpec` field | Kind file equivalent | Rules |
| --- | --- | --- |
| `version` | `kind AlertRouting version n` | Required, from 1 |
| `accepts` | `, accepts: n` | From 1 to `version`. Every version when left out |
| `inputs` | `input alert: Alert` | Declaration order |
| `enums` | `enum ...` | Enums nothing reaches. They print after the ones something uses |
| `functions` | `fn split(string, string) -> list<string>` | `Record<string, Fn>`. A result can't be optional |
| `decisions` | `collect one`, `precedence page > drop > notify` | Highest precedence first. Needs `default` |
| `collect` | `collect all` | Instead of `decisions` |
| `precedence` | `precedence ...` in a `collect all` kind | Lists every decision of `collect` |
| `reasonPrecedence` | `precedence page: critical_alert > sustained` | Each entry a decision, ranking its reasons in declared order, or a list of one decision's reasons |
| `exclusive` | `exclusive a, b` | Sets of at least two decisions or reasons |
| `default` | `default notify(reason: unrouted)` | Required with `decisions`. Every payload field of its decision needs a default |
| `conflict` | `conflict ...` | Only with `decisions` |

| `Kind` member | Signature | Does |
| --- | --- | --- |
| `name`, `version`, `spec` | | As defined |
| `schema` | `() => string` | The kind file, byte for byte what Go's `Kind.Schema` writes for the same kind |
| `file` | `(path?: string) => SourceFile` | The kind file as a virtual file, `alert_routing.sigil` for `AlertRouting` by default |
| `functions` | `() => Record<string, HostFunction>` | The implementations given to `fn` |
| `check` | `(sigil: Sigil) => Diagnostic[]` | Checks the kind file with the engine |
| `compile` | `(sigil: Sigil, files, options?: KindCompileOptions) => Policy<InputOf<this>>` | Compiles with the kind file added when neither `files` nor `trustedFiles` holds it, and the kind's implementations passed along. Throws when they hold a kind file that isn't `schema()` |
| `compile` | `(sigil: SigilWorker, files, options?: KindWorkerCompileOptions) => Promise<WorkerPolicy<InputOf<this>>>` | The same in a worker. Host functions come from the worker's functions module |

`KindCompileOptions` adds `kindFile`, the path of the kind file `compile` adds.

| Handle member | Signature | Does |
| --- | --- | --- |
| `decision.reason` | `(name: R) => Outcome` | A reason handle. A reason the decision doesn't declare is a type error, and throws with a did-you-mean |
| `decision.match` | `(res: EvalResult) => PayloadOf<P> \| undefined` | The typed payload when the outcome is exactly one entry of the decision |
| `decision.matchAll` | `(res: EvalResult) => Matched<R, PayloadOf<P>>[]` | Every entry of the decision, in outcome order, with `payload`, `reason`, `policy` and `position` |
| `outcome.is` | `(res: EvalResult) => boolean` | Whether the outcome is exactly one entry, of this decision with this reason |

`match` and `is` throw on the result of a `collect all` kind without precedence.

| Type helper | Is |
| --- | --- |
| `InputOf<typeof K>` | The input a kind's policies take |
| `PayloadOf<P>` | A decision's payload as it comes back |
| `In<T>`, `Out<T>` | What a host passes for a type, and what comes back. They differ for timestamps (`Date` or string in, string out) and optionals (a key may be left out going in) |
| `ArgsOf<P>` | A host function's arguments |
| `FieldsIn<F>`, `FieldsOut<F>` | A struct's fields going in and coming back |

The type classes behind the builder, `SigilType`, `BasicType`, `ListType`, `MapType`, `OptionalType`, `EnumType`, `StructType`, `Defaulted`, `Decision`, `Outcome`, `Fn` and `Kind`, are exported for `instanceof` and type annotations. Build values with the functions above.

### Durations

| Export | Signature | Does |
| --- | --- | --- |
| `Duration` | `string` | A duration in Sigil's syntax: whole numbers with `d`, `h`, `m`, `s` and `ms`, largest first, each once |
| `duration` | `(text: string) => Duration` | Validates a duration and returns its canonical form: `duration("90m")` is `"1h30m"` |
| `ms` | `(milliseconds: number) => Duration` | `ms(720_000)` is `"12m"` |
| `toMs` | `(d: Duration) => number` | `toMs("12m")` is `720000` |

```text
SigilError: invalid duration "90 minutes"
  help: units are d, h, m, s and ms, largest first, each at most once: "1h30m"
```

### Constants

| Export | Is |
| --- | --- |
| `ABI_VERSION` | `1`, the ABI version the package speaks. `Sigil.load` throws for a module that speaks another |
| `Timestamp` | `string`, an RFC 3339 timestamp as it comes back |
