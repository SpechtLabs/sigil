# Performance investigation, 28 September 2026

This report records measurements made with the deploygate application on the `feat/examples-access` branch. The library optimizations, Go benchmark suite and CI regression gate now live in the `perf/evaluation-benchmarks` worktree, based on `main` at `c4695b6`. The example application is maintained separately.

The local deploygate profiles pointed to avoidable allocation in Sigil frames, result conversion and location formatting. The changes reduce allocation in all four policy benchmark cases. Whole-service allocation fell 3.1%. An HTTP latency or whole-process CPU improvement was not established.

## Changes

- Give each compiled instance a frame slot. Evaluations allocate a pointer table instead of a map and still create each frame only when reached. The table uses one pointer per compiled instance, including skipped instances.
- Preallocate result, candidate and failure slices from the sizes already known, preserving nil empty outcomes and traces.
- Use typed stable sorting for candidates and assertions, preserving precedence and source order.
- Format locations directly into a string builder instead of copying the call chain through temporary slices.

No shared evaluation state, pools, sampling changes or telemetry reductions were introduced.

## Policy benchmarks

Medians of six samples per case, 500 ms per sample, Go 1.27.1 on darwin/arm64 with 18 logical CPUs. Compilation is outside the timed section. The benchmark uses the embedded AccessGrant and DeployApproval policies and includes public results and traces. Baseline library sources are from `4528fe1`, supplied through a Go source overlay so both revisions run the same benchmark.

| Serial case | Before, ns/op | After, ns/op | Before, B/op | After, B/op | Allocations before → after |
| --- | ---: | ---: | ---: | ---: | ---: |
| access-member | 2073 | 1771 | 4832 | 4040 | 58 → 51 |
| access-conflict | 2161 | 1882 | 5632 | 4760 | 66 → 59 |
| deploy-owner | 2123.5 | 1968 | 4131 | 3995 | 58 → 56 |
| access-assert | 469.6 | 415.3 | 1744 | 1576 | 19 → 17 |

Serial median evaluation times fell 7-15%; bytes per evaluation fell 3-16%. Parallel timings were less consistent: access-member, access-conflict and deploy-owner improved, while access-assert was 5.8% slower. All parallel cases allocated fewer bytes. The raw samples are retained rather than treating one machine's timings as a general speed guarantee.

The example-only benchmark is preserved as [a reproducible patch](testdata/performance/examples-benchmark.patch). Apply it to a checkout of the examples branch before running this command. The root benchmark suite runs independently with `mise run bench`.

```bash
cd examples
go test ./internal/store -run '^$' -bench BenchmarkPolicies -benchmem -count=6 -benchtime=500ms
```

## Local HTTP load and telemetry

Both compared runs used the existing eight-case k6 workload at 1,000 iterations/second for two minutes, with 20 preallocated VUs and a maximum of 100. Docker reported 18 CPUs and 16,817,168,384 bytes of memory on aarch64. Tracing, logging, metrics and every configured Go profile stayed enabled. Both runs passed all outcome checks with zero failed requests and zero dropped iterations.

| Measurement | Before | After |
| --- | ---: | ---: |
| k6 HTTP p95, ms | 0.858 | 0.952 |
| k6 HTTP p99, ms | 2.199 | 2.319 |
| Process allocated bytes / POST | 46417 | 44973 |
| Process CPU, ms / POST | 0.253 | 0.260 |
| Mean resident memory, MiB | 44.69 | 47.84 |

The process figures use Mimir counter differences over fixed 90-second windows, divided by the POST count. Before: 16:21:00 to 16:22:30 UTC, 89,999 POSTs. After: 16:24:15 to 16:25:45 UTC, 90,004 POSTs. Resident memory is the mean of the scraped gauge, not an allocation count. The process was rebuilt and restarted between runs; caches, GC timing and the shared local host can affect CPU, RSS and latency. CPU per POST rose 2.8% in this comparison. Lower allocation is the demonstrated service-level improvement.

Pyroscope allocation profiles corroborate the changed paths over those windows. Cumulative allocation under public result conversion fell from 112.03 to 75.52 MiB, internal result construction from 96.02 to 80.02 MiB, and candidate location formatting from 64.50 to 38.50 MiB. These are sampled cumulative allocations, not retained heap, and nested totals must not be added together. CPU profile totals were not used to claim a speedup because their recorded samples do not match process CPU counter coverage.

The baseline mutex profile contained about 0.96 seconds of aggregate wait over 90 seconds, mostly the logger and Go runtime. The evaluator has no shared mutex; the store reads snapshots atomically. Initial block profiles were dominated by idle background channel/select waits. Those waits do not establish request lock contention. Synchronous logging and trace export remain substantial service costs.

Tempo confirmed that requests carry separate access and deploy spans with policy outcomes and candidate events. Trace samples were used to check execution and telemetry, not as a latency distribution or a matched before/after comparison.

```bash
cd examples
RATE=1000 DURATION=2m RUN_ID=perf-after-1000 bash test/load/run.sh
```

## Validation and evidence

- Root `go test -race ./...` passed, including the new concurrent frame-isolation test for shared imports, repeated invocations, skipped imports, failed assertions and retained results.
- Root and examples custom golangci-lint checks passed. E2E-tagged vet passed.
- `FuzzComposition`, `FuzzPolicyOrder` and `FuzzCollect` each passed 10 seconds of fuzzing with four workers.
- The examples race run passed its unit tests and 115 of 117 integration specs. Both failing specs also fail against the original library: "an outsider with a short soak is still just not eligible". `not_eligible` and `soak_too_short` have equal rank, so the policy returns a conflict where the fixture expects a denial. Policy semantics were left unchanged.
- The deployed E2E suite passed with `-ginkgo.skip 'an outsider with a short soak'`, excluding that known fixture case. It checked HTTP decisions, reloads and the observability backends.

At the end of the investigation, the optimized service was running in the local Compose stack and the original image was retained as `deploygate:perf-before-20260928`. These are local experiment artifacts.

Local evidence is saved in [testdata/performance/results/20260928](testdata/performance/results/20260928/): raw benchmarks, k6 summaries, fixed-window PromQL queries and results, exported Pyroscope profiles, image IDs, test logs and baseline source overlays. This directory is ignored by Git. The code, report and evidence are now in `.claude/worktrees/performance`. The original experiment metadata retains its source revisions and paths. The later ten-sample benchmark comparison is preserved in the ignored `benchmark-results/` directory.

[Baseline Grafana window](http://localhost:3000/d/deploygate?from=1790612460000&to=1790612550000) · [Optimized Grafana window](http://localhost:3000/d/deploygate?from=1790612655000&to=1790612745000)
