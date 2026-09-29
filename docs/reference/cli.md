---
title: CLI & editor tooling
icon: mdi:console
createTime: 2026/09/24 22:30:00
permalink: /reference/cli/
---

This page is the reference for the `sigil` command line: every command, its flags, its output and its exit status, plus the files the tools read (`sigil.yaml` and `*_test.yaml`). Policy authors and the people who maintain a policy repository's CI use it most. Host engineers need the section on [host binaries](#host-functions-and-host-binaries) and the [Go API](/reference/go-api/).

The tools read the exported kind file (`deploy_approval.sigil` in the running example), so they work in a team's policy repository without the host's Go code. One `sigil` binary covers the command line.

| Command | Does | Needs |
| --- | --- | --- |
| [`sigil fmt`](#sigil-fmt) | Rewrites files into the one canonical style, like `gofmt` | Nothing |
| [`sigil check`](#sigil-check) | Parses, type-checks and compiles policies against a kind, and lints them | Kind file |
| [`sigil eval`](#sigil-eval) | Evaluates a policy against a JSON input and prints the result and trace | Kind file; a host binary for functions |
| [`sigil explain`](#sigil-explain) | Flattens a policy into its guarded decisions, with every invocation inlined | Kind file |
| [`sigil test`](#sigil-test) | Runs test cases: an input plus the expected decision and reason, or the asserts that fail | Kind file; a host binary for functions |
| [`sigil export`](#sigil-export) | Writes the kind file of a kind linked into a host binary | A host binary |
| [`sigil version`](#sigil-version) | Shows the version and build information | Nothing |
| [`sigil breaking`](#sigil-breaking) | Not implemented yet. Will compare two kind versions and flag incompatible changes | Two kind files |
| [`sigil gen go`](#sigil-gen-go) | Not implemented yet. Will generate typed Go code from a kind file | Kind file |
| [`sigil lsp`](#sigil-lsp) | Not implemented yet. Will run the language server | Kind file |

`sigil completion bash|fish|powershell|zsh` prints a shell completion script, and `sigil help <command>` or `--help` (`-h`) prints any command's help.

::: warning Not implemented yet
`sigil breaking`, `sigil gen go` and `sigil lsp` are registered, so their help is there, but each one only prints an error such as `Error: "sigil lsp" is not implemented yet` and exits with status 1. `explain --input` is planned too. The [roadmap](/project/roadmap/) tracks them.
:::

Policy, module and kind documents share the `.sigil` extension, and one file can hold several of them. Each document's header keyword, `policy`, `module` or `kind`, tells the tools which one they're looking at.

## Inputs

Commands that read policies take their input the way `kubectl -f` does: every argument is a file, a directory or `-` for stdin, and the tool combines all the documents it finds into one bundle.

```text
sigil check   --kind deploy_approval.sigil deploy/*.sigil payments/*.sigil
sigil check   --kind deploy_approval.sigil policies.sigil
sigil explain --kind deploy_approval.sigil --policy payments.production deploy/ payments/
cat policies.sigil | sigil eval --kind deploy_approval.sigil --input release.json --policy payments.production -
```

- **Files and globs.** A file may hold any number of documents. The shell expands globs; the CLI never does.
- **Directories.** A directory contributes the `*.sigil` files directly inside it. For `check`, `eval` and `explain`, `-R` (`--recursive`) includes subdirectories too; `fmt` and `test` always search directories recursively. Entries whose names start with `.` are skipped, as the [loader](/reference/policy-files/#loading-files) does, so pointing the CLI at a mounted ConfigMap volume works.
- **Stdin.** `-` reads one stream, which may hold several documents. It's how a CI job checks a key extracted from a rendered ConfigMap. `eval` can't read both the bundle and `--input` from stdin.
- **One bundle.** Documents from all arguments are indexed by the names in their headers, exactly as the host's `Load` does, so a name defined in two files is an error here too.
- **The kind stays separate.** `--kind` (`-k`) names the kind file, and it's never part of the bundle. A kind document for the same kind found among the inputs, for example because a glob matched the exported file, must match `--kind` exactly, or the command fails; that's how CI notices an export that wasn't regenerated. Kind documents for other kinds are ignored. A missing `--kind` is an error, except in a [host binary](#host-functions-and-host-binaries).

`eval` needs a root policy; `check`, `fmt` and `test` don't. If the bundle holds exactly one policy, that's the root, so a single-file bundle needs no flag. Otherwise `--policy` (`-p`) names it, and leaving it out is an error that lists the policies found. `explain` is the exception: `--policy` also takes a pattern such as `'payments.*'`, and without it `explain` explains every policy in the bundle, one after another, which is handy for reviewing a whole ConfigMap. In a pattern, `*` matches any run of characters, dots included, and a pattern that matches nothing is an error.

## Output and exit status

Every command takes two global flags:

| Flag | Default | Does |
| --- | --- | --- |
| `-o`, `--output` | `text` | Output format: `text`, `json` or `yaml`. Every command honors it; each command's section describes its records |
| `--color` | `auto` | When to color text output: `auto`, `always` or `never`. `auto` colors on a terminal, and not when piped or when `NO_COLOR` is set |

`check`, `test`, `fmt --check`, `fmt --write` and `export --out` end their text output with one line that sums the run up, marked `✓`, `!` or `✗`:

```text
✓ checked 4 files, no problems found
! checked 4 files, 2 warnings
✗ checked 4 files, 1 error and 2 warnings
```

That's the report. A command that couldn't do its job at all, because a flag is wrong or a file is missing, prints an `Error:` block instead that says what to do:

```text
Error: the kind file couldn't be read

What you can do
  • pass the exported kind file with --kind

Caused by
  • open deploy_approval.sigil: no such file or directory
```

With `-o json` or `-o yaml`, standard output holds the command's records and nothing else, so a pipeline can parse it: no summary line, and no styling. The records carry what the text shows, including what failed, such as `check`'s error diagnostics or the `error` record of a [planned command](#sigil-breaking). The `Error:` block of a command that couldn't run at all still goes to standard error as text, and the exit status is the same in every format.

Every command exits with status 0 on success and 1 on any failure: a usage error, an unreadable file, an error found by `check`, a failed evaluation, a failing test case, an unformatted file under `fmt --check` or a stale file under `export --check`. There are no other exit codes. Warnings don't change the status.

## Host functions and host binaries

A kind file carries each host function's signature but not its implementation. The stock `sigil` binary checks, formats and explains any policy, and evaluates it as long as no rule reaches a host function call. A call it reaches is a runtime error that names the function:

```text
runtime error: deploy/common.sigil:5:3: host function split failed: no implementation in this sigil binary; build a host binary with split linked in (see sigil's pkg/cli)
```

The running example's `deploy.common` calls `split`, so the `eval` and `test` samples on this page come from a host binary.

A host that wants `eval` and `test` to call its real functions builds its own `sigil` binary. Package `cli` is the whole command line with the host's kind linked in:

```go
package main

import (
	"github.com/spechtlabs/sigil/pkg/cli"

	"example.com/deploygate"
)

func main() {
	cli.Main(cli.WithKind(deploygate.Deploy))
}
```

That binary decodes inputs into the host's own Go types and calls its functions. Every command uses the linked kind without `--kind`; a `--kind` file for the same kind must match it exactly, which catches a stale export. `cli.WithKind` can be repeated for a host with several kinds, and a `--kind` file then picks one. `cli.WithVersion` sets what `sigil version` reports. `go test` doesn't need any of this: [`policytest`](#policytest-for-go-hosts) runs inside the host.

## `sigil fmt`

Rewrites policy, module and kind documents into one canonical style. It needs nothing but the files themselves.

```text
sigil fmt [PATH...] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-w`, `--write` | off | Writes the result back to the files instead of printing it |
| `--check` | off | Only lists the files that aren't formatted, and fails if there are any |

With no paths, `fmt` formats the current directory; directories are searched recursively, and `-` reads stdin. The formatted source is printed unless `--write` rewrites the files in place, which touches only files that change, keeps their permissions, and lists what it rewrote. `--write` and `--check` can't be combined, and `--write` can't write back to stdin. `--check` prints the path of every file that isn't formatted and fails if there's one, which is what CI runs. A file that doesn't parse is reported with its syntax errors and left alone, and fails the run.

```text
$ sigil fmt --check .
payments/production.sigil
✗ 1 of 4 files is not formatted
```

`-o json` and `-o yaml` print a list with one record per file, in every mode: `file`, `formatted` (whether it was already in the canonical style), `written` when `--write` rewrote it, `source` with the formatted source when `fmt` prints rather than checks or writes, and `diagnostics` for a file that doesn't parse, with the fields of [`check`'s records](#sigil-check). A file with diagnostics was left alone, so it has neither `formatted: true` nor a `source`. Stdin is named `<stdin>`.

```text
$ sigil fmt --check -o json .
[
  {
    "file": "deploy/common.sigil",
    "formatted": true
  },
  {
    "file": "deploy/guardrails.sigil",
    "formatted": true
  },
  {
    "file": "deploy/production.sigil",
    "formatted": true
  },
  {
    "file": "payments/production.sigil",
    "formatted": false
  }
]
```

The formatter matters more than it looks. Sigil's grammar is whitespace-insensitive so that templating can't break it, and a whitespace-insensitive grammar lets styles drift: one team indents continuation lines by two spaces, another by four, a third puts `and` at the end of the line. One canonical form keeps diffs across teams readable and makes the formatter's output the only style anyone has to learn.

The canonical style:

- Two spaces of indentation, one space around binary operators, no trailing whitespace, and one newline at the end of the file.
- A blank line after each document's header. Top-level statements group by kind, with a blank line between the groups: `use`s, `param`s, `let`s and `assert`s in policies and modules, and `input`s, `fn`s, and the `collect`, `precedence` and `exclusive` lines in kinds. Every `when` block, invocation, `type`, `decision` and `default` stands alone with a blank line around it. Inside a `when` body only the author's blank lines are kept, never more than one in a row.
- In a file with several documents, one `---` line between each pair, with a blank line on each side, and none before the first or after the last.
- Line breaks follow the author, the way `gofmt` does. An `and`, `or` or `xor` chain breaks only where the source broke next to the operator, and the break always goes before the operator, with continuation lines one level deeper than the statement. A `let` value written on the line after `let x =` stays there, one level in. A list, map or argument list whose first item started a new line gets one item per line and a trailing comma; any other list is joined onto one line.
- A `when` with a single decision, invocation or assert written on one line stays on one line: `when frozen { deny(freeze) }`.
- A quantifier or filter body whose top level is `and`, `or` or `xor` gets parentheses, so its extent is visible: `any r in actor.roles: (r like "sre-*" and release.hotfix)`. The parentheses change nothing, since a body extends as far right as possible; they only show where it ends. No other parentheses are added or removed.
- Literals keep their source text, so a raw string stays raw.
- A comment on the same line as code stays there; any other comment gets its own line at the indentation of what follows it. Trailing comments on consecutive lines are aligned.

`Schema()` prints kinds in this style, so an exported kind file passes `sigil fmt --check` as it is.

It exits 1 under `--check` when a file isn't formatted, and in any mode when a file doesn't parse.

## `sigil check`

Parses and type-checks policies and modules against a kind, resolves imports and invocations, detects `let`, import and invocation cycles, compiles every policy, and reports [lints](#lints). This is the command a policy repository runs in CI. It doesn't need host function implementations, only their signatures from the kind file.

```text
sigil check PATH... [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-k`, `--kind` | none | Kind file to check against; optional in a host binary |
| `-R`, `--recursive` | off | Reads `.sigil` files in subdirectories of directory arguments too |
| `--require` | none | Policy every root must invoke unconditionally. Repeatable |
| `--trusted` | none | File or directory to read required policies from, as `policy.From` does. Repeatable |
| `-p`, `--policy` | every uninvoked policy | Root policy name or pattern for `--require`. Repeatable |
| `--config` | nearest `sigil.yaml` | Configuration file with lint levels |

```text
sigil check --kind deploy_approval.sigil --recursive .
```

Every document in the bundle is checked and every policy compiled, including ones no policy imports, so a broken document fails CI instead of failing the host's `Load` later. Compiling catches what type-checking can't, such as an invocation argument outside its param's `min` and `max`. A policy whose params have no defaults, like `deploy.production`, is checked and compiled with them unbound, the way `explain` shows it: it's a template, and the policies that invoke it bind the params.

Errors fail the check; warnings are printed and don't. Both use the [error format](#error-messages), in file and line order, and a warning says which lint it comes from. The last line counts them:

```text
payments/production.sigil:11:3: warning: deploy.guardrails holds deny rules and is invoked under `when` [gated-deny]
   |
11 |   guardrails()
   |   ^^^^^^^^^^^^
   = help: its deny rules only fire while the condition holds; invoke it at the top level, or have the host require it

! checked 4 files, 1 warning
```

When the paths hold no `.sigil` files at all, `check` warns `no .sigil files found, so nothing was checked` instead of reporting a clean run. It still exits 0, so point CI at the right directory, with `--recursive` if the files are in subdirectories.

`-o json` and `-o yaml` print a list with one record per diagnostic, for annotating pull requests. A record has `severity` (`error` or `warning`), `message`, and, where they apply, `lint`, `file`, `document`, `line`, `column` and `help`. A clean check prints `[]`.

`--require` makes the same check a host makes with `policy.Require`: every root policy must invoke the named policy unconditionally, through top-level invocations only. Repeat the flag to require several. A policy repository that runs it in CI finds a gated or missing guardrail before the host refuses to load the policy.

```text
sigil check --kind deploy_approval.sigil \
  --require deploy.guardrails --trusted deploy/ \
  --policy 'payments.*' payments/
```

- `--trusted` does what `policy.From` does in the host: required policies, and everything they import and invoke, are read from those paths, and the bundle may not define any name they define. Trusted directories are always read recursively. Run CI with the same trusted source the host uses.
- `--policy` names the roots, by name or by pattern, and can be repeated. Name them in CI. Without `--policy`, the roots are the bundle's policies that no other policy invokes, apart from the required ones. That guess misfires on a library bundle: checked on its own, the platform's `deploy/` has two uninvoked policies, and `deploy.production` fails `--require` for not invoking the guardrails. With `--trusted deploy/`, the platform's documents aren't part of the bundle, so they're never roots.

`sigil check` doesn't compute costs; that comes with the static cost analysis on the [roadmap](/project/roadmap/) (see [Halting by construction](/understanding/halting/)).

It exits 1 when there's an error, including a lint set to `error` and a failed `--require`.

## `sigil eval`

Evaluates a policy against a JSON input and prints the result and the full trace: every candidate, the outcome, which conditions held for each candidate of the winning decision, and any failing asserts.

```text
sigil eval PATH... --input FILE [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-i`, `--input` | required | Input document (JSON) to evaluate against, or `-` for stdin |
| `-k`, `--kind` | none | Kind file the policy is written against; optional in a host binary |
| `-p`, `--policy` | the bundle's only policy | Name of the policy to evaluate; required when the bundle holds more than one |
| `-R`, `--recursive` | off | Reads `.sigil` files in subdirectories of directory arguments too |

Candidates read the way a policy writes them, `review(service_owner)`, with the conditions that held after `when` and the payload beneath; the ones in the outcome are marked with `*`.

```text
$ sigil eval --kind deploy_approval.sigil --input owner-deploy.json --policy payments.production deploy/ payments/
payments.production: review(service_owner)
  approvers = ["payments-leads"]

trace: 2 candidates
  * review(service_owner)  payments/production.sigil:14:3 → deploy/production.sigil:16:5
      when service.labels["compliance"] != "pci"
       and cleared
       and service.tier in ["standard", "internal"] and owns_service
      approvers = ["payments-leads"]
    approve(payments_sre)  payments/production.sigil:18:3
      bake = 15m
```

`-o json` and `-o yaml` print the same result as a record with `policy`, `decision`, `reason`, `payload`, `outcome` and `trace`, plus `error` when the evaluation failed.

The input is a JSON object with one key per input the kind declares. The decoding is strict where a mistake would go unnoticed and lenient where Go is:

- A key the kind doesn't declare, at any depth, is an error with a did-you-mean hint, so a typo in a fixture fails instead of silently testing the zero value.
- A missing key reads as its type's zero value, as it would after `encoding/json` decoded into the host's struct.
- A `duration` is a string in Sigil's syntax, `"1h30m"`; a `timestamp` is an RFC 3339 string, `"2026-09-28T14:00:00Z"`. A number where a duration belongs is an error, because nobody can tell whether `90` meant seconds or nanoseconds.
- `null` is allowed for optionals, lists and maps. Map keys of a non-string type are written the way their values are: `"3"` for an `int` key.

```text
Error: bad.json: actor.nmae: unknown field "nmae" on type Actor

What you can do
  • did you mean "name"? declared: name, teams, roles, regions
  • the input is a JSON object with one key per input the kind declares
```

When the evaluation fails, with a runtime error, a conflict or a failing assert, `eval` prints the fallback the host would act on, the kind's default, then why it failed with a `= help:` line on what to do about it:

```text
payments.production: a runtime error stopped the evaluation, the host falls back to deny(no_rule_matched), the kind's default

runtime error: deploy/common.sigil:5:3: host function split failed: no implementation in this sigil binary; build a host binary with split linked in (see sigil's pkg/cli)
  = help: this sigil binary has only split's signature from the kind file; evaluate with the host's own binary, built with sigil's pkg/cli, which links the real function in

trace: no rule fired
```

The help depends on what failed. Most runtime errors get "fix the expression the runtime error points at, or the input it read", and a host function that isn't linked in gets the advice above.

A failing outcome assert lists the candidates that formed the outcome it read, with their payloads, so you can see which grants broke it and what they carried. Here the separation-of-duties assert from the examples' access policies catches an auditor who also got deploy rights through on-call:

```text
$ sigil eval --kind access_grant.sigil --input access/testdata/auditor-sre.json --policy access.main access platform/access
access.main: an assert failed, the host falls back to no decisions

assert sod_auditor_deployer failed at access/main.sigil:6:1 → platform/access/guardrails.sigil:11:1
  the outcome it read:
    deployer(oncall)            access/main.sigil:19:3
      ttl = 2h
    auditor(compliance_member)  access/main.sigil:40:3
  = help: the input breaks an assert of the policy; if the input is right, the policy's assumption is wrong

trace: 2 candidates
    deployer(oncall)            access/main.sigil:19:3
      when on_call
      ttl = 2h
    auditor(compliance_member)  access/main.sigil:40:3
      when compliance_member
```

In `-o json` and `-o yaml`, each entry of `error.asserts` carries the same list as `outcome`. An assert whose condition raised a runtime error has that error in `cause`, and `help` when the error comes with advice of its own.

Diagnostics and trace entries name the document as well as the position, `policies.sigil:42:5 (payments.production)`, so a trace stays readable when many documents share one file. The name is left out when the file's path matches the name, as in `deploy/production.sigil:16:5`. This is the text form of [`policy.Position`](/reference/go-api/#positions-in-errors-and-traces).

It exits 1 when the bundle doesn't check, the root doesn't compile, the input doesn't decode, or the evaluation fails.

## `sigil explain`

Flattens a policy into one list of guarded decisions. Every invocation is inlined, and its gates are pushed down into each rule's condition. Invocation makes composition flexible; `explain` keeps it transparent, so the answer to "what does this policy actually do" is one command away.

```text
sigil explain PATH... [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-k`, `--kind` | none | Kind file the policy is written against; optional in a host binary |
| `-p`, `--policy` | every policy | Name or pattern of the policies to explain |
| `-R`, `--recursive` | off | Reads `.sigil` files in subdirectories of directory arguments too |

```text
$ sigil explain --kind deploy_approval.sigil --policy payments.production deploy/ payments/
payments.production: 7 rules from 3 policies and 1 module

  deny(not_eligible)        payments.production:7 → deploy.guardrails:8
    when not eligible

  deny(soak_too_short)      payments.production:7 → deploy.guardrails:12
    when release.soak < 4h and not release.hotfix

  approve(release_manager)  payments.production:10 → deploy.production:11
    when service.labels["compliance"] == "pci"
     and cleared
     and service.tier == "critical" and "release_manager" in actor.roles

  review(service_owner)     payments.production:10 → deploy.production:16
    when service.labels["compliance"] == "pci"
     and cleared
     and service.tier in ["standard", "internal"] and owns_service
    with approvers = ["payments-leads", "security-leads"]

  approve(release_manager)  payments.production:14 → deploy.production:11
    when service.labels["compliance"] != "pci"
     and cleared
     and service.tier == "critical" and "release_manager" in actor.roles

  review(service_owner)     payments.production:14 → deploy.production:16
    when service.labels["compliance"] != "pci"
     and cleared
     and service.tier in ["standard", "internal"] and owns_service
    with approvers = ["payments-leads"]

  approve(payments_sre)     payments.production:18
    when cleared and "payments-sre" in actor.teams
    with bake = 15m
```

The header counts the rules, the policies they come from (the explained policy and every policy it invokes), and the modules those policies import from. Each entry names the rule the way a policy writes it, `deny(not_eligible)`, then the call chain that reaches it, the full condition it fires under after `when`, and its payload after `with`. A rule with no condition reads `always`. The chain names every document by its full name and line, `payments.production:7 → deploy.guardrails:8`, whatever name the `use` bound it to. Asserts appear as entries too, `assert named_actor (input)` or `(outcome)`, so a reader can tell which ones run before the rules, with the condition under which they're checked after `when` and the condition they check after `check`. The condition after `when` is every `when` around every call on the chain, joined with `and`. Params show as their bound values, which is why invocation arguments can't depend on inputs. `let`s stay by name, so a condition reads the way its author wrote it.

A policy explained on its own, without a policy that invokes it, shows a required param by its name, `approvers = approvers`, since nothing binds it. A planned `--input` flag will also mark which rules fired and which candidate won; like `eval`, that needs a host binary for policies that call host functions.

`-o json` and `-o yaml` print a list with one record per policy: `policy`, the `policies` and `modules` counts, and `rules`, where each rule has `kind` (`decision` or `assert`), `decision`, `reason`, `phase` for an assert, `chain`, `conditions`, `check` and `payload`.

[The tour](/getting-started/tour/#what-the-team-policy-adds-up-to) walks through this output.

It exits 1 when the bundle doesn't check, a policy doesn't compile, or `--policy` matches nothing.

## `sigil test`

Runs test cases, each an input plus the expected decision and reason. Asserting on the reason as well as the decision catches the case where a deploy is denied for the wrong reason, which is a common way policy regressions hide.

```text
sigil test [PATH...] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-k`, `--kind` | none | Kind file the policies are written against; optional in a host binary |
| `--run` | every case | Only runs cases whose name matches this regular expression |
| `-v`, `--verbose` | off | Lists passing cases too |

Every path is a file or a directory, searched recursively; with no paths, `test` searches the current directory. The `.sigil` files it finds form one bundle, and every test file found runs against it.

Test cases live in YAML files named `*_test.yaml` (or `*_test.yml`), next to the policies they test, one file per policy:

```yaml
# payments/production_test.yaml
policy: payments.production
cases:
  - name: an owner's deploy goes to review
    input_file: testdata/owner.json
    expect:
      decision: review
      reason: service_owner
      payload:
        approvers: [payments-leads]
  - name: a short soak is denied
    input_file: testdata/short-soak.json
    expect:
      decision: deny
      reason: soak_too_short
  - name: an unnamed actor fails the assert
    input:
      environment: production
      actor: {name: ""}
    expect:
      asserts: [named_actor]
```

| Key | Holds |
| --- | --- |
| `policy` | The root policy the cases evaluate. Required |
| `cases` | The list of cases |
| `cases[].name` | The case's name, which `--run` matches and failures report. Required and unique in the file |
| `cases[].input` | The input, inline, in YAML |
| `cases[].input_file` | A file holding the input, relative to the test file: JSON, or YAML when the name ends in `.yaml` or `.yml` |
| `cases[].expect` | What the case expects: `decision` and `reason`, `outcome`, or `asserts` |

- **The input** is exactly one of `input` and `input_file`. Either way it's decoded by [`eval`'s rules](#sigil-eval).
- **`decision` and `reason`** expect one outcome of a `collect one` kind; both are required. `payload` lists the fields to compare, in the same encoding as the input, and fields it leaves out aren't checked.
- **`outcome`** expects the whole outcome of a `collect all` kind: a list of `decision`, `reason` and optional `payload` entries, matched in any order. It passes only if the outcome holds exactly those entries; `outcome: []` expects nothing to fire.
- **`asserts`** expects the evaluation to fail its asserts, naming their reasons. Any other failing assert, or none, fails the case.
A case expects exactly one of the three. It can't expect a [conflict](/reference/evaluation/#resolution): a conflict fails every one of the three forms, and a test file has no key that names the conflicting candidates. Test a conflict from Go instead, as [Testing a conflict](#testing-a-conflict) shows. Everything a test file names is checked against the kind before anything runs: an unknown key, decision, reason or payload field is an error with a did-you-mean hint.

The output follows `go test`. Here the second case expects the wrong reason:

```text
$ sigil test --kind deploy_approval.sigil
--- FAIL: payments/production_test.yaml:10: a short soak is denied
      want deny(not_eligible)
      got  deny(soak_too_short)
FAIL  payments/production_test.yaml  1 of 3 cases failed
✗ 1 of 3 test cases failed in 1 file
```

`-o json` and `-o yaml` print one record per test file with its `file`, `policy` and `cases`, each case with `name`, `line`, `passed` and, when it failed, `failures`.

Like `eval`, a case that reaches a host function needs a host binary; the stock binary fails it with the runtime error.

It exits 1 when a case fails or a test file is invalid.

### Testing a conflict

A `*_test.yaml` case can't expect a conflict today, so a conflict the kind is meant to catch gets its test in Go, next to the host's [`policytest`](#policytest-for-go-hosts) run. The [example service](/guides/example-service/)'s `AccessGrant` kind declares `exclusive admin, release_manager`, and `access.main` grants both to a break-glass member who is also in `platform`. `Eval` then returns a `*policy.ConflictError`: find it with `errors.As`, and check its `Candidates`, which hold every candidate of the exclusive set's members and nothing else. The result that comes with the error holds the kind's default, which for a collecting kind without one, like `AccessGrant`, is an empty outcome. Both tests load the bundle the way the service does:

::: tabs

@tab Go testing

```go
func TestConflicts(t *testing.T) {
	p, err := access.Kind.Load(os.DirFS("../../policies/access"), "access.main",
		policy.Require("access.guardrails", policy.From(os.DirFS("../../policies/platform/access"))))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		input access.Input
		want  []string // the conflicting candidates as decision(reason), sorted
	}{
		{
			name: "break-glass in platform is admin and release_manager",
			input: access.Input{
				Actor:       access.Actor{Name: "margaret", Groups: []string{"break-glass", "platform"}},
				Team:        "payments",
				Environment: "production",
			},
			want: []string{"admin(break_glass)", "release_manager(platform_member)"},
		},
		{
			name: "grants outside the exclusive set aren't in the conflict",
			input: access.Input{
				Actor:       access.Actor{Name: "grace", Groups: []string{"break-glass", "platform", "payments"}},
				Team:        "payments",
				Environment: "production",
			},
			want: []string{"admin(break_glass)", "release_manager(platform_member)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := p.Eval(t.Context(), tt.input)

			var conflict *policy.ConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("Eval() error = %v, want a *policy.ConflictError", err)
			}
			var got []string
			for _, c := range conflict.Candidates {
				got = append(got, c.Decision+"("+c.Reason+")")
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("conflicting candidates = %v, want %v", got, tt.want)
			}
			// The result holds the kind's default. AccessGrant collects
			// and declares none, so the outcome is empty.
			if len(res.Outcome) != 0 {
				t.Errorf("outcome = %v, want it empty", res.Outcome)
			}
		})
	}
}
```

@tab Ginkgo

```go
var _ = Describe("access.main", func() {
	var p *policy.Policy[access.Input]

	BeforeEach(func() {
		var err error
		p, err = access.Kind.Load(os.DirFS("../../policies/access"), "access.main",
			policy.Require("access.guardrails", policy.From(os.DirFS("../../policies/platform/access"))))
		Expect(err).NotTo(HaveOccurred())
	})

	It("fails with a conflict for a break-glass member in platform", func(ctx SpecContext) {
		res, err := p.Eval(ctx, access.Input{
			Actor:       access.Actor{Name: "margaret", Groups: []string{"break-glass", "platform"}},
			Team:        "payments",
			Environment: "production",
		})

		var conflict *policy.ConflictError
		Expect(errors.As(err, &conflict)).To(BeTrue(), "Eval() error = %v, want a *policy.ConflictError", err)
		Expect(conflict.Candidates).To(ConsistOf(
			And(HaveField("Decision", "admin"), HaveField("Reason", "break_glass")),
			And(HaveField("Decision", "release_manager"), HaveField("Reason", "platform_member")),
		))
		// The result holds the kind's default. AccessGrant collects and
		// declares none, so the outcome is empty.
		Expect(res.Outcome).To(BeEmpty())
	})
})
```

:::

`ConsistOf` matches in any order, as the sorted slice does in the table test. A `collect one` kind's conflict has the same shape, with the candidates tied at the top rank; there the result holds the kind's `default` decision instead of an empty outcome.

## `sigil export`

Prints the kind file of the kind linked into a [host binary](#host-functions-and-host-binaries), the text `Schema()` returns.

```text
sigil export [KIND] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `--out` | stdout | Kind file to write instead of printing it |
| `--check` | off | Only compares with the `--out` file, and fails when it's stale. Needs `--out` |

`--out` writes the file, and says whether it wrote it or found it current, which suits `go generate`. With several kinds linked, `KIND` picks one by name: `sigil export DeployApproval`. The directive goes in the host binary's `main` package, so `go run .` builds it:

```go
//go:generate go run . export --out ../../policies/deploy_approval.sigil
```

```text
$ sigil export --check --out ../../policies/deploy_approval.sigil
✓ ../../policies/deploy_approval.sigil is up to date
```

`-o json` and `-o yaml` print one record: `kind` and `version` from the kind header, `source` with the kind file as the binary exports it, and, with `--out`, `file` and `status`. `status` is `current` when the file already matched, `written` when `export` wrote it, and `stale` when `--check` found it out of date, which also exits 1:

```text
$ sigil export --check --out ../../policies/deploy_approval.sigil -o yaml
kind: DeployApproval
version: 1
file: ../../policies/deploy_approval.sigil
status: stale
source: |
  kind DeployApproval version 1
  ...
```

The stock binary links no kind, so `export` there fails with `no kind is linked into this binary` and explains how to build one.

It exits 1 when no kind is linked, `KIND` names none of them, or `--check` finds the file stale.

## `sigil version`

Shows the release version, plus the commit, commit time, Go version and platform the binary was built with.

```text
sigil version [flags]
```

It takes only the global flags. The commit details come from the version control information the Go toolchain embeds in every build; a binary built with `go run` reports them as unknown. `-o json` prints one object with `version`, `commit`, `commitTime`, `dirty`, `goVersion` and `platform`, for a bug report. A host binary reports the version it set with `cli.WithVersion`.

## `sigil breaking`

::: warning Not implemented yet
`sigil breaking` prints `Error: "sigil breaking" is not implemented yet` and exits with status 1. This section describes the design.
:::

With `-o json` or `-o yaml`, `breaking`, `gen go` and `lsp` print the error as a record on standard output instead, in the shape of the `error` record [`sigil eval`](#sigil-eval) prints for a failed evaluation, and still exit with status 1:

```json
{
  "error": {
    "kind": "not_implemented",
    "message": "\"sigil breaking\" is not implemented yet",
    "help": "the command is planned for a later milestone; track progress at https://github.com/SpechtLabs/sigil/blob/main/roadmap.yml"
  }
}
```

Compares two versions of a kind file and flags changes that would break existing policies, modeled on `buf breaking`.

```text
sigil breaking OLD_KIND_FILE NEW_KIND_FILE [flags]
```

It takes only the global flags. It also checks the two numbers in the kind header: it fails when the contract changed but `version` didn't, and when a change is breaking but `accepts` wasn't raised to the new version. The compatibility rules are in [Kind files](/reference/kind-files/#versioning), and [Evolve a kind safely](/guides/evolve-a-kind/) walks through using it in CI. The intended output:

```text
deploy_approval.sigil: breaking: precedence changed
  - deny > review > approve
  + deny > approve > review
  = help: raise `accepts` to 4, so policies pinned to older versions are reviewed before they load
deploy_approval.sigil: breaking: decision deny lost reason `no_release`
  = help: policies that construct deny(no_release) no longer compile; raise `accepts` to 4
```

## `sigil gen go`

::: warning Not implemented yet
`sigil gen go` prints `Error: "sigil gen go" is not implemented yet`, or [an `error` record](#sigil-breaking) with `-o json` or `-o yaml`, and exits with status 1. This section describes the design.
:::

Generates typed Go code from a kind file: a struct for every input type and decision payload, so a second Go service can consume decisions with typed payload structs instead of importing the host or loading the kind dynamically. See [Go API](/reference/go-api/#loading-a-kind-elsewhere).

```text
sigil gen go KIND_FILE [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-p`, `--package` | the kind's name | Go package name of the generated code |
| `--out` | stdout | File to write the generated code to |

## `sigil lsp`

::: warning Not implemented yet
`sigil lsp` prints `Error: "sigil lsp" is not implemented yet`, or [an `error` record](#sigil-breaking) with `-o json` or `-o yaml`, and exits with status 1. This section describes the design.
:::

Runs the Sigil language server, which editors start in the background and talk to over stdin and stdout.

```text
sigil lsp [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `--stdio` | on | Talks to the editor over stdin and stdout, the only transport. Editors pass it by convention |

The server reads the kind file and offers completion for inputs, fields, functions and decision payload keys, and hover that shows a decision's full signature. Editor completion working from a kind file alone is the exit criterion for the editor milestone on the [roadmap](/project/roadmap/).

Imports and invocations get their own support:

- Completion after `use deploy.common.{` lists the module's `pub let`s. Path-first imports are what make this work: the editor knows the file before you type the names.
- Go-to-definition works across imports and into invoked policies.
- A code lens on each invocation summarizes what it contributes, for example "production: 1 approve, 1 review, gated by compliance != pci".
- Hovering an invocation shows its flattened rules, the same view as `sigil explain`, scoped to that call.

## Lints

`sigil check` reports lints as warnings. They don't fail the check unless the repository promotes them to errors.

| Lint | Default | Fires when |
| --- | --- | --- |
| `unused-import` | warn | A `use` binds a name nothing references. Invoking an imported policy counts as a reference |
| `shadowed-kind-name` | warn | A document pinned to an older kind version keeps a name the kind has since given to an input, host function or decision. Rename it and raise the pin (see [Versioning](/reference/kind-files/#adding-a-name-never-breaks-a-policy)) |
| `unused-let` | warn | A `let` that isn't `pub` is never read. Only private lets can be checked, because a `pub let` may have importers in other files |
| `gated-assert` | warn | A policy that contains asserts, directly or through the policies it invokes, is invoked inside `when` and isn't required by `--require`. Its asserts only run while the gate holds |
| `gated-deny` | warn | A policy that contains denies, directly or through the policies it invokes, is invoked inside `when` and isn't required by `--require`. That may be intended, but it's the pattern that silently switches denies off. A deny is a constructor of the decision a `collect one` kind ranks highest; a `collect all` kind has none |
| `duplicate-invocation` | warn | A document invokes the same policy twice with identical arguments, in any order |
| `qualified-imports` | off | A selective import is used. For teams that want Go-style provenance at every use site |
| `path-matches-name` | off | A file holds a document whose name doesn't match the file's path (see [File names](/reference/policy-files/#file-names)). Repositories that protect required policies with CODEOWNERS should promote it to an error |

Lints only run on a bundle that type-checks, since they read what the checker learned about each document.

### `sigil.yaml`

A repository configures lints in `sigil.yaml`, which `sigil check` looks for in the working directory and then in each parent, using the first one it finds; `--config` names a file instead. The file holds one key, `lints`, a map from lint name to level. Each lint is `off`, `warn` or `error`, and lints the file doesn't name keep their defaults:

```yaml
# sigil.yaml
lints:
  gated-deny: error
  path-matches-name: error
  qualified-imports: warn
```

An unknown lint name, level or key is an error, so a typo can't leave a lint at its default without anyone noticing.

## `policytest` for Go hosts

Package `policytest` runs the same test files from `go test`. Policies then ship with table tests next to the code that consumes them, and the tests use the host's real Go types and function implementations, which sidesteps the binding question the stock CLI has.

```go
//go:embed policies
var policies embed.FS

func TestPolicies(t *testing.T) {
	sub, _ := fs.Sub(policies, "policies")
	policytest.Run(t, Deploy, sub, policy.Require("deploy.guardrails"))
}

func TestKindFileIsCurrent(t *testing.T) {
	policytest.Schema(t, Deploy, "policies/deploy_approval.sigil")
}
```

`Run` loads each test file's policy with `Load` and the options given, the same ones the host passes, and runs every test file in the `fs.FS` as a subtest named after its path, with a subtest per case, so `go test -run` selects them. `Schema` fails when the checked-in kind file isn't the kind's `Schema()`. A conflict needs a Go test of its own, next to these; see [Testing a conflict](#testing-a-conflict). See [Testing policies](/reference/go-api/#testing-policies) in the Go API.

## Error messages

Error messages follow the format of [filt-rs](https://github.com/SierraSoftworks/filters): file, line and column, the severity, what went wrong, and, when there's an obvious one, a concrete fix after `= help:`. Unless the file's path matches the document's name, the name follows the position: `policies.sigil:42:5 (payments.production): error: ...`. Several diagnostics are separated by a blank line.

```text
deploy/production.sigil:9:16: error: unknown field "teir" on type Service
  |
9 |   when service.teir == "critical"
  |                ^^^^
  = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

A missing payload field quotes the decision's declaration from the kind in its help, so the author sees what `review` expects without opening another file:

```text
payments/production.sigil:8:3: error: decision review needs field "approvers"
  |
8 |   review(service_owner)
  |   ^^^^^^^^^^^^^^^^^^^^^
  = help: review is declared as: decision review(approvers: list<string>) { service_owner }
```

The same format applies to every tool and to errors returned from the Go API's `Load` and `Compile`.
