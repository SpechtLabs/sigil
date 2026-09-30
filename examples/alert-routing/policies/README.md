# alertrouter policies

The Sigil documents alertrouter evaluates, laid out the way a policy
repository shared by a platform team and product teams would be. There is one
kind, `AlertRouting`: for each firing alert it decides whether to `page`
someone, `drop` the alert, or `notify` a channel.

## Layout

| Path | Owner | What it holds |
| --- | --- | --- |
| `alert_routing.sigil` | generated | The `AlertRouting` kind file, exported from `internal/routing`. Regenerate it with `go generate ./cmd/sigilc`; `go test` fails when it's stale |
| `platform/alerts.sigil` | platform | `platform.alerts`: the shared `let` `in_production` |
| `platform/paging.sigil` | platform | `platform.paging`: a critical production alert, or a production warning that fires longer than `page_after`, pages the team's on-call. Every team is required to invoke it |
| `platform/routing.sigil` | platform | `platform.routing`: production warnings notify the team channel, and alerts outside production and alerts named in `muted` are dropped |
| `teams/<team>/alerts.sigil` | each team | `<team>.alerts`: the policy alertrouter evaluates for that team's alerts |
| `teams/<team>/alerts_test.yaml` | each team | Test cases for that policy, one per rule it can reach |
| `sigil.yaml` | platform | The paging `sigilc check` requires, and its lint levels |
| `embed.go` | platform | Embeds `platform/` and `teams/` into the alertrouter binary |

The platform documents are trusted. alertrouter always reads them from the
copy embedded in its binary and requires `platform.paging` in every team
policy, so a team can't drop the paging or redefine it. The team documents
are the untrusted bundle. alertrouter loads them from `--policies` (a mounted
ConfigMap in a cluster, `./policies/teams` in the compose stack) and falls
back to the embedded copy when the flag is empty.

A team tunes the platform's policies through their params: `platform.paging`'s
`page_after`, how long a warning fires before it pages (30m by default,
between 5m and 1h), and `platform.routing`'s `muted`, the alert names it
doesn't want to hear about. The bounds are the platform's: a team that passes
`page_after: 2h` doesn't load. Muting drops the notification, but never a
page: `page` outranks `drop`, so a muted alert that is critical, or a muted
warning that keeps firing, still pages.

A team adds `notify` rules of its own, to post some alerts in another channel,
and never `page` rules. Two rules of the same decision and reason with
different payloads are a conflict, and an evaluation that ends in a conflict
fails. A team page for a critical or sustained alert would tie with
`platform.paging`'s, and a team notify for a warning would tie with
`platform.routing`'s; that's why
both teams' notify rules only take `info` alerts, which no platform rule routes.

## Checks

CI runs these from `examples/alert-routing/`, through `mise run policies`. The
same commands work locally.

```sh
# Type-check every policy with the platform's documents as the trusted
# source, and fail if a team policy doesn't invoke platform.paging at the top
# level, as sigil.yaml requires. This is the check the service makes when it
# loads the bundle.
mise run sigilc check --config policies/sigil.yaml policies

# Run every test file.
mise run sigilc test -v policies

# From policies/, sigil.yaml is found without --config, and its kinds: and
# trusted: paths let a command work on one team's directory. The kind has no
# host functions, so the stock sigil binary runs the tests too.
cd policies && sigil check teams/payments && sigil test teams/payments

# The same test files from go test, loaded the way the service loads them.
go test ./internal/routing/...

# Formatting; the root `mise run sigil-fmt` covers these files too.
mise run sigilc fmt --check policies
```

`sigilc` is a host binary: the stock `sigil` CLI with the `AlertRouting` kind
linked in, so `test` decodes each input into the Go types alertrouter
evaluates, and `export` writes `alert_routing.sigil` from them.

The request samples in `../requests` carry their expected outcomes in
`cases.json`, and `go test ./requests/...` routes each one through these
policies, so the samples the demo, the test suites and the load test send
can't drift from what the policies decide.

## Adding a team

1. Add `teams/<team>/alerts.sigil` defining `<team>.alerts`. It must invoke
   `paging()` at the top level, with the team's own `page_after`, and should
   invoke `routing()` there too, with its `muted` alerts.
2. Add `teams/<team>/alerts_test.yaml` with a case for every decision and
   reason the policy can reach.
3. Add the team to the team directory, `internal/teams/teams.yaml`, or the
   file `--teams-file` names, with its on-call target and channel. alertrouter
   loads one policy per team in the directory, so the team is served from the
   next start. The binary embeds everything under `teams/`, so the new policy
   ships without further changes.
4. Label the team's alerting rules with `team: <team>`, and `severity` one of
   `critical`, `warning` or `info`.
