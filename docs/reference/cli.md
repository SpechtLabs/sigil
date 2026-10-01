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
| [`sigil export`](#sigil-export) | Writes the kind file of a kind linked into a host binary | A host binary; `sigil --help` lists it only there |
| [`sigil version`](#sigil-version) | Shows the version and build information | Nothing |

`sigil completion bash|fish|powershell|zsh` prints a shell completion script, and `sigil help <command>` or `--help` (`-h`) prints any command's help. The script completes `--policy` and `--require` with the names of the policies in the command's paths, or in `.` when it names none. It reads only the documents' headers, so it needs no kind file, and it completes `--require` from the `--trusted` paths when the command line has any.

`sigil breaking`, `sigil gen go` and `sigil lsp` are planned:

| Command | Will | Needs |
| --- | --- | --- |
| [`sigil breaking`](#sigil-breaking) | Compare two kind versions and flag incompatible changes | Two kind files |
| [`sigil gen go`](#sigil-gen-go) | Generate typed Go code from a kind file | Kind file |
| [`sigil lsp`](#sigil-lsp) | Run the language server | Kind file |

::: warning Planned
`sigil breaking`, `sigil gen go`, `sigil lsp` and `explain --input` aren't implemented. The first three still run, so their help is there (`sigil breaking --help`), but `sigil --help` doesn't list them, and each one only prints an error. Their designs are in [Planned designs](/project/planned/), and the [roadmap](/project/roadmap/) tracks them.
:::

## Inputs

Commands that read policies take their input the way `kubectl -f` does: every argument is a file, a directory or `-` for stdin, and the tool combines all the documents it finds into one bundle. `fmt`, `check`, `eval`, `explain` and `test` all follow the same rules, and with no arguments each reads the current directory, `.`.

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

`check`, `test`, `fmt --check`, `fmt --write` and `export --out` end their text output with one line that sums the run up, marked `✓`, `!` or `✗`:

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
- The records carry what the text shows, including what failed, such as `check`'s error diagnostics or the `error` record of a [planned command](#sigil-breaking).
- The `Error:` block of a command that couldn't run at all still goes to standard error as text.

Every command exits with status 0 on success and 1 on any failure: a usage error, an unreadable file, an error found by `check`, a failed evaluation, a failing test case, an unformatted file under `fmt --check` or a stale file under `export --check`. There are no other exit codes. Warnings don't change the status, and the status is the same in every output format.

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
- `sigil check` doesn't compute costs; see [Static cost analysis](/project/planned/#static-cost-analysis).

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
- `--trusted` does what [`policy.Trusted`](/reference/go-api/#trusted) does: the documents under those paths resolve first, and no other document may define a name they define. Trusted directories are always read recursively.
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

::: warning Planned
Not implemented. It prints `Error: "sigil breaking" is not implemented yet` and exits with status 1. The design is in [sigil breaking](/project/planned/#sigil-breaking).
:::

With `-o json` or `-o yaml`, `breaking`, `gen go` and `lsp` print the error as a record on standard output instead, in the shape of the `error` record [`sigil eval`](#records) prints for a failed evaluation, and still exit with status 1:

```json
{
  "error": {
    "kind": "not_implemented",
    "message": "\"sigil breaking\" is not implemented yet",
    "help": "the command is planned for a later milestone; track progress at https://github.com/SpechtLabs/sigil/blob/main/roadmap.yml"
  }
}
```

## `sigil gen go`

```text
sigil gen go KIND_FILE [flags]
```

::: warning Planned
Not implemented. It prints `Error: "sigil gen go" is not implemented yet`, or [an `error` record](#sigil-breaking) with `-o json` or `-o yaml`, and exits with status 1. The design is in [sigil gen go](/project/planned/#sigil-gen-go).
:::

Aliases: `sigil generate go`, `sigil gen golang`.

## `sigil lsp`

```text
sigil lsp [flags]
```

::: warning Planned
Not implemented. It prints `Error: "sigil lsp" is not implemented yet`, or [an `error` record](#sigil-breaking) with `-o json` or `-o yaml`, and exits with status 1. The design is in [sigil lsp](/project/planned/#sigil-lsp).
:::

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
