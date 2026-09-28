---
title: The example service
icon: mdi:rocket-launch-outline
createTime: 2026/09/28 12:00:00
permalink: /guides/example-service/
---

This guide introduces deploygate, a complete Go service built on Sigil. It serves the `DeployApproval` policies from the [tour](/getting-started/tour/) over HTTP, and it wires up everything the rest of these docs describe one piece at a time: a kind defined in Go, an exported kind file, guardrails required from a trusted source, team policies loaded from a directory with hot reload, typed matching, and metrics and traces for every decision.

The code lives in [`examples/`](https://github.com/SpechtLabs/sigil/tree/main/examples) in the repository, as a Go module of its own. Its README is the full walkthrough; this page is the short version.

## What it shows

| Piece | Where | Read more |
| --- | --- | --- |
| The `DeployApproval` kind, declared with `policy.NewKind` | `internal/deploy` | [Defining a kind](/reference/go-api/#defining-a-kind) |
| The kind file, written by the host's own `sigilc export` and checked by a test | `cmd/sigilc`, `policies/deploy_approval.sigil` | [Exporting the kind](/reference/go-api/#exporting-the-kind) |
| Platform guardrails embedded in the binary and required with `policy.From` | `policies/embed.go`, `internal/store` | [Where required policies come from](/reference/go-api/#where-required-policies-come-from) |
| Team policies read from a directory, reloaded with last-known-good semantics | `internal/store` | [Hot reload](/reference/go-api/#hot-reload), [Policies in a ConfigMap](/guides/configmaps/) |
| Results matched into typed payloads | `internal/server` | [Typed matching](/reference/go-api/#typed-matching) |
| A counter and a span per decision, with the reason and the trace | `internal/telemetry` | |

## Run it

You need [mise](https://mise.jdx.dev/) and Docker. From the repository root:

```bash
mise install
mise run examples-up
```

That starts deploygate on port 8080 together with an OpenTelemetry Collector, Jaeger 2.20 on 16686, Prometheus on 9090 and Grafana on 3000, which has a dashboard for the service. The command returns once deploygate reports ready: its image has no shell, so compose probes it with `deploygate healthcheck`, which asks the server's own `/readyz`. Then ask for a deploy, from `examples/`:

```bash
curl -si -X POST localhost:8080/api/v1/teams/payments/deployments \
  -H 'Content-Type: application/json' \
  -d @policies/teams/payments/testdata/owner.json
```

deploygate answers `202 Accepted` with the decision, its reason, the payload and the trace:

```json
{
  "team": "payments",
  "policy": "payments.production",
  "decision": "review",
  "reason": "service_owner",
  "payload": {"approvers": ["payments-leads", "security-leads"]},
  "trace": [
    {
      "decision": "review",
      "reason": "service_owner",
      "policy": "deploy.production",
      "location": "payments/production.sigil:10:3 → deploy/production.sigil:16:5",
      "conditions": [
        "service.labels[\"compliance\"] == \"pci\"",
        "cleared",
        "service.tier in [\"standard\", \"internal\"] and owns_service"
      ],
      "payload": {"approvers": ["payments-leads", "security-leads"]},
      "winner": true
    }
  ]
}
```

The status encodes the decision: `200` for approve, `202` for review, `403` for deny, and `422` when the evaluation failed, for example on a failed assert. A request deploygate won't evaluate, such as one with an unknown field or a negative soak, gets `400` before any policy runs. A client can act on the status alone and read the body for the details.

## Watch it reload

The compose stack mounts `examples/policies/teams` into the container, the way a ConfigMap would be mounted in a cluster. Edit a team policy, then reload:

```bash
curl -s -X POST localhost:8080/api/v1/policies/reload
```

The next request sees the change. Break the file instead, and the reload answers `500` with the compiler's diagnostics while deploygate keeps serving the bundle it loaded last. The `deploygate_policy_reloads_total{result="failure"}` counter goes up, and that's the one to alert on. deploygate also reloads on `SIGHUP`, and whenever a poll finds the directory's content changed: every 5 seconds in the compose stack, every 30 by default.

Startup is the exception. With no bundle loaded yet there's nothing to fall back to, so a team policy that doesn't compile stops deploygate with the diagnostics instead, and on Kubernetes the rollout stays on the old pods.

## Check policies with the host's binary

The stock `sigil` binary can't run the kind's `split` host function, so the example builds its own, `sigilc`, with the [`cli` package](/reference/cli/#host-functions-and-host-binaries). A policy repository's CI runs it the same way the service loads the policies. `mise run examples-policies` runs the check and the tests; by hand, from `examples/`:

```bash
go run ./cmd/sigilc check --config policies/sigil.yaml \
  --require deploy.guardrails --trusted policies/platform \
  --policy 'payments.*' --policy 'checkout.*' -R policies/teams
go run ./cmd/sigilc test policies
go run ./cmd/sigilc explain --policy payments.production -R policies
```

## Tests

The example has table-driven unit tests, the policy tests run from `go test` with `policytest`, a Ginkgo integration suite against the real store and server in process, and a Ginkgo end-to-end suite against the compose stack. The two Ginkgo suites share one table of requests and expected decisions. `mise run examples-test` runs everything that needs no Docker; `mise run examples-e2e` brings the stack up and runs the end-to-end suite, which also checks that Prometheus scraped the service and that Jaeger stored its spans.

## Further reading

- The [example's README](https://github.com/SpechtLabs/sigil/tree/main/examples#readme) covers every package, metric, span and flag.
- [Per-team policies](/guides/team-policies/) explains the composition the team policies use.
- [Policies in a ConfigMap](/guides/configmaps/) shows how to ship the team bundle to a cluster.
