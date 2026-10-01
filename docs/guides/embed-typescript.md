---
title: Embed Sigil in TypeScript
icon: mdi:language-typescript
createTime: 2026/09/30 12:00:00
permalink: /guides/embed-typescript/
---

By the end of this guide your TypeScript program defines the `AlertRouting` kind in TypeScript, exports its kind file, compiles a team's policy with the platform's page rules required, and routes alerts through typed payloads. The package, `@spechtlabs/sigil`, runs Sigil's Go engine compiled to WebAssembly, so a policy decides exactly what it decides in a Go host. [WebAssembly module](/reference/wasm/) lists the module's ABI and every export of the package, and [One engine for every host](/understanding/one-engine/) explains why it's the Go engine and not a port.

The policies are the ones [Getting Started](/getting-started/share-rules/) builds: the platform's `platform.alerts` module, `platform.paging` and `platform.routing` from [step 7](/getting-started/require-guardrails/), and checkout's `checkout.alerts`, which invokes both. The program looks like this by the end:

::: file-tree

- alerting
  - package.json
  - src
    - kind.ts # the kind, its decisions and types
    - kind.test.ts # fails when the kind file is stale
    - export-kind.ts # writes policies/alert_routing.sigil
    - files.ts # reads .sigil files into virtual files
    - route.ts # compiles checkout.alerts and routes sample alerts
  - policies
    - alert_routing.sigil # generated, committed
    - platform
      - alerts.sigil
      - paging.sigil
      - routing.sigil
    - checkout
      - alerts.sigil

:::

## Install the package

```sh
npm install @spechtlabs/sigil
```

The package's version is the Sigil release it was built from, so `@spechtlabs/sigil@0.7.0` decides exactly as `sigil` v0.7.0 does. It runs on Node 20 and later, Bun and browsers, and has no runtime dependencies. To build it from a checkout of the repository instead, see the [package README](https://github.com/SpechtLabs/sigil/tree/main/bindings/typescript#build-from-the-repository). Everything below ran under Bun 1.4.2, and `src/route.ts` runs unchanged under Node 24.

## Define the kind

The kind is the contract between your program and the policies. `defineKind` is the TypeScript twin of Go's `policy.NewKind`, and this one declares the same kind as the Go program in [Define the input](/getting-started/define-the-input/). Put it in `src/kind.ts`:

```ts
import { decision, defineKind, enumType, struct, t } from "@spechtlabs/sigil";

export const Severity = enumType("Severity", ["critical", "warning", "info"]);

export const Alert = struct("Alert", {
  name: t.string,
  severity: Severity,
  labels: t.map(t.string, t.string),
  firing_for: t.duration,
});

export const Team = struct("Team", {
  name: t.string,
  oncall: t.string,
  channel: t.string,
});

export const Page = decision("page", ["critical_alert", "sustained"], { target: t.string });
export const Drop = decision("drop", ["muted", "not_production"]);
export const Notify = decision("notify", ["routine", "unrouted"], { channel: t.string.default("#alerts") });

export const AlertRouting = defineKind("AlertRouting", {
  version: 1,
  inputs: { alert: Alert, team: Team },
  decisions: [Page, Drop, Notify], // order = precedence
  reasonPrecedence: [Page, Drop, Notify], // each decision's reasons, in declared order
  default: Notify.reason("unrouted"),
});
```

- `enumType`, `struct` and the `t` types build the kind's types, in the order they're written. `t.duration` values are strings in Sigil's syntax, like `"1h30m"`.
- `decision` declares a decision with every reason a rule may give for it, and its payload. `.default(...)` gives a payload field its default, which the kind's `default` needs.
- `decisions` lists the decisions highest precedence first. `reasonPrecedence` ranks each decision's reasons in the order `decision` declared them.
- `defineKind` throws a `SigilError` listing every problem when the kind breaks a [validity rule](/reference/kind-files/#validity-rules), so a broken contract fails when the module loads.

The handles are typed. A reason the decision doesn't declare is a type error:

```ts
export const CriticalAlert = Page.reason("critcal_alert");
```

```text
src/typo.ts(3,42): error TS2345: Argument of type '"critcal_alert"' is not assignable to parameter of type '"critical_alert" | "sustained"'.
```

Plain JavaScript gets the same check when the line runs: `SigilError: decision page has no reason "critcal_alert"`. Every option of `defineKind` is in [The kind builder](/reference/wasm/#the-kind-builder).

## Export the kind file

The `sigil` CLI and other hosts read the kind as a kind file. `schema()` writes it byte for byte as Go's `Kind.Schema` writes the same kind. Add `src/export-kind.ts`:

```ts
import { writeFileSync } from "node:fs";

import { AlertRouting } from "./kind.ts";

writeFileSync("policies/alert_routing.sigil", AlertRouting.schema());
console.log("wrote policies/alert_routing.sigil");
```

```text
$ bun src/export-kind.ts
wrote policies/alert_routing.sigil
```

Commit the file next to the policies, and fail the tests when it's stale, in `src/kind.test.ts`:

```ts
import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { AlertRouting } from "./kind.ts";

test("policies/alert_routing.sigil is current", () => {
  expect(readFileSync("policies/alert_routing.sigil", "utf8")).toBe(AlertRouting.schema());
});
```

```text
$ bun test
bun test v1.4.2 (744846f84)

 1 pass
 0 fail
 1 expect() calls
Ran 1 test across 1 file. [8.00ms]
```

A stale kind file can't take effect in the program either. When the files you compile hold a kind file for `AlertRouting` that isn't `schema()`, the compile throws `platform/alert_routing.sigil declares kind AlertRouting, but not as this program defines it`.

## Load the module

Load the module once, at startup:

```ts
const sigil = await Sigil.load(import.meta.resolve("@spechtlabs/sigil/sigil.wasm"));
```

Loading compiles about 11 MB of WebAssembly and starts the Go runtime in it, about 33 ms on an Apple M5 Pro. After that, every call is synchronous: the work runs inside WebAssembly on the calling thread. An instance runs one call at a time, so a program that evaluates in parallel loads one instance per worker thread.

`Sigil.load` also takes the module's bytes, a `Response` or a compiled `WebAssembly.Module`. In a browser, a bundler gives you the URL; see [Run it in a browser](#run-it-in-a-browser).

## Compile a team's policy

The engine has no filesystem. It reads virtual files, `{ path, source }`, and names them in positions by their paths. Read a directory of `.sigil` files into them with `src/files.ts`:

```ts
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

import type { SourceFile } from "@spechtlabs/sigil";

/** Every .sigil file below dir, with its path relative to root, as the CLI names it. */
export function sigilFiles(root: string, dir: string): SourceFile[] {
  return readdirSync(dir, { recursive: true, encoding: "utf8" })
    .filter((name) => name.endsWith(".sigil"))
    .sort()
    .map((name) => ({ path: relative(root, join(dir, name)), source: readFileSync(join(dir, name), "utf8") }));
}
```

Compile the team's files with the kind, and require the platform's page rules the way a Go host does with `policy.Require("platform.paging", policy.From(platform))`:

```ts
function compileTeam(sigil: Sigil, team: string) {
  try {
    return AlertRouting.compile(sigil, sigilFiles("policies", `policies/${team}`), {
      policy: `${team}.alerts`,
      require: [{ policy: "platform.paging" }],
      trustedFiles: sigilFiles("policies", "policies/platform"),
    });
  } catch (err) {
    if (!(err instanceof SigilError)) throw err;
    console.error(err.message);
    for (const d of err.diagnostics) {
      console.error(`${d.file}:${d.line}:${d.column}: ${d.severity}: ${d.message}\n  help: ${d.help}`);
    }
    process.exit(1);
  }
}
```

- `AlertRouting.compile` adds the kind file when the files don't hold it, and passes the kind's host functions along. It returns a `Policy` whose `eval` takes the kind's input type.
- `trustedFiles` are your own documents, never read from the team's directory. With them, `platform.paging` must come from them: a team file that defines its own fails the compile, and so does a path that's in both lists. Trust comes from the list a file is in, not from its path.
- `require` makes the compile fail unless `checkout.alerts` invokes `platform.paging` at its top level, with arguments inside the param's bounds.

A compile that fails throws a `SigilError` whose `diagnostics` are the records `sigil check -o json` prints. A checkout policy that drops the `paging(...)` call:

```text
$ bun src/route.ts
the policy doesn't compile, so nothing was compiled
checkout/alerts.sigil:1:1: error: checkout.alerts doesn't invoke platform.paging
  help: the host requires platform.paging for every AlertRouting policy; import it with `use platform.paging` and invoke it at the top level
```

One that invokes it under `when alert.labels["env"] == "production"`:

```text
$ bun src/route.ts
the policy doesn't compile, so nothing was compiled
checkout/alerts.sigil:7:3: error: platform.paging must be invoked unconditionally
  help: the host requires platform.paging for every AlertRouting policy; move the call to the top level
```

And a `checkout/paging.sigil` that defines a `platform.paging` of its own:

```text
$ bun src/route.ts
checkout.alerts doesn't check, so nothing was compiled
checkout/paging.sigil:1:8: error: policy platform.paging is defined twice
  help: the name belongs to the trusted source, defined at platform/paging.sigil:1:1; documents resolve by name, so each name has one definition
```

The rules are in [Required policies](/reference/wasm/#required-policies), and why the host needs its own copy in [Why required policies need a trusted source](/understanding/bundles/#why-required-policies-need-a-trusted-source).

## Evaluate and act on the result

Compile once and evaluate for every alert. Here's `src/route.ts`, with `compileTeam` from above left out:

```ts
import { type EvalResult, type InputOf, Sigil, SigilError } from "@spechtlabs/sigil";

import { sigilFiles } from "./files.ts";
import { AlertRouting, Notify, Page } from "./kind.ts";

type Input = InputOf<typeof AlertRouting>;

const Unrouted = Notify.reason("unrouted");

const checkout = { name: "checkout", oncall: "checkout-primary", channel: "#checkout-alerts" };

const alerts: Input["alert"][] = [
  { name: "CheckoutErrorRate", severity: "critical", labels: { env: "production" }, firing_for: "2m" },
  { name: "CheckoutLatencyHigh", severity: "warning", labels: { env: "production" }, firing_for: "12m" },
  { name: "CheckoutLatencyHigh", severity: "warning", labels: { env: "production" }, firing_for: "45m" },
  { name: "CheckoutErrorRate", severity: "critical", labels: { env: "staging" }, firing_for: "2m" },
  { name: "CheckoutQueueStuck", severity: "critical", labels: {}, firing_for: "3m" },
  { name: "CheckoutCanaryLatency", severity: "warning", labels: { env: "production" }, firing_for: "5m" },
];

const sigil = await Sigil.load(import.meta.resolve("@spechtlabs/sigil/sigil.wasm"));

using policy = compileTeam(sigil, "checkout");

for (const alert of alerts) {
  const res = policy.eval({ alert, team: checkout });
  const env = alert.labels["env"] ?? "-";
  console.log(`${alert.name.padEnd(22)} ${alert.severity.padEnd(8)} ${env.padEnd(10)} ${alert.firing_for.padStart(4)}  → ${route(res)}`);
}

function route(res: EvalResult): string {
  const page = Page.match(res);
  if (page !== undefined) return `page ${page.target} (${res.reason})`;

  const note = Notify.match(res);
  if (note !== undefined) return `post to ${note.channel} (${Unrouted.is(res) ? "no rule covers it" : res.reason})`;

  return `drop (${res.reason})`;
}
```

```text
$ bun src/route.ts
CheckoutErrorRate      critical production   2m  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production  12m  → page checkout-primary (sustained)
CheckoutLatencyHigh    warning  production  45m  → page checkout-primary (sustained)
CheckoutErrorRate      critical staging      2m  → drop (not_production)
CheckoutQueueStuck     critical -            3m  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production   5m  → drop (muted)
```

That's the Go program's output from [step 7](/getting-started/require-guardrails/#make-it-stick), decision for decision.

- `InputOf<typeof AlertRouting>` is the input the kind declares, so `severity: "critcal"` is a type error. An input that gets past the types anyway, from JSON say, makes `eval` throw a `SigilError`: `the input: alert.severity: "critcal" is not a value of Severity`.
- `Page.match(res)` returns the payload, typed `{ target: string }`, when the outcome is exactly one `page`, and `undefined` otherwise.
- `Unrouted.is(res)` checks the decision and the reason. Use a handle instead of comparing `res.reason` with a string, which type-checks with a typo in it.
- `using` releases the compiled policy inside the module when `policy` goes out of scope. Call `policy.release()` yourself where `using` doesn't fit.

A collecting kind can grant a decision more than once, so read it with `matchAll`, which returns every entry of that decision with its typed payload. `match` and `is` throw on a collecting kind without precedence.

## Call host functions

A host function answers what a policy can't compute itself. Alert labels are strings, so shared infrastructure that names every team an alert affects writes `affects="payments,checkout"`, and a policy needs to split it. Declare the function in the kind with `fn`, and bump the version, since every change to the kind does:

```ts
import { decision, defineKind, enumType, fn, struct, t } from "@spechtlabs/sigil";

import { split } from "./host-functions.ts";

// Severity, Alert, Team and the decisions as before

export const AlertRouting = defineKind("AlertRouting", {
  version: 2,
  inputs: { alert: Alert, team: Team },
  functions: {
    split: fn([t.string, t.string], t.list(t.string), split),
  },
  decisions: [Page, Drop, Notify], // order = precedence
  reasonPrecedence: [Page, Drop, Notify], // each decision's reasons, in declared order
  default: Notify.reason("unrouted"),
});
```

Keep the implementation in a module of its own, `src/host-functions.ts`, so [the worker](#run-it-in-a-browser) can load it too:

```ts
// The kind's host functions. The kind uses them directly, and the worker
// helper imports this module inside its worker, since functions can't be
// sent to one.

export function split(s: string, sep: string): string[] {
  return s === "" ? [] : s.split(sep);
}
```

`fn` declares `fn split(string, string) -> list<string>` in the kind file and types the implementation from it, so an implementation that returns a `string` doesn't compile. Export the kind again, and checkout can use it, pinned to the version that has it:

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

Add two info alerts to the list in `src/route.ts`:

```ts
  { name: "PaymentsReplicaLag", severity: "info", labels: { env: "production", affects: "payments,checkout" }, firing_for: "8m" },
  { name: "EdgeCertExpiring", severity: "info", labels: { env: "production", affects: "search" }, firing_for: "1h" },
```

```text
$ bun src/route.ts
CheckoutErrorRate      critical production   2m  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production  12m  → page checkout-primary (sustained)
CheckoutLatencyHigh    warning  production  45m  → page checkout-primary (sustained)
CheckoutErrorRate      critical staging      2m  → drop (not_production)
CheckoutQueueStuck     critical -            3m  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production   5m  → drop (muted)
PaymentsReplicaLag     info     production   8m  → post to #checkout-alerts (routine)
EdgeCertExpiring       info     production   1h  → post to #alerts (no rule covers it)
```

- A host function runs synchronously, in the middle of the evaluation. It can't be `async`, and it can't call back into the `Sigil` instance that's running it.
- Arguments and results take the JSON form of inputs: durations as strings like `"2h30m"`, timestamps as RFC 3339 strings, enum values as their names.
- A function that throws fails the evaluation with a runtime error that quotes it: `checkout/alerts.sigil:10:46: host function split failed: label too long`.
- Like a Go host's, it must be pure and terminate. The module can't stop an evaluation while a host function runs; see [Handle a failed evaluation](#handle-a-failed-evaluation).

To stand in for a function without running it, pass `stubs` to `compile`, in the format of a test file's [`stubs:`](/reference/test-files/#stubs).

## Handle a failed evaluation

A failed evaluation doesn't throw. A runtime error, a conflict, a failing assert or a deadline returns a result whose `error` says why, with the kind's default as the outcome, which for `AlertRouting` is a post to `#alerts`. So check `error` before you match: a page that failed to evaluate comes back as a post.

The fix [step 7](/getting-started/require-guardrails/#what-a-guardrail-can-t-stop) suggests is to evaluate the platform's page rules on their own when a team's policy fails. Compile `platform.paging` from your own files next to the team's policy, give every evaluation a deadline, and call `decide` where the loop called `policy.eval`:

```ts
using policy = compileTeam(sigil, "checkout");
using paging = AlertRouting.compile(sigil, sigilFiles("policies", "policies/platform"), { policy: "platform.paging" });

for (const alert of alerts) {
  const res = decide({ alert, team: checkout });
  const env = alert.labels["env"] ?? "-";
  console.log(`${alert.name.padEnd(22)} ${alert.severity.padEnd(8)} ${env.padEnd(10)} ${alert.firing_for.padStart(4)}  → ${route(res)}`);
}

function decide(input: Input): EvalResult {
  const res = policy.eval(input, { timeoutMs: 100 });
  if (res.error === undefined) return res;

  console.error(`${policy.name}: ${res.error.kind}: ${res.error.message}`);
  // The team's policy failed, and res holds the kind's default. Page anyway
  // if the platform's page rules say so.
  const fallback = paging.eval(input, { timeoutMs: 100 });
  return fallback.error === undefined ? fallback : res;
}
```

`timeoutMs` bounds one evaluation. Past it, the module stops the evaluation at its next check, and `error.kind` is `canceled`. Add an alert whose `affects` label names two million teams:

```ts
  { name: "FleetWideNoise", severity: "info", labels: { env: "production", affects: "team,".repeat(2_000_000) }, firing_for: "1m" },
```

Then give checkout a second page for the same reason as the platform's:

```sigil
when alert.labels["env"] == "production" and alert.severity == critical {
  page(reason: critical_alert, target: "nobody")
}
```

Checkout's policy now conflicts on the production error rate alert, and the noisy alert runs past its deadline. The platform's page still goes out:

```text
$ bun src/route.ts
checkout.alerts: conflict: collect one: 2 candidates at the top rank
CheckoutErrorRate      critical production   2m  → page checkout-primary (critical_alert)
CheckoutLatencyHigh    warning  production  12m  → page checkout-primary (sustained)
CheckoutLatencyHigh    warning  production  45m  → page checkout-primary (sustained)
CheckoutErrorRate      critical staging      2m  → drop (not_production)
CheckoutQueueStuck     critical -            3m  → page checkout-primary (critical_alert)
CheckoutCanaryLatency  warning  production   5m  → drop (muted)
PaymentsReplicaLag     info     production   8m  → post to #checkout-alerts (routine)
EdgeCertExpiring       info     production   1h  → post to #alerts (no rule covers it)
checkout.alerts: canceled: the evaluation was stopped: context deadline exceeded
FleetWideNoise         info     production   1m  → post to #alerts (no rule covers it)
```

Remove the second page again before moving on.

The module checks the deadline before every rule, after every host function call and every few hundred elements of a list it goes through. It can't interrupt a host function, or anything else that runs between two checks, so `timeoutMs` alone doesn't bound time when a host function might hang. The [worker helper](#run-it-in-a-browser) does.

| `error.kind` | Why | What to do |
| --- | --- | --- |
| `runtime` | An expression failed, such as a host function that threw | Fix the policy or the input it read |
| `conflict` | Two candidates the kind doesn't rank reached the top | Fix the policy; the fallback decides meanwhile |
| `assertion` | An `assert` failed; `phase` says whether on the input or the outcome | Reject the input, or fix the policy's assumption |
| `canceled` | The evaluation ran past `timeoutMs` | Not a policy bug; retry, or fall back |

[Handle failed evaluations](/guides/handle-errors/) covers the same failures in a Go host, and the fields of `error` are in the [eval record](/reference/cli/#records).

## Run it in a browser

`Sigil` blocks the thread it runs on, which a UI can't afford. `SigilWorker`, from `@spechtlabs/sigil/worker`, runs the module in a Web Worker (a `worker_threads` worker on Node) with the same API, except that every method returns a promise. With Vite, a page compiles and evaluates checkout's policy like this:

```ts
import wasm from "@spechtlabs/sigil/sigil.wasm?url";
import { SigilWorker } from "@spechtlabs/sigil/worker";

import { AlertRouting, Page } from "./kind.ts";

// Vite reads the policies at build time; a real UI might take them from an editor instead.
const sources = import.meta.glob<string>("../policies/**/*.sigil", { query: "?raw", import: "default", eager: true });
const files = (dir: string) =>
  Object.entries(sources)
    .filter(([path]) => path.startsWith(`../policies/${dir}/`))
    .map(([path, source]) => ({ path: path.slice("../policies/".length), source }));

const sigil = new SigilWorker({ wasm, functions: new URL("/host-functions.js", location.href), timeoutMs: 2_000 });

const policy = await AlertRouting.compile(sigil, files("checkout"), {
  policy: "checkout.alerts",
  require: [{ policy: "platform.paging" }],
  trustedFiles: files("platform"),
  functions: ["split"],
});

const res = await policy.eval(
  {
    alert: { name: "CheckoutErrorRate", severity: "critical", labels: { env: "production" }, firing_for: "2m" },
    team: { name: "checkout", oncall: "checkout-primary", channel: "#checkout-alerts" },
  },
  { timeoutMs: 100 },
);
document.querySelector("#out")!.textContent = `page ${Page.match(res)?.target}`;
```

- Functions can't be sent to a worker, so the worker imports its host functions from the ES module at `functions`, and `compile` names the exports it uses. The module must be JavaScript at that URL: here it's `public/host-functions.js`, which Vite serves as it is.
- The helper starts its worker with `new Worker(new URL("./worker-entry.js", import.meta.url), { type: "module" })`, which Vite and other bundlers recognize. Pass `worker: () => new Worker(...)` to start it another way.
- `AlertRouting.compile` with a `SigilWorker` returns a promise of a typed policy, and `Page.match` reads its results as before.

Every call has a deadline, the helper's `timeoutMs` by default. An evaluation's own `timeoutMs` goes to the module first, and the helper waits half a second longer before it terminates the worker and rejects with a `SigilTimeoutError`. The next call starts a new worker, and compiled policies compile again in it by themselves.

`src/worker.ts` runs the same helper under Bun, with an alert the module cancels, one whose host function call alone outlasts the helper's deadline, and then a normal one:

```ts
import { SigilTimeoutError, SigilWorker } from "@spechtlabs/sigil/worker";

import { sigilFiles } from "./files.ts";
import { AlertRouting, Page } from "./kind.ts";

const sigil = new SigilWorker({
  wasm: import.meta.resolve("@spechtlabs/sigil/sigil.wasm"),
  functions: new URL("./host-functions.ts", import.meta.url),
  timeoutMs: 2_000,
});

const policy = await AlertRouting.compile(sigil, sigilFiles("policies", "policies/checkout"), {
  policy: "checkout.alerts",
  require: [{ policy: "platform.paging" }],
  trustedFiles: sigilFiles("policies", "policies/platform"),
  functions: ["split"],
});

const team = { name: "checkout", oncall: "checkout-primary", channel: "#checkout-alerts" };
const alerts = [
  { name: "CheckoutErrorRate", severity: "critical", labels: { env: "production" }, firing_for: "2m" },
  { name: "FleetWideNoise", severity: "info", labels: { env: "production", affects: "team,".repeat(500_000) }, firing_for: "1m" },
  { name: "FleetWideFlood", severity: "info", labels: { env: "production", affects: "team,".repeat(2_000_000) }, firing_for: "1m" },
  { name: "CheckoutErrorRate", severity: "critical", labels: { env: "production" }, firing_for: "2m" },
] as const;

for (const alert of alerts) {
  try {
    const res = await policy.eval({ alert, team }, { timeoutMs: 100 });
    const page = Page.match(res);
    console.log(`${alert.name}: ${res.error?.kind ?? (page !== undefined ? `page ${page.target}` : res.decision)}`);
  } catch (err) {
    if (!(err instanceof SigilTimeoutError)) throw err;
    console.error(`${alert.name}: ${err.message}`); // the worker is gone; the next call starts a new one
  }
}

sigil.terminate();
```

```text
$ bun src/worker.ts
CheckoutErrorRate: page checkout-primary
FleetWideNoise: canceled
FleetWideFlood: the Sigil worker didn't answer eval within 600 ms
CheckoutErrorRate: page checkout-primary
```

## Next steps

- [`examples/alert-routing`](https://github.com/SpechtLabs/sigil/tree/main/examples/alert-routing): the same router as a complete Next.js service, with an HTTP API, hot reload, a paging fallback for failed evaluations, an operator console that previews decisions in the browser, telemetry and load tests.
- [WebAssembly module](/reference/wasm/): every export of the package, and the ABI for hosts in other languages.
- [Per-team policies](/guides/team-policies/) and [Test your policies](/guides/test-policies/): the policy side, which doesn't change with the host's language. `sigil.test(files, tests)` runs the same test files in your program and returns the records `sigil test -o json` prints.
- [Check policies in CI](/guides/ci/): check the team's policies against the exported kind file before your program ever loads them.
