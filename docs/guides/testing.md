---
title: Testing and fuzzing
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

The first command runs unit tests, golden tests, fuzz seeds and checked-in regression inputs with the race detector and coverage. The second also runs mutation fuzz smoke tests and the repository's lint and configuration checks. Plain `go test ./...` runs the same test cases without the race detector. It does not generate new fuzz inputs.

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
