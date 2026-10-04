---
title: One engine for every host
icon: mdi:cube-outline
createTime: 2026/09/30 12:00:00
permalink: /understanding/one-engine/
---

Sigil is implemented once, in Go. A Go program embeds it as a library, and every other host runs the same Go code compiled to WebAssembly: the TypeScript package loads `sigil.wasm`, and a Rust or Python host would load the same file. This page explains why there are no ports, why the module is shaped the way it is, and what that costs. The ABI itself is in [WebAssembly module](/reference/wasm/), and the TypeScript walkthrough in [Embed Sigil in TypeScript](/guides/embed-typescript/).

## Why not a port

A policy language is only as trustworthy as the promise that a policy means the same thing wherever it runs. A team checks its policy with the `sigil` CLI in CI, tests it with `sigil test`, and then a service evaluates it. If the service ran a TypeScript reimplementation, every place where JavaScript and Go disagree would be a place where the tested policy and the deployed one decide differently. Nobody would notice until an alert went to the wrong person.

They disagree in more places than it seems:

- **Regular expressions.** `matches` is Go's RE2: linear time, no backreferences, no lookaround, and its own syntax for flags and named groups. A JavaScript `RegExp` backtracks, so a pattern that's safe in Go can take exponential time in a port, and some patterns one accepts the other rejects or matches differently. Reimplementing RE2 faithfully is a project of its own.
- **Characters.** `like "?"` matches exactly one character, which in Go is one Unicode code point. A JavaScript string is UTF-16, and an emoji is two code units. Map keys print sorted by their UTF-8 bytes, which isn't the order JavaScript's string comparison gives for characters outside the Basic Multilingual Plane.
- **Durations.** A `duration` is a 64-bit count of nanoseconds. A JavaScript number holds integers exactly only up to 2⁵³, which is about 104 days of nanoseconds, so `alert.firing_for + 200d` would round in a port unless every duration went through `BigInt`.
- **Overflow.** `int` and `duration` arithmetic that leaves 64 bits is a [runtime error](/reference/evaluation/#runtime-errors). A port has to detect the same overflow at the same place, with the same message.
- **Everything else the CLI prints.** Diagnostics, their positions and hints, the trace, the order of candidates. The golden tests pin them for the Go implementation; a port would need its own copy of every one.

Each of these is solvable, and each would drift. The Go engine already has fuzz targets, golden tests and the CLI as its oracle. Compiling it to WebAssembly gives another host all of that for free, and the module's own tests check that its answers are the CLI's, record for record.

## Why WASI preview 1 and a flat ABI

Go can target WebAssembly three ways, and only one fits a module every host can load.

- **`GOOS=js`** produces a module that only runs next to `wasm_exec.js`, a glue script tied to the Go release that built it, and only in a JavaScript engine. Wasmtime, wazero and every non-JavaScript host are out.
- **TinyGo** produces much smaller modules, but its `reflect` is incomplete. Sigil's engine binds kinds and host functions with reflection, including `reflect.MakeFunc`, so the engine wouldn't build, or would build and fail at run time.
- **WASI preview 2** and the Component Model would give typed interfaces instead of bytes in linear memory. Mainline Go doesn't target them.

So the module is `GOOS=wasip1`, built as a reactor: the host initializes it once and then calls its exports, instead of running a `main` that exits. WASI preview 1 is what wazero, Wasmtime, Wasmer and Node's `node:wasi` all speak, and a browser needs only a small shim; the TypeScript package carries its own, about 250 lines.

The interface on top is deliberately flat: four exports, one import, and JSON in both directions. Every WebAssembly runtime can pass two 32-bit integers and read bytes out of memory, and every language can read JSON. The records are the ones the CLI already prints with `-o json`, so there's one schema for the CLI, the module and every binding, and a binding is a few hundred lines rather than a second implementation of the types.

None of this locks the design in. A Component Model wrapper, a WIT interface over the same ops, can be added once the toolchain supports it, without changing the ABI underneath. A request that doesn't start with `{` is reserved for a binary encoding, such as CBOR, if JSON ever turns out to be the cost that matters.

## Why compiled policies stay in the module

Compiling a policy parses, type-checks and builds closures over the kind's binding. It's the expensive step, and it produces nothing that could be serialized: the compiled form is Go closures in the module's memory. So `compile` returns a handle, a number, and the compiled policy stays where it was built. Each evaluation sends only the input and gets back only the result. That's the same shape as the Go API, where a host compiles once and evaluates the immutable `*policy.Policy` per request.

The cost is that a host has to release handles it no longer needs, like closing a file. The TypeScript package makes that `using`, and releases a policy that's garbage collected without it.

## What JSON and WebAssembly cost

Every evaluation through the module encodes the input as JSON, decodes it into the kind's Go values, evaluates, and encodes the result and its trace. The [measurements](/reference/performance/#through-webassembly) separate the steps, on one policy of the example service:

- The evaluator alone takes 1.17 µs.
- The same evaluation with JSON in and out, natively, takes 5.33 µs. JSON is about four fifths of that.
- Through the module, it takes 26.7 µs under Bun and 35.7 µs under Node.

Go compiled to WebAssembly runs several times slower than native Go, and that, more than JSON, is what a host outside Go pays. A few tens of microseconds per decision is small next to the network request or the queue message that usually triggers it, and the example service already spends far more on HTTP and telemetry than on evaluating. A binary encoding would shrink the JSON share, but not the WebAssembly one, so it waits for a host that needs it.

Loading the module costs more: about 33 ms to compile 11 MB of WebAssembly and start the Go runtime. A service does it once, at startup, and a page does it once per worker. The Rust crate compiles it with Cranelift, about 4 s of CPU (0.3 s of wall time on 12 cores), and its `precompiled` feature moves that to build time, which brings the load down to milliseconds.

## Why the module enforces its own deadline

In Go, a host bounds an evaluation with a context deadline. `context.WithDeadline` starts a timer, and the Go scheduler fires it from another goroutine while the evaluation runs. Inside a WASI preview 1 reactor there's one thread and no preemption: while `sigil_call` runs, nothing else in the module gets scheduled, so the timer can't fire until the evaluation has already finished.

So the engine polls instead. The evaluator already checks its context before every rule, after every host function call, and every few hundred elements of a loop; in the module each of those checks reads the clock. An evaluation that runs past `timeout_ms` stops at the next check with a `canceled` failure, and the instance stays usable.

Polling can't stop what runs between two checks. A host function that never returns holds the thread, and no code inside the module runs until it does. A host that must bound time strictly runs the module where it can kill it from outside, which is what the TypeScript package's worker helper does: it waits for the module's own deadline, then terminates the worker. The Rust crate adds a hard deadline that needs no worker: wasmtime's epoch interruption stops runaway engine work at the next epoch check, and the crate can also meter fuel to bound a call by work rather than time. It doesn't close the gap for a host function that never returns. Epoch interruption stops WebAssembly, not native code, so a blocking host function is stopped only once it returns; a Rust host gives its own I/O a timeout, or evaluates on `spawn_blocking` and abandons the instance. Either way a killed instance is stopped, because Go can't resume after a cut-off call.

## Why trusted files are stricter than `policy.From`

A Go host that requires its guardrails passes them as a separate `fs.FS` with `policy.From`, and the separation is structural: the trusted documents come from a different filesystem value than the team's bundle. Through the module, both arrive as lists of `{path, source}` strings in one request, so the rules have to carry what the type system carried in Go.

- **Trust comes from the list, never the path.** A path that appears among both the files and the trusted files fails the request. Otherwise a team file named `platform/paging.sigil` could pass for the platform's.
- **A required policy must come from the trusted files.** When the trusted files don't define it, Go falls back to the bundle's copy, which anyone who writes to the bundle controls. The module fails the compile instead: a host that bothered to pass trusted files has said where the policy comes from, and a copy from anywhere else is the thing it was guarding against.

Both rules only reject, so a request that passes them compiles exactly as the Go host would compile the same documents. The reasoning behind trusted sources in general is in [Why required policies need a trusted source](/understanding/bundles/#why-required-policies-need-a-trusted-source).
