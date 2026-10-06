---
title: Performance
icon: mdi:speedometer
createTime: 2026/09/28 21:00:00
permalink: /reference/performance/
description: What compiling and evaluating Sigil policies costs, as measured
---

What compiling and evaluating Sigil policies costs, as measured on one machine; see [how these numbers were measured](#how-these-numbers-were-measured). Evaluating a realistic policy takes under two microseconds and allocates 1.5 to 5 KiB, including the result and the trace the host gets back. Times scale with the machine and allocation counts don't, so read the times as orders of magnitude and [measure your own](/project/contributing/#measure-performance) before sizing a service on them.

## What to expect

- **Compile once, evaluate often.** Compiling a one-rule policy costs about 7 µs, and evaluating it under 1 µs. A service compiles when it loads or reloads policies, never per request.
- **Evaluation cost follows the rules that match.** Every rule's condition is evaluated, and each rule that matches also builds a candidate that's folded, ranked and traced. In the synthetic 64-rule policy below, a rule costs about 140 ns when it doesn't match and 375 ns when it does, so the policy takes 8.8 µs when one rule matches and 24 µs when all of them do. Most real policies decide with a handful of matching rules.
- **Cost grows in proportion to the policy.** At every size up to 512 rules, each rule that doesn't match adds about 140 ns and one allocation, and compiling costs about 5 µs and 8 KiB per rule. When every rule matches, the cost per rule rises slowly with their number, to 525 ns at 512 rules, while the allocations stay at six per rule.
- **Compiled policies are safe to share.** A compiled policy is immutable. Concurrent evaluations share no mutable state and take no locks, so any number of goroutines can evaluate the same policy.
- **Allocation is predictable.** An evaluation allocates the same number of objects every time for the same input and outcome; no evaluation benchmark's count varied between samples. Most of it is the result and the trace a host receives.
- **Every evaluation halts.** Its cost grows with the input's lists, and there's no cost budget yet; see [Halting by construction](/understanding/halting/).
- **Checking the context is nearly free.** The [context checks](/reference/evaluation/#context-checks) cost about 2.5% in the tightest loop, a quantifier comparing two ints, and nothing measurable on the evaluation benchmarks below. Under `context.Background()`, which is never done, nothing is polled.

## Evaluating policies

The policies of the [example service](/guides/example-service/), evaluated through the public [Go API](/reference/go-api/) with their results and traces. **Parallel** is two goroutines evaluating the same compiled policy on two CPUs, reported as wall time per evaluation, so it reads as throughput. Apple M5 Pro, `GOMAXPROCS=2`, medians of ten samples:

| Case | Serial | Parallel | Allocated | Allocations |
| --- | ---: | ---: | ---: | ---: |
| `AccessGrant`, a member granted two roles | 1.79 µs | 1.47 µs | 3.9 KiB | 49 |
| `AccessGrant`, two exclusive grants that conflict | 1.74 µs | 1.53 µs | 4.6 KiB | 54 |
| `DeployApproval`, the service's owner deploys | 1.98 µs | 1.58 µs | 4.1 KiB | 58 |
| `AccessGrant`, an input that fails an assertion | 378 ns | 386 ns | 1.6 KiB | 17 |

The engine's own benchmarks evaluate a synthetic policy for each kind of outcome, also through the public API. The rules read scalar, list and map fields, and one quantifies over a list. Apple M5 Pro, `GOMAXPROCS=2`, medians of ten samples:

| Case | Time | Allocated | Allocations |
| --- | ---: | ---: | ---: |
| One ranked decision | 769 ns | 1.9 KiB | 29 |
| Collected outcomes from an import and two invocations | 1.32 µs | 4.0 KiB | 47 |
| The same, two goroutines on two CPUs | 1.08 µs | 4.0 KiB | 47 |
| Two exclusive decisions that conflict | 1.43 µs | 5.0 KiB | 56 |
| A failed assertion | 354 ns | 1.6 KiB | 18 |
| No rule matches; the fallback decides | 282 ns | 1.0 KiB | 15 |

### By the size of the policy

Below the public API, the evaluator alone takes 478 ns for one matching rule and 761 ns for a policy composed from an import and two invocations. Its cost grows with the rules. The synthetic policy repeats one rule shape: in the first column every copy matches, and in the second only the last one does, so the rules cost the same to evaluate and only the candidates differ. Apple M5 Pro, `GOMAXPROCS=2`, medians of ten samples:

<LineChart
  title="Evaluation time by number of rules"
  x-label="Rules in the policy"
  x-unit="rule"
  :x="[1, 8, 16, 32, 64, 128, 256, 512]"
  :x-ticks="[0, 128, 256, 384, 512]"
  :series="[
    { label: 'Every rule matches', values: [0.478, 3.02, 5.83, 11.7, 24.0, 51.1, 119, 269] },
    { label: 'One rule matches', values: [0.470, 1.40, 2.43, 4.53, 8.84, 17.8, 35.8, 70.7] },
  ]"
/>

| Rules | Every rule matches | Allocated | Allocations | One rule matches | Allocated | Allocations |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 478 ns | 952 B | 18 | 470 ns | 952 B | 18 |
| 8 | 3.02 µs | 4.3 KiB | 64 | 1.40 µs | 1.3 KiB | 25 |
| 16 | 5.83 µs | 8.1 KiB | 113 | 2.43 µs | 1.8 KiB | 33 |
| 32 | 11.7 µs | 15.8 KiB | 210 | 4.53 µs | 2.8 KiB | 49 |
| 64 | 24.0 µs | 31.3 KiB | 403 | 8.84 µs | 4.8 KiB | 81 |
| 128 | 51.1 µs | 62.1 KiB | 788 | 17.8 µs | 8.3 KiB | 145 |
| 256 | 119 µs | 124 KiB | 1,557 | 35.8 µs | 16.0 KiB | 273 |
| 512 | 269 µs | 249 KiB | 3,094 | 70.7 µs | 31.4 KiB | 529 |

Dividing each time by the number of rules shows what a rule costs. With one rule matching, the cost settles at 140 ns a rule from 32 rules up; below that, the evaluation's fixed cost is a large share. With every rule matching, it's about 370 ns a rule from 8 to 64 rules, then rises as their number grows: 399 ns at 128, 465 ns at 256 and 525 ns at 512.

<LineChart
  title="Evaluation time per rule"
  x-label="Rules in the policy"
  x-unit="rule"
  :x="[1, 8, 16, 32, 64, 128, 256, 512]"
  :x-ticks="[0, 128, 256, 384, 512]"
  :series="[
    { label: 'Every rule matches', values: [0.478, 0.378, 0.364, 0.366, 0.375, 0.399, 0.465, 0.525] },
    { label: 'One rule matches', values: [0.470, 0.175, 0.152, 0.142, 0.138, 0.139, 0.140, 0.138] },
  ]"
/>

Allocations don't bend that way. Every evaluation starts at 18; each rule that doesn't match adds exactly one, and each rule that matches adds six:

<LineChart
  title="Allocations per evaluation by number of rules"
  x-label="Rules in the policy"
  x-unit="rule"
  unit="count"
  :x="[1, 8, 16, 32, 64, 128, 256, 512]"
  :x-ticks="[0, 128, 256, 384, 512]"
  :series="[
    { label: 'Every rule matches', values: [18, 64, 113, 210, 403, 788, 1557, 3094] },
    { label: 'One rule matches', values: [18, 25, 33, 49, 81, 145, 273, 529] },
  ]"
/>

A `collect one` kind with a precedence costs the same as the second column, to within 2%, at every size: 8.90 µs at 64 rules and 70.8 µs at 512. Through the public API, with the result and its trace, the 64-rule policy takes 34.5 µs, 77 KiB and 602 allocations when every rule matches, and 9.20 µs, 5.7 KiB and 91 allocations when one does. Building the result and the trace, which lists every candidate, accounts for about 10 µs of the first.

## Compiling and checking

Compiling a policy through the Go API parses it, which includes tokenizing, type-checks it against its kind, and builds what the evaluator runs. Every stage grows in proportion to the policy, or close to it: a 64-rule policy compiles in under 300 µs, and a 512-rule one in under 3 ms. Apple M5 Pro, `GOMAXPROCS=2`, medians of ten samples:

<LineChart
  title="Compile time by number of rules"
  x-label="Rules in the policy"
  x-unit="rule"
  :x="[1, 8, 16, 32, 64, 128, 256, 512]"
  :x-ticks="[0, 128, 256, 384, 512]"
  :series="[
    { label: 'Compile', values: [6.91, 37.9, 74.9, 145, 291, 583, 1290, 2710] },
    { label: 'Parse', values: [2.80, 16.2, 31.6, 61.9, 122, 245, 487, 1000] },
    { label: 'Type-check', values: [1.62, 10.3, 21.1, 42.2, 87.0, 179, 382, 905] },
    { label: 'Tokenize', values: [1.14, 7.04, 13.9, 27.5, 54.8, 110, 219, 438] },
  ]"
/>

| Rules | Compile | Parse | Type-check | Tokenize | Format |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 6.91 µs | 2.80 µs | 1.62 µs | 1.14 µs | 4.97 µs |
| 8 | 37.9 µs | 16.2 µs | 10.3 µs | 7.04 µs | 29.0 µs |
| 16 | 74.9 µs | 31.6 µs | 21.1 µs | 13.9 µs | 56.3 µs |
| 32 | 145 µs | 61.9 µs | 42.2 µs | 27.5 µs | 110 µs |
| 64 | 291 µs | 122 µs | 87.0 µs | 54.8 µs | 216 µs |
| 128 | 583 µs | 245 µs | 179 µs | 110 µs | 429 µs |
| 256 | 1.29 ms | 487 µs | 382 µs | 219 µs | 855 µs |
| 512 | 2.71 ms | 1.00 ms | 905 µs | 438 µs | 1.79 ms |

Per rule, compiling costs 4.5 µs at 64 rules and 5.3 µs at 512. Parsing and tokenizing stay at 1.9 µs and 0.86 µs a rule; the type checker, at 1.4 µs a rule at 64 rules and 1.8 µs at 512, accounts for over half of the rise. From 8 rules up, tokenizing reads about 176 MB of source a second and parsing about 77 MB. Formatting a file, which `sigil fmt` does and compiling doesn't, costs about 3.5 µs a rule.

What compiling allocates grows in proportion too: about 8 KiB a rule at every size. These are allocated bytes, not what the compiled policy retains:

<LineChart
  title="Memory allocated while compiling, by number of rules"
  x-label="Rules in the policy"
  x-unit="rule"
  unit="bytes"
  :x="[1, 8, 16, 32, 64, 128, 256, 512]"
  :x-ticks="[0, 128, 256, 384, 512]"
  :series="[
    { label: 'Compile', values: [14.2, 68.8, 133, 263, 521, 1038, 2075, 4150] },
    { label: 'Parse', values: [3.95, 21.8, 42.2, 83.2, 163, 325, 649, 1300] },
    { label: 'Type-check', values: [4.35, 25.6, 51.1, 105, 212, 427, 855, 1712] },
    { label: 'Tokenize', values: [0.188, 1.17, 2.30, 4.55, 9.05, 18.0, 36.0, 72.0] },
  ]"
/>

| Rules | Compile | Parse | Type-check | Tokenize | Format |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 14.2 KiB | 3.9 KiB | 4.4 KiB | 192 B | 6.0 KiB |
| 8 | 68.8 KiB | 21.8 KiB | 25.6 KiB | 1.2 KiB | 36.5 KiB |
| 16 | 133 KiB | 42.2 KiB | 51.1 KiB | 2.3 KiB | 69.5 KiB |
| 32 | 263 KiB | 83.2 KiB | 105 KiB | 4.5 KiB | 140 KiB |
| 64 | 521 KiB | 163 KiB | 212 KiB | 9.0 KiB | 283 KiB |
| 128 | 1.01 MiB | 325 KiB | 427 KiB | 18.0 KiB | 558 KiB |
| 256 | 2.03 MiB | 649 KiB | 855 KiB | 36.0 KiB | 1.08 MiB |
| 512 | 4.05 MiB | 1.27 MiB | 1.67 MiB | 72.0 KiB | 2.24 MiB |

Building a kind from Go types takes 3.4 µs; it records where every input field lives, so `Eval` reads the host's values in place without copying them. Loading an exported kind file takes 6.9 µs, and decoding one JSON input into the kind's Go type 0.7 µs.

::: details Every benchmark
Apple M5 Pro, `GOMAXPROCS=2`, medians of ten samples each; see [how they were measured](#how-these-numbers-were-measured).

| Package | Benchmark | Time/op | B/op | Allocs/op |
| --- | --- | ---: | ---: | ---: |
| cmd/sigil/internal/config | ConfigParse | 2.47 µs | 8.78 KiB | 69 |
| internal/ast | ASTPrint | 178 ns | 280 B | 6 |
| internal/bundle | BundleCompile/rules=1 | 6.8 µs | 13.8 KiB | 219 |
| internal/bundle | BundleCompile/rules=8 | 37.3 µs | 67.4 KiB | 1.06k |
| internal/bundle | BundleCompile/rules=16 | 73.2 µs | 130 KiB | 2.03k |
| internal/bundle | BundleCompile/rules=32 | 142 µs | 258 KiB | 3.94k |
| internal/bundle | BundleCompile/rules=64 | 282 µs | 511 KiB | 7.76k |
| internal/bundle | BundleCompile/rules=128 | 569 µs | 0.99 MiB | 15.4k |
| internal/bundle | BundleCompile/rules=256 | 1.29 ms | 1.99 MiB | 30.7k |
| internal/bundle | BundleCompile/rules=512 | 2.68 ms | 3.97 MiB | 61.7k |
| internal/check | CheckPolicy/rules=1 | 1.62 µs | 4.35 KiB | 26 |
| internal/check | CheckPolicy/rules=8 | 10.3 µs | 25.6 KiB | 53 |
| internal/check | CheckPolicy/rules=16 | 21.1 µs | 51.1 KiB | 89 |
| internal/check | CheckPolicy/rules=32 | 42.2 µs | 105 KiB | 143 |
| internal/check | CheckPolicy/rules=64 | 87 µs | 212 KiB | 248 |
| internal/check | CheckPolicy/rules=128 | 179 µs | 427 KiB | 453 |
| internal/check | CheckPolicy/rules=256 | 382 µs | 855 KiB | 858 |
| internal/check | CheckPolicy/rules=512 | 905 µs | 1.67 MiB | 1.66k |
| internal/check | LoadKind | 6.92 µs | 12 KiB | 232 |
| internal/constant | ConstantEval | 134 ns | 384 B | 5 |
| internal/diag | DiagnosticRender | 620 ns | 1.09 KiB | 15 |
| internal/eval | CompileExpression | 495 ns | 1.31 KiB | 34 |
| internal/eval | EvalEnum/list | 461 ns | 1.02 KiB | 21 |
| internal/eval | EvalEnum/value | 431 ns | 1.02 KiB | 21 |
| internal/eval | EvalPolicy/composed | 761 ns | 1.98 KiB | 30 |
| internal/eval | EvalPolicy/one-matching/rules=1 | 470 ns | 952 B | 18 |
| internal/eval | EvalPolicy/one-matching/rules=8 | 1.4 µs | 1.31 KiB | 25 |
| internal/eval | EvalPolicy/one-matching/rules=16 | 2.43 µs | 1.75 KiB | 33 |
| internal/eval | EvalPolicy/one-matching/rules=32 | 4.53 µs | 2.75 KiB | 49 |
| internal/eval | EvalPolicy/one-matching/rules=64 | 8.84 µs | 4.75 KiB | 81 |
| internal/eval | EvalPolicy/one-matching/rules=128 | 17.8 µs | 8.25 KiB | 145 |
| internal/eval | EvalPolicy/one-matching/rules=256 | 35.8 µs | 16 KiB | 273 |
| internal/eval | EvalPolicy/one-matching/rules=512 | 70.7 µs | 31.4 KiB | 529 |
| internal/eval | EvalPolicy/ranked/rules=1 | 478 ns | 960 B | 19 |
| internal/eval | EvalPolicy/ranked/rules=8 | 1.4 µs | 1.32 KiB | 26 |
| internal/eval | EvalPolicy/ranked/rules=16 | 2.44 µs | 1.76 KiB | 34 |
| internal/eval | EvalPolicy/ranked/rules=32 | 4.54 µs | 2.76 KiB | 50 |
| internal/eval | EvalPolicy/ranked/rules=64 | 8.9 µs | 4.76 KiB | 82 |
| internal/eval | EvalPolicy/ranked/rules=128 | 17.8 µs | 8.26 KiB | 146 |
| internal/eval | EvalPolicy/ranked/rules=256 | 36.2 µs | 16 KiB | 274 |
| internal/eval | EvalPolicy/ranked/rules=512 | 70.8 µs | 31.4 KiB | 530 |
| internal/eval | EvalPolicy/rules=1 | 478 ns | 952 B | 18 |
| internal/eval | EvalPolicy/rules=8 | 3.02 µs | 4.27 KiB | 64 |
| internal/eval | EvalPolicy/rules=16 | 5.83 µs | 8.09 KiB | 113 |
| internal/eval | EvalPolicy/rules=32 | 11.7 µs | 15.8 KiB | 210 |
| internal/eval | EvalPolicy/rules=64 | 24 µs | 31.3 KiB | 403 |
| internal/eval | EvalPolicy/rules=128 | 51.1 µs | 62.1 KiB | 788 |
| internal/eval | EvalPolicy/rules=256 | 119 µs | 124 KiB | 1.56k |
| internal/eval | EvalPolicy/rules=512 | 269 µs | 249 KiB | 3.09k |
| internal/format | Format/rules=1 | 4.97 µs | 6.02 KiB | 176 |
| internal/format | Format/rules=8 | 29 µs | 36.5 KiB | 986 |
| internal/format | Format/rules=16 | 56.3 µs | 69.5 KiB | 1.9k |
| internal/format | Format/rules=32 | 110 µs | 140 KiB | 3.73k |
| internal/format | Format/rules=64 | 216 µs | 283 KiB | 7.38k |
| internal/format | Format/rules=128 | 429 µs | 558 KiB | 14.7k |
| internal/format | Format/rules=256 | 855 µs | 1.08 MiB | 29.3k |
| internal/format | Format/rules=512 | 1.79 ms | 2.24 MiB | 59k |
| internal/gokind | DecodeInput | 679 ns | 800 B | 22 |
| internal/gokind | GoKindBuild | 3.44 µs | 6.15 KiB | 105 |
| internal/gokind | Synthesize | 1.11 µs | 2.57 KiB | 47 |
| internal/kind | KindSource | 664 ns | 1.7 KiB | 30 |
| internal/lexer | Lexer/rules=1 | 1.14 µs | 192 B | 34 |
| internal/lexer | Lexer/rules=8 | 7.04 µs | 1.17 KiB | 216 |
| internal/lexer | Lexer/rules=16 | 13.9 µs | 2.3 KiB | 424 |
| internal/lexer | Lexer/rules=32 | 27.5 µs | 4.55 KiB | 840 |
| internal/lexer | Lexer/rules=64 | 54.8 µs | 9.05 KiB | 1.67k |
| internal/lexer | Lexer/rules=128 | 110 µs | 18 KiB | 3.34k |
| internal/lexer | Lexer/rules=256 | 219 µs | 36 KiB | 6.66k |
| internal/lexer | Lexer/rules=512 | 438 µs | 72 KiB | 13.3k |
| internal/lint | Lint | 7.46 µs | 176 B | 6 |
| internal/parser | ParseExpression | 1.26 µs | 2.05 KiB | 49 |
| internal/parser | ParseFile/rules=1 | 2.8 µs | 3.95 KiB | 106 |
| internal/parser | ParseFile/rules=8 | 16.2 µs | 21.8 KiB | 606 |
| internal/parser | ParseFile/rules=16 | 31.6 µs | 42.2 KiB | 1.18k |
| internal/parser | ParseFile/rules=32 | 61.9 µs | 83.2 KiB | 2.31k |
| internal/parser | ParseFile/rules=64 | 122 µs | 163 KiB | 4.58k |
| internal/parser | ParseFile/rules=128 | 245 µs | 325 KiB | 9.13k |
| internal/parser | ParseFile/rules=256 | 487 µs | 649 KiB | 18.2k |
| internal/parser | ParseFile/rules=512 | 1 ms | 1.27 MiB | 36.9k |
| internal/result | Result/assertion | 172 ns | 848 B | 10 |
| internal/result | Result/success | 1.06 µs | 2.94 KiB | 39 |
| internal/testsuite | TestSuiteParse | 240 µs | 245 KiB | 4.7k |
| pkg/policy | CandidateLocation | 98.2 ns | 144 B | 5 |
| pkg/policy | PolicyCompile/rules=1 | 6.91 µs | 14.2 KiB | 227 |
| pkg/policy | PolicyCompile/rules=8 | 37.9 µs | 68.8 KiB | 1.07k |
| pkg/policy | PolicyCompile/rules=16 | 74.9 µs | 133 KiB | 2.04k |
| pkg/policy | PolicyCompile/rules=32 | 145 µs | 263 KiB | 3.95k |
| pkg/policy | PolicyCompile/rules=64 | 291 µs | 521 KiB | 7.77k |
| pkg/policy | PolicyCompile/rules=128 | 583 µs | 1.01 MiB | 15.4k |
| pkg/policy | PolicyCompile/rules=256 | 1.29 ms | 2.03 MiB | 30.7k |
| pkg/policy | PolicyCompile/rules=512 | 2.71 ms | 4.05 MiB | 61.7k |
| pkg/policy | PolicyEval/assertion | 354 ns | 1.55 KiB | 18 |
| pkg/policy | PolicyEval/collect | 1.32 µs | 3.95 KiB | 47 |
| pkg/policy | PolicyEval/conflict | 1.43 µs | 4.98 KiB | 56 |
| pkg/policy | PolicyEval/fallback | 282 ns | 1.02 KiB | 15 |
| pkg/policy | PolicyEval/parallel | 1.08 µs | 3.95 KiB | 47 |
| pkg/policy | PolicyEval/ranked | 769 ns | 1.91 KiB | 29 |
| pkg/policy | PolicyEvalRules/all-matching/rules=64 | 34.5 µs | 76.7 KiB | 602 |
| pkg/policy | PolicyEvalRules/one-matching/rules=64 | 9.2 µs | 5.73 KiB | 91 |
| pkg/policy | PolicyEvalRules/ranked/rules=64 | 9.15 µs | 5.73 KiB | 92 |

:::

## In a service

Engine benchmarks leave out everything around an evaluation. The example service measures that too: deploygate answering HTTP requests with every decision logged, traced, counted in metrics and continuously profiled. On 29 September 2026, the Apple M5 Pro ran the example's Docker Compose stack in OrbStack, whose VM has 18 CPUs and 15.7 GiB of memory, and k6 sent a constant 1,000 iterations per second for two minutes with 20 preallocated and at most 100 virtual users:

| At 1,000 requests per second | |
| --- | ---: |
| p95 latency | 0.98 ms |
| p99 latency | 2.65 ms |
| Allocated per request | 46 KiB |
| CPU per request | 0.28 ms |
| Resident memory | 46 MiB |

A decision request evaluates two policies, access then deploy. Those evaluations are a small part of it: under 10 KiB of the 46 KiB it allocates, and a few microseconds of its 0.28 ms of CPU. HTTP handling, JSON and the telemetry make up the rest. k6 rotated through its eight request cases; no request failed and no iteration was dropped. Latencies are k6's. Allocation and CPU per request are the process counters over a fixed 90-second window, divided by the decision requests (POSTs) in it, and resident memory is that window's mean.

## Through WebAssembly

The [WebAssembly module](/reference/wasm/) runs the same engine, with JSON in and out and the WebAssembly runtime in between. One evaluation of the example service's `access.main` with the `admin-platform` input, the result and trace included, on an Apple M5 Pro:

| How | Time per evaluation |
| --- | ---: |
| The evaluator alone, natively, on an input decoded once | 1.17 µs |
| The engine natively, JSON request in and JSON record out | 5.33 µs |
| The module in Bun 1.4.2, through `@spechtlabs/sigil` | 26.7 µs |
| The module in Node 24.18.0, through `@spechtlabs/sigil` | 35.7 µs |
| The module in wazero 1.12.0, from Go | 117 µs |

A request that does almost nothing, formatting an empty source, costs 0.61 µs natively and 14.9 µs through wazero: that's the ABI's round trip. Loading the module in Bun, compiling its 10.9 MB and starting the Go runtime, took 33 ms, and compiling `checkout.alerts` with a required policy 12 to 17 ms.

These were measured on 30 September 2026, on the machine described [below](#how-these-numbers-were-measured). The native and wazero rows are `mise run wasm-bench`: Go 1.27.1, five samples each at the default benchmark time and `GOMAXPROCS`, medians. The Bun and Node rows evaluated the same compiled policy in a loop: 5,000 evaluations to warm up, then ten samples of 20,000, medians. Why the module costs what it does: [One engine for every host](/understanding/one-engine/#what-json-and-webassembly-cost).

## How these numbers were measured

Every number on this page but those [in a service](#in-a-service) and [through WebAssembly](#through-webassembly) was measured on 6 October 2026 on an Apple M5 Pro with 18 CPU cores and 64 GB of memory, running macOS 26.6.2 and Go 1.27.1 for darwin/arm64:

- Each benchmark ran ten samples of 200 ms each with `GOMAXPROCS=2`, and the tables and charts show the median sample. The parallel cases use `b.RunParallel` with two goroutines.
- Compilation happens before the timer starts in every evaluation benchmark. Evaluation benchmarks include building the public result and its trace, because a host always gets them.
- The synthetic policies come from `internal/benchtest`. `rules=N` repeats one rule shape N times, at 1, 8, 16, 32, 64, 128, 256 and 512 rules, and every copy matches, except in the `one-matching` and `ranked` cases, where only the last one does.
- Time per operation depends on the machine. Allocated bytes and allocation counts change little between machines with the same Go version and architecture, so they're the numbers to compare across machines. Allocated bytes aren't retained heap.
- For scale, an Apple M1 Max, a 2021 chip with 10 CPU cores and 64 GB running macOS 26.2, ran the benchmarks on 29 September 2026 with the same settings. The 14 benchmarks compared took 1.4 to 1.7 times as long there as on the M5 Pro that day, 1.6 times on average, with the same allocation counts.

To run the same benchmarks and the load test yourself, see [Measure performance](/project/contributing/#measure-performance).
