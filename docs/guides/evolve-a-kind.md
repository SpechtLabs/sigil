---
title: Evolve a kind safely
icon: mdi:source-branch
createTime: 2026/09/24 22:30:00
permalink: /guides/evolve-a-kind/
---

Policies across other teams compile against your exported kind file, so changing a Go struct changes a public contract. With this guide you tell which kind changes are safe, catch the unsafe ones in CI, and ship a breaking change without turning any policy repository red.

## Keep the exported kind in the policy repo

Commit the exported kind file, `deploy_approval.sigil`, where the policies live, and regenerate it from Go with `sigil export --out` in the host binary, as [Export the kind](/guides/host-binary/#export-the-kind) shows, so every kind change reaches review as a diff to that file in the same pull request as the Go change.

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

The rule for changing them is short. Bump `version` with every change to the kind, compatible or not. When the change is breaking, also raise `accepts` to the new version. You never keep old kinds around: every policy compiles against the one kind you have, and `accepts` only decides which pins you still load. A policy pinned below `accepts` fails with a message that tells its team to review the change and move the pin, instead of loading against a contract it wasn't written for:

```text
payments/production.sigil:1:44: error: DeployApproval@1 is no longer accepted; the kind accepts version 3 and later
  |
1 | policy payments.production: DeployApproval@1
  |                                            ^
  = help: review the document against the kind's changes since version 1, then raise the pin
```

A pin above `version` fails too, because the document was written for a kind this host doesn't have yet.

Nothing enforces the two numbers yet, so make them part of the review of every kind change.

## Know what's compatible

A change is compatible when every policy that compiled before still compiles and still means the same thing. Look your change up in the compatibility table in [Kind files](/reference/kind-files/#versioning), and make the one decision this page turns on: is it breaking? If it is, raise `accepts` along with `version`, and ship it in steps as [below](#ship-a-breaking-change). If it isn't, bump `version` only.

Additions are compatible, new names included: a policy pinned below the version that added a name keeps its own, and the `shadowed-kind-name` lint tells its team to rename and move the pin ([how the pin makes that safe](/understanding/kinds/#adding-a-name-never-breaks-a-policy)). Removals and renames are always breaking, and the type checker reports them in the policy repo's CI.

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

Every existing `approve(release_manager)` still compiles and gets `notify = false`. Policies that want the new behavior opt in with `approve(release_manager, notify: true)`.

Leave the default off and every existing call site breaks:

```text
payments/production.sigil:18:3: error: decision approve needs field "notify"
   |
18 |   approve(payments_sre, bake: 15m)
   |   ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
   = help: approve is declared as: decision approve(bake: duration = 1h, notify: bool) { release_manager, payments_sre }
```

Sometimes that's what you want, because every policy author should make a conscious choice. Then treat it as a breaking change and follow the steps below.

## Watch for changes that compile but change results

Reordering `precedence`, changing `default`, and adding, removing or changing a `conflict` outcome pass the type checker and still change decisions. Swapping `review` and `approve` makes the tour's service-owner deploy skip review ([the example](/understanding/kinds/#why-raise-accepts-for-a-change-that-still-compiles)), and a `default` changed from `deny(no_rule_matched)` to a review sends every deploy no rule covered to a human's queue. A `conflict` outcome only touches evaluations that already fail with a conflict, but the host still acts on what they return: adding `conflict deny(conflicting_rules)` turns their `deny(no_rule_matched)` into `deny(conflicting_rules)`, which a host checking `NoRuleMatched.Is` no longer sees, and a `conflict` that constructs an approval would turn a defect in a policy into a grant. For any of these changes:

1. Treat it as breaking and raise `accepts`, so every policy written against the old behavior stops loading until its team has looked at the new one.
2. Keep test cases that pin decisions and reasons. They catch a reordered `precedence` or a changed `default` where the type checker can't, because they pin decisions rather than types. A test case can't expect a conflict, so a changed `conflict` outcome only shows in the kind file diff, and in a Go test that checks the outcome of a conflict, as [Test a conflict](/guides/test-policies/#test-a-conflict) does.

## Check for breaking changes in CI

Three checks run today, split between the two repositories:

- In the host repo, `policytest.Schema` in `go test`, or `sigil export --check --out ../policies/deploy_approval.sigil` from a host binary, fails when the Go kind changed and the kind file wasn't regenerated. Every contract change then reaches review as a diff to `deploy_approval.sigil`.
- In the policy repo, `sigil check` against the new kind file fails on every use of a removed or renamed name, on every payload that misses a new required field, and on every pin below `accepts`.
- `sigil test`, also in the policy repo, fails when a test case's decision changes, which is the only automated catch for a reordered `precedence` or a new `default`. It can't catch a changed `conflict` outcome.

None of them checks the header. Whether `version` moved, and whether `accepts` should have, is a question for the reviewer of the kind file diff.

::: warning Planned: `sigil breaking`
[`sigil breaking OLD_KIND_FILE NEW_KIND_FILE`](/project/planned/#sigil-breaking) will classify every change by the compatibility table and fail when `version` didn't move or a breaking change didn't raise `accepts`. Today it exits with "not implemented yet".
:::

The commands for these checks, with the rest of a policy repository's job, are in [Check policies in CI](/guides/ci/#keep-the-kind-file-current).

## Ship a breaking change

Removing or renaming something breaks every policy that uses it. Do it in steps so no policy repo is ever red:

1. Add the new name alongside the old one, and bump `version`. For a rename of `Service.tier` to `Service.criticality`, both fields exist for a while. This is a compatible change, so `accepts` stays where it is.
2. Move the policies over to the new name. `sigil check` tells you where the old one is still used, because every reference is a type-checked field access.
3. Remove the old name, bump `version`, and raise `accepts` to it. Teams that finished step 2 move their pins forward in the same change. A policy that didn't fails with `DeployApproval@3 is no longer accepted`, which tells its team to review the kind's changes and raise the pin, next to the type errors about the missing field.

There's no deprecation marker in the kind format yet, so step 1 relies on communicating the migration out of band.

Changing a field's type follows the same pattern: add a field with the new type under a new name, migrate, then remove the old one.

## Remember the other evaluators

Adding a host function doesn't break existing policies, but anything that evaluates a policy calling it needs its implementation. The stock `sigil` binary knows only the signature from the kind file, so `sigil eval` and `sigil test` return a runtime error when they reach the call; a host binary built with [`pkg/cli`](/reference/cli/#host-functions-and-host-binaries) links the real function. Rebuild and ship those binaries before policies start using a new `fn`. Tools that only type-check, such as `sigil check`, work from the exported signature right away.
