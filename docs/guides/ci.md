---
title: Check policies in CI
icon: mdi:source-pull
createTime: 2026/09/29 12:00:00
permalink: /guides/ci/
---

This guide sets up the CI job of a policy repository. When it's done, a pull request fails on an unformatted file, a document that doesn't check, a team policy that skips the guardrails, a failing test case or a stale kind file, and the problems show up as annotations on the diff.

The examples use the layout of the [tour](/getting-started/tour/): the platform's documents in `deploy/`, each team's in its own directory such as `payments/`, and the exported kind file `deploy_approval.sigil` at the root. The job needs that kind file checked in and a `sigil` binary. The stock binary is enough for everything but `sigil test`; see [Test](#test).

## Format

Fail the job on any file that isn't in the canonical style:

```text
$ sigil fmt --check .
payments/production.sigil
✗ 1 of 5 files is not formatted
```

`--check` lists every file that isn't formatted and exits 1 if there's one. It searches directories recursively, and a file that doesn't parse fails it too. Authors fix their files locally with `sigil fmt --write .`. The exported kind file passes as it is, because `Schema()` prints kinds in the same style. [`sigil fmt`](/reference/cli/#sigil-fmt) lists the rules.

## Check

Check every document in the repository against the kind:

```text
sigil check --recursive .
```

`check` type-checks and compiles every document it's given, including ones no policy imports, so a broken document fails the pull request instead of failing the host's `Load` later. Errors fail the job; warnings from [lints](/reference/lints/) are printed and don't.

`--recursive .` also picks up the checked-in `deploy_approval.sigil`, which is where `check` finds the kind the policies name, and checks the kind file itself. A repository with policies of several kinds checks them all in this one run. In a host binary, the kind file must match the kind linked in, so CI notices an export that wasn't regenerated.

Point it at the right directory. When the paths hold no `.sigil` files, `check` warns `no .sigil files found, so nothing was checked` and still exits 0, so a job that checks the wrong directory passes. Directory arguments contribute only the files directly inside them unless you pass `--recursive`.

### Set lint levels

Promote the lints your repository relies on to errors, and switch off the ones it doesn't want, in a `sigil.yaml` at the repository root:

```yaml
lints:
  gated-deny: error
  path-matches-name: error
```

`gated-deny` as an error fails a team policy that invokes a policy holding denies under a `when`, which is almost always a mistake. Promote `path-matches-name` when CODEOWNERS protects the directories that hold the required policies, so each document's name matches the path CODEOWNERS sees ([a review aid, not a control](/understanding/bundles/#path-matches-name-is-a-review-aid)).

`check` uses the first `sigil.yaml` it finds in the working directory or a parent. When the job runs from somewhere else, name the file with `--config`. [sigil.yaml](/reference/lints/#sigil-yaml) has the format and [Lints](/reference/lints/#lints) the lints and their defaults.

## Require the guardrails

The host loads each team policy with `policy.Require("deploy.guardrails", policy.From(...))`. Run the same check in CI, so a team finds a gated or missing guardrail in its pull request instead of in a service that refuses to load the policy:

```text
sigil check \
  --require deploy.guardrails --trusted deploy/ \
  --policy 'payments.*' --policy 'checkout.*' \
  deploy_approval.sigil payments/ checkout/
```

1. **Name the required policy with `--require`.** Every root must invoke it unconditionally, through top-level invocations only. A team policy that gates it fails:

   ```text
   payments/production.sigil:7:3: error: deploy.guardrails must be invoked unconditionally
     |
   7 |   guardrails(min_soak: 4h)
     |   ^^^^^^^^^^^^^^^^^^^^^^^^
     = help: the host requires deploy.guardrails for every DeployApproval policy; move the call to the top level
   ```

2. **Pass the host's trusted source with `--trusted`.** Required policies, and everything they import and invoke, then come from `deploy/`, the way `policy.From` reads them in the host, and a team document that claims one of their names is an error. Use the same source the host uses; a trusted directory is always read recursively. A trusted directory that's also among the bundle paths, by name or through `--recursive .`, is read as trusted only.

3. **Name the roots with `--policy`.** Give a name or a pattern per team, and add one when a team joins. A pattern that matches nothing is an error, so a renamed team can't drop out of the check unnoticed.

Always name the roots in CI. Without `--policy`, `check` guesses: the roots are the bundle's policies that no other policy invokes, apart from the required ones. That guess misfires on a library bundle. Checked on its own, the platform's `deploy/` has two uninvoked policies, and `deploy.production` fails `--require` for not invoking the guardrails, although no host ever loads it as a root. With `--trusted deploy/`, the platform's documents aren't part of the bundle, so they're never roots.

When the service loads its policies from a ConfigMap that overlays add to, check the rendered ConfigMap as well. Extract its keys and pipe them to `check` with `-` as the path, which reads the documents from stdin; [Policies in a ConfigMap](/guides/configmaps/#check-in-ci-what-the-service-will-load) shows the pipeline. [`sigil check`](/reference/cli/#sigil-check) has every flag, and [Trusted sources](/reference/bundles/#trusted-sources) the rules `--trusted` follows.

To review what a ConfigMap change does, run `sigil explain` on the same input without `--policy`. It explains every policy in the bundle, one after another, and its output can go into the pull request.

## Test

Run the test cases:

```text
sigil test
```

`test` reads every `.sigil` file under the current directory into one bundle and runs every `*_test.yaml` against it, and exits 1 when a case fails. [Test your policies](/guides/test-policies/) shows how to write the cases.

Use the host team's build of the CLI for this step. The stock binary has only the signatures of the kind's host functions, and it fails every case whose evaluation reaches a call, such as `deploy.common`'s `split`. [Build a host binary](/guides/host-binary/) shows how the host team builds one. Run `check` with it too, so the kind file is compared with the kind linked into it.

## Keep the kind file current

The policy repository only sees the kind through the exported file, so the host repository's CI has to fail when the Go kind changed and the file wasn't regenerated. From the host binary's `main` package:

```text
$ sigil export --check --out ../../policies/deploy_approval.sigil
✓ ../../policies/deploy_approval.sigil is up to date
```

`export --check` exits 1 when the file is stale. A host that runs `policytest.Schema` in `go test` already has the same check. [Keep the export current](/guides/host-binary/#keep-the-export-current) covers both, and [Evolve a kind safely](/guides/evolve-a-kind/#check-for-breaking-changes-in-ci) what CI can and can't catch about a kind change.

## Annotate pull requests

With `-o json`, `check` prints one record per diagnostic, with `severity`, `message` and, where they apply, `lint`, `file`, `line`, `column` and `help`. On GitHub Actions, turn them into workflow commands, and keep the exit status, since the records alone don't fail the step:

```sh
status=0
sigil check --recursive . -o json > check.json || status=$?
jq -r '.[] | "::\(.severity) file=\(.file),line=\(.line),col=\(.column),title=\(.lint // "sigil check")::\(.message)"' check.json
exit "$status"
```

`severity` is `error` or `warning`, which GitHub reads as the annotation level. Run the command from the repository root, so `file` is the path GitHub expects. A command that can't run at all, for example because the kind file is missing, prints its `Error:` block to standard error as text and nothing to standard output, and exits 1 all the same.

`sigil test -o json` prints one record per test file with its `file` and `cases`, and each failed case has its `line` and either `failures` or, when its input couldn't be read, `error`:

```sh
status=0
sigil test -o json > test.json || status=$?
jq -r '.[] | .file as $f | .cases[] | select(.passed | not)
  | "::error file=\($f),line=\(.line),title=\(.name)::\((.failures // [.error]) | join("; "))"' test.json
exit "$status"
```

A test file that can't run at all, because it's invalid or its policy doesn't compile, has an `error` of its own next to its `file`, and no failed cases for this filter to find; the exit status still fails the step.

[Output and exit status](/reference/cli/#output-and-exit-status) describes the formats every command shares.

## The whole job

```sh
set -eu
sigil fmt --check .
sigil check --recursive .
sigil check \
  --require deploy.guardrails --trusted deploy/ \
  --policy 'payments.*' --policy 'checkout.*' \
  deploy_approval.sigil payments/ checkout/
sigil test
```

The [example service](/guides/example-service/) runs the check and test steps on its own policies with its host binary, `sigilc`, in the `policies` task of `examples/.mise.toml`.
