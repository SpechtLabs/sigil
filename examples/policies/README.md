# deploygate policies

The Sigil documents deploygate evaluates, laid out the way a policy repository
shared by a platform team and product teams would be.

## Layout

| Path | Owner | What it holds |
| --- | --- | --- |
| `deploy_approval.sigil` | generated | The `DeployApproval` kind file, exported from `internal/deploy`. Regenerate it with `go generate ./cmd/sigilc`; `go test` fails when it's stale |
| `platform/deploy/common.sigil` | platform | `deploy.common`: shared `let`s (`eligible`, `cleared`, `owns_service`) |
| `platform/deploy/guardrails.sigil` | platform | `deploy.guardrails`: the denies every team gets, with a tunable `min_soak` |
| `platform/deploy/production.sigil` | platform | `deploy.production`: the review and approval rules teams invoke with their own `approvers` and `tiers` |
| `teams/<team>/production.sigil` | each team | `<team>.production`: the policy deploygate evaluates for that team |
| `teams/<team>/production_test.yaml` | each team | Test cases for that policy, with inputs under `testdata/` |
| `sigil.yaml` | platform | Lint levels for `sigilc check` |
| `embed.go` | platform | Embeds `platform/` and `teams/` into the deploygate binary |

The platform documents are trusted. deploygate always reads them from the copy
embedded in its binary and requires `deploy.guardrails` in every team policy,
so a team can tune `min_soak` but can't drop the guardrails or redefine them.
The team documents are the untrusted bundle. deploygate loads them from
`--policies` (a mounted ConfigMap in a cluster, `./policies/teams` in the
compose stack) and falls back to the embedded copy when the flag is empty.

## Checks

CI runs these from `examples/`, through the `examples-*` mise tasks. The same
commands work locally.

```sh
# Type-check every team policy with the platform documents as the trusted
# source, and fail if a team policy doesn't invoke the guardrails at the top
# level. This is the check the service makes when it loads the bundle.
go run ./cmd/sigilc check --config policies/sigil.yaml \
  --require deploy.guardrails --trusted policies/platform \
  --policy 'payments.*' --policy 'checkout.*' -R policies/teams

# Run every *_test.yaml under policies/.
go run ./cmd/sigilc test -v policies

# The same test files from go test, loaded the way the service loads them.
go test ./internal/deploy/...

# Formatting; the root `mise run sigil-fmt` covers these files too.
go run ./cmd/sigilc fmt --check policies
```

`sigilc` is a host binary: the stock `sigil` CLI with the `DeployApproval` kind
and its `split` host function linked in, so `test` can evaluate rules that call
`split`.

## Adding a team

1. Add `teams/<team>/production.sigil` defining `<team>.production`. It must
   invoke `guardrails()` at the top level.
2. Add `teams/<team>/production_test.yaml` with a case for every decision and
   reason the policy can reach, and add `--policy '<team>.*'` to the check.
3. Serve it with `--team <team>` or `DEPLOYGATE_TEAMS`.
