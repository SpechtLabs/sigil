# deploygate policies

The Sigil documents deploygate evaluates, laid out the way a policy repository
shared by a platform team and product teams would be. There are two kinds:
`DeployApproval` decides whether a deploy goes ahead, and `AccessGrant` decides
which roles the requestor holds for it.

## Layout

| Path | Owner | What it holds |
| --- | --- | --- |
| `deploy_approval.sigil` | generated | The `DeployApproval` kind file, exported from `internal/deploy`. Regenerate it with `go generate ./cmd/sigilc`; `go test` fails when it's stale |
| `access_grant.sigil` | generated | The `AccessGrant` kind file, exported from `internal/access` the same way |
| `platform/deploy/common.sigil` | platform | `deploy.common`: shared `let`s (`eligible`, `cleared`, `owns_service`) |
| `platform/deploy/guardrails.sigil` | platform | `deploy.guardrails`: the denies every team gets, with a tunable `min_soak` |
| `platform/deploy/production.sigil` | platform | `deploy.production`: the review and approval rules teams invoke with their own `approvers` and `tiers` |
| `platform/access/common.sigil` | platform | `access.common`: shared `let`s for group membership and clearance, and each team's on-call SRE group |
| `platform/access/guardrails.sigil` | platform | `access.guardrails`: the asserts every access evaluation must pass, `named_actor` and `sod_auditor_deployer` |
| `access/main.sigil` | platform | `access.main`: the roles deploygate grants for every team |
| `access/main_test.yaml` | platform | Test cases for `access.main`, with inputs under `testdata/` |
| `teams/<team>/production.sigil` | each team | `<team>.production`: the policy deploygate evaluates for that team |
| `teams/<team>/production_test.yaml` | each team | Test cases for that policy, with inputs under `testdata/` |
| `sigil.yaml` | platform | Lint levels for `sigilc check` |
| `embed.go` | platform | Embeds `platform/`, `teams/` and `access/` into the deploygate binary |

The platform documents are trusted. deploygate always reads them from the copy
embedded in its binary and requires `deploy.guardrails` in every team policy,
so a team can tune `min_soak` but can't drop the guardrails or redefine them.
The team documents are the untrusted bundle. deploygate loads them from
`--policies` (a mounted ConfigMap in a cluster, `./policies/teams` in the
compose stack) and falls back to the embedded copy when the flag is empty.

The access tree works the same way, and the platform team owns all of it.
`access.main` is the bundle, loaded from `--access-policies` or the embedded
copy, and `access.guardrails` is required from the embedded `platform/access`.
Each kind has its own trusted source, `platform/deploy` or `platform/access`,
never `platform/` as a whole: a bundle holding documents of another kind
doesn't load. `embed.go` exposes `PlatformDeploy` and `PlatformAccess`, the
platform tree narrowed to one kind's directory, and the service passes each to
`policy.From` for its kind. The commands below name each kind's own
directories the same way.

## Two-stage evaluation

A deploy request doesn't say which roles its actor holds. deploygate first
evaluates `access.main` with the actor's name, groups and clearance, the team
and the environment. `AccessGrant` is a `collect all` kind, so the outcome is
every role that fired: a payments member who is also in `platform` gets
`reader`, `deployer` and `release_manager`. deploygate turns `deployer` and
`release_manager` into the deploy actor's roles, `admin` into both, and then
evaluates `<team>.production` with those roles. A failed assert or a
conflict ends the request before the deploy policy runs. An auditor who would
also deploy fails `sod_auditor_deployer`. A break-glass member in `platform` would get
`admin` and `release_manager`, which the kind declares `exclusive`, so the
evaluation fails with a conflict. No roles at all isn't an error: the deploy
policy then denies `not_eligible`.

## Checks

CI runs these from `examples/`, through `mise run policies`. The same
commands work locally. `sigilc` links both kinds, so every command that reads
policies picks one with `--kind`. The flag takes the exported kind file, not
the kind's name, and the linked kind must match that file exactly, which is
how a stale export fails the check.

```sh
# Type-check every team policy with the platform's deploy documents as the
# trusted source, and fail if a team policy doesn't invoke the guardrails at
# the top level. This is the check the service makes when it loads the bundle.
mise run sigilc check --kind policies/deploy_approval.sigil \
  --config policies/sigil.yaml \
  --require deploy.guardrails --trusted policies/platform/deploy \
  --policy 'payments.*' --policy 'checkout.*' -R policies/teams

# The same check for the access policy.
mise run sigilc check --kind policies/access_grant.sigil \
  --config policies/sigil.yaml \
  --require access.guardrails --trusted policies/platform/access \
  --policy access.main policies/access

# Run the test files, once per kind: each run reads that kind's documents only.
mise run sigilc test -v --kind policies/deploy_approval.sigil \
  policies/platform/deploy policies/teams
mise run sigilc test -v --kind policies/access_grant.sigil \
  policies/platform/access policies/access

# The same test files from go test, loaded the way the service loads them.
go test ./internal/deploy/... ./internal/access/...

# Formatting; the root `mise run sigil-fmt` covers these files too.
mise run sigilc fmt --check policies
```

`sigilc` is a host binary: the stock `sigil` CLI with the `DeployApproval` and
`AccessGrant` kinds and the `split` host function linked in, so `test` can
evaluate rules that call `split`.

A test case expects a decision, an outcome or failing asserts. It can't expect
a conflict: a conflict fails every one of those forms, and the format has no
key for the conflicting candidates. The break-glass and `platform` conflict is
covered by the Go integration suite, through the API's 409. To test a conflict
against the policy itself, call `Eval` from Go and check the
`*policy.ConflictError` it returns: [Testing a
conflict](../../docs/reference/cli.md#testing-a-conflict) shows this conflict
tested with plain `testing` and with Ginkgo.

## Adding a team

1. Add `teams/<team>/production.sigil` defining `<team>.production`. It must
   invoke `guardrails()` at the top level.
2. Add `teams/<team>/production_test.yaml` with a case for every decision and
   reason the policy can reach, and add `--policy '<team>.*'` to the deploy
   check in the `policies` task of `examples/.mise.toml`.
3. Add the team's on-call group to `sre_groups` in
   `platform/access/common.sigil`, so its SREs get `deployer(oncall)`.
4. Serve it with `--team <team>` or `DEPLOYGATE_TEAMS`, and add it to
   `DEPLOYGATE_TEAMS` in `docker-compose.yaml`. The binary embeds everything
   under `teams/`, so the new policy ships without further changes.
