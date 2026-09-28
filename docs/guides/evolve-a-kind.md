---
title: Evolve a kind safely
icon: mdi:source-branch
createTime: 2026/09/24 22:30:00
permalink: /guides/evolve-a-kind/
---

This guide is for host engineers who own a kind. Policies across other teams compile against your exported kind file, so changing a Go struct is changing a public contract. Here's how to tell which changes are safe, how to catch the unsafe ones in CI, and how to ship a change that would otherwise break policies.

::: warning `sigil breaking` is planned
The command exists but only reports that it isn't implemented yet. Until it is, the version numbers are yours to check in review; [Check for breaking changes in CI](#check-for-breaking-changes-in-ci) shows what CI can catch today. Everything else on this page works as described.
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

The rule for changing them is short. Bump `version` with every change to the kind, compatible or not. When the change is breaking, also raise `accepts` to the new version. You never keep old kinds around: every policy compiles against the one kind you have, and `accepts` only decides which pins you still load. A policy pinned below `accepts` fails with a message that tells its team to review the change and move the pin, instead of loading against a contract it wasn't written for:

```text
payments/production.sigil:1:44: error: DeployApproval@1 is no longer accepted; the kind accepts version 3 and later
  |
1 | policy payments.production: DeployApproval@1
  |                                            ^
  = help: review the document against the kind's changes since version 1, then raise the pin
```

A pin above `version` fails too, because the document was written for a kind this host doesn't have yet.

Nothing enforces the two numbers yet. The planned `sigil breaking` will fail CI when you forget either one; until then, make them part of the review of every kind change.

## Know what's compatible

A change is compatible when every policy that compiled before still compiles and still means the same thing.

| Change | Effect |
| --- | --- |
| Add an input, type field, function, decision or reason | Compatible |
| Add a payload field with a default | Compatible |
| Remove or rename anything | Breaking |
| Change a type | Breaking |
| Add a payload field without a default | Breaking |
| Reorder `precedence`, add or reorder a scoped `precedence`, add an `exclusive` set, or change `default` | Breaking in behavior, even though every policy still compiles |
| Switch between `collect one` and `collect all` | Breaking |

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

Two changes pass the type checker and still change decisions: reordering `precedence` and changing `default`.

Swap review and approve in the `DeployApproval` kind:

```diff
-precedence deny > review > approve
+precedence deny > approve > review
```

Every policy compiles. But the service-owner deploy from the [tour](/getting-started/tour/#a-service-owner-ships-after-six-hours-of-soak), which produced a review from `deploy.production` and an approval from the payments team, now resolves to `approve`. The payments team's SRE fast path suddenly bypasses review, and no compiler told anyone.

Changing the `default` from `deny(no_rule_matched)` to a review is the same kind of change: every deploy that no rule covered used to be refused and now lands in a human's queue.

Test cases catch these, because they pin decisions rather than types. Treat both changes as breaking and raise `accepts`, which the planned `sigil breaking` will insist on. That's what makes them safe to ship: every policy written against the old order stops loading until its team has looked at the new one.

## Check for breaking changes in CI

Three checks run today, split between the two repositories:

- In the host repo, `policytest.Schema` in `go test`, or `sigil export --check --out ../policies/deploy_approval.sigil` from a host binary, fails when the Go kind changed and the kind file wasn't regenerated. Every contract change then reaches review as a diff to `deploy_approval.sigil`.
- In the policy repo, `sigil check` against the new kind file fails on every use of a removed or renamed name, on every payload that misses a new required field, and on every pin below `accepts`.
- `sigil test`, also in the policy repo, fails when a test case's decision changes, which is the only automated catch for a reordered `precedence` or a new `default`.

None of them checks the header. Whether `version` moved, and whether `accepts` should have, is a question for the reviewer of the kind file diff.

::: warning Planned: `sigil breaking`
`sigil breaking OLD_KIND_FILE NEW_KIND_FILE` is meant to compare two versions of a kind file, modeled on `buf breaking`: classify every change by the table above, and fail when `version` didn't move or when a breaking change didn't raise `accepts`. It would need nothing but the two kind files, so it could run in either repository, against the kind file on the main branch. Today it exits with "not implemented yet".
:::

## Ship a breaking change

Removing or renaming something breaks every policy that uses it. Do it in steps so no policy repo is ever red:

1. Add the new name alongside the old one, and bump `version`. For a rename of `Service.tier` to `Service.criticality`, both fields exist for a while. This is a compatible change, so `accepts` stays where it is.
2. Move the policies over to the new name. `sigil check` tells you where the old one is still used, because every reference is a type-checked field access.
3. Remove the old name, bump `version`, and raise `accepts` to it. Teams that finished step 2 move their pins forward in the same change. A policy that didn't fails with `DeployApproval@3 is no longer accepted`, which tells its team to review the kind's changes and raise the pin, next to the type errors about the missing field.

There's no deprecation marker in the kind format yet, so step 1 relies on communicating the migration out of band.

Changing a field's type follows the same pattern: add a field with the new type under a new name, migrate, then remove the old one.

## Remember the other evaluators

Adding a host function doesn't break existing policies, but anything that evaluates a policy calling it needs its implementation. The stock `sigil` binary knows only the signature from the kind file, so `sigil eval` and `sigil test` return a runtime error when they reach the call; a host binary built with [`pkg/cli`](/reference/cli/#host-functions-and-host-binaries) links the real function. Rebuild and ship those binaries before policies start using a new `fn`. Tools that only type-check, such as `sigil check`, work from the exported signature right away.
