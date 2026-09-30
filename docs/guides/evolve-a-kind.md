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
 decision approve {
   reason: release_manager | payments_sre
   bake: duration = 1h
+  notify: bool = false
 }
```

Every existing `approve(reason: release_manager)` still compiles and gets `notify = false`. Policies that want the new behavior opt in with `approve(reason: release_manager, notify: true)`.

Leave the default off and every existing call site breaks:

```text
deploy/production.sigil:11:5: error: decision approve needs field "notify"
   |
11 |     approve(reason: release_manager)
   |     ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
   = help: approve takes reason: release_manager | payments_sre, bake: duration = 1h, and notify: bool

payments/production.sigil:18:3: error: decision approve needs field "notify"
   |
18 |   approve(reason: payments_sre, bake: 15m)
   |   ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
   = help: approve takes reason: release_manager | payments_sre, bake: duration = 1h, and notify: bool
```

Sometimes that's what you want, because every policy author should make a conscious choice. Then treat it as a breaking change and follow the steps below.

## Add an enum value

Say the platform starts running batch jobs through the same gate, and `Tier` needs a fourth value. Add a constant and pass it to `WithEnum`:

```go
const (
	TierCritical Tier = "critical"
	TierStandard Tier = "standard"
	TierInternal Tier = "internal"
	TierBatch    Tier = "batch"
)

var Deploy = policy.NewKind[Input]("DeployApproval",
	policy.WithVersion(2),
	policy.WithEnum(TierCritical, TierStandard, TierInternal, TierBatch),
	...
)
```

An added value is compatible, so bump `version` and leave `accepts` alone. Export the kind, and the diff shows both changes:

```diff
-kind DeployApproval version 1
+kind DeployApproval version 2

-enum Tier: critical | standard | internal
+enum Tier: critical | standard | internal | batch
```

Every policy that compiled before still compiles, unless the third point applies. Before you ship, check:

1. No existing rule names `batch`, so a batch service matches none of them, and the kind's default decides for it. For `DeployApproval` that's `deny(reason: no_rule_matched)`, which is safe, but tell the teams: `deploy.production`'s `tiers` defaults to `[standard, internal]`, and a team that wants batch services reviewed has to pass `tiers: [standard, internal, batch]`. Add a test case for a batch service so the decision is pinned either way.
2. An enum value joins the kind's namespace, like an input or a decision. A document pinned to `@1` that has its own `let batch` keeps it, and `sigil check` warns with [`shadowed-kind-name`](/reference/lints/#shadowed-kind-name) until its team renames the `let` and raises the pin to `@2`. A document already pinned to `@2` can't declare `batch` at all.
3. If another enum of the kind already declares `batch`, the addition is breaking: a policy that writes `batch` where nothing fixes its type, such as `let t = batch`, stops compiling, because the name is now [ambiguous](/reference/expressions/#enum-values). Raise `accepts` along with `version`, and have those policies write `Tier.batch`.

Removing or renaming a value is breaking: every `service.tier == internal` in a policy stops compiling. Raise `accepts` and follow [Ship a breaking change](#ship-a-breaking-change). Turning an existing `string` field into an enum is breaking too, because it changes the field's type; [Replace a string field with an enum](/guides/patterns/#replace-a-string-field-with-an-enum) walks through it. Why these rules hold: [Enums and versions](/understanding/kinds/#enums-and-versions).

## Watch for changes that compile but change results

Reordering `precedence`, changing `default`, and adding, removing or changing a `conflict` outcome pass the type checker and still change decisions. Swapping `review` and `approve` makes the tour's service-owner deploy skip review ([the example](/understanding/kinds/#why-raise-accepts-for-a-change-that-still-compiles)), and a `default` changed from `deny(reason: no_rule_matched)` to a review sends every deploy no rule covered to a human's queue. A `conflict` outcome only touches evaluations that already fail with a conflict, but the host still acts on what they return: adding `conflict deny(reason: conflicting_rules)` turns their `deny(reason: no_rule_matched)` into `deny(reason: conflicting_rules)`, which a host checking `NoRuleMatched.Is` no longer sees, and a `conflict` that constructs an approval would turn a defect in a policy into a grant. For any of these changes:

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

Adding a host function doesn't break existing policies, but anything that evaluates a policy calling it needs its implementation. The stock `sigil` binary knows only the signature from the kind file, so `sigil eval` and `sigil test` return a runtime error when they reach the call; a host binary built with [`pkg/cli`](/reference/cli/#host-functions-and-host-binaries) links the real function. Rebuild and ship those binaries before policies start using a new `fn`; until they ship, test files and `sigil eval --stub` can [stub](/reference/test-files/#stubs) the new function. Tools that only type-check, such as `sigil check`, work from the exported signature right away.

## Migrate to the new decision syntax

Sigil used to declare a decision's payload in parentheses and its reasons in a block, and constructors passed the reason first, without a label. Both are now written with `reason:`:

```sigil
// before
decision approve(bake: duration = 1h) {
  release_manager
  payments_sre
}

approve(payments_sre, bake: 15m)
default deny(no_rule_matched)

// after
decision approve {
  reason: release_manager | payments_sre
  bake: duration = 1h
}

approve(reason: payments_sre, bake: 15m)
default deny(reason: no_rule_matched)
```

The old forms still parse, so nothing has to change by hand. They don't check any more: the kind loader rejects the old declaration and the type checker rejects a positional reason, and each error's help holds the rewritten form. Why every argument is named now: [Why arguments are named](/understanding/language-choices/#why-arguments-are-named).

1. In the host repo, upgrade Sigil and export the kind again with `sigil export --out` from the host binary. Your Go code doesn't change: `NewDecision` and its reason handles work as before, and the export writes the new syntax.
2. In the policy repo, run `sigil check` against the new kind file. It reports every positional reason:

   ```text
   $ sigil check deploy_approval.sigil deploy/ payments/
   deploy/guardrails.sigil:8:8: error: the reason is a named argument
     |
   8 |   deny(not_eligible)
     |        ^^^^^^^^^^^^
     = help: write `deny(reason: not_eligible)`

   deploy/guardrails.sigil:12:8: error: the reason is a named argument
      |
   12 |   deny(soak_too_short)
      |        ^^^^^^^^^^^^^^
      = help: write `deny(reason: soak_too_short)`

   deploy/production.sigil:11:13: error: the reason is a named argument
      |
   11 |     approve(release_manager)
      |             ^^^^^^^^^^^^^^^
      = help: write `approve(reason: release_manager)`

   deploy/production.sigil:16:12: error: the reason is a named argument
      |
   16 |     review(service_owner, approvers: approvers)
      |            ^^^^^^^^^^^^^
      = help: write `review(reason: service_owner, approvers: approvers)`

   payments/production.sigil:18:11: error: the reason is a named argument
      |
   18 |   approve(payments_sre, bake: 15m)
      |           ^^^^^^^^^^^^
      = help: write `approve(reason: payments_sre, bake: 15m)`

   ✗ checked 4 files, 5 errors
   ```

3. Rewrite every file at once. `sigil fmt --write` turns old decision declarations into the new syntax, in kind files you maintain by hand as well, and puts `reason:` in front of every positional reason:

   ```text
   $ sigil fmt --write deploy/ payments/
   ✓ reformatted 3 files, 1 left unchanged
     deploy/guardrails.sigil
     deploy/production.sigil
     payments/production.sigil
   ```

4. Run `sigil check` and `sigil test` again. `fmt` only relabels, so the decisions your test cases pin don't change. Commit the rewrite on its own, apart from any rule change, so the diff is easy to review.

The new syntax doesn't change the contract, so the kind's `version` stays where it is.
