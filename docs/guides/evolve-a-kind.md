---
title: Evolve a kind safely
icon: mdi:source-branch
createTime: 2026/09/24 22:30:00
permalink: /guides/evolve-a-kind/
---

This guide is for host engineers who own a kind. Policies across other teams compile against your exported kind file, so changing a Go struct is changing a public contract. Here's how to tell which changes are safe, how to catch the unsafe ones in CI, and how to ship a change that would otherwise break policies.

::: info Planned tooling
`Schema()`, `sigil breaking` and the Go API used below are planned, not implemented. The rules about what counts as breaking are part of the language design and won't change with the tooling.
:::

## Keep the exported kind in the policy repo

The kind is defined in Go, and `Schema()` renders it as a `.sigil` file that starts with the `kind` keyword. Commit that text wherever policies live, and regenerate it with `go generate` so it can't drift from the code:

```go
//go:generate go run ./cmd/export-kind -o ../policies/deploy_approval.sigil
```

```go
// cmd/export-kind/main.go
func main() {
	out := flag.String("o", "deploy_approval.sigil", "where to write the kind file")
	flag.Parse()

	if err := os.WriteFile(*out, []byte(deploygate.Deploy.Schema()), 0o644); err != nil {
		log.Fatal(err)
	}
}
```

Every change to the kind now shows up as a diff to `deploy_approval.sigil` in the same pull request as the Go change. Reviewers see the contract change, not just the struct change.

## Know what's compatible

A change is compatible when every policy that compiled before still compiles and still means the same thing.

| Change | Effect |
| --- | --- |
| Add an input, type field, function or decision | Compatible |
| Add a payload field with a default | Compatible |
| Remove or rename anything | Breaking |
| Change a type | Breaking |
| Add a payload field without a default | Breaking |
| Reorder `precedence` or change `default` | Breaking in behaviour, even though every policy still compiles |

Adding things is almost always safe, because no existing policy refers to them. The exception is a name collision: inputs and functions share one namespace with each policy's params, lets and imported names (proposed), so a new `input approvers` breaks any policy that declares `param approvers`. `sigil breaking` only compares the two kind files and can't see that; run `sigil check` over the policy repo against the new kind before you ship it. The [kind files reference](/reference/kind-files/) has the details. Removing or renaming is always breaking, because the type checker rejects any policy that still uses the old name. That's the point: the break shows up at compile time, in the policy repo's CI, instead of as a rule that silently stops matching.

## Add a payload field

Say approvals should be able to notify the service's owners when the rollout starts. Add the field to the payload struct with a default:

```go
type ApproveData struct {
	Bake   time.Duration `policy:"bake,default=1h"`
	Notify bool          `policy:"notify,default=false"`
}
```

The regenerated kind changes in one line:

```diff
-decision approve(reason: string, bake: duration = 1h)
+decision approve(reason: string, bake: duration = 1h, notify: bool = false)
```

Every existing `approve("release_manager")` still compiles and gets `notify = false`. Policies that want the new behaviour opt in with `approve("release_manager", notify: true)`.

Leave the default off and every existing call site breaks:

```text
payments/production.sigil:18:3: error: decision approve is missing required payload field "notify"
   |
18 |   approve("payments_sre", bake: 15m)
   |   ^^^^^^^
  = note: DeployApproval declares: decision approve(reason: string, bake: duration = 1h, notify: bool)
```

Sometimes that's what you want, because every policy author should make a conscious choice. Then treat it as a breaking change and follow the steps below.

## Watch for changes that compile but change results

Two changes pass the type checker and still change decisions: reordering `precedence` and changing `default`.

Swap review and approve in the `DeployApproval` kind:

```diff
-precedence deny > review > approve
+precedence deny > approve > review
```

Every policy compiles. But the service-owner deploy from the [tour](/getting-started/tour/#a-service-owner-ships-after-six-hours-of-soak), which produced a review from `deploy.production` and an approval from the payments team, now resolves to `approve`. The payments team's SRE fast path suddenly bypasses review, and no compiler told anyone.

Changing `default deny("no_rule_matched")` to `default review("no_rule_matched", approvers: [...])` is the same kind of change: every deploy that no rule covered used to be refused and now lands in a human's queue.

Test cases catch these, because they pin decisions rather than types. So does `sigil breaking`, which treats both changes as breaking on purpose.

## Check for breaking changes in CI

`sigil breaking` compares two versions of a kind file, modeled on `buf breaking`. Run it against the version on your main branch:

::: terminal Compare against main

```shell
$ git show origin/main:policies/deploy_approval.sigil > /tmp/deploy_approval.main.sigil
$ sigil breaking /tmp/deploy_approval.main.sigil policies/deploy_approval.sigil
policies/deploy_approval.sigil: breaking: precedence changed
  - deny > review > approve
  + deny > approve > review
  = note: every policy still compiles, but inputs where review and approve both fire now resolve to approve
```

:::

It needs nothing but the two kind files, so it runs in the host repo's CI without the policy repo, and in the policy repo's CI without the host's code.

## Ship a breaking change

Removing or renaming something breaks every policy that uses it. Do it in steps so no policy repo is ever red:

1. Add the new name alongside the old one. For a rename of `Service.tier` to `Service.criticality`, both fields exist for a while. This is a compatible change.
2. Move the policies over to the new name. `sigil check` tells you where the old one is still used, because every reference is a type-checked field access.
3. Remove the old name, and bump the kind version with `policy.Version(2)`. `sigil breaking` will flag the removal; that's expected, and the version bump is what tells other consumers of the kind file that the contract changed.

There's no deprecation marker in the kind format yet, so step 1 relies on communicating the migration out of band.

Changing a field's type follows the same pattern: add a field with the new type under a new name, migrate, then remove the old one.

## Remember the other evaluators

Other Go services may load your kind file with `LoadKind` and evaluate policies themselves. For them, "compatible" has one more condition. Adding a host function doesn't break any policy, but a service that evaluates policies must bind an implementation for every function the kind declares, and `Eval` refuses to run until it has. Before you add a `fn`, make sure every evaluating service can bind it, or coordinate the rollout with them. Services that only type-check, like a CI linter, don't care.
