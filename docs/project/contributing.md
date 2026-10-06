---
title: "Contributing: test, fuzz and benchmark Sigil"
icon: mdi:test-tube
createTime: 2026/09/28 20:00:00
permalink: /project/contributing/
---

This page is for contributors changing Sigil itself: the lexer, parser, checker, evaluator, CLI and Go API. It covers the repository's test suite, its benchmarks, its fuzz targets and the CI workflows that run them.

::: tip Testing your own policies
To test policies you wrote, you don't need any of this. Write `*_test.yaml` cases and run them with [`sigil test`](/reference/cli/#sigil-test), or from `go test` in the host with [`policytest.Run`](/reference/go-api/#package-policytest). [Test your policies](/guides/test-policies/) shows both, and [Check, evaluate and test](/getting-started/check-and-test/) walks through a test file.
:::

Run the commands below from the repository root. The tools come from `.mise.toml`, so `mise install` sets up the Go toolchain that `go.mod` pins.

## Run the tests

```sh
mise run test
mise run check
```

The first command runs unit tests, golden tests, fuzz seeds and checked-in regression inputs with the race detector and coverage. The second also runs mutation fuzz smoke tests, benchmark smoke tests and the repository's lint and configuration checks. Plain `go test ./...` runs the same test cases without the race detector. It doesn't generate new fuzz inputs or measure benchmarks.

`examples/deploy-gates/` is a separate Go module with its own `.mise.toml`. Run `mise run test` or `mise run check` from that directory to test the example service and its policies.

## The devtool CLI

The benchmark and fuzz tasks run `devtool`, the repository's own tooling CLI in `cmd/devtool`. It isn't released. Arguments after `--` reach it, as in `mise run bench -- --baseline main`, and `mise run devtool -- --help` lists every command.

`devtool bench` and `devtool fuzz` work the same way. Each has a `run` and a `list` command that take package patterns (`./...` by default), and both `run` commands share their flags:

| Flag | `bench run` | `fuzz run` |
| --- | --- | --- |
| `-f`, `--filter` | Benchmarks to run, by name | Fuzz targets to run, by name |
| `--time` | Time or iterations per sample (200ms) | Time or iterations per target (10s) |
| `--cpu` | CPUs for every sample, as GOMAXPROCS (2) | CPUs for every target, as fuzzing workers (2) |
| `--timeout` | Timeout per `go test` process (3m) | Timeout per `go test` process (30m) |
| `-v`, `--verbose` | Print go test's output instead of a status line | The same |
| `--results` | Where results go (`benchmark-results/`) | Where results go (`fuzz-results/`) |

`bench run` adds `-b`, `--baseline` to compare with a revision, `--no-baseline` to measure only the checkout even when `BENCH_BASELINE` is set, `--count` for the samples per revision (10) and `--benchstat` for the benchstat binary. `fuzz run` adds `--no-restore` and `--remote`, described under [Share the corpus](#share-the-corpus), and `fuzz corpus pull` and `fuzz corpus push` move the corpus between the Go cache and the `fuzz-corpus` branch. `fuzz summary` and `fuzz report` are for CI only: the extended fuzzing workflow merges its jobs' results into one summary with the first, and opens or updates the failure issue with the second. The `list` commands share `--filter`, `--packages` and `-o text|json|yaml`. Every flag can also be set with an environment variable, `BENCH_` or `FUZZ_` followed by its name in capitals: `BENCH_BASELINE=main`, `FUZZ_TIME=1m`.

Both `run` commands look alike too. A box shows what the run is about to do, then each step (a round of samples, or a fuzz target) gets a numbered status line on a terminal and a ✓ line once it's done. The run ends with a verdict line that says where the results are. Each results directory holds go test's raw output, a `summary.md` that CI adds to the job page and a `metadata.json` with the commit and Go version. `fuzz run` also writes `results.json`, how each target went, which `fuzz summary` merges across a campaign's jobs. A fuzzing summary leads with how many targets passed, then lists each failure with the command that replays it, then every target's execs, new inputs and result, failures first. Ctrl-C stops a run cleanly.

## Measure performance

Benchmarks use Go's `testing.B` framework. [Performance](/reference/performance/) lists what they measure on one machine. Run them from the repository root:

```sh
# Run every workload once to check that it works; no performance gate.
mise run bench-smoke

# Print every workload's median time, bytes and allocations over ten samples.
mise run bench

# Did an uncommitted change regress the evaluator? Only builds what it measures.
mise run bench -- --baseline HEAD ./internal/eval

# Compare the branch with the commit it forked from main.
mise run bench -- --baseline "$(git merge-base HEAD main)"

# Compare only selected workloads.
mise run bench -- --baseline main --filter 'PolicyEval|Lexer'

# Measure a single layer directly with Go.
go test ./internal/eval -run '^$' -bench . -benchmem -count 10 -cpu 2
```

The runner records `ns/op` (elapsed time per operation), `B/op` (allocated bytes) and `allocs/op` (allocation count). Allocated bytes are not retained heap size. Use application load tests and profiles to investigate service throughput, live memory and contention under load.

| Layer | Workloads |
| --- | --- |
| Lexer, parser and AST | Tokenization and file parsing from 1 to 512 rules, expression parsing, AST printing |
| Checker and kinds | Policy checking from 1 to 512 rules, contract loading and source generation |
| Go bindings | Kind construction, synthesized bindings and input decoding |
| Constants and evaluator | Constant evaluation, expression compilation, policy evaluation from 1 to 512 rules with every rule matching, only one, or only one under a precedence, and composed policies |
| Bundles, lints and formatter | Bundle compilation and formatting from 1 to 512 rules, linting |
| Results and public API | Result conversion, compilation from 1 to 512 rules, ranked and collecting evaluation, a 64-rule policy with every rule or only one matching, conflicts, assertions, fallback and concurrent evaluation |
| Tooling | Diagnostic rendering, CLI configuration and YAML test-suite parsing |

Compilation and evaluation are measured separately. Evaluation benchmarks prepare policies and inputs before the timer starts. Serial benchmarks use `b.Loop()`; the concurrent public API workload uses `b.RunParallel()` against a shared compiled policy. The default runner uses two Go execution threads (`GOMAXPROCS=2` and `-cpu=2`). This does not reserve two physical cores. A full `mise run bench` takes a few minutes, prints each workload's medians and writes the raw samples to `benchmark-results/`.

### Measure the example service

The example service's policies have a benchmark of their own, with the same settings. Run it from `examples/deploy-gates/`:

```sh
go test ./internal/store -run '^$' -bench BenchmarkPolicies -benchmem -count 10 -cpu 2 -benchtime 200ms
```

For the service measurement in [Performance](/reference/performance/), start the example's stack as [The example service](/guides/example-service/#run-it) shows, then run the load test from `examples/deploy-gates/` at the same rate:

```sh
RATE=1000 DURATION=2m mise run loadtest
```

k6 writes its report under `examples/deploy-gates/results/`, and the deploygate dashboard in Grafana shows the run's throughput and latency next to the service's profiles.

### CI regression gate

The **Benchmark regressions** job, in the Fuzz and benchmarks workflow that CI runs on every pull request, compares a PR's tested merge result with the PR base commit. Manual runs of that workflow compare the checked-out commit with its parent. Both revisions run on the same runner and Go version, with ten samples of at least 200 ms per workload. All binaries compile before measurement; the order alternates between base-first and head-first samples.

CI copies the current `*_bench_test.go` files and `internal/benchtest` fixtures onto the base snapshot, so both revisions execute the same workloads. A benchmark the base doesn't declare is new and can't exist there: the comparison leaves it out of the base, runs it on the head only and lists it as not compared, so it can't fail the gate. Its helpers belong in an ordinary `_test.go` file, which isn't copied. When the current fixtures don't build or run on the base, as after a change to the Sigil syntax they're written in, that package's base runs its own `internal/benchtest` instead, and the output says so; each revision then measures its own version of the workloads. Setup that depends on an API a change touches, such as building a kind with its options, belongs in a `*_benchsetup_test.go` file next to the benchmarks, like `pkg/policy/policy_benchsetup_test.go`. The comparison leaves those files with their revision: the base keeps its own copy and takes the checkout's only when it has none, so an API change edits only the setup file and both sides still run the same workloads. Otherwise adapt the benchmark to a shared API, or choose a compatible baseline for a local comparison.

The job fails when any measured workload increases by **more than 10%** in time, allocated bytes or allocation count, and [benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat) reports a statistically significant change with `-alpha 0.01`. An increase from zero to nonzero also qualifies. A benchmark only one revision measured is listed as not compared. Missing measurements on the head, incomplete samples, workloads the base can't run with either fixtures, and tool or build failures fail the check. The build job depends on this result.

The job summary includes the comparison. Its `benchmarks` artifact retains raw samples, benchstat text and CSV, and revision/toolchain metadata. Local runs write these files to the ignored `benchmark-results/` directory; each run replaces its previous results. `BENCH_COUNT` changes the sample count, with at least ten required for comparisons; `BENCH_TIME` changes the measurement duration. The equivalent flags are `--count` and `--time`, and `--results` picks another directory. In a terminal, the comparison lists only the significant changes, regressions first; `benchstat.txt` holds the full report.

Hosted runners and busy developer machines can still produce misleading timing changes. Review the raw samples and rerun a surprising failure on an idle machine before attributing it to code. Allocation changes are usually easier to reproduce. Benchmark smoke tests only check workload correctness and do not establish a performance baseline.

### Add a workload

Put benchmarks in `*_bench_test.go` files so the comparison runner can copy them. Keep shared fixtures in `internal/benchtest`; avoid relying on helpers in ordinary unit-test files or mutable external services. The runner discovers benchmark packages within the root Go module and excludes nested Go modules.

Use deterministic inputs, report allocations and keep setup outside the timed loop unless setup is what the benchmark measures. Check that the operation succeeds and exercises the intended path. Include realistic sizes and a larger case when scaling matters. Review workload changes alongside results: identical inputs across revisions make a comparison useful, but the chosen workload still determines what regressions it can catch.

## Run mutation fuzzing

```sh
# Every target, ten seconds each, two workers.
mise run fuzz

# Every target, one minute each.
FUZZ_TIME=1m mise run fuzz

# Just one package, or one target.
mise run fuzz -- --time 1m ./internal/parser
mise run fuzz -- --time 1m --filter FuzzParseExpr ./internal/parser

# List exactly what the runner discovers.
mise run devtool -- fuzz list
```

The runner discovers `Fuzz...` functions in Go test files, runs each separately and stops on the first failure. When a target finds a failing input, the error names the saved file and the command that replays it. `--time` (`FUZZ_TIME`) applies to each target. `--cpu` (`FUZZ_CPU`) defaults to two workers; lower it on a busy machine. `--timeout` (`FUZZ_TIMEOUT`) is the timeout for each `go test` process and defaults to 30 minutes. Increase it for longer targets.

For an hour per target:

```sh
FUZZ_TIME=60m FUZZ_TIMEOUT=90m mise run fuzz
```

### Share the corpus

Fuzzing keeps the inputs that reached new code, the corpus, and starts the next run from them, so a target explores further the longer it has been fuzzed in total. `go test` keeps them in its cache, under `$(go env GOCACHE)/fuzz`, where they stay on one machine. The `fuzz-corpus` branch holds them for everyone: one file per input, under the package's directory and the target's name, such as `internal/eval/FuzzEvalExpr/<hash>`. It shares no history with the code.

`fuzz run` restores the corpus before it fuzzes. It fetches the branch from `origin` and copies the selected targets' inputs into the cache, and the box at the start of the run says how many were new. `--remote` (`FUZZ_REMOTE`) fetches from another remote, such as `upstream` in a fork, and `--no-restore` (`FUZZ_NO_RESTORE`) skips the step. A remote without the branch, or a fetch that fails, doesn't stop the run: it starts from the seeds and the cache instead, with a warning when the fetch failed.

After a long local campaign, push what it found:

```sh
mise run fuzz-push

# Or only some packages, the same way fuzz run selects them.
mise run devtool -- fuzz corpus push ./internal/parser
```

`fuzz corpus push` commits the inputs the branch doesn't have yet as one commit and pushes it; with nothing new it pushes nothing. `fuzz corpus pull` restores without fuzzing. Neither touches the working tree, the index or the checked-out branch: they work on git's object database directly. Inputs are named by their content's hash and only ever added, so pushes from several machines and CI never conflict. A push that another one beats is rebuilt on top of it and retried.

The Fuzz and benchmarks workflow, which CI runs on every pull request, runs short smoke tests: five seconds per target, two workers and a one-minute timeout per test process. The fuzz step has a five-minute limit; its whole job, including tool setup, has a ten-minute limit. With the current 33 targets, mutation time totals under three minutes, plus compilation and seed replay. Local `mise run fuzz` and `mise run check` retain the ten-second default.

The Extended fuzzing workflow runs on `main` every Monday at 02:17 UTC, with an hour per target. Manual runs must also select `main` and can choose one, ten or sixty minutes per target. It discovers the targets automatically and fuzzes each in a job of its own, so a package with several targets takes no longer than one with a single target; jobs beyond the account's limit on concurrent jobs wait for a free runner. Each job starts from the `fuzz-corpus` branch and uploads logs and regression inputs. Afterwards a separate job, the only one allowed to push, collects every job's corpus and pushes the new inputs to the branch. The smoke tests on pull requests restore the corpus too, so they replay every input earlier campaigns found. Elapsed wall time and aggregate target time are different measurements.

A summary job publishes one summary of every target on the run's page, so the per-target jobs don't each show their own. A failed extended campaign opens a GitHub issue that names the failed targets in its title and says what went wrong: each failing target with what go test reported, the command that replays it and links to its job log and to the artifact with the failing input, then every job that failed without leaving results, such as one whose runner died, with what GitHub's annotations say about why. Further failures add comments to the existing open issue; after it is closed, a later failure opens a new issue. Discovery and setup failures and job timeouts are reported too. Successful or cancelled campaigns do not create reports unless a job failed. Only the reporting job has permission to write issues; PR smoke tests retain their failures as artifacts.

## What the properties check

| Layer | Properties |
| --- | --- |
| Lexer and literals | Progress to stable EOF, source spans, accepted literal decoding, arbitrary byte string round trips |
| Parser and AST | Deterministic trees and diagnostics, partial-tree rendering, expression structure after printing and reparsing |
| Formatter | Parseable output, idempotence and comment preservation apart from trailing whitespace |
| Checker and kinds | Deterministic types and errors; export/import equality for parsed and generated contracts |
| Go bindings | Reflection/export/import, synthesized field access, strict JSON/YAML decoding without input mutation |
| Constants | Integer arithmetic checked against `math/big`; representable scalar and collection literal round trips |
| Evaluator | Checked expressions compile and evaluate repeatably; whenever constant folding accepts an expression, evaluation gives the same int, float, duration, list, map or enum value; list operators and nested quantifiers agree with set membership |
| Bundles and lints | Repeatable compilation, diagnostics and lints; source and filesystem loading agree |
| Public policy API | Repeatability, formatting preserves outcomes, rule order preserves winners, required policies remain unconditional and trusted, duplicate candidates fold and exclusive outcomes conflict |
| Go builder | Printed expressions parse back to the same tree, and every pair of parentheses the printer wrote is needed, apart from those `sigil fmt` adds itself |
| WebAssembly engine | Any JSON request gets a JSON answer with a boolean `ok` and no recovered panic, and two fresh engines answer byte for byte the same; any evaluation input, with any host-function answer, gets one decision and the same answer twice |
| Stubs | `Validate` and `Bind` report the same errors, and an entry called with its own arguments always matches |
| `sigil compile` | `Patch` and `Verify` never panic on any bytes; a refused patch leaves the binary unchanged, and a code signature valid before a patch stays valid after it |
| Tooling | Diagnostic bounds and determinism, configuration parsing, YAML test validation and test-input loading |

Arbitrary source targets return normally on expected parse, type or runtime errors. They do not recover unexpected panics. Generated targets build valid contracts, rules or collections on every mutation so deeper layers get exercised even when random text would fail parsing. Generated type nesting, collection lengths and duplicate counts are bounded to keep individual cases practical. These generator bounds are not production resource limits.

## Keep a regression

Go writes a minimized failure to `<package>/testdata/fuzz/<target>/<hash>` and prints a reproduction command. For example:

```sh
go test ./internal/parser -run 'FuzzParseExpr/<hash>'
```

Keep a real failure in that directory with its fix. Ordinary `go test` then replays it. Add an explanatory unit test when the behavior needs a clearer specification. Interesting non-failing inputs live under `$(go env GOCACHE)/fuzz` and on the `fuzz-corpus` branch; they are where the search starts, not checked-in regressions. CI artifacts retain failures from hosted runs so they can be reproduced locally.

## Record a campaign

Record the commit, `go version`, target list, duration and worker count, logs, and any fixes or retained failures. `fuzz run` writes most of that to `metadata.json` and `results.json` in its results directory, and the next run replaces them, so copy them somewhere first. The corpus branch only keeps inputs: `fuzz corpus push` names the machine and the commit checked out when you push, not when the run started.

The [roadmap](/project/roadmap/)'s hardening milestone asked for a day of fuzzing without failures. Four hour-per-target campaigns on October 4 and 5, two on a local machine and two from the Extended fuzzing workflow, ran about 132 target-hours; the problems they found are fixed, and the last campaign passed every target.
