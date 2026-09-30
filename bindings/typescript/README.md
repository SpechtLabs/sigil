# @spechtlabs/sigil

Sigil for JavaScript and TypeScript: the Go engine compiled to WebAssembly, behind a typed API. It targets browsers, Node 20 and later, Bun and Deno, and uses only web-standard APIs plus `node:` modules loaded on demand.

The test suite runs under Bun, and it's been run by hand on Node 20 and 24, including the worker helper on Node's `worker_threads`. No browser runs in the test suite yet, and Deno isn't tested.

The module is the stock `sigil` CLI's engine without a filesystem or a terminal. For the same files and input, its answers are the CLI's `-o json` records. Regexes, Unicode comparison, duration arithmetic and integer overflow all behave exactly as they do in Go, because it is the same Go code.

> [!NOTE]
> The package isn't published to npm yet (`"private": true`). Build it from the repository as described below.

## Build

From the repository root:

```sh
mise run wasm-build   # dist/wasm/sigil.wasm
mise run ts-build     # bindings/typescript/dist/, with sigil.wasm copied in
mise run ts-test      # bun test against the real module
```

To use it from another package in the repository, depend on it by path (`"@spechtlabs/sigil": "file:../bindings/typescript"`).

## Example

```ts
import { Sigil } from "@spechtlabs/sigil";

const sigil = await Sigil.load(import.meta.resolve("@spechtlabs/sigil/sigil.wasm"));
const files = [
  { path: "deploy_approval.sigil", source: kindSource },
  { path: "teams/payments/production.sigil", source: policySource },
];

for (const d of sigil.check(files)) console.log(`${d.file}:${d.line}: ${d.message}`);

using policy = sigil.compile(files, {
  policy: "payments.production",
  functions: { split: (s: string, sep: string) => s.split(sep) },
});
const result = policy.eval({ release: { soak: "6h", hotfix: false }, service, actor, environment: "production" });
console.log(result.decision, result.reason, result.payload);
```

## API

Everything on `Sigil` and `Policy` is synchronous. The work runs inside WebAssembly on the calling thread.

| Call | Returns | Like |
| --- | --- | --- |
| `Sigil.load(source, { output? })` | `Promise<Sigil>` | |
| `sigil.version()` | `VersionInfo` | `sigil version -o json` |
| `sigil.check(files, { policies?, require?, lints?, trustedFiles? })` | `Diagnostic[]`, errors and warnings | `sigil check -o json` |
| `sigil.compile(files, { policy?, require?, trustedFiles?, stubs?, functions? })` | `Policy`, or throws `SigilError` with diagnostics | |
| `policy.eval(input, { timeoutMs? })` | `EvalResult` | `sigil eval -o json` |
| `policy.explain()` | `Explanation` | `sigil explain -o json --policy` |
| `policy.release()`, `using policy = ...` | | |
| `sigil.explain(files, { policy? })` | `Explanation[]` | `sigil explain -o json` |
| `sigil.format(source, { path? })` | the formatted source, or throws `SigilError` with diagnostics | `sigil fmt` |

`source` is a `URL` or URL string (`file:` URLs work on Node, Bun and Deno), a `Response` or a promise of one, the module's bytes, or a compiled `WebAssembly.Module`. The package exports the module as `@spechtlabs/sigil/sigil.wasm`: `import.meta.resolve` finds it on Node, Bun and Deno, and a bundler gives its URL (`import url from "@spechtlabs/sigil/sigil.wasm?url"` in Vite).

Files are virtual: `{ path, source }`. Paths appear in diagnostics and positions exactly as the CLI prints them for the same relative paths. The kind file is one of the files, as it is among the CLI's paths.

`Diagnostic`, `EvalResult`, `Explanation` and the other records are exported types that match the CLI's JSON field for field.

### Errors

- A `SigilError` has `message`, `help` (how to fix it, when known) and `diagnostics`. It's thrown for files that don't compile, a source that doesn't parse, an input that doesn't fit the kind, a `require` entry that can't hold (the CLI's configuration error), or a released policy.
- A failed evaluation doesn't throw. A runtime error, a conflict, a failing assert or a `timeoutMs` deadline returns a result whose `error` says why (`kind` is `runtime`, `conflict`, `assertion` or `canceled`), with the kind's fallback as the outcome, like the CLI's JSON.
- If the Go runtime inside the module panics, every later call throws the same `SigilError`, quoting the panic. Load a new instance.

### Required policies

A host that compiles bundles it doesn't control, such as a team's policies, keeps its own guardrails in with `require` and `trustedFiles`. This is Go's `policy.Require(name, policy.From(trusted))`:

```ts
sigil.compile(teamFiles, {
  policy: "checkout.alerts",
  require: [{ policy: "platform.paging" }],
  trustedFiles: platformFiles, // the host's own documents, never read from the team's directory
});
```

The compile fails with diagnostics when the team's policy doesn't invoke `platform.paging` at its top level. It also fails when the policy invokes it under `when`, passes a param out of its declared bounds, or defines a `platform.paging` of its own. With `trustedFiles`, a required policy must be defined there. Trust comes from the list, not the path, so a path can't be in both lists. Without `trustedFiles`, any policy among the files satisfies a requirement.

### Host functions

A kind file declares its host functions with `fn`. You can answer them in two ways:

- `functions` holds JavaScript implementations. They run synchronously, in the middle of the evaluation. Their arguments and results use the JSON form of inputs: durations as strings like `"2h30m"`, timestamps as RFC 3339 strings, enum values as their names. A function that throws fails the evaluation with a runtime error, `host function split failed: <message>`. A function can't be async, and it can't call back into the same `Sigil` instance.
- `stubs` holds stand-ins in the format of a test file's `stubs:`: `{ split: { returns: ["eu", "us"] } }`, `{ split: { error: "..." } }` or `{ split: { calls: [{ args: ["eu,us", ","], returns: ["eu", "us"] }] } }`. A stub replaces an implementation of the same name.

A function with neither fails the way it does in the stock `sigil` binary.

### Numbers

JSON numbers become JavaScript numbers. An `int` beyond ±2^53 loses precision on the way in and on the way out.

## Kinds in TypeScript

A host can define its kind in TypeScript, the twin of Go's `policy.NewKind`, instead of keeping a kind file by hand. `schema()` writes the kind file, byte for byte what Go's `Kind.Schema` writes for the same kind. The tests hold it to that: they run a Go program that builds the same kinds with `policy.NewKind` and compare the output.

```ts
import { decision, defineKind, enumType, struct, t, type InputOf } from "@spechtlabs/sigil";

const Severity = enumType("Severity", ["critical", "warning", "info"]);
const Alert = struct("Alert", { name: t.string, severity: Severity, labels: t.map(t.string, t.string), firing_for: t.duration });
const Team = struct("Team", { name: t.string, oncall: t.string, channel: t.string });

export const Page = decision("page", ["critical_alert", "sustained"], { target: t.string });
export const Drop = decision("drop", ["muted", "not_production"]);
export const Notify = decision("notify", ["routine", "unrouted"], { channel: t.string.default("#alerts") });

export const AlertRouting = defineKind("AlertRouting", {
  version: 1,
  inputs: { alert: Alert, team: Team },
  decisions: [Page, Drop, Notify], // collect one, highest precedence first
  reasonPrecedence: [Page, Drop, Notify], // each decision's reasons in declared order
  default: Notify.reason("unrouted"),
});

writeFileSync("policies/alert_routing.sigil", AlertRouting.schema()); // check it in; test that it's current

using policy = AlertRouting.compile(sigil, files, { policy: "checkout.alerts" }); // Policy<InputOf<typeof AlertRouting>>
const res = policy.eval(input);
Page.match(res); // { target: string } | undefined
Notify.reason("unrouted").is(res); // boolean
```

- **Types.** `t.string`, `t.bool`, `t.int`, `t.float`, `t.duration`, `t.timestamp`, `t.list(T)`, `t.map(K, V)`, `t.optional(T)`, plus `enumType(name, values)` and `struct(name, fields)`. Field and value order is declaration order in the kind file. Enums and struct types print in the order the inputs, then the functions, then the payloads first reach them, as in Go.
- **`defineKind(name, spec)`.** It takes `version`, `accepts`, `inputs` and `functions`. For decisions it takes either `decisions` (`collect one`, in precedence order) or `collect` (`collect all`, with optional `precedence`). Then `reasonPrecedence` (a decision, which ranks its reasons in declared order, or a list of one decision's reasons), `exclusive` (sets of decisions or reasons), `default` and `conflict`. `enums` declares enums nothing reaches, like Go's `WithEnum`. An invalid kind throws a `SigilError` listing every problem, like `NewKind`'s panic. `kind.check(sigil)` runs the full model validation in the engine.
- **Payload defaults.** Write them with `.default(value)`: `t.duration.default("1h")`, `t.list(t.string).default([])`. A timestamp can't have a default, and neither can `none`, as in Go.
- **Host functions.** `functions: { split: fn([t.string, t.string], t.list(t.string), (s, sep) => s.split(sep)) }` declares `fn split(string, string) -> list<string>`, with the implementation typed from the declaration. The implementation is optional. `compile` passes the implementations along; its `functions` and `stubs` override them.
- **`kind.compile(sigil, files, options)`.** If the files lack the kind file, `compile` adds it (`kind.file()`, named like `alert_routing.sigil`). If they hold one that differs from `schema()`, it throws, which catches a stale export. It returns a `Policy<InputOf<typeof Kind>>`. With a `SigilWorker` as the first argument, it returns a promise of a typed `WorkerPolicy`.
- **Reading results.** `Decision.match(res)` returns the typed payload when the outcome is exactly one entry of that decision. `Decision.matchAll(res)` returns every entry of it, which is how a collecting kind is read. `decision.reason("x").is(res)` checks decision and reason. A misspelled reason is a type error, and it throws with a did-you-mean at run time. Like Go's `Match`, `match` and `is` throw on an unranked collecting kind's result.
- **Durations.** A `Duration` is a string in Sigil's syntax, not Go's: integer components with the units `d`, `h`, `m`, `s` and `ms`, largest first, each once, like `"1h30m"` or `"2d"`. Inputs, payloads and host function arguments all use it. `duration("90m")` validates a duration and returns its canonical form, `"1h30m"`. `ms(720_000)` is `"12m"`, and `toMs("12m")` is `720000`.
- **Timestamps.** Timestamps come back as RFC 3339 strings. An input can also take a `Date`.

## The worker helper

`@spechtlabs/sigil/worker` runs the module in a Web Worker (a `worker_threads` worker on Node). The API is the same, except that every method returns a promise. A call that runs past its deadline terminates the worker and rejects with a `SigilTimeoutError`. The next call starts a fresh worker, and policies compile again in it by themselves. Use it for UIs that must never freeze.

```ts
import { SigilWorker } from "@spechtlabs/sigil/worker";

const sigil = new SigilWorker({
  wasm: import.meta.resolve("@spechtlabs/sigil/sigil.wasm"),
  functions: new URL("./host-functions.js", import.meta.url), // optional
  timeoutMs: 2_000,
});
const policy = await sigil.compile(files, { policy: "payments.production", functions: ["split"] });
const result = await policy.eval(input, { timeoutMs: 500 });
```

- Functions can't be sent to a worker, so host functions live in their own ES module. The worker imports it, and `compile` names the exports it uses.
- `eval`'s `timeoutMs` goes to the module first, which stops the evaluation with a `canceled` error. The worker is terminated only if that hasn't happened within half a second more. Every other call is bounded by the helper's `timeoutMs`, which defaults to 10 seconds.
- By default the helper starts `worker-entry.js` from next to itself with `new Worker(new URL("./worker-entry.js", import.meta.url), { type: "module" })`, which Vite and other bundlers recognize. Pass `worker: () => new Worker(...)` to start it another way.

## Inside

- `sigil.wasm` is `cmd/sigil-wasm` built for `GOOS=wasip1` as a reactor. JSON requests go in through a small, versioned ABI (`sigil_alloc`, `sigil_call`, `sigil_free`, plus one import, `sigil.host_call`). The Go package documents the ABI.
- The package has no runtime dependencies. `src/wasi.ts` implements the handful of WASI preview1 calls a Go reactor makes. Standard error goes to `output` (by default `console.error`). There's no filesystem and no environment.
- Compiled policies stay inside the module as handles, so an evaluation sends only the input across. A `Policy` that is garbage collected without `release()` is released eventually. Release it explicitly anyway.
- Size: the JavaScript is about 9 kB min+gzip for the main entry (3.7 kB of it the engine API, the rest the kind builder) and 1.7 kB for the worker helper. `sigil.wasm` is about 10.9 MB, or 2.9 MB gzipped.
