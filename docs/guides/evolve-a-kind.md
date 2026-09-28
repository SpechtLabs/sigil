---
title: Evolve a kind safely
icon: mdi:source-branch
createTime: 2026/09/24 22:30:00
permalink: /guides/evolve-a-kind/
---

This guide is for host engineers who own a kind. Policies across other teams compile against your exported kind file, so changing a Go struct is changing a public contract. Here's how to tell which changes are safe, how to catch the unsafe ones in CI, and how to ship a change that would otherwise break policies.

::: info Planned tooling
`NewKind`, its options, `Schema()` and `sigil export` are implemented; `sigil breaking` is planned. The rules about what counts as breaking are part of the language design and won't change with the tooling.
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

A host that builds its own `sigil` binary with package `cli` doesn't need the program: `sigil export --out ../policies/deploy_approval.sigil` writes the same file, and `policytest.Schema` fails `go test` when the copy is stale (see [Exporting the kind](/reference/go-api/#exporting-the-kind)).

Every change to the kind now shows up as a diff to `deploy_approval.sigil` in the same pull request as the Go change. Reviewers see the contract change, not just the struct change.

## Bump the version, raise `accepts` when it breaks

Every policy and module pins the kind version it was written against, `policy payments.production: DeployApproval@3`, and the kind declares two numbers:

```go
policy.NewKind[Input]("DeployApproval",
	policy.WithVersion(4),
	policy.WithAccepts(3),
	...
)
```

```sigil
kind DeployApproval version 4, accepts: 3
```

The rule for changing them is short. Bump `version` with every change to the kind, compatible or not. When the change is breaking, also raise `accepts` to the new version. You never keep old kinds around: every policy compiles against the one kind you have, and `accepts` only decides which pins you still load. A policy pinned below `accepts` fails with a message that tells its team to review the change and move the pin, instead of loading against a contract it wasn't written for.

`sigil breaking` enforces both numbers, so forgetting either one fails CI rather than production.

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

Adding things is safe, because no existing policy refers to them. That includes names: inputs, functions and decisions share one namespace with each policy's own names, but a policy pinned to an older version keeps a name you add later, so a new `input approvers` doesn't break a policy that already declares `param approvers`. The policy just can't reach your new input until its team renames the param and moves the pin; the `shadowed-kind-name` lint reminds them. See [Adding a name never breaks a policy](/reference/kind-files/#adding-a-name-never-breaks-a-policy). Removing or renaming is always breaking, because the type checker rejects any policy that still uses the old name. That's the point: the break shows up at compile time, in the policy repo's CI, instead of as a rule that silently stops matching.

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
-decision approve(bake: duration = 1h) {
+decision approve(bake: duration = 1h, notify: bool = false) {
   release_manager
   payments_sre
 }
```

Every existing `approve(release_manager)` still compiles and gets `notify = false`. Policies that want the new behaviour opt in with `approve(release_manager, notify: true)`.

Leave the default off and every existing call site breaks:

```text
payments/production.sigil:18:3: error: decision approve is missing required payload field "notify"
   |
18 |   approve(payments_sre, bake: 15m)
   |   ^^^^^^^
  = note: DeployApproval declares: decision approve(bake: duration = 1h, notify: bool) { release_manager, payments_sre }
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

Changing `default deny(no_rule_matched)` to `default review(no_rule_matched, approvers: [...])` is the same kind of change: every deploy that no rule covered used to be refused and now lands in a human's queue.

Test cases catch these, because they pin decisions rather than types. So does `sigil breaking`, which treats both changes as breaking on purpose and asks you to raise `accepts`. That's what makes these changes safe to ship: every policy written against the old order stops loading until its team has looked at the new one.

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
  = help: raise `accepts` to 4, so policies pinned to older versions are reviewed before they load
```

:::

It needs nothing but the two kind files, so it runs in the host repo's CI without the policy repo, and in the policy repo's CI without the host's code.

## Ship a breaking change

Removing or renaming something breaks every policy that uses it. Do it in steps so no policy repo is ever red:

1. Add the new name alongside the old one, and bump `version`. For a rename of `Service.tier` to `Service.criticality`, both fields exist for a while. This is a compatible change, so `accepts` stays where it is.
2. Move the policies over to the new name. `sigil check` tells you where the old one is still used, because every reference is a type-checked field access.
3. Remove the old name, bump `version`, and raise `accepts` to it. `sigil breaking` flags the removal and checks that you did both. Teams that finished step 2 move their pins forward in the same change; a policy that didn't gets a clear "no longer accepted" error instead of a type error about a missing field.

There's no deprecation marker in the kind format yet, so step 1 relies on communicating the migration out of band.

Changing a field's type follows the same pattern: add a field with the new type under a new name, migrate, then remove the old one.

## Remember the other evaluators

Other Go services may load your kind file with `LoadKind` and evaluate policies themselves. For them, "compatible" has one more condition. Adding a host function doesn't break any policy, but a service that evaluates policies must bind an implementation for every function the kind declares, and `Eval` refuses to run until it has. Before you add a `fn`, make sure every evaluating service can bind it, or coordinate the rollout with them. Services that only type-check, like a CI linter, don't care.
