---
title: Performance
icon: mdi:speedometer
createTime: 2026/09/28 21:00:00
permalink: /reference/performance/
description: What compiling and evaluating Sigil policies costs, as measured, and how to measure it yourself
---

A host compiles a policy once and evaluates it for every request. Evaluating a realistic policy takes one to two microseconds and allocates 1.5 to 5 KiB on the machine below, including the result and the trace the host gets back. Compiling the same policy takes a few microseconds per rule. These numbers come from one machine; read them as orders of magnitude, and [measure your own](#measure-it-yourself) before sizing a service on them.

## What to expect

- **Compile once, evaluate often.** Compiling a one-rule policy costs about 7 µs, and evaluating it under 1 µs. A service compiles when it loads or reloads policies, never per request.
- **Evaluation cost follows the rules that match.** Every rule's condition is evaluated, and each rule that matches also builds a candidate that's folded, ranked and traced. In the synthetic 64-rule policy below, a rule costs about 140 ns when it doesn't match and 370 ns when it does, so the policy takes 8.9 µs when one rule matches and 24 µs when all of them do. Most real policies decide with a handful of matching rules.
- **Compiled policies are safe to share.** A compiled policy is immutable. Concurrent evaluations share no mutable state and take no locks, so any number of goroutines can evaluate the same policy.
- **Allocation is predictable.** An evaluation allocates the same number of objects every time for the same input and outcome; no evaluation benchmark's count varied between samples. Most of it is the result and the trace a host receives.
- **Every evaluation halts.** Sigil has no loops or recursion; quantifiers and filters range over finite lists, so an evaluation ends as long as its host functions do. Its cost still grows with the input's lists and there's no cost budget yet, so bound input sizes and evaluate under a context with a deadline, which `Eval` checks while it runs. See [Halting by construction](/understanding/halting/).

## Evaluating policies

The policies of the [example service](/guides/example-service/), evaluated through the public [Go API](/reference/go-api/) with their results and traces. **Parallel** is two goroutines evaluating the same compiled policy on two CPUs, reported as wall time per evaluation, so it reads as throughput.

| Case | Serial | Parallel | Allocated | Allocations |
| --- | ---: | ---: | ---: | ---: |
| `AccessGrant`, a member granted two roles | 1.73 µs | 1.43 µs | 3.9 KiB | 50 |
| `AccessGrant`, two exclusive grants that conflict | 1.76 µs | 1.51 µs | 4.6 KiB | 58 |
| `DeployApproval`, the service's owner deploys | 1.81 µs | 1.45 µs | 3.9 KiB | 56 |
| `AccessGrant`, an input that fails an assertion | 372 ns | 371 ns | 1.5 KiB | 17 |

The engine's own benchmarks evaluate a synthetic policy for each kind of outcome, also through the public API. The rules read scalar, list and map fields, and one quantifies over a list.

| Case | Time | Allocated | Allocations |
| --- | ---: | ---: | ---: |
| One ranked decision | 783 ns | 1.9 KiB | 30 |
| Collected outcomes from an import and two invocations | 1.35 µs | 4.0 KiB | 49 |
| The same, two goroutines on two CPUs | 1.08 µs | 4.0 KiB | 49 |
| Two exclusive decisions that conflict | 1.49 µs | 5.1 KiB | 60 |
| A failed assertion | 348 ns | 1.5 KiB | 18 |
| No rule matches; the fallback decides | 294 ns | 1.0 KiB | 15 |

Below the public API, the evaluator alone takes 490 ns for one matching rule and 788 ns for a policy composed from an import and two invocations. Its cost grows in proportion to the rules. The synthetic policy repeats one rule shape: in the first column every copy matches, and in the second only the last one does, so the rules cost the same to evaluate and only the candidates differ.

| Rules | Every rule matches | Allocations | One rule matches | Allocations |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 490 ns | 18 | 490 ns | 18 |
| 8 | 3.02 µs | 64 | 1.42 µs | 25 |
| 16 | 5.78 µs | 113 | 2.46 µs | 33 |
| 32 | 11.7 µs | 210 | 4.57 µs | 49 |
| 64 | 23.9 µs | 403 | 8.89 µs | 81 |
| 128 | 50.6 µs | 788 | 17.9 µs | 145 |

A `collect one` kind with a precedence costs the same when one rule matches: 8.87 µs at 64 rules. Through the public API, with the result and its trace, the 64-rule policy takes 35.3 µs, 77 KiB and 666 allocations when every rule matches, and 9.13 µs, 5.8 KiB and 92 allocations when one does. Building the result and the trace, which lists every candidate, accounts for 11 µs of the first.

## Compiling and checking

| Operation | 1 rule | 64 rules |
| --- | ---: | ---: |
| Compile a policy through the Go API | 6.7 µs, 14 KiB | 287 µs, 554 KiB |
| Type-check a parsed policy | 1.6 µs, 4.2 KiB | 85 µs, 212 KiB |
| Parse a file | 2.8 µs, 4.4 KiB | 127 µs, 197 KiB |
| Tokenize a file | 1.2 µs, 384 B | 58 µs, 21.5 KiB |
| Format a file | 5.1 µs, 7.0 KiB | 227 µs, 317 KiB |

Building a kind from Go types takes 3.4 µs, loading an exported kind file 6.8 µs, and decoding one JSON input into the kind's Go type 0.7 µs.

::: details Every benchmark
Medians of ten samples each; see [how they were measured](#how-these-numbers-were-measured).

| Package | Benchmark | Time/op | B/op | Allocs/op |
| --- | --- | ---: | ---: | ---: |
| cmd/sigil/internal/config | ConfigParse | 2.4 µs | 8.1 KiB | 61 |
| internal/ast | ASTPrint | 178 ns | 280 B | 6 |
| internal/bundle | BundleCompile/rules=1 | 6.57 µs | 13.6 KiB | 220 |
| internal/bundle | BundleCompile/rules=64 | 286 µs | 545 KiB | 8.26k |
| internal/check | CheckPolicy/rules=1 | 1.55 µs | 4.24 KiB | 28 |
| internal/check | CheckPolicy/rules=64 | 84.8 µs | 212 KiB | 493 |
| internal/check | LoadKind | 6.84 µs | 11.9 KiB | 232 |
| internal/constant | ConstantEval | 132 ns | 384 B | 5 |
| internal/diag | DiagnosticRender | 605 ns | 1.08 KiB | 15 |
| internal/eval | CompileExpression | 475 ns | 1.31 KiB | 34 |
| internal/eval | EvalPolicy/composed | 788 ns | 2.05 KiB | 30 |
| internal/eval | EvalPolicy/one-matching/rules=8 | 1.42 µs | 1.34 KiB | 25 |
| internal/eval | EvalPolicy/one-matching/rules=16 | 2.46 µs | 1.78 KiB | 33 |
| internal/eval | EvalPolicy/one-matching/rules=32 | 4.57 µs | 2.78 KiB | 49 |
| internal/eval | EvalPolicy/one-matching/rules=64 | 8.89 µs | 4.78 KiB | 81 |
| internal/eval | EvalPolicy/one-matching/rules=128 | 17.9 µs | 8.28 KiB | 145 |
| internal/eval | EvalPolicy/ranked/rules=64 | 8.87 µs | 4.79 KiB | 82 |
| internal/eval | EvalPolicy/ranked/rules=128 | 17.7 µs | 8.29 KiB | 146 |
| internal/eval | EvalPolicy/rules=1 | 490 ns | 984 B | 18 |
| internal/eval | EvalPolicy/rules=8 | 3.02 µs | 4.3 KiB | 64 |
| internal/eval | EvalPolicy/rules=16 | 5.78 µs | 8.12 KiB | 113 |
| internal/eval | EvalPolicy/rules=32 | 11.7 µs | 15.9 KiB | 210 |
| internal/eval | EvalPolicy/rules=64 | 23.9 µs | 31.4 KiB | 403 |
| internal/eval | EvalPolicy/rules=128 | 50.6 µs | 62.1 KiB | 788 |
| internal/format | Format/rules=1 | 5.05 µs | 7 KiB | 183 |
| internal/format | Format/rules=64 | 227 µs | 317 KiB | 7.83k |
| internal/gokind | DecodeInput | 692 ns | 800 B | 22 |
| internal/gokind | GoKindBuild | 3.39 µs | 6.04 KiB | 107 |
| internal/gokind | Synthesize | 1.69 µs | 3.16 KiB | 55 |
| internal/kind | KindSource | 689 ns | 1.79 KiB | 30 |
| internal/lexer | Lexer/rules=1 | 1.17 µs | 384 B | 36 |
| internal/lexer | Lexer/rules=64 | 58.1 µs | 21.5 KiB | 1.8k |
| internal/lint | Lint | 8.19 µs | 2.17 KiB | 70 |
| internal/parser | ParseExpression | 1.23 µs | 2.02 KiB | 49 |
| internal/parser | ParseFile/rules=1 | 2.79 µs | 4.36 KiB | 110 |
| internal/parser | ParseFile/rules=64 | 127 µs | 197 KiB | 4.9k |
| internal/result | Result/assertion | 180 ns | 864 B | 10 |
| internal/result | Result/success | 1.09 µs | 3.03 KiB | 41 |
| internal/testsuite | TestSuiteParse | 240 µs | 245 KiB | 4.7k |
| pkg/policy | CandidateLocation | 97.9 ns | 144 B | 5 |
| pkg/policy | PolicyCompile/rules=1 | 6.7 µs | 14.1 KiB | 228 |
| pkg/policy | PolicyCompile/rules=64 | 287 µs | 554 KiB | 8.27k |
| pkg/policy | PolicyEval/assertion | 348 ns | 1.55 KiB | 18 |
| pkg/policy | PolicyEval/collect | 1.35 µs | 4.05 KiB | 49 |
| pkg/policy | PolicyEval/conflict | 1.49 µs | 5.08 KiB | 60 |
| pkg/policy | PolicyEval/fallback | 294 ns | 1.05 KiB | 15 |
| pkg/policy | PolicyEval/parallel | 1.08 µs | 4.05 KiB | 49 |
| pkg/policy | PolicyEval/ranked | 783 ns | 1.95 KiB | 30 |
| pkg/policy | PolicyEvalRules/all-matching/rules=64 | 35.3 µs | 77.3 KiB | 666 |
| pkg/policy | PolicyEvalRules/one-matching/rules=64 | 9.13 µs | 5.77 KiB | 92 |
| pkg/policy | PolicyEvalRules/ranked/rules=64 | 9.28 µs | 5.77 KiB | 93 |

:::

## In a service

Engine benchmarks leave out everything around an evaluation. The example service measures that too: deploygate answering HTTP requests with every decision logged, traced, counted in metrics and continuously profiled.

| At 1,000 requests per second | |
| --- | ---: |
| p95 latency | 0.95 ms |
| p99 latency | 2.32 ms |
| Allocated per request | 44 KiB |
| CPU per request | 0.26 ms |
| Resident memory | 48 MiB |

A decision request evaluates two policies, access then deploy. Those evaluations are a small part of it: under 10 KiB of the 44 KiB it allocates, and a few microseconds of its 0.26 ms of CPU. HTTP handling, JSON and the telemetry make up the rest. k6 sent its eight request cases at a constant 1,000 iterations per second for two minutes, with 20 to 100 virtual users, against the example's Docker Compose stack with 18 CPUs; no request failed and no iteration was dropped. Latencies are k6's. Allocation and CPU per request are the process counters over a fixed 90-second window, divided by the decision requests (POSTs) in it, and resident memory is that window's mean.

## How these numbers were measured

Every number on this page was measured on 28 September 2026, and the engine's evaluation benchmarks again on 29 September, on an Apple M5 Pro with 18 CPUs, running macOS and Go 1.27.1 for darwin/arm64:

- Each benchmark ran ten samples of 200 ms each with `GOMAXPROCS=2`, and the tables show the median sample. The parallel cases use `b.RunParallel` with two goroutines.
- Compilation happens before the timer starts in every evaluation benchmark. Evaluation benchmarks include building the public result and its trace, because a host always gets them.
- The synthetic policies come from `internal/benchtest`. `rules=N` repeats one rule shape N times, and every copy matches, except in the `one-matching` and `ranked` cases, where only the last one does.
- Time per operation depends on the machine. Allocated bytes and allocation counts change little between machines with the same Go version and architecture, so they're the numbers to compare across machines. Allocated bytes aren't retained heap.

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
