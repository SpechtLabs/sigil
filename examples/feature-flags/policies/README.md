# featuregate policies

The Sigil documents featuregate evaluates, laid out the way a policy repository shared by a platform team and product teams would be. There is one kind, `FeatureRollout`: for a flag and a user it decides whether to `enable` the flag, with a variant, or `disable` it.

## Layout

| Path | Owner | What it holds |
| --- | --- | --- |
| `feature_rollout.sigil` | generated | The `FeatureRollout` kind file, exported from `src/kind.rs`. Regenerate it with `mise run generate`; `cargo test` fails when it's stale |
| `platform/residency.sigil` | platform | `platform.residency`: the `ready_regions` constant, a `let` a policy can read but not widen |
| `platform/guardrails.sigil` | platform | `platform.guardrails`: `disable(region_not_ready)` outside the ready regions, and `disable(kill_switch)` for a killed flag. Every flag policy is required to invoke it |
| `flags/<key>.sigil` | product teams | `flags.<key>`: the policy featuregate evaluates for that flag, with the key's hyphens written as underscores in both names |
| `flags/flags.yaml` | product teams | The flag manifest: each string flag's `type: string` and `off:` value; a flag with no entry is boolean |
| `flags/<key>_test.yaml` | product teams | Test cases for that policy, one per rule it can reach |
| `sigil.yaml` | platform | The guardrail `sigil check` requires of `flags.*`, and its lint levels |

The platform documents are trusted: featuregate compiles them into the binary (`src/embedded.rs`) and passes them to the engine as trusted files, apart from the flag directory, requiring `platform.guardrails` of every flag policy. featuregate reads `flags/` from `FEATUREGATE_POLICIES` (a mounted ConfigMap in a cluster, `./policies/flags` in the compose stack) and serves the copy compiled into the binary when it's empty.

## What a flag policy looks like

```sigil
policy flags.new_checkout: FeatureRollout@1

use platform.guardrails

guardrails()

param percent: int = 25, min: 0, max: 100

when user.plan == enterprise {
  enable(reason: enterprise)
}

when bucket < percent {
  enable(reason: rollout)
}
```

- `guardrails()` is required, at the top level, and never under a `when`. The platform's disables then apply to the flag however its own rules read.
- `bucket` is the user's place in this flag's rollout, 0 to 99, computed by the host from the flag key and the targeting key. `bucket < percent` is a stable percentage.
- `user.attributes["cohort"]` reads a context attribute; a missing one is the empty string, so `== "checkout-pilot"` is simply false.
- `enable(reason: ..., variant: "semantic")` serves a variant; the variant is `on` when left out. Rules of different reasons never conflict over the variant: the kind ranks `enterprise > beta_tester > targeted > rollout`, and the highest reason that fired wins.
- Two rules that enable with the same reason and different variants do conflict, and the evaluation fails: featuregate answers the flag off with reason `ERROR`. Give each variant its own reason.

## Checking and testing

From `examples/feature-flags/`, with the stock CLI:

```bash
mise run policies
```

which runs `sigil check --config policies/sigil.yaml policies` and `sigil test policies`. The Rust suite (`cargo test`, `tests/policies.rs`) runs the same test files through the crate against the policies compiled the way the service compiles them.
