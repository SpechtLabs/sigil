---
title: Testing, fuzzing and benchmarks
icon: mdi:test-tube
createTime: 2026/09/28 20:00:00
permalink: /guides/testing/
---

Use these commands from the repository root with the Go toolchain pinned in `go.mod`. They test Sigil itself. To test a policy repository, use [`sigil test`](/reference/cli/#sigil-test) or [`policytest.Run`](/reference/go-api/).

## Run the tests

```sh
mise run test
mise run check
```

The first command runs unit tests, golden tests, fuzz seeds and checked-in regression inputs with the race detector and coverage. The second also runs mutation fuzz smoke tests, benchmark smoke tests and the repository's lint and configuration checks. Plain `go test ./...` runs the same test cases without the race detector. It does not generate new fuzz inputs or measure benchmarks.

## Measure performance

Benchmarks use Go's `testing.B` framework. Run them from the repository root:

```sh
# Run every workload once to check that it works; no performance gate.
mise run bench-smoke

# Measure the current checkout with ten samples per workload.
mise run bench

# Compare the current checkout with a commit or branch.
BENCH_BASE_REF=main mise run bench

# Measure a single layer directly with Go.
go test ./internal/eval -run '^$' -bench . -benchmem -count 10 -cpu 2

# Compare only selected workloads.
python3 scripts/benchmark.py --baseline main --bench 'PolicyEval|Lexer'
```

The runner records `ns/op` (elapsed time per operation), `B/op` (allocated bytes) and `allocs/op` (allocation count). Allocated bytes are not retained heap size. Use application load tests and profiles to investigate service throughput, live memory and contention under load.

| Layer | Workloads |
| --- | --- |
| Lexer, parser and AST | Tokenization and file parsing at 1 and 64 rules, expression parsing, AST printing |
| Checker and kinds | Policy checking at both sizes, contract loading and source generation |
| Go bindings | Kind construction, synthesized bindings and input decoding |
| Constants and evaluator | Constant evaluation, expression compilation, policy evaluation at both sizes and composed policies |
| Bundles, lints and formatter | Bundle compilation, linting and formatting |
| Results and public API | Result conversion, compilation, ranked and collecting evaluation, conflicts, assertions, fallback and concurrent evaluation |
| Tooling | Diagnostic rendering, CLI configuration and YAML test-suite parsing |

Compilation and evaluation are measured separately. Evaluation benchmarks prepare policies and inputs before the timer starts. Serial benchmarks use `b.Loop()`; the concurrent public API workload uses `b.RunParallel()` against a shared compiled policy. The default runner uses two Go execution threads (`GOMAXPROCS=2` and `-cpu=2`). This does not reserve two physical cores.

### CI regression gate

The **Benchmark regressions** job compares a PR's tested merge result with the PR base commit. Release and manual runs compare the checked-out commit with its parent. Both revisions run on the same runner and Go version, with ten samples of at least 200 ms per workload. All binaries compile before measurement; the order alternates between base-first and head-first samples.

CI copies the current `*_bench_test.go` files and `internal/benchtest` fixtures onto the base snapshot. Both revisions therefore execute the same workloads, including newly added benchmarks. If those workloads cannot compile against the base, the job fails: adapt the benchmark to a shared API or choose a compatible baseline for a local comparison.

The job fails when any measured workload increases by **more than 10%** in time, allocated bytes or allocation count, and [benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat) reports a statistically significant change with `-alpha 0.01`. An increase from zero to nonzero also qualifies. Missing measurements, mismatched workloads, incomplete samples and tool or build failures fail the check too. The build job depends on this result.

The job summary includes the comparison. Its `benchmarks` artifact retains raw samples, benchstat text and CSV, and revision/toolchain metadata. Local runs write these files to the ignored `benchmark-results/` directory; each run replaces its previous results. `BENCH_COUNT` changes the sample count, with at least ten required for comparisons; `BENCH_TIME` changes the measurement duration. The equivalent flags are `--count`, `--benchtime` and `--output`.

Hosted runners and busy developer machines can still produce misleading timing changes. Review the raw samples and rerun a surprising failure on an idle machine before attributing it to code. Allocation changes are usually easier to reproduce. Benchmark smoke tests only check workload correctness and do not establish a performance baseline.

### Add a workload

Put benchmarks in `*_bench_test.go` files so the comparison runner can copy them. Keep shared fixtures in `internal/benchtest`; avoid relying on helpers in ordinary unit-test files or mutable external services. The runner discovers benchmark packages within the root Go module and excludes nested Go modules.

Use deterministic inputs, report allocations and keep setup outside the timed loop unless setup is what the benchmark measures. Check that the operation succeeds and exercises the intended path. Include realistic sizes and a larger case when scaling matters. Review workload changes alongside results: identical inputs across revisions make a comparison useful, but the chosen workload still determines what regressions it can catch.

## Run mutation fuzzing

```sh
# Every target, ten seconds each, two workers.
mise run fuzz

# Every target, one minute each.
FUZZTIME=1m mise run fuzz

# Just one package, or one target.
FUZZTIME=1m bash scripts/fuzz.sh ./internal/parser
go test ./internal/parser -run '^$' -fuzz '^FuzzParseExpr$' -fuzztime=1m -parallel=2

# List exactly what the runner discovers.
bash scripts/fuzz.sh --list
```

The runner discovers `Fuzz...` functions in Go test files, runs each separately and stops on the first failure. `FUZZTIME` applies to each target. `FUZZPARALLEL` defaults to two workers; lower it on a busy machine. `FUZZTIMEOUT` is the timeout for each `go test` process and defaults to 30 minutes. Increase it for longer targets.

For an hour per target:

```sh
FUZZTIME=60m FUZZTIMEOUT=90m mise run fuzz
```

The CI workflow runs short smoke tests on pull requests: five seconds per target, two workers and a one-minute timeout per test process. The fuzz step has a five-minute limit; its whole job, including tool setup, has a ten-minute limit. With the current 24 targets, mutation time totals about two minutes, plus compilation and seed replay. Local `mise run fuzz` and `mise run check` retain the ten-second default.

The Extended fuzzing workflow runs on `main` every Monday at 02:17 UTC, with an hour per target. Manual runs must also select `main` and can choose one, ten or sixty minutes per target. It discovers packages automatically, runs packages in parallel, caches interesting inputs and uploads logs and regression inputs. Elapsed wall time and aggregate target time are different measurements.

A failed extended campaign opens a GitHub issue with the tested commit, duration, job results, and links to the run logs and artifacts. Further failures add comments to the existing open issue; after it is closed, a later failure opens a new issue. Discovery and setup failures and job timeouts are reported too. Successful or cancelled campaigns do not create reports unless a job failed. Only the reporting job has permission to write issues; PR smoke tests retain their failures as artifacts.

## What the properties check

| Layer | Properties |
| --- | --- |
| Lexer and literals | Progress to stable EOF, source spans, accepted literal decoding, arbitrary byte string round trips |
| Parser and AST | Deterministic trees and diagnostics, partial-tree rendering, expression structure after printing and reparsing |
| Formatter | Parseable output, idempotence and comment preservation apart from trailing whitespace |
| Checker and kinds | Deterministic types and errors; export/import equality for parsed and generated contracts |
| Go bindings | Reflection/export/import, synthesized field access, strict JSON/YAML decoding without input mutation |
| Constants | Integer arithmetic checked against `math/big`; representable scalar and collection literal round trips |
| Evaluator | Checked expressions compile and evaluate repeatably; arithmetic agrees with constant folding; list operators and nested quantifiers agree with set membership |
| Bundles and lints | Repeatable compilation, diagnostics and lints; source and filesystem loading agree |
| Public policy API | Repeatability, formatting preserves outcomes, rule order preserves winners, required policies remain unconditional and trusted, duplicate candidates fold and exclusive outcomes conflict |
| Tooling | Diagnostic bounds and determinism, configuration parsing, YAML test validation and test-input loading |

Arbitrary source targets return normally on expected parse, type or runtime errors. They do not recover unexpected panics. Generated targets build valid contracts, rules or collections on every mutation so deeper layers get exercised even when random text would fail parsing. Generated type nesting, collection lengths and duplicate counts are bounded to keep individual cases practical. These generator bounds are not production resource limits.

## Keep a regression

Go writes a minimized failure to `<package>/testdata/fuzz/<target>/<hash>` and prints a reproduction command. For example:

```sh
go test ./internal/parser -run 'FuzzParseExpr/<hash>'
```

Keep a real failure in that directory with its fix. Ordinary `go test` then replays it. Add an explanatory unit test when the behavior needs a clearer specification. Interesting non-failing inputs live under `$(go env GOCACHE)/fuzz`; they are a search cache, not checked-in regressions. CI artifacts retain failures from hosted runs so they can be reproduced locally.

## Record a campaign

Record the commit, `go version`, target list, duration and worker count, logs, and any fixes or retained failures. M7 requires a continuous 24-hour campaign that exercises every target for at least an hour without failures. Parallel target-hours do not substitute for elapsed campaign time. With the current 24 targets, the sequential hour-per-target command above supplies that duration; rerun after fixing any failure. The scheduled workflow supplies additional parallel testing but cannot alone satisfy the elapsed-time criterion.

A short passing campaign is evidence for those runs only. M7's day-long criterion remains open until a complete campaign is recorded. Public dynamic `policy.LoadKind` and static cost analysis also remain on the [roadmap](/project/roadmap/).
