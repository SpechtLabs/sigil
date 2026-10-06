---
title: CLI & editor tooling
icon: mdi:console
createTime: 2026/09/24 22:30:00
permalink: /reference/cli/
---

Every `sigil` command: its synopsis, flags, behavior, output and exit status. The files the commands read have pages of their own: [test files](/reference/test-files/) and [the configuration file](/reference/config/).

The tools read the exported kind file (`deploy_approval.sigil` in the running example) like any other input, so they work in a team's policy repository without the host's Go code.

| Command | Does | Needs |
| --- | --- | --- |
| [`sigil fmt`](#sigil-fmt) | Rewrites files into the one canonical style, like `gofmt` | Nothing |
| [`sigil check`](#sigil-check) | Parses, type-checks and compiles policies against their kinds, and lints them | Kind file |
| [`sigil eval`](#sigil-eval) | Evaluates a policy against a JSON or YAML input and prints the result and trace | Kind file; a host binary or stubs for functions |
| [`sigil explain`](#sigil-explain) | Flattens a policy into its guarded decisions, with every invocation inlined | Kind file |
| [`sigil test`](#sigil-test) | Runs test cases: an input plus the expected decision and reason, the asserts that fail, or a runtime error | Kind file; a host binary or stubs for functions |
| [`sigil compile`](#sigil-compile) | Checks policies, then writes a copy of the binary with them compiled in, which evaluates them without any file | Kind file; a host binary for kinds with functions |
| [`sigil export`](#sigil-export) | Writes the kind file of a kind linked into a host binary | A host binary; `sigil --help` lists it only there |
| [`sigil gen go`](#sigil-gen-go) | Generates typed Go code from a kind file, for a Go service that doesn't import the host | Kind file |
| [`sigil breaking`](#sigil-breaking) | Compares two versions of a kind file, classifies every change, and checks `version` and `accepts` | Two kind files |
| [`sigil lsp`](#sigil-lsp) | Runs the language server editors start for diagnostics, completion, hover, go-to-definition and formatting | Kind file |
| [`sigil version`](#sigil-version) | Shows the version and build information | Nothing |

`sigil completion bash|fish|powershell|zsh` prints a shell completion script, and `sigil help <command>` or `--help` (`-h`) prints any command's help. The script completes `--policy` and `--require` with the names of the policies in the command's paths, or in `.` when it names none. It reads only the documents' headers, so it needs no kind file, and it completes `--require` from the `--trusted` paths when the command line has any.

## Inputs

Commands that read policies take their input the way `kubectl -f` does: every argument is a file, a directory or `-` for stdin, and the tool combines all the documents it finds into one bundle. `fmt`, `check`, `eval`, `explain`, `test` and `compile` all follow the same rules, and with no arguments each reads the current directory, `.`.

```text
sigil check   deploy_approval.sigil deploy/*.sigil payments/*.sigil
sigil check   policies.sigil
sigil explain --policy payments.production deploy_approval.sigil deploy/ payments/
cat policies.sigil | sigil eval --input release.json --policy payments.production -
```

| Argument | Reads |
| --- | --- |
| A file | Any number of documents. The shell expands globs; the CLI never does |
| A directory | Every `*.sigil` file below it, at any depth, and for `test` every [test file](/reference/test-files/) too. Entries whose names start with `.` are skipped, as the [loader](/reference/bundles/#loading-files) skips them, so a mounted ConfigMap volume reads as its keys. Symbolic links are followed |
| `-` | One stream from stdin, which may hold several documents. `eval` then needs `--input` naming a file, since stdin can't hold both |

- Documents from all arguments form one bundle, indexed by the names in their headers, as the host's `Load` does. A name defined in two files is an error here too, even when the two documents are of different kinds.
- Every file is read and parsed once, however many arguments name it, and a parse error is reported once.
- There's no Go-style `./...`: a bundle isn't a package per directory, so there's nothing a non-recursive mode would select. To narrow a run, name the files or directories.
- `-R` (`--recursive`) is still accepted by `check`, `eval` and `explain` and does nothing, since directories are always read recursively. It prints a deprecation notice.

### Kinds

Each policy's and module's header names its kind, `policy payments.production: DeployApproval@1`. The command finds that kind in these sources, in this order:

1. The kinds linked into a [host binary](#host-functions-and-host-binaries).
2. The kind documents in the files `--kind` (`-k`) names, and in the files the `kinds` key of [the configuration file](/reference/config/) lists. Repeat `--kind` for several kind files.
3. The kind documents among the inputs, the paths before the `--trusted` paths: a kind file in a directory argument, or a kind document in the same file as the policies, separated by `---`.

- One run can hold documents of several kinds. Each document is checked, compiled and evaluated against its own kind.
- A kind file named with `--kind` contributes only its kind documents. Its other documents count only when the file is also among the inputs.
- The same kind from two sources must be identical, so a kind file both named with `--kind` and found in `.` is fine. A kind document that differs from one in an earlier source, in the order above, is an error at the later one. A `--kind` file that differs from a linked kind fails the command with `doesn't match the kind DeployApproval linked into this binary`, a stale export.
- The inputs' kind documents are checked like every other document, so `sigil check` reports an error in a kind file too.
- A document whose kind no source provides is an error at the kind's name:

```text
payments/production.sigil:1:29: error: policy payments.production is written against kind DeployAproval, but no kind DeployAproval was found
  |
1 | policy payments.production: DeployAproval@1
  |                             ^^^^^^^^^^^^^
  = help: did you mean `DeployApproval`? add its kind file to the paths or name it with --kind
```

The root policy:

- `eval` needs a root policy; `check`, `fmt` and `test` don't.
- If the bundle holds exactly one policy, that's the root, so a single-file bundle needs no flag. Otherwise `--policy` (`-p`) names it, and leaving it out is an error that lists the policies found.
- `explain` is the exception: `--policy` also takes a pattern such as `'payments.*'`, and without it `explain` explains every policy in the bundle, one after another.
- In a pattern, `*` matches any run of characters, dots included. A pattern that matches nothing is an error.

## Output and exit status

Every command takes two global flags:

| Flag | Default | Does |
| --- | --- | --- |
| `-o`, `--output` | `text` | Output format: `text`, `json` or `yaml`. Every command honors it; each command's section describes its records |
| `--color` | `auto` | When to color text output: `auto`, `always` or `never`. `auto` colors on a terminal, and not when piped or when `NO_COLOR` is set |

`check`, `test`, `compile`, `fmt --check`, `fmt --write`, `export --out`, `gen go --out` and `breaking` end their text output with one line that sums the run up, marked `✓`, `!` or `✗`:

```text
✓ checked 4 files, no problems found
! checked 4 files, 2 warnings
✗ checked 4 files, 1 error and 2 warnings
```

A command that couldn't do its job at all, because a flag is wrong or a file is missing, prints an `Error:` block instead that says what to do:

```text
Error: the kind file couldn't be read

What you can do
  • pass the exported kind file with --kind

Caused by
  • open deploy_approval.sigil: no such file or directory
```

- With `-o json` or `-o yaml`, standard output holds the command's records and nothing else: no summary line, and no styling.
- The records carry what the text shows, including what failed, such as `check`'s error diagnostics or the `error` record of a [failed evaluation](#failed-evaluations).
- The `Error:` block of a command that couldn't run at all still goes to standard error as text.

Every command exits with status 0 on success and 1 on any failure: a usage error, an unreadable file, an error found by `check`, a failed evaluation, a failing test case, a bundle `compile` refuses, an unformatted file under `fmt --check`, a stale file under `export --check` or `gen go --check`, or a kind header that `breaking` finds breaks a rule. There are no other exit codes. Warnings don't change the status, and the status is the same in every output format.

## Host functions and host binaries

A kind file carries each host function's signature but not its implementation.

- The stock `sigil` binary type-checks, formats and explains any policy against the `fn` signatures alone.
- It evaluates a policy until a rule reaches a host function call. A call in a branch that doesn't run doesn't get in the way. A call it reaches is a runtime error that names the function:

```text
runtime error: deploy/common.sigil:5:3: host function split failed: no implementation in this sigil binary
  = help: this sigil binary has only split's signature from the kind file; stub it with `stubs:` in the test file or `--stub split=VALUE` on sigil eval, or evaluate with a host binary built with sigil's pkg/cli, which links the real function in
```

- A stub stands in for a host function in either binary: `eval` takes stubs with `--stub` and `--stubs`, and `test` from a test file's [`stubs:`](/reference/test-files/#stubs). A stub replaces a linked function too.

A host binary is the whole command line with the host's kind linked in by [package `cli`](/reference/go-api/#package-cli):

- It decodes inputs into the host's own Go types and calls its real functions in `eval` and `test`.
- Every command uses the linked kind for the documents written against it, with no kind file. A kind file for the same kind, named with `--kind` or among the inputs, must match it exactly, which catches a stale export.
- With several kinds linked (`cli.WithKind` repeated), each document uses the kind its header names, and `sigil export` takes the kind's name as its argument.
- `sigil version` reports what `cli.WithVersion` set.
- `sigil compile` copies the host binary, so the binary it writes calls the real functions, and it compiles bundles whose kinds have host functions, which the stock binary refuses.

The running example's `deploy.common` calls `split`, so the `eval` and `test` samples on this page come from a host binary. To build one, see [Build a host binary](/guides/host-binary/). `go test` doesn't need one: [`policytest`](/reference/go-api/#package-policytest) runs inside the host.

::: warning Planned
Loading a kind file into a Go service and binding functions to it at run time doesn't exist yet; see [Loading a kind at run time](/project/planned/#loading-a-kind-at-run-time).
:::

## `sigil fmt`

Rewrites policy, module and kind documents into one canonical style. Alias: `sigil format`.

```text
sigil fmt [PATH...] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-w`, `--write` | off | Writes the result back to the files instead of printing it |
| `--check` | off | Only lists the files that aren't formatted, and fails if there are any |
| `-d`, `--diff` | off | Prints a unified diff of every file that would change, even for a single file |

- With no paths, formats the current directory. Directories are searched recursively, and `-` reads stdin.
- Without flags, a single file or stdin prints its formatted source. A directory or several paths print a unified diff of every file that would change, then a line counting them, so `sigil fmt`, which is `sigil fmt .`, shows what `sigil fmt -w` would change.
- The diff is labeled the way `gofmt -d` labels it: `--- path.orig` and `+++ path`. Diff mode exits 0 whether or not a file would change; `--check` is the mode that fails.
- `--write` rewrites the files in place, touches only files that change, keeps their permissions, and lists what it rewrote.
- `--check` prints the path of every file that isn't formatted.
- `--write`, `--check` and `--diff` can't be combined, and `--write` can't write back to stdin.
- A file that doesn't parse is reported with its syntax errors and left alone, and fails the run.
- It rewrites the old decision syntax, `decision approve(bake: duration = 1h) { release_manager }`, and a positional reason, `deny(soak_too_short)`, into the forms `check` accepts; see [Migrate to the new decision syntax](/guides/evolve-a-kind/#migrate-to-the-new-decision-syntax).

The canonical style:

| Construct                                                         | Canonical form                                                                                                                                                               |
| ----------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Indentation                                                       | Two spaces                                                                                                                                                                   |
| Binary operators                                                  | One space around them                                                                                                                                                        |
| Line ends                                                         | No trailing whitespace; one newline at the end of the file                                                                                                                   |
| Document header                                                   | Followed by a blank line                                                                                                                                                     |
| Top-level statement groups                                        | A blank line between groups: `use`s, `param`s, `let`s and `assert`s in policies and modules; `input`s, `fn`s, and the `collect`, `precedence` and `exclusive` lines in kinds |
| `when` block, invocation, `enum`, `type`, `decision`              | Stands alone, with a blank line around it                                                                                                                                    |
| `default` and `conflict` in a kind                                | Together, with a blank line around them and none between them                                                                                                                |
| `decision` body                                                   | `reason:` first, then the payload fields in declaration order, one per line                                                                                                  |
| Decision constructor with a positional reason                     | The reason labeled and placed first: `deny(reason: soak_too_short)`                                                                                                          |
| Blank lines inside a `when` body                                  | Only the author's, never more than one in a row                                                                                                                              |
| Several documents in one file                                     | One `---` line between each pair, with a blank line on each side; none before the first or after the last                                                                    |
| Line breaks                                                       | Follow the author, the way `gofmt` does                                                                                                                                      |
| `and`, `or`, `xor` chain                                          | Breaks only where the source broke next to the operator, always before the operator; continuation lines one level deeper than the statement                                  |
| `let` value on the line after `let x =`                           | Stays there, one level in                                                                                                                                                    |
| List, map or argument list whose first item started a new line    | One item per line and a trailing comma                                                                                                                                       |
| Any other list, map or argument list                              | Joined onto one line                                                                                                                                                         |
| `when` with a single decision, invocation or assert on one line   | Stays on one line: `when frozen { deny(reason: freeze) }`                                                                                                                    |
| Quantifier or filter body whose top level is `and`, `or` or `xor` | Gets parentheses: `any r in actor.roles: (r like "sre-*" and release.hotfix)`. They change nothing, since a body extends as far right as possible                            |
| Other parentheses                                                 | Neither added nor removed                                                                                                                                                    |
| Literals                                                          | Keep their source text, so a raw string stays raw                                                                                                                            |
| Comment on the same line as code                                  | Stays there                                                                                                                                                                  |
| Any other comment                                                 | Its own line, at the indentation of what follows it                                                                                                                          |
| Trailing comments on consecutive lines                            | Aligned                                                                                                                                                                      |

`Schema()` prints kinds in this style, so an exported kind file passes `sigil fmt --check` as it is. Why there's one style: [Why the language looks like this](/understanding/language-choices/).

```text
$ sigil fmt
--- payments/production.sigil.orig
+++ payments/production.sigil
@@ -4,7 +4,7 @@
 use deploy.production
 use deploy.common.{cleared}
 
-guardrails( min_soak:4h )
+guardrails(min_soak: 4h)
 
 when service.labels["compliance"] == "pci" {
   production(approvers: ["payments-leads", "security-leads"])
@@ -15,7 +15,7 @@
 }
 
 when cleared and "payments-sre" in actor.teams {
-    approve(reason: payments_sre,bake: 15m)
+  approve(reason: payments_sre, bake: 15m)
 }
 
 assert("named_actor", actor.name != "")
ℹ 1 of 5 files would be reformatted
  run `sigil fmt -w` on the same paths to rewrite it
```

```text
$ sigil fmt --check .
payments/production.sigil
✗ 1 of 5 files is not formatted
```

`-o json` and `-o yaml` print a list with one record per file, in every mode:

| Field | Holds |
| --- | --- |
| `file` | The path; stdin is `<stdin>` |
| `formatted` | Whether the file was already in the canonical style |
| `written` | `true` when `--write` rewrote it |
| `source` | The formatted source, when `fmt` prints a single file or stdin |
| `diff` | The unified diff to the formatted source, in diff mode, for a file that would change |
| `diagnostics` | The syntax errors of a file that doesn't parse, with the fields of [`check`'s records](#sigil-check) |

A file with diagnostics was left alone, so it has neither `formatted: true` nor a `source` or `diff`.

```text
$ sigil fmt --check -o json .
[
  {
    "file": "deploy_approval.sigil",
    "formatted": true
  },
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

Exits 1 under `--check` when a file isn't formatted, and in any mode when a file doesn't parse.

## `sigil check`

Parses and type-checks policies and modules against their kinds, resolves imports and invocations, detects `let`, import and invocation cycles, compiles every policy, and reports [lints](/reference/lints/).

```text
sigil check [PATH...] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-k`, `--kind` | none | Kind file the inputs don't hold. Repeatable. See [Kinds](#kinds) |
| `--require` | none | Policy every root must invoke unconditionally. Repeatable |
| `--trusted` | none | File or directory read as trusted, as `policy.Trusted` reads a source: no other document may define a name it defines, and the `--require` policies must come from it. Repeatable |
| `-p`, `--policy` | every policy | Name or pattern of the policies to check, with what they use; the roots for `--require`. Repeatable |
| `--config` | the nearest [configuration file](/reference/config/#finding-the-file) | File with the [kind files, trusted paths, requirements and lint levels](/reference/config/), in YAML, JSON or TOML by its extension |

- Needs host function signatures from the kind file, not their implementations.
- Checks every document in the bundle and compiles every policy, including ones no policy imports. Documents of several kinds are checked in one run, and the kind documents among the inputs are checked too.
- Compiling catches what type-checking can't, such as an invocation argument outside its param's `min` and `max`.
- A policy whose params have no defaults, like `deploy.production`, is checked and compiled with them unbound, the way `explain` shows it.
- The summary line counts every file read: the paths, the trusted paths and the kind files, each once. One tree gives one count, with or without `--policy`.
- Errors fail the check; warnings are printed and don't. Both use the [error format](#error-messages), in file and line order, and a warning names its lint.
- When the paths hold no `.sigil` files at all, `check` warns `no .sigil files found, so nothing was checked` and exits 0.

`--policy` narrows the check to the policies it matches and every document they use, directly or through the documents they use, trusted ones included:

- Only those policies are compiled, and only the diagnostics and lint findings in those documents are reported, along with a second definition of one of their names. A parse error outside every document counts when it's in one of their files.
- An error elsewhere in the bundle doesn't stop them from compiling, so `check --policy 'payments.*'` passes while another team's policy is broken. The host's `Load` fails on any error in the bundle, so CI also runs `check` without `--policy`.
- A pattern that matches no policy is an error that lists the policies found:

```text
$ sigil check --policy 'checkout.*'
Error: no policy matches "checkout.*"

What you can do
  • the bundle defines: deploy.guardrails, deploy.production, payments.production
```

Requirements come from the `require` key of [the configuration file](/reference/config/), one entry per required policy with its trusted paths and roots, or from `--require`, `--trusted` and `--policy`. `--require` replaces `require` for that run. `--trusted` adds to the file's `trusted` and keeps `require`, so on its own it requires nothing. `--policy` keeps `require` and narrows each entry's roots to the policies it matches, so `check --policy 'payments.*'` still requires the guardrails of `payments.production`; an entry whose roots `--policy` leaves out is skipped. The flags:

- `--require` makes the check a host makes with [`policy.Require`](/reference/go-api/#require): every root policy must invoke the named policy unconditionally, through top-level invocations only. Repeat it to require several.
- `--trusted` does what [`policy.Trusted`](/reference/go-api/#trusted) does: the documents under those paths resolve first, and no other document may define a name they define. Trusted directories are always read recursively. A `--trusted` path that holds no `.sigil` file is an error, since it would protect nothing: `--trusted platform/vocabulary holds no .sigil files`.
- With `--require`, `--trusted` also does what [`policy.From`](/reference/go-api/#from) does: each required policy must be defined below the `--trusted` paths, and everything it imports and invokes resolves there first.
- A file under a trusted path is read as trusted only, even when a path argument also holds it, so `--trusted deploy/ .` reads `deploy/` once.
- The policies `--policy` matches are the roots of every `--require`. Without it, the roots are the bundle's policies that no other policy invokes, apart from the required ones.
- A required policy applies to the roots of its own kind. A `--require` name no document defines applies to every root, which then fails for not invoking it; in the configuration file, it's an error at the entry.
- Trusted documents, from `--trusted` or the configuration file, aren't part of the bundle, so they're never roots.

```text
sigil check --require deploy.guardrails --trusted deploy/ \
  --policy 'payments.*' deploy_approval.sigil payments/
```

A team's module that takes the name of one under `--trusted`, with no policy required:

```text
$ sigil check --trusted platform/ .
payments/common.sigil:1:8 (deploy.common): error: module deploy.common is defined twice
  |
1 | module deploy.common: DeployApproval@1
  |        ^^^^^^^^^^^^^
  = help: the name belongs to the trusted source, defined at platform/deploy/common.sigil:1:1; documents resolve by name, so each name has one definition

✗ checked 4 files, 1 error
```

To run it in CI, see [Check policies in CI](/guides/ci/#check).

```text
payments/production.sigil:11:3: warning: deploy.guardrails holds deny rules and is invoked under `when` [gated-deny]
   |
11 |   guardrails()
   |   ^^^^^^^^^^^^
   = help: its deny rules only fire while the condition holds; invoke it at the top level, or have the host require it

! checked 4 files, 1 warning
```

`-o json` and `-o yaml` print a list with one record per diagnostic. A clean check prints `[]`.

| Field | Holds |
| --- | --- |
| `severity` | `error` or `warning` |
| `message` | What's wrong |
| `lint` | The lint's name, for a lint finding |
| `file` | The file |
| `document` | The document it's in, when that's known |
| `line` | From 1; left out when the diagnostic has no position |
| `column` | In characters, from 1 |
| `help` | How to fix it |

Every field but `severity` and `message` appears only where it applies.

Exits 1 when there's an error, including a lint set to `error` and a failed requirement.

## `sigil eval`

Evaluates a policy against an input and prints the result and the full trace: every candidate, the outcome, which conditions held for each candidate of the winning decision, and any failing asserts. Alias: `sigil evaluate`.

```text
sigil eval [PATH...] [--input FILE] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-i`, `--input` | stdin | Input document, JSON or YAML, to evaluate against, or `-` for stdin |
| `-k`, `--kind` | none | Kind file the inputs don't hold. Repeatable. See [Kinds](#kinds) |
| `-p`, `--policy` | the bundle's only policy | Name of the policy to evaluate; required when the bundle holds more than one |
| `--config` | the nearest [configuration file](/reference/config/#finding-the-file) | File with the [kind files and trusted paths](/reference/config/) to load, in YAML, JSON or TOML by its extension |
| `--stubs` | none | YAML or JSON file of host function [stubs](/reference/test-files/#stubs), keyed by function name |
| `--stub` | none | `NAME=VALUE`: the host function `NAME` returns `VALUE`, JSON or YAML, whatever its args. Repeatable |

- Candidates read the way a policy writes them, `review(reason: service_owner)`, with the conditions that held after `when` and the payload beneath. The ones in the outcome are marked with `*`.
- Diagnostics and trace entries name the document as well as the position, `policies.sigil:42:5 (payments.production)`. The name is left out when the file's path matches the name, as in `deploy/production.sigil:16:5`. This is the text form of [`policy.Position`](/reference/go-api/#positions).
- A policy that reaches a host function call needs a [host binary](#host-functions-and-host-binaries) or a stub of the function. `--stub` flags apply after the `--stubs` file, in order, and a later stub of a function replaces an earlier one. A stub replaces a linked function too.
- A stub that doesn't parse or doesn't fit the kind fails the command before anything is evaluated, with every problem listed, a file's at its line.
- Only the problems of the policy, the documents it uses and the kind documents stop the evaluation: `payments.production` evaluates while another team's policy in the bundle is broken, which `sigil check` reports.

```text
$ sigil eval --input owner-deploy.json --policy payments.production deploy_approval.sigil deploy/ payments/
payments.production: review(reason: service_owner)
  approvers = ["payments-leads"]

trace: 2 candidates
  * review(reason: service_owner)  payments/production.sigil:14:3 → deploy/production.sigil:16:5
      when service.labels["compliance"] != "pci"
       and cleared
       and service.tier in [standard, internal] and owns_service
      approvers = ["payments-leads"]
    approve(reason: payments_sre)  payments/production.sigil:18:3
      bake = 15m
```

The stock binary with `split` stubbed, in the examples' `policies/` directory:

```text
$ sigil eval --input teams/payments/testdata/owner.json --policy payments.production --stub 'split=[eu, us]'
payments.production: review(reason: service_owner)
  approvers = ["payments-leads", "security-leads"]

trace: 1 candidate
  * review(reason: service_owner)  teams/payments/production.sigil:10:3 → platform/deploy/production.sigil:16:5
      when service.labels["compliance"] == "pci"
       and cleared
       and service.tier in [standard, internal] and owns_service
      approvers = ["payments-leads", "security-leads"]
```

### Input documents

The input is a JSON or YAML object with one key per input the kind declares.

- `--input` names the file, or `-` for stdin. Without it, `eval` reads the input from stdin, unless the bundle comes from stdin or stdin is a terminal; then it fails with `--input is required when ...`.
- `eval` reads the input as JSON first, keeping every number exact, and as YAML when it isn't JSON. An input that's neither fails with both parsers' errors.
- An empty input is an error, not an input with every key missing.

| Input                                        | Rule                                                                                                                                                                                         |
| -------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A key the kind doesn't declare, at any depth | An error with a did-you-mean hint                                                                                                                                                            |
| A missing key                                | Its type's zero value, as after `encoding/json` decoded into the host's struct. For an enum that's the empty string, which isn't a value, so a rule that reads it fails with a runtime error |
| A `duration`                                 | A string in Sigil's syntax, `"1h30m"`. A number is an error: `90` could mean seconds or nanoseconds                                                                                          |
| A `timestamp`                                | An RFC 3339 string, `"2026-09-28T14:00:00Z"`, which YAML may leave unquoted                                                                                                                  |
| An [enum](/reference/types/#enums)           | A string naming one of its values, `"critical"`. Any other string is an error with a did-you-mean hint                                                                                       |
| `null`                                       | Allowed for optionals, lists and maps                                                                                                                                                        |
| A map key of a non-string type               | Written the way its values are: `"3"` for an `int` key                                                                                                                                       |
| A `string` that looks like a date or a number | Quoted in YAML, `"2026-01-01"` or `"1.10"`. Unquoted, YAML reads it as a timestamp or a number, which isn't a string |

```text
Error: bad.json: actor.nmae: unknown field "nmae" on type Actor

What you can do
  • did you mean "name"? declared: name, teams, roles, regions
  • the input is a JSON or YAML object with one key per input the kind declares

Error: badtier.json: service.tier: "standrd" is not a value of Tier

What you can do
  • did you mean `standard`? Tier declares: critical, standard, internal
  • the input is a JSON or YAML object with one key per input the kind declares
```

### Failed evaluations

When the evaluation fails, with a runtime error, a conflict or a failing assert, `eval` prints the fallback the host would act on, then why it failed, with a `= help:` line on what to do about it. The fallback is the kind's default, or no decisions for a collecting kind. After a conflict, a kind that declares a [`conflict`](/reference/kind-files/#conflict) outcome falls back to that instead, and the first line names it as the kind's conflict outcome.

- Most runtime errors get "fix the expression the runtime error points at, or the input it read".
- A host function that isn't linked in gets the advice shown below.
- A stub's `error` fails the call with its message, and a call no entry of a stub's `calls` matches fails with `no stubbed call matches split("eu", ",")`; each gets advice on the stub.
- A failing outcome assert lists the candidates that formed the outcome it read, with their payloads.

```text
payments.production: a runtime error stopped the evaluation, the host falls back to deny(reason: no_rule_matched), the kind's default

runtime error: deploy/common.sigil:5:3: host function split failed: no implementation in this sigil binary
  = help: this sigil binary has only split's signature from the kind file; stub it with `stubs:` in the test file or `--stub split=VALUE` on sigil eval, or evaluate with a host binary built with sigil's pkg/cli, which links the real function in

trace: no rule fired
```

The separation-of-duties assert from the examples' access policies, failing for an auditor who also got deploy rights through on-call:

```text
$ sigil eval --input access/testdata/auditor-sre.json --policy access.main access_grant.sigil access platform/access
access.main: an assert failed, the host falls back to no decisions

assert sod_auditor_deployer failed at access/main.sigil:6:1 → platform/access/guardrails.sigil:11:1
  the outcome it read:
    deployer(reason: oncall)            access/main.sigil:19:3
      ttl = 2h
    auditor(reason: compliance_member)  access/main.sigil:40:3
  = help: the input breaks an assert of the policy; if the input is right, the policy's assumption is wrong

trace: 2 candidates
    deployer(reason: oncall)            access/main.sigil:19:3
      when on_call
      ttl = 2h
    auditor(reason: compliance_member)  access/main.sigil:40:3
      when compliance_member
```

A kind that declares `conflict deny(reason: conflicting_rules)` and leaves two deny reasons unranked, with both firing:

```text
$ sigil eval --input conflict.json --policy access.main access_grant.sigil access
access.main: the candidates conflict, the host falls back to deny(reason: conflicting_rules), the kind's conflict outcome

conflict: collect one: 2 candidates at the top rank
    deny(reason: too_old)            access/main.sigil:14:3
    deny(reason: banned)             access/main.sigil:18:3
  = help: a conflict is a defect in the policy: rank the reasons with precedence, or keep the exclusive outcomes' conditions apart

trace: 3 candidates
    deny(reason: too_old)            access/main.sigil:14:3
    deny(reason: banned)             access/main.sigil:18:3
    allow(reason: team_member)       access/main.sigil:10:3
      ttl = 1h
      scopes = []
```

### Records

`-o json` and `-o yaml` print one record:

| Field | Holds |
| --- | --- |
| `policy` | The root policy |
| `decision`, `reason` | The winner of a `collect one` kind; left out for a collecting kind |
| `payload` | The winner's payload |
| `outcome` | The entries the host acts on; never null |
| `trace` | Every candidate the rules produced, winners first; never null |
| `collect` | `true` for a collecting kind |
| `error` | Why the evaluation failed; left out when it didn't |

Each `outcome` and `trace` entry:

| Field | Holds |
| --- | --- |
| `decision`, `reason`, `payload` | The candidate |
| `policy` | The policy whose rule produced it; left out for the default and the conflict outcome |
| `position` | Of the rule; left out for the default and the conflict outcome |
| `chain` | The invocations it was reached through, outermost first |
| `conditions` | The `when` conditions that held, for a candidate of a winning decision |
| `outcome` | `true` when it's in the outcome the host acts on |

An enum value in a payload is a string holding the value's name, `"critical"`.

The `error` record:

| Field | Holds |
| --- | --- |
| `kind` | `runtime`, `conflict` or `assertion` |
| `phase` | For a failed assert, the phase it failed in: `input`, checked before any rule runs, or `outcome`, checked against the outcome the rules formed |
| `message` | What failed |
| `help` | What to do about it |
| `candidates` | For a conflict, the conflicting candidates, as entries |
| `asserts` | For a failed assert, one record per failing assert: `reason`, `policy` (the policy or module it's in), `position`, `outcome` (the candidates that formed the outcome it read, for an outcome assert), `cause` (the runtime error its condition raised) and `help` (when that error comes with advice of its own) |

Exits 1 when the bundle doesn't check, the root doesn't compile, the input doesn't decode, or the evaluation fails.

## `sigil explain`

Flattens a policy into one list of guarded decisions. Every invocation is inlined, and its gates are pushed down into each rule's condition.

```text
sigil explain [PATH...] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-k`, `--kind` | none | Kind file the inputs don't hold. Repeatable. See [Kinds](#kinds) |
| `-p`, `--policy` | every policy | Name or pattern of the policies to explain |
| `--config` | the nearest [configuration file](/reference/config/#finding-the-file) | File with the [kind files and trusted paths](/reference/config/) to load, in YAML, JSON or TOML by its extension |

```text
$ sigil explain --policy payments.production deploy_approval.sigil deploy/ payments/
payments.production: 7 rules from 3 policies and 1 module

  deny(reason: not_eligible)        payments.production:7 → deploy.guardrails:8
    when not eligible

  deny(reason: soak_too_short)      payments.production:7 → deploy.guardrails:12
    when release.soak < 4h and not release.hotfix

  approve(reason: release_manager)  payments.production:10 → deploy.production:11
    when service.labels["compliance"] == "pci"
     and cleared
     and service.tier == critical and "release_manager" in actor.roles

  review(reason: service_owner)     payments.production:10 → deploy.production:16
    when service.labels["compliance"] == "pci"
     and cleared
     and service.tier in [standard, internal] and owns_service
    with approvers = ["payments-leads", "security-leads"]

  approve(reason: release_manager)  payments.production:14 → deploy.production:11
    when service.labels["compliance"] != "pci"
     and cleared
     and service.tier == critical and "release_manager" in actor.roles

  review(reason: service_owner)     payments.production:14 → deploy.production:16
    when service.labels["compliance"] != "pci"
     and cleared
     and service.tier in [standard, internal] and owns_service
    with approvers = ["payments-leads"]

  approve(reason: payments_sre)     payments.production:18
    when cleared and "payments-sre" in actor.teams
    with bake = 15m
```

| Part           | Shows                                                                                                                                           |
| -------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Header         | The rules, the policies they come from (the explained policy and every policy it invokes), and the modules those policies import from           |
| Rule name      | The rule the way a policy writes it, `deny(reason: not_eligible)`                                                                               |
| Chain          | Every document on the call chain by its full name and line, `payments.production:7 → deploy.guardrails:8`, whatever name the `use` bound it to  |
| `when`         | Every `when` around every call on the chain, joined with `and`. A rule with no condition reads `always`                                         |
| `with`         | The payload                                                                                                                                     |
| Assert entries | `assert named_actor (input)` or `(outcome)`, with the condition under which it's checked after `when` and the condition it checks after `check` |

- Params show as their bound values. A policy explained on its own, without a policy that invokes it, shows a required param by its name, `approvers = approvers`, since nothing binds it.
- `let`s stay by name, so a condition reads the way its author wrote it.

::: warning Planned
`--input`, which would also mark which rules fired and which candidate won, doesn't exist yet; see [explain --input](/project/planned/#explain-input).
:::

`-o json` and `-o yaml` print a list with one record per policy: `policy`, the `policies` and `modules` counts, and `rules`, every rule and then every assert. Each rule:

| Field | Holds |
| --- | --- |
| `kind` | `decision` or `assert` |
| `decision` | The decision a rule constructs; left out for an assert |
| `reason` | A rule's reason, or an assert's name |
| `phase` | `input` or `outcome`, for an assert |
| `chain` | `policy:line` for each step, outermost call first, the rule last |
| `conditions` | Every `when` on the way, outermost first |
| `check` | An assert's own condition |
| `payload` | A rule's payload arguments, as `name = expression` |

[See what a policy adds up to](/getting-started/explain/) walks through this output.

Exits 1 when a policy to explain, or a document it uses, doesn't check or doesn't compile, when a kind document doesn't check, or when `--policy` matches nothing. An error in a document none of them uses doesn't stop the explanation; `sigil check` reports it.

## `sigil test`

Runs the cases of every [test file](/reference/test-files/) it finds.

```text
sigil test [PATH...] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-k`, `--kind` | none | Kind file the inputs don't hold. Repeatable. See [Kinds](#kinds) |
| `--run` | every case | Only runs cases whose name matches this regular expression |
| `-v`, `--verbose` | off | Lists passing cases too |
| `--config` | the nearest [configuration file](/reference/config/#finding-the-file) | File with the [kind files and trusted paths](/reference/config/) to load, in YAML, JSON or TOML by its extension |

- The `.sigil` files it finds form one bundle, and every test file found runs against it, with the kind of the policy it names.
- A test file couldn't run when its policy, a document the policy uses, or a kind document doesn't check; the diagnostics show under the file. An error in another document doesn't stop it.
- A case that reaches a host function needs a [host binary](#host-functions-and-host-binaries) or a [stub](/reference/test-files/#stubs) of the function in its test file; the stock binary fails it with the runtime error.
- A case whose evaluation fails with a runtime error prints `got  a runtime error`, with the error and its help on the lines below.

The output follows `go test`. Here the second case expects the wrong reason:

```text
$ sigil test
--- FAIL: payments/production_test.yaml:10: a short soak is denied
      want deny(reason: not_eligible)
      got  deny(reason: soak_too_short)
FAIL  payments/production_test.yaml  1 of 3 cases failed
✗ 1 of 3 test cases failed in 1 file
```

`-o json` and `-o yaml` print one record per test file:

| Field | Holds |
| --- | --- |
| `file` | The test file |
| `policy` | The policy it tests |
| `error` | Why the file couldn't run: unreadable, invalid, or its policy doesn't compile |
| `cases` | The cases `--run` selects, in file order |
| `cases[].name` | The case's name |
| `cases[].line` | Its line in the test file |
| `cases[].passed` | Whether it passed |
| `cases[].failures` | How the evaluation differs from what the case expects, when it failed |
| `cases[].error` | Why the case couldn't run: its input can't be read or doesn't fit the kind |

Exits 1 when a case fails or a test file is invalid.

## `sigil compile`

Checks a bundle as [`sigil check`](#sigil-check) does, then writes a copy of the running binary with the bundle compiled in. The copy's commands are in [Compiled binaries](#compiled-binaries).

```text
sigil compile --out FILE [PATH...] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `--out` | required | File to write the binary to. There's no `-o`, which is `--output` |
| `-p`, `--policy` | the bundle's only policy | Name of the policy the compiled `eval` evaluates by default. Narrows the bundle to it; see below |
| `-k`, `--kind` | none | Kind file the inputs don't hold. Repeatable. See [Kinds](#kinds) |
| `--require` | none | Policy every root must invoke unconditionally, instead of the configuration's `require`. Repeatable |
| `--trusted` | none | File or directory to read the `--require` policies from, as `policy.From` does. Needs `--require`. Repeatable |
| `--config` | the nearest [configuration file](/reference/config/#finding-the-file) | File with the [kind files, requirements and lint levels](/reference/config/), in YAML, JSON or TOML by its extension |

- Reads and checks the bundle as `sigil check` does: the same kinds, configuration file, requirements, trusted paths and lint levels. An error, a lint set to `error` and a failed requirement included, stops it with `the bundle doesn't check, so nothing was compiled`. Warnings are printed, and it goes on.
- Without `--policy`, it checks every document and compiles in every file it read. The root is the bundle's only policy; a bundle with several has none, and the compiled `eval` then needs `--policy`.
- `--policy` takes a name, or a pattern that matches exactly one policy, and checks the way `check --policy` does: an error in a file that doesn't go into the binary doesn't stop it.
- With `--policy`, the binary holds the files that hold the policy and every document it uses, trusted and required ones included, and the kind files of their kinds. A file goes in whole, so another document in it goes in too, and the binary's `eval --policy` can evaluate it. A requirement goes in only when its required policy does.
- Before it writes anything, `compile` checks what goes into the binary once more, on its own: every policy in it, with the same lints and the requirements that went in. The binary's bundle passes `sigil check` by itself, so a document that shares a file with the `--policy` policy and doesn't check, or breaks a requirement, fails the compile. Move it to a file of its own, or fix it.
- A kind of a document that goes in may not declare host functions the binary doesn't link: the stock binary refuses such a bundle, and a [host binary](#host-functions-and-host-binaries) compiles it with its real functions. With `--policy`, that includes the kinds of the other documents in its files.
- File names are relative to the configuration file's directory, or to the working directory when there's none. A name outside that directory is relative with `..`, and only an absolute name outside both directories stays absolute; stdin is `<stdin>`. Diagnostics and traces of the compiled binary use these names.
- The digest is `sha256:` and the hex SHA-256 of the files' names and contents, the root and the requirements. Since the names are relative to the configuration file, a repository compiles to the same digest from any directory in it and on any machine. Renaming or moving a file changes the digest; the sigil version and the build time don't.
- The binary records when it was compiled: `SOURCE_DATE_EPOCH`, in seconds since 1970, when it's set, otherwise the current time, in UTC. A value that isn't a number fails the command.
- `--out` can't be `-`, a directory, the running binary, or a file `compile` reads: a policy, kind, trusted or configuration file. Each is refused before anything is written.
- The output is a copy of the running binary, of the same size and for the same platform; see [cross targets](/project/planned/#cross-targets-for-compile). It's written to a temporary file next to `--out`, given mode `0755` whatever the umask, and renamed over `--out`. A symbolic link at `--out` is replaced by the binary, not written through.
- The bundle, as JSON compressed with DEFLATE, goes into an area of 1 MiB that the binary reserves at link time. A bundle that doesn't fit fails the command with its size and the limit.
- On macOS, compile updates the binary's ad-hoc signature. It refuses a binary signed with an identity, such as a Developer ID, or with Authenticode on Windows, and a universal macOS binary. Sign the compiled binary instead; see [Sign it for other Macs](/guides/compile/#sign-it-for-other-macs).

Why it's built this way: [How compile works](/understanding/compile/).

In the examples' `policies/` directory, where `sigil.yaml` requires `access.guardrails` from `platform/access`:

```text
$ sigil compile --out gate --policy access.main
✓ compiled access.main from 4 files into gate (sha256:555f77fa9b73…)
```

The same directory without `--policy`, whose deploy policies call `split`:

```text
$ sigil compile --out gate
Error: the bundle needs host functions this binary doesn't implement, so nothing was compiled: DeployApproval@2 declares split

What you can do
  • build a host binary with cli.Main(cli.WithKind(...)) from the pkg/cli package, which links the kind and its functions in, and compile with its compile command
  • or name a policy of a kind without host functions with --policy, which compiles in only that policy and what it uses
```

`-o json` and `-o yaml` print one record:

| Field | Holds |
| --- | --- |
| `out` | The `--out` file, as given |
| `root` | The default policy; empty when there's none |
| `digest` | The bundle's digest |
| `files` | The files compiled in, kind files and trusted files included, each once |
| `bytes` | The size of the encoded bundle in the binary |
| `warnings` | The check's warnings, as [`check`'s records](#sigil-check); left out when there are none |

```text
$ sigil compile --out gate --policy access.main -o json
{
  "out": "gate",
  "root": "access.main",
  "digest": "sha256:555f77fa9b731b1e9fbc2084594b6a09bd7981333ad5366bd984cb23cae828fb",
  "files": 4,
  "bytes": 1410
}
```

When the check fails, `-o json` and `-o yaml` print the diagnostics as [`check`'s records](#sigil-check) instead, and nothing else.

Exits 1 when `--out` is missing or refused, the bundle doesn't check, `--policy` matches no policy of the bundle or several, a kind needs host functions the binary doesn't link, or the binary can't be written.

## Compiled binaries

A binary that `sigil compile` wrote is named after its file, so `./gate --help` shows `gate` in every usage line. It has these commands, plus `help`, `completion` and the global `--output` and `--color`:

| Command | Does |
| --- | --- |
| `eval` | Evaluates a compiled policy against an input and prints the result and trace |
| `explain` | Flattens the compiled policies into their guarded decisions |
| `test` | Runs test files against the compiled policies |
| `version` | Shows the build information and what was compiled in |

- They read no `.sigil` file, kind file or configuration file. The policies, kind files and trusted documents come from the bundle, and a host binary's linked kinds are in every binary it compiles.
- There's no `--kind`, `--config`, `--stub` or `--stubs`: host functions are the real ones a host binary linked in, and the stock binary compiles only kinds without them.
- A binary whose bundle is damaged, for example because its file changed after it was compiled, fails every command with `this binary's compiled policies are damaged`.

### Compiled `eval`

```text
NAME eval [--input FILE] [--policy NAME] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-i`, `--input` | stdin | Input document, JSON or YAML, to evaluate against, or `-` for stdin |
| `-p`, `--policy` | the bundle's root | Name of the compiled policy to evaluate; required when the bundle has no root |

- Takes no paths.
- `--policy` names one of the bundle's policies. A required policy read from a trusted path isn't one of them.
- Inputs, output and exit status are those of [`sigil eval`](#sigil-eval), and its JSON and YAML record has one more field, `bundle`, the digest.

```text
$ ./gate eval access/testdata/sre.json
Error: eval takes no paths, got access/testdata/sre.json: this binary has its policies compiled in

What you can do
  • pass the input with --input or on stdin, such as `gate eval < access/testdata/sre.json`
```

### Compiled `explain`

```text
NAME explain [--policy PATTERN] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-p`, `--policy` | the bundle's root, or every policy when it has none | Name or pattern of the compiled policies to explain |

Takes no paths. The output is that of [`sigil explain`](#sigil-explain).

### Compiled `test`

```text
NAME test [PATH...] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `--run` | every case | Only runs cases whose name matches this regular expression |
| `-v`, `--verbose` | off | Lists passing cases and skipped test files too |

- Finds test files as [`sigil test`](#sigil-test) does, and runs them against the compiled policies. `.sigil` files under the paths are ignored.
- A test file whose policy isn't compiled in is skipped, so the binary runs in a repository that holds other policies' tests too. A line before the summary counts the skipped files, and the summary counts only the files that ran. `-v` lists each skipped file with its policy, and in the JSON and YAML records a skipped file has `skipped: true` and no cases.
- A test file that doesn't parse still fails.

```text
$ ./gate test
ok    access/main_test.yaml  17 cases
skipped 2 test files for policies not compiled in
✓ 17 cases passed in 1 file
```

When every test file it finds is skipped, `test` fails:

```text
$ ./gate test teams
Error: no test files for the compiled policies among teams

What you can do
  • the test files found test checkout.production, payments.production, and this binary has access.main compiled in
```

### Compiled `version`

```text
NAME version [flags]
```

Shows what [`sigil version`](#sigil-version) shows for the binary, then the bundle:

| Line | JSON and YAML field | Holds |
| --- | --- | --- |
| `Bundle` | `digest` | The bundle's digest |
| `Root policy` | `root` | The default policy; left out of the record when there's none |
| `Kinds` | `kinds` | `Name@Version` of every kind the documents are written against |
| `Files` | `files` | The files compiled in, each once |
| `Requires` | `require` | Each requirement compile enforced: `policy`, and the `roots` it applies to |
| `Compiled by` | `compiledBy` | The version of the sigil that compiled it |
| `Built` | `built` | When it was compiled, RFC 3339 in UTC |

The fields are under `bundle` in the record, next to the build information.

```text
$ ./gate version
Version:     (devel)
Commit:      unknown
Commit time: unknown
Go version:  go1.27.1
Platform:    darwin/arm64

Bundle:      sha256:555f77fa9b731b1e9fbc2084594b6a09bd7981333ad5366bd984cb23cae828fb
Root policy: access.main
Kinds:       AccessGrant@1
Files:       4
Requires:    access.guardrails for access.main
Compiled by: (devel)
Built:       2026-10-01T12:30:24Z
```

## `sigil export`

Prints the kind file of the kind linked into a [host binary](#host-functions-and-host-binaries), the text [`Schema()`](/reference/go-api/#kind-methods) returns.

```text
sigil export [KIND] [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `--out` | stdout | Kind file to write instead of printing it |
| `--check` | off | Only compares with the `--out` file, and fails when it's stale. Needs `--out` |

- `--out` writes the file, and says whether it wrote it or found it current.
- With several kinds linked, `KIND` picks one by name: `sigil export DeployApproval`.
- The stock binary links no kind, so `export` there fails with `no kind is linked into this binary` and explains how to build one.
- To run it from `go generate`, see [Build a host binary](/guides/host-binary/#keep-the-export-current).

```text
$ sigil export --check --out ../../policies/deploy_approval.sigil
✓ ../../policies/deploy_approval.sigil is up to date
```

`-o json` and `-o yaml` print one record:

| Field | Holds |
| --- | --- |
| `kind`, `version` | From the kind header |
| `source` | The kind file as the binary exports it |
| `file` | With `--out`: the file |
| `status` | With `--out`: `current` when the file already matched, `written` when `export` wrote it, `stale` when `--check` found it out of date |

```text
$ sigil export --check --out ../../policies/deploy_approval.sigil -o yaml
kind: DeployApproval
version: 1
file: ../../policies/deploy_approval.sigil
status: stale
source: |
  kind DeployApproval version 1

  enum Tier: critical | standard | internal
  ...
```

Exits 1 when no kind is linked, `KIND` names none of them, or `--check` finds the file stale.

## `sigil version`

Shows the release version, plus the commit, commit time, Go version and platform the binary was built with.

```text
sigil version [flags]
```

- Takes only the global flags.
- The commit details come from the version control information the Go toolchain embeds in every build; a binary built with `go run` reports them as unknown.
- A host binary reports the version it set with `cli.WithVersion`.

`-o json` and `-o yaml` print one object with `version`, `commit`, `commitTime`, `dirty`, `goVersion` and `platform`.

## `sigil breaking`

```text
sigil breaking OLD_KIND_FILE NEW_KIND_FILE [flags]
```

Compares two versions of a kind file, classifies every change by the [compatibility table](/reference/kind-files/#versioning), and checks the new kind's `version` and `accepts`. It takes only the global flags.

- Either file may be `-` for stdin, but not both: `git show main:deploy_approval.sigil | sigil breaking - deploy_approval.sigil`.
- Both files are loaded as `sigil check` loads a kind file. One that doesn't load prints its diagnostics, as `check` does, and nothing is compared.
- Changes are found on the kind, not on its text, so comments and layout never count. Order counts only where the [canonical form](/reference/kind-files/#canonical-form) prints it: reordering an enum's values is a compatible change.
- A rename is a removal and an addition. Nothing guesses that two names mean the same thing.
- Every line names the new file, the one to fix.

| Class | Means | Printed as |
| --- | --- | --- |
| `compatible` | Every policy written against the old kind compiles and decides as before | `compatible` |
| `breaking` | A policy written against the old kind may stop compiling | `breaking`, or `covered` when `accepts` covers it |
| `behavior` | Every policy still compiles, but decisions may change | the same as `breaking` |

A breaking or behavior change is covered when the new `accepts` is the new `version` and above the old one, so every policy pinned to a version the old kind had stops loading until its team raises the pin. A covered change is listed with `= note:` and doesn't fail. An uncovered one has `= help:` naming the number to raise `accepts` to.

The header rules, checked after the changes:

| Rule | Severity | When |
| --- | --- | --- |
| `version_unchanged` | error | The contract changed, but `version` didn't |
| `version_decreased` | error | `version` went down |
| `accepts_not_raised` | error | A change is breaking or breaking in behavior, and `accepts` doesn't cover it |
| `accepts_lowered` | error | `accepts` went down |
| `version_only` | note | `version` went up, but the contract didn't change |

```text
$ git show main:deploy_approval.sigil | sigil breaking - deploy_approval.sigil
deploy_approval.sigil: breaking: enum Plan declares `standard`, which Tier declares too
  = help: a bare `standard` without context becomes ambiguous; raise `accepts` to 4, and qualify it as `Tier.standard`
deploy_approval.sigil: compatible: input region was added
deploy_approval.sigil: breaking: decision deny lost reason `no_release`
  = help: policies that construct deny(reason: no_release) no longer compile; raise `accepts` to 4
deploy_approval.sigil: breaking: precedence changed
  - deny > review > approve
  + deny > approve > review
  = help: every policy still compiles, but the decisions rank differently; raise `accepts` to 4, so policies pinned to older versions are reviewed before they load
deploy_approval.sigil: error: 3 breaking changes, but `accepts` is 2
  = help: raise `accepts` to 4, so policies written against version 3 or earlier are reviewed before they load

✗ DeployApproval 3 → 4: 3 breaking changes and 1 compatible change
```

With `kind DeployApproval version 4, accepts: 4`, the same changes are covered:

```text
deploy_approval.sigil: covered: enum Plan declares `standard`, which Tier declares too
  = note: a bare `standard` without context becomes ambiguous
deploy_approval.sigil: compatible: input region was added
deploy_approval.sigil: covered: decision deny lost reason `no_release`
  = note: policies that construct deny(reason: no_release) no longer compile
deploy_approval.sigil: covered: precedence changed
  - deny > review > approve
  + deny > approve > review
  = note: every policy still compiles, but the decisions rank differently

✓ DeployApproval 3 → 4: 3 breaking changes that `accepts: 4` covers and 1 compatible change
```

`-o json` and `-o yaml` print one object:

| Field | Holds |
| --- | --- |
| `old`, `new` | Each file's `file`, and its header's `kind`, `version` and `accepts` |
| `ok` | Whether the new header breaks no rule. The exit status follows it |
| `changes` | Every change, in the order the kinds declare what changed |
| `problems` | The header rules the new kind breaks, each with `rule`, `severity`, `message` and `help`, then the notes |
| `minVersion`, `minAccepts` | The lowest `version` and `accepts` the new kind may declare |

Each change has:

| Field | Holds |
| --- | --- |
| `change` | `added`, `removed`, `changed`, `reordered`, or `ambiguous` for an enum value a bare name can no longer resolve |
| `class` | `compatible`, `breaking` or `behavior` |
| `covered` | Whether `accepts` covers a breaking or behavior change |
| `path` | What changed, as the kind file spells it: `input region`, `enum Tier value batch`, `type Release field soak`, `fn split`, `decision deny reason no_release`, `decision approve field bake`, `collect`, `precedence`, `precedence deny`, `exclusive approve, deny`, `default`, `conflict` or `kind`. A reorder names the list: `enum Tier values`, `type Release fields`, `decision deny reasons`, `decision approve fields`, `enums`, `types`, `inputs`, `functions`, `decisions` or `exclusive` |
| `old`, `new` | The declaration as each kind file writes it, on one line; left out where that kind doesn't declare it |
| `message` | What changed |
| `help` | For a breaking or behavior change, why it breaks, then the fix unless it's covered |

```json
{
  "change": "removed",
  "class": "breaking",
  "covered": false,
  "path": "decision deny reason no_release",
  "old": "no_release",
  "message": "decision deny lost reason `no_release`",
  "help": "policies that construct deny(reason: no_release) no longer compile; raise `accepts` to 4"
}
```

A kind file that doesn't load prints its diagnostics as the records `check` prints.

It exits with status 0 when the new header breaks no rule, covered breaking changes included, and 1 when a file can't be read, a kind file doesn't load or a header rule is broken. To run it in CI, see [Check for breaking changes in CI](/guides/evolve-a-kind/#check-for-breaking-changes-in-ci).

## `sigil gen go`

Generates a Go file from a kind file: Go types for the kind and a constructor that builds it with [`policy.NewKind`](/reference/go-api/#newkind), for a Go service that doesn't import the host that defines the kind.

```text
sigil gen go KIND_FILE [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-p`, `--package` | the kind's name in lower case | Package name of the generated file. A Go identifier that isn't a keyword |
| `--out` | stdout | File to write the code to instead of printing it. Its directory is created |
| `--check` | off | Only compares with the `--out` file, and fails when it's stale. Needs `--out` |

- `KIND_FILE` is one kind file, or `-` for stdin.
- `--out` writes the file, and says whether it wrote it or found it current.
- Aliases: `sigil generate go`, `sigil gen golang`.

What the file declares, for the running example's `DeployApproval`:

| Kind file | Go |
| --- | --- |
| `enum Tier: critical \| standard \| internal` | `type Tier string`, and the constants `TierCritical`, `TierStandard`, `TierInternal` |
| `type Service { ... }` | `type Service struct`, one field per field, tagged `policy:"name"` |
| the `input`s | `type Input struct`, one field per input; `NewKind`'s type parameter |
| `decision approve { ... }` | `type ApproveData struct`, its payload, with defaults in the tags: `policy:"bake,default=1h"` |
| the decisions | `var Deny`, `Review`, `Approve`: `policy.Decision` handles |
| the reasons | `var DenyNoRuleMatched`, `ApprovePaymentsSre`, ...: `policy.Outcome` handles, `<Decision><Reason>` |
| the `fn`s | `type Funcs struct`, one `func(...) (T, error)` field per function. Only when the kind declares one |
| the kind | `func NewKind(funcs Funcs, opts ...policy.Option) *policy.Kind[Input]`, without `funcs` when the kind declares no function |

- Types map as in [Go type mapping](/reference/go-api/#go-type-mapping), with one Go type per Sigil type: `int` is `int64`, `float` is `float64`, `duration` is `time.Duration`, `timestamp` is `time.Time`, `?T` is `*T`, `list<T>` is `[]T`, `map<K, V>` is `map[K]V`.
- Every decision has a payload type of its own, an empty struct when it has no payload fields, so a type switch on `Result.Value` has one case per decision.
- `NewKind` builds the kind with the declaring options in kind-file order, then `opts`. Its `Schema()` is the kind file, byte for byte, so `Load` accepts a bundle that holds the kind file. An option in `opts` that declares contract, such as `WithVersion`, makes the two differ.
- `NewKind` panics when a field of `funcs` is nil, naming every nil field: `approval.NewKind: no implementation for host functions: Funcs.Split`.
- The file starts with `// Code generated by sigil gen go. DO NOT EDIT.` and names the kind's version, not sigil's, so it changes only when the kind does.

Names:

| Declaration | Go name |
| --- | --- |
| Struct type, enum | The kind's name, unchanged. A name Go doesn't export, such as `service`, also gets an exported alias, `type Service = service` |
| Everything else | Each `_`-separated word capitalized, initialisms such as `id`, `ttl` and `url` in capitals: `user_id` is `UserID`. A name that would start with a digit gets an `X` |
| A clash | Two declarations with one Go name: the first in this order keeps it, a later one gets a number from 2: struct types and enums, their aliases, `Input` (then `<Kind>Input`), `Funcs`, `NewKind`, the decisions, each followed by its payload type and reasons, the enum values. Fields number within their struct |
| `time` | Imported as `gotime` when a struct type or enum is called `time` |

`gen go` refuses a kind file Go can't declare exactly, and names each declaration with a fix. A kind file a host exported already declares everything in `NewKind`'s order.

| Refused | Why |
| --- | --- |
| A struct type or enum named after a Go keyword or predeclared identifier, `init` or `_` | The generated type keeps the kind's name |
| Struct types or enums in another order than `NewKind` declares them | `NewKind` declares them in the order the inputs, host functions and payloads first use them; enums nothing uses come last |
| A struct type nothing uses | `NewKind` declares only the types it reaches |
| A `collect one` precedence other than the decisions' declaration order | `WithDecisions` declares the decisions in precedence order |
| Payload arguments on `default` or `conflict` | `WithDefault` and `WithConflict` take a reason; the payload comes from the field defaults |
| A package name, `--package` or the kind's, that isn't a Go identifier or is a keyword | It names the package |

```text
deploy_approval.sigil:3:6: error: a Go kind can't declare type Release here
  |
3 | type Release {
  |      ^^^^^^^
  = help: a Go kind declares struct types in the order its inputs, host functions and payloads first use them; declare them as Service, Release, which changes no policy
```

```text
$ sigil gen go --check --package approval --out approval/kind.go deploy_approval.sigil
✓ approval/kind.go is up to date
```

`-o json` and `-o yaml` print one record:

| Field | Holds |
| --- | --- |
| `kind`, `version` | From the kind header |
| `package` | The generated file's package name |
| `code` | The generated Go source |
| `file` | With `--out`: the file |
| `status` | With `--out`: `current` when the file already matched, `written` when `gen go` wrote it, `stale` when `--check` found it out of date |

A kind file that doesn't load, or that Go can't declare, prints its diagnostics as [`check`](#sigil-check) does instead.

Exits 1 when the kind file can't be read, doesn't load or can't be declared in Go, a flag is wrong, or `--check` finds the file stale. To use the generated code in a service, see [Use a kind from another Go service](/guides/generate-go/).

## `sigil lsp`

Runs the Sigil language server, which an editor starts in the background and talks to over stdin and stdout with the [Language Server Protocol](https://microsoft.github.io/language-server-protocol/) 3.17. To connect an editor, see [Set up your editor](/guides/editors/).

```text
sigil lsp [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `--stdio` | on | Talks to the editor over stdin and stdout, the only transport. Editors pass it by convention |

- Stdout carries protocol messages only. Logs go to stderr, which editors keep in their language server log.
- It needs the kind files, not the host's Go code. A host binary's linked kinds count too, as for `check`.
- Exits 0 after the editor's `shutdown` and `exit`, and 1 when the editor exits or closes stdin without `shutdown`, or the stream breaks.

### Projects

The server reads each open document's project the way `sigil check` run in the project's root reads it:

| The document is | The root is | It reads |
| --- | --- | --- |
| At or below a directory with a [configuration file](/reference/config/#finding-the-file) | The nearest such directory | The root, with the file's kind files, trusted paths, requirements and lint levels |
| Below a workspace folder, with no configuration file above it | The deepest workspace folder holding it | The folder, with the default lint levels |
| Outside every folder and configuration file | The document itself | The document alone |

- Open documents replace their files on disk, and a new `.sigil` document below the root counts before it's saved.
- A project is read again 200 ms after its last change, and at once when a document opens or closes, or when a request needs it.
- Workspace folders added or removed by the editor move documents to their new roots.

### Diagnostics

| Behavior | Rule |
| --- | --- |
| What's reported | Exactly what `sigil check` reports at the root: parse, check and compile errors, requirements, and lints at the configured levels |
| Which files | Every file of the project with a problem, open or not |
| Severity | `error` or `warning`, as `check` prints it; a lint's name is the diagnostic's code |
| Message | The message, then `help:` and the fix on the next line |
| Version | The document's version the diagnostics were computed from, for an open document |
| On close | The project is read again from disk; when no document of the project is open any more, every diagnostic it published is cleared |

What stops the check, such as a configuration file that doesn't parse or a requirement that can't be enforced, is shown as an error message instead. The documents still get completion, hover and definition.

### Completion

Completion works in a document that doesn't parse, as it's being typed.

| Where | Offers |
| --- | --- |
| Start of a file, or after `---` | `policy`, `module` |
| After `policy name:` or `module name:` | The kinds the project knows |
| After `Kind@` | The kind's current version |
| Start of a statement | `use`, `param`, `let`, `pub let`, `when`, `assert`, and the imported policies to invoke; in a `when` body, the kind's decisions to construct; in a module, `use`, `let` and `pub let` |
| After `use` | The policies and modules of the document's kind, trusted ones included, as dotted names |
| Inside `use path.{` | The pub lets of `path` the import doesn't list yet |
| After `param name:` | The built-in types, `list`, `map`, and the kind's struct types and enums |
| After a param's default and `,` | `min`, `max` |
| An operand | Inputs, lets, imported lets, params, quantifier and filter variables, host functions with their signatures, enum values, enums, imported modules, `any`, `all`, `filter`, `not`, `present`, `true`, `false`; in an assert, the decisions and `outcome` |
| An operand compared with `==` or `!=`, or a payload field's or param's value | The same, with the values of the operand's enum first |
| A value two enums declare | `Enum.value` for each, not the bare value |
| After `x.` or `x?.` | The fields of `x`'s struct type, through optional chaining, indexing and parentheses |
| After `Enum.` | The enum's values |
| After `module.` | The module's pub lets |
| After `outcome.` | The decisions, whose candidates it reads |
| After `outcome.d.`, `d.` | The decision's reasons |
| After a candidate variable and `.` | The decision's payload fields and `reason` |
| After an operand | The keyword operators: `and`, `or`, `xor`, `in`, `not in`, `has`, `like`, `matches`, `all in`, `any in`, `one in`, `exclusive in` |
| Inside `decision(` or after `,` | `reason` and the payload fields not given yet, those without a default first |
| After `reason:` | The decision's reasons |
| Inside `policy(` or after `,` | The invoked policy's params not given yet, required ones first |

Nothing completes inside a comment or a string, or where a name is being declared.

### Hover and definition

| On | Hover shows | Definition goes to |
| --- | --- | --- |
| The kind in a header | The whole kind | The kind document |
| A `use` path or alias, an invoked policy | The document's header, params and pub lets | The document's name |
| An imported let, selective or `module.let` | `pub let name: type` and where it comes from | The let in the other document |
| A decision constructor, or a decision in an assert | The decision's declaration: reasons and payload fields with types and defaults | The decision in the kind document |
| A constructor's `reason:` or payload field | The field, with its type and default | The field in the kind document |
| A reason | The reason and its decision's declaration | The reason in the kind document |
| An invocation's argument | The param with its type, default and bounds | The param in the invoked policy |
| An input, a field after `.` | The name and its type, with the struct type's or enum's declaration | The declaration in the kind document |
| A host function | Its signature | The `fn` in the kind document |
| An enum value or enum | The enum's declaration | The value or enum in the kind document |
| A let, param, quantifier or filter variable | The name and its type | Its declaration |

A kind linked into a host binary has no kind document, so definition finds nothing for its names.

### Formatting

Formatting the document applies [`sigil fmt`](#sigil-fmt)'s canonical style as one edit; the editor's formatting options are ignored. A document that doesn't parse can't be formatted, and the request fails with its first syntax error.

## Error messages

Error messages follow the format of [filt-rs](https://github.com/SierraSoftworks/filters): file, line and column, the severity, what went wrong, and, when there's an obvious one, a concrete fix after `= help:`.

- Unless the file's path matches the document's name, the name follows the position: `policies.sigil:42:5 (payments.production): error: ...`.
- Several diagnostics are separated by a blank line.
- A missing payload field lists the decision's reasons and fields from the kind in its help.
- The same format applies to every tool and to errors returned from the Go API's `Load` and `Compile`.

```text
deploy/production.sigil:9:16: error: unknown field "teir" on type Service
  |
9 |   when service.teir == critical
  |                ^^^^
  = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

```text
payments/production.sigil:8:3: error: decision review needs field "approvers"
  |
8 |   review(reason: service_owner)
  |   ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
  = help: review takes reason: service_owner, and approvers: list<string>
```
