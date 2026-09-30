---
title: The example service
icon: mdi:rocket-launch-outline
createTime: 2026/09/28 12:00:00
permalink: /guides/example-service/
---

This guide is for Go developers who want to see Sigil embedded in a real service before they embed it in their own. It introduces deploygate, a complete Go service built on Sigil. It serves the `DeployApproval` policies from the [tour](/getting-started/tour/) over HTTP, and it wires up everything the rest of these docs describe one piece at a time: a kind defined in Go, an exported kind file, guardrails required from a trusted source, policies loaded from a directory with hot reload, typed matching, and metrics and traces for every decision. A second, collecting kind, `AccessGrant`, grants the roles each deploy is decided with, so no client names its own.

The code lives in [`examples/`](https://github.com/SpechtLabs/sigil/tree/main/examples) in the repository, as a Go module of its own. Its README is the full walkthrough; this page is the short version.

## What it shows

| Piece                                                                                                    | Where                                 | Read more                                                                                                                |
| -------------------------------------------------------------------------------------------------------- | ------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| The `DeployApproval` kind, declared with `policy.NewKind`                                                | `internal/deploy`                     | [Define the kind](/guides/embed-go/#define-the-kind)                                                                     |
| The `AccessGrant` kind, a `collect all` kind with an `exclusive` line                                    | `internal/access`                     | [Collecting kinds](/reference/kind-files/#collecting-kinds)                                                              |
| Both kind files, written by the host's own `sigilc export` and checked by a test                         | `cmd/sigilc`, `policies/*.sigil`      | [Export the kind](/guides/host-binary/#export-the-kind)                                                                  |
| Platform guardrails embedded in the binary and required with `policy.From`, one trusted source per kind  | `policies/embed.go`, `internal/store` | [Trusted sources](/reference/bundles/#trusted-sources)                                                                   |
| Team and access policies read from directories, reloaded with last-known-good semantics                  | `internal/store`                      | [Reload without an outage](/guides/configmaps/#reload-without-an-outage), [Policies in a ConfigMap](/guides/configmaps/) |
| Results matched into typed payloads, with `Match` for the deploy kind and `MatchAll` for the access kind | `internal/server`                     | [Typed matching](/reference/go-api/#typed-matching)                                                                      |
| A counter and a span per decision and per grant, with the reason and the trace                           | `internal/telemetry`                  |                                                                                                                          |

## Run it

You need [mise](https://mise.jdx.dev/) and Docker. From the repository root:

```bash
cd examples
mise install
mise run up
```

The tasks live in `examples/.mise.toml`. Run them from `examples/`, or use `mise -C examples run <task>` from the repository root.

That starts deploygate on port 8080 with Alloy, single-process Loki, Tempo and Mimir, plus Pyroscope. Grafana on [port 3000](http://localhost:3000/d/deploygate) has a provisioned dashboard with metrics, logs, traces, profiles and k6 load measurements. The command waits for the telemetry backends and for deploygate with both bundles loaded: its image has no shell, so compose probes it with `deploygate healthcheck`, which asks the server's own `/readyz`. The example includes `demo-cli`, which stands in for the platform tooling that supplies identity and release metadata. Ask for a deploy, from `examples/`:

```bash
mise run demo deploy owner --json
```

Omit `--json` for a readable summary, or use `--explain` to include the winning rules and their source locations. `mise run demo scenarios` lists the built-in deployment and access scenarios. The CLI also accepts `--file` for your own JSON and `--url` to select another deploygate instance.

The request describes the actor by their groups, `"groups": ["payments"]`, not by roles. deploygate evaluates the access policy first, which makes a member of the `payments` group a reader and a deployer, then the team's deploy policy with those roles. It answers `202 Accepted` with the decision, its reason, the payload, the trace and the roles it decided with. An excerpt:

```json
{
  "decision": "review",
  "reason": "service_owner",
  "payload": { "approvers": ["payments-leads", "security-leads"] },
  "trace": [
    {
      "decision": "review",
      "reason": "service_owner",
      "policy": "deploy.production",
      "location": "payments/production.sigil:10:3 → deploy/production.sigil:16:5",
      "winner": true
    }
  ],
  "access": {
    "policy": "access.main",
    "grants": [
      { "role": "reader", "reason": "team_member" },
      { "role": "deployer", "reason": "team_member", "ttl": "8h" }
    ]
  }
}
```

The README's [walkthrough](https://github.com/SpechtLabs/sigil/tree/main/examples#a-review) shows the full body, including the conditions each trace entry held under.

The status encodes the decision: `200` for approve, `202` for review and `403` for deny. A failed evaluation answers by whose fault it is. A failed input assert is the caller's, so it's a `422`. A conflict, a failed outcome assert or a runtime error means the policy failed on a valid request, so it's a `500`, which counts against deploygate's error budget instead of reading as a client mistake. deploygate reads which it was from the assert error's phase rather than the trace, which is just as empty when an outcome assert fails with nothing fired. An evaluation that runs past deploygate's evaluation timeout, one second by default, is the service failing to answer in time, so it's a `503`. Whichever it is, the body holds the kind's default decision and says what failed. A client that disconnects mid-evaluation stops it too, and gets `499` with no body: no one failed, so it stays out of both the client and the server error rates. The kinds recover host function panics with [`policy.WithRecoverHostPanics()`](/guides/handle-errors/#recover-host-panics), so a panic fails the evaluation closed with a `500` and a fallback, counted like any runtime error, instead of an empty `500` from gin's recovery that the request metrics never see. A request deploygate won't evaluate, such as one with an unknown field like `roles`, a negative soak, or a tier the `Tier` enum doesn't declare, gets `400` before any policy runs. A client can act on the status alone and read the body for the details.

## An authorization layer with collect all

Roles combine instead of competing: a payments engineer on the platform team is a reader, a deployer and a release manager at once. So `AccessGrant` declares `collect all`, and every role whose rule fires is part of the outcome, in declaration order. A collecting kind has no deny that outranks the rest, so the example guards the combinations that must never happen in the two places the docs describe:

- The host's kind declares `exclusive admin, release_manager`. An evaluation that grants both fails with a conflict, which the API answers with `500` and both candidates named.
- The platform's `access.guardrails`, which the host requires, asserts `[auditor, deployer] exclusive in outcome`, so a compliance member who is also in the team can't audit their own deploys. It's an outcome assert, so that's a `500` too.

The first rule belongs to the host and changes with a release; the second is platform policy that reloads like any other document. `POST /api/v1/access/grants` runs the access stage on its own:

```bash
mise run demo access member
```

```text
HTTP 200 OK
Team: payments
Environment: production
Access policy: access.main
Roles:
  reader: team_member
  deployer: team_member, expires in 8h
```

It answers `200` when at least one role is granted and `403` with the same body, and an empty `grants`, when none is. The README walks through the conflict and the failed assert with their full responses.

## Watch it reload

The compose stack mounts `examples/policies/teams` and `examples/policies/access` into the container, the way ConfigMaps would be mounted in a cluster. Edit a team policy or the access policy, then reload:

```bash
mise run demo policies reload
```

The next request sees the change. Break a file instead, and the reload answers `500` with the compiler's diagnostics while deploygate keeps serving the bundle it loaded last. deploygate also reloads on `SIGHUP`, and whenever a poll finds a directory's content changed: every 5 seconds in the compose stack, every 30 by default.

Each bundle reports its own reload health: `deploygate_policy_last_reload_successful{kind}` is `1` when that bundle's latest load attempt succeeded and `0` when it failed, and `deploygate_policy_last_reload_timestamp_seconds{kind}` holds the time of its last successful load. Alert on `deploygate_policy_last_reload_successful == 0` for five minutes. The `deploygate_policy_reloads_total{result="failure"}` counter can't do that job: a poll reports a broken bundle once rather than at every poll, so an alert on its rate resolves while the stale bundle keeps serving. The timestamp can't do it alone either, since a bundle only reloads when something changes, and an idle week and a week of rejected edits both leave it a week old.

Startup is the exception. With no bundle loaded yet there's nothing to fall back to, so a policy that doesn't compile stops deploygate with the diagnostics instead, and on Kubernetes the rollout stays on the old pods.

## Check policies with the host's binary

The stock `sigil` binary can't run the kind's `split` host function, so the example builds its own, `sigilc`, with the [`cli` package](/guides/host-binary/), and links both kinds in. With the kinds linked, no command needs `--kind`; each document uses the kind its header names. A policy repository's CI runs it the same way the service loads the policies. `mise run policies` runs the checks and the tests for both kinds. The team policies' check, from `examples/`, requires the guardrails from the same trusted directory the service embeds:

```bash
mise run sigilc check --config policies/sigil.yaml \
  --require deploy.guardrails --trusted policies/platform/deploy \
  --policy 'payments.*' --policy 'checkout.*' -R policies/teams
```

The README's [sigilc section](https://github.com/SpechtLabs/sigil/tree/main/examples#your-own-sigil-binary-sigilc) has the access policy's check, the test runs, `explain` output for both kinds and the `export --check` that catches a stale kind file.

## Tests

The example has table-driven unit tests, the policy tests run from `go test` with `policytest` for both kinds, a Ginkgo integration suite against the real stores and server in process, and a Ginkgo end-to-end suite against the compose stack. The two Ginkgo suites share one table of requests and expected decisions and grants. `mise run test` runs the unit, policy and integration tests, none of which need Docker; `mise run e2e` brings the stack up and runs the end-to-end suite, which also checks metrics in Mimir, spans in Tempo, logs in Loki, profiles in Pyroscope and Grafana datasource health.

## Load tests and profiles

From `examples/`, run `mise run loadtest-smoke` to verify the eight request cases, or `DURATION=15m RATE=100 mise run loadtest` to generate sustained traffic. `mise run loadtest-stress` ramps to five times the configured rate. The scripts check policy outcomes as well as HTTP responses, apply latency budgets, send measurements to Mimir and write a JSON report under `examples/results/`.

Select a **Load run** in Grafana to inspect achieved throughput, p95/p99, incorrect outcomes and dropped iterations. The **Sigil evaluation performance** section shows throughput and latency around the policy evaluator itself. CPU and runtime flame graphs show where the service spends resources; **Runtime profile** selects memory, goroutine, mutex, blocking or detected leak profiles. Follow a Loki log's trace link into Tempo, then open the surrounding service profile in Pyroscope. The load measurements include the full HTTP service and telemetry; their interpretation and configuration are covered in the [example README](https://github.com/SpechtLabs/sigil/tree/main/examples#generate-load-with-k6). [Performance](/reference/performance/#in-a-service) records one such run next to the engine's own benchmarks.

## Further reading

- The [example's README](https://github.com/SpechtLabs/sigil/tree/main/examples#readme) covers every package, metric, span and flag.
- [Embed Sigil in a Go service](/guides/embed-go/) builds the same kind of host step by step, and [Handle failed evaluations](/guides/handle-errors/) shows how deploygate's statuses and error counts come about.
- [Evaluation semantics](/reference/evaluation/#collecting-kinds) defines how a collecting kind's outcome forms.
- [Per-team policies](/guides/team-policies/) explains the composition the team policies use.
- [Policies in a ConfigMap](/guides/configmaps/) shows how to ship a bundle to a cluster.
