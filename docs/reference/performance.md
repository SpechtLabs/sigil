---
title: Performance
icon: mdi:speedometer
createTime: 2026/09/28 21:00:00
permalink: /reference/performance/
description: What compiling and evaluating Sigil policies costs, as measured, and how to measure it yourself
---

A host compiles a policy once and evaluates it for every request. Every number on this page comes from one machine: an Apple M5 Pro with 18 CPU cores and 64 GB of memory, running macOS 26.6.2 and Go 1.27.1, with the benchmarks limited to two CPUs (`GOMAXPROCS=2`). On it, evaluating a realistic policy takes under two microseconds and allocates 1.5 to 5 KiB, including the result and the trace the host gets back, and compiling the same policy takes a few microseconds per rule. The same benchmarks took about 1.6 times as long on an Apple M1 Max, a 2021 chip: times scale with the machine, allocation counts don't. Read the times as orders of magnitude, and [measure your own](#measure-it-yourself) before sizing a service on them.

## What to expect

- **Compile once, evaluate often.** Compiling a one-rule policy costs about 6 µs, and evaluating it under 1 µs. A service compiles when it loads or reloads policies, never per request.
- **Evaluation cost follows the rules that match.** Every rule's condition is evaluated, and each rule that matches also builds a candidate that's folded, ranked and traced. In the synthetic 64-rule policy below, a rule costs about 130 ns when it doesn't match and 350 ns when it does, so the policy takes 8.5 µs when one rule matches and 22 µs when all of them do. Most real policies decide with a handful of matching rules.
- **Compiled policies are safe to share.** A compiled policy is immutable. Concurrent evaluations share no mutable state and take no locks, so any number of goroutines can evaluate the same policy.
- **Allocation is predictable.** An evaluation allocates the same number of objects every time for the same input and outcome; no evaluation benchmark's count varied between samples. Most of it is the result and the trace a host receives.
- **Every evaluation halts.** Sigil has no loops or recursion; quantifiers and filters range over finite lists, so an evaluation ends as long as its host functions do. Its cost still grows with the input's lists and there's no cost budget yet, so bound input sizes and evaluate under a context with a deadline, which `Eval` checks while it runs. See [Halting by construction](/understanding/halting/).

## Evaluating policies

The policies of the [example service](/guides/example-service/), evaluated through the public [Go API](/reference/go-api/) with their results and traces. **Parallel** is two goroutines evaluating the same compiled policy on two CPUs, reported as wall time per evaluation, so it reads as throughput. Apple M5 Pro, `GOMAXPROCS=2`, medians of ten samples:

| Case | Serial | Parallel | Allocated | Allocations |
| --- | ---: | ---: | ---: | ---: |
| `AccessGrant`, a member granted two roles | 1.73 µs | 1.47 µs | 3.9 KiB | 50 |
| `AccessGrant`, two exclusive grants that conflict | 1.80 µs | 1.56 µs | 4.7 KiB | 58 |
| `DeployApproval`, the service's owner deploys | 1.78 µs | 1.47 µs | 3.9 KiB | 56 |
| `AccessGrant`, an input that fails an assertion | 378 ns | 378 ns | 1.6 KiB | 17 |

The engine's own benchmarks evaluate a synthetic policy for each kind of outcome, also through the public API. The rules read scalar, list and map fields, and one quantifies over a list. Apple M5 Pro, `GOMAXPROCS=2`, medians of ten samples:

| Case | Time | Allocated | Allocations |
| --- | ---: | ---: | ---: |
| One ranked decision | 724 ns | 1.9 KiB | 30 |
| Collected outcomes from an import and two invocations | 1.25 µs | 4.0 KiB | 49 |
| The same, two goroutines on two CPUs | 1.05 µs | 4.0 KiB | 49 |
| Two exclusive decisions that conflict | 1.38 µs | 5.0 KiB | 60 |
| A failed assertion | 332 ns | 1.6 KiB | 18 |
| No rule matches; the fallback decides | 267 ns | 1.0 KiB | 15 |

Below the public API, the evaluator alone takes 451 ns for one matching rule and 725 ns for a policy composed from an import and two invocations. Its cost grows in proportion to the rules. The synthetic policy repeats one rule shape: in the first column every copy matches, and in the second only the last one does, so the rules cost the same to evaluate and only the candidates differ. Apple M5 Pro, `GOMAXPROCS=2`, medians of ten samples:

<LineChart
  title="Evaluation time by number of rules"
  x-label="Rules in the policy"
  x-unit="rule"
  :x="[1, 8, 16, 32, 64, 128]"
  :x-ticks="[0, 32, 64, 96, 128]"
  :series="[
    { label: 'Every rule matches', values: [0.451, 2.83, 5.45, 10.9, 22.4, 48.1] },
    { label: 'One rule matches', values: [0.451, 1.34, 2.35, 4.37, 8.45, 17.0] },
  ]"
/>

| Rules | Every rule matches | Allocations | One rule matches | Allocations |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 451 ns | 18 | 451 ns | 18 |
| 8 | 2.83 µs | 64 | 1.34 µs | 25 |
| 16 | 5.45 µs | 113 | 2.35 µs | 33 |
| 32 | 10.9 µs | 210 | 4.37 µs | 49 |
| 64 | 22.4 µs | 403 | 8.45 µs | 81 |
| 128 | 48.1 µs | 788 | 17.0 µs | 145 |

A `collect one` kind with a precedence costs the same when one rule matches: 8.40 µs at 64 rules. Through the public API, with the result and its trace, the 64-rule policy takes 33.7 µs, 77 KiB and 666 allocations when every rule matches, and 8.80 µs, 5.7 KiB and 92 allocations when one does. Building the result and the trace, which lists every candidate, accounts for 11 µs of the first.

## Compiling and checking

Apple M5 Pro, `GOMAXPROCS=2`, medians of ten samples:

| Operation | 1 rule | 64 rules |
| --- | ---: | ---: |
| Compile a policy through the Go API | 6.2 µs, 14 KiB | 269 µs, 528 KiB |
| Type-check a parsed policy | 1.5 µs, 4.2 KiB | 82 µs, 212 KiB |
| Parse a file | 2.5 µs, 4.0 KiB | 112 µs, 171 KiB |
| Tokenize a file | 1.1 µs, 176 B | 53 µs, 8.6 KiB |
| Format a file | 4.6 µs, 6.4 KiB | 202 µs, 278 KiB |

Building a kind from Go types takes 3.1 µs, loading an exported kind file 6.4 µs, and decoding one JSON input into the kind's Go type 0.6 µs.

::: details Every benchmark
Apple M5 Pro, `GOMAXPROCS=2`, medians of ten samples each; see [how they were measured](#how-these-numbers-were-measured).

| Package | Benchmark | Time/op | B/op | Allocs/op |
| --- | --- | ---: | ---: | ---: |
| cmd/sigil/internal/config | ConfigParse | 2.29 µs | 8.1 KiB | 61 |
| internal/ast | ASTPrint | 169 ns | 280 B | 6 |
| internal/bundle | BundleCompile/rules=1 | 6.08 µs | 13.2 KiB | 214 |
| internal/bundle | BundleCompile/rules=64 | 266 µs | 519 KiB | 7.87k |
| internal/check | CheckPolicy/rules=1 | 1.5 µs | 4.24 KiB | 28 |
| internal/check | CheckPolicy/rules=64 | 81.5 µs | 212 KiB | 493 |
| internal/check | LoadKind | 6.37 µs | 11.5 KiB | 226 |
| internal/constant | ConstantEval | 126 ns | 384 B | 5 |
| internal/diag | DiagnosticRender | 578 ns | 1.08 KiB | 15 |
| internal/eval | CompileExpression | 466 ns | 1.31 KiB | 34 |
| internal/eval | EvalPolicy/composed | 725 ns | 1.98 KiB | 30 |
| internal/eval | EvalPolicy/one-matching/rules=8 | 1.34 µs | 1.31 KiB | 25 |
| internal/eval | EvalPolicy/one-matching/rules=16 | 2.35 µs | 1.75 KiB | 33 |
| internal/eval | EvalPolicy/one-matching/rules=32 | 4.37 µs | 2.75 KiB | 49 |
| internal/eval | EvalPolicy/one-matching/rules=64 | 8.45 µs | 4.75 KiB | 81 |
| internal/eval | EvalPolicy/one-matching/rules=128 | 17 µs | 8.25 KiB | 145 |
| internal/eval | EvalPolicy/ranked/rules=64 | 8.4 µs | 4.76 KiB | 82 |
| internal/eval | EvalPolicy/ranked/rules=128 | 16.9 µs | 8.26 KiB | 146 |
| internal/eval | EvalPolicy/rules=1 | 451 ns | 952 B | 18 |
| internal/eval | EvalPolicy/rules=8 | 2.83 µs | 4.27 KiB | 64 |
| internal/eval | EvalPolicy/rules=16 | 5.45 µs | 8.09 KiB | 113 |
| internal/eval | EvalPolicy/rules=32 | 10.9 µs | 15.8 KiB | 210 |
| internal/eval | EvalPolicy/rules=64 | 22.4 µs | 31.3 KiB | 403 |
| internal/eval | EvalPolicy/rules=128 | 48.1 µs | 62.1 KiB | 788 |
| internal/format | Format/rules=1 | 4.6 µs | 6.39 KiB | 174 |
| internal/format | Format/rules=64 | 202 µs | 278 KiB | 7.25k |
| internal/gokind | DecodeInput | 647 ns | 800 B | 22 |
| internal/gokind | GoKindBuild | 3.13 µs | 5.65 KiB | 101 |
| internal/gokind | Synthesize | 1.6 µs | 3.17 KiB | 55 |
| internal/kind | KindSource | 660 ns | 1.79 KiB | 30 |
| internal/lexer | Lexer/rules=1 | 1.08 µs | 176 B | 33 |
| internal/lexer | Lexer/rules=64 | 52.7 µs | 8.55 KiB | 1.61k |
| internal/lint | Lint | 7.79 µs | 2.17 KiB | 70 |
| internal/parser | ParseExpression | 1.17 µs | 2.02 KiB | 49 |
| internal/parser | ParseFile/rules=1 | 2.52 µs | 3.95 KiB | 104 |
| internal/parser | ParseFile/rules=64 | 112 µs | 171 KiB | 4.52k |
| internal/result | Result/assertion | 168 ns | 848 B | 10 |
| internal/result | Result/success | 1.02 µs | 2.95 KiB | 41 |
| internal/testsuite | TestSuiteParse | 228 µs | 245 KiB | 4.7k |
| pkg/policy | CandidateLocation | 93.8 ns | 144 B | 5 |
| pkg/policy | PolicyCompile/rules=1 | 6.23 µs | 13.7 KiB | 222 |
| pkg/policy | PolicyCompile/rules=64 | 269 µs | 528 KiB | 7.88k |
| pkg/policy | PolicyEval/assertion | 332 ns | 1.55 KiB | 18 |
| pkg/policy | PolicyEval/collect | 1.25 µs | 3.97 KiB | 49 |
| pkg/policy | PolicyEval/conflict | 1.38 µs | 5.02 KiB | 60 |
| pkg/policy | PolicyEval/fallback | 267 ns | 1.02 KiB | 15 |
| pkg/policy | PolicyEval/parallel | 1.05 µs | 3.97 KiB | 49 |
| pkg/policy | PolicyEval/ranked | 724 ns | 1.92 KiB | 30 |
| pkg/policy | PolicyEvalRules/all-matching/rules=64 | 33.7 µs | 77.2 KiB | 666 |
| pkg/policy | PolicyEvalRules/one-matching/rules=64 | 8.8 µs | 5.73 KiB | 92 |
| pkg/policy | PolicyEvalRules/ranked/rules=64 | 8.89 µs | 5.74 KiB | 93 |

:::

## In a service

Engine benchmarks leave out everything around an evaluation. The example service measures that too: deploygate answering HTTP requests with every decision logged, traced, counted in metrics and continuously profiled. The Apple M5 Pro ran the example's Docker Compose stack in OrbStack, whose VM has 18 CPUs and 15.7 GiB of memory, and k6 sent a constant 1,000 iterations per second for two minutes with 20 preallocated and at most 100 virtual users:

| At 1,000 requests per second | |
| --- | ---: |
| p95 latency | 0.98 ms |
| p99 latency | 2.65 ms |
| Allocated per request | 46 KiB |
| CPU per request | 0.28 ms |
| Resident memory | 46 MiB |

A decision request evaluates two policies, access then deploy. Those evaluations are a small part of it: under 10 KiB of the 46 KiB it allocates, and a few microseconds of its 0.28 ms of CPU. HTTP handling, JSON and the telemetry make up the rest. k6 rotated through its eight request cases; no request failed and no iteration was dropped. Latencies are k6's. Allocation and CPU per request are the process counters over a fixed 90-second window, divided by the decision requests (POSTs) in it, and resident memory is that window's mean.

## How these numbers were measured

Every number on this page was measured on 29 September 2026 on an Apple M5 Pro with 18 CPU cores and 64 GB of memory, running macOS 26.6.2 and Go 1.27.1 for darwin/arm64:

- Each benchmark ran ten samples of 200 ms each with `GOMAXPROCS=2`, and the tables show the median sample. The parallel cases use `b.RunParallel` with two goroutines.
- Compilation happens before the timer starts in every evaluation benchmark. Evaluation benchmarks include building the public result and its trace, because a host always gets them.
- The synthetic policies come from `internal/benchtest`. `rules=N` repeats one rule shape N times, and every copy matches, except in the `one-matching` and `ranked` cases, where only the last one does.
- Time per operation depends on the machine. Allocated bytes and allocation counts change little between machines with the same Go version and architecture, so they're the numbers to compare across machines. Allocated bytes aren't retained heap.
- For scale, an Apple M1 Max, a 2021 chip with 10 CPU cores and 64 GB running macOS 26.2, ran the same benchmarks the same day with the same settings, on a revision from before the last two changes to the engine. The 14 benchmarks those changes don't touch took 1.4 to 1.7 times as long there, 1.6 times on average, with the same allocation counts.

## Measure it yourself

From a checkout of the repository, the engine's benchmarks run with the same settings as above:

```sh
mise run bench
```

That prints each benchmark's medians, writes the raw samples to `benchmark-results/`, and takes a few minutes. Narrow it to what you're changing, or compare against another revision, with the flags in [Testing, fuzzing and benchmarking Sigil](/guides/testing/#measure-performance):

```sh
mise run bench -- --filter PolicyEval ./pkg/policy
mise run bench -- --baseline main
```

The example service's policies have a benchmark of their own. From `examples/`:

```sh
go test ./internal/store -run '^$' -bench BenchmarkPolicies -benchmem -count 10 -cpu 2 -benchtime 200ms
```

For the service measurement, start the example's stack as the [example service guide](/guides/example-service/#run-it) shows, then run the load test from `examples/` at the same rate:

```sh
RATE=1000 DURATION=2m mise run loadtest
```

k6 writes its report under `examples/results/`, and the deploygate dashboard in Grafana shows the run's throughput and latency next to the service's profiles.
