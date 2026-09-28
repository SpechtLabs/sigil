---
title: CLI & editor tooling
icon: mdi:console
createTime: 2026/09/24 22:30:00
permalink: /reference/cli/
---

::: warning Partly implemented
`sigil fmt`, `check`, `eval`, `explain`, `test` and `export` exist, and so does the `policytest` package. `sigil breaking`, `sigil gen go` and `sigil lsp` are planned, and so is `explain --input`. Flags and output formats may still change before the first release.
:::

All tooling reads the exported kind file (`deploy_approval.sigil` in the running example), so it works in a team's policy repository without the host's Go code. One `sigil` binary covers the command line.

| Command          | Does                                                                                         | Needs                                  |
| ---------------- | -------------------------------------------------------------------------------------------- | -------------------------------------- |
| `sigil fmt`      | Rewrites files into the one canonical style, like `gofmt`                                    | Nothing                                |
| `sigil check`    | Parses, type-checks and compiles policies against a kind, and lints them                     | Kind file                              |
| `sigil eval`     | Evaluates a policy against a JSON input and prints the result and trace                      | Kind file; a host binary for functions |
| `sigil explain`  | Flattens a policy into its guarded decisions, with every invocation inlined                  | Kind file                              |
| `sigil test`     | Runs test cases: an input plus the expected decision and reason, or the asserts that fail    | Kind file; a host binary for functions |
| `sigil export`   | Writes the kind file of a kind linked into a host binary                                     | A host binary                          |
| `sigil breaking` | Compares two kind versions and flags incompatible changes (planned)                          | Two kind files                         |
| `sigil gen go`   | Generates typed Go code from a kind file (planned)                                           | Kind file                              |
| `sigil lsp`      | Completion from the kind and imports, hover, go-to-definition across imports and invocations (planned) | Kind file                    |

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
- **Directories.** A directory contributes the `*.sigil` files directly inside it. `-R` (`--recursive`) includes subdirectories too. Entries whose names start with `.` are skipped, as the [loader](/reference/policy-files/#loading-files) does, so pointing the CLI at a mounted ConfigMap volume works. `fmt` and `test` always search directories recursively.
- **Stdin.** `-` reads one stream, which may hold several documents. It's how a CI job checks a key extracted from a rendered ConfigMap.
- **One bundle.** Documents from all arguments are indexed by the names in their headers, exactly as the host's `Load` does, so a name defined in two files is an error here too.
- **The kind stays separate.** `--kind` names the kind file, and it's never part of the bundle. A kind document for the same kind found among the inputs, for example because a glob matched the exported file, must match `--kind` exactly, or the command fails; that's how CI notices an export that wasn't regenerated. Kind documents for other kinds are ignored.

`eval` needs a root policy; `check`, `fmt` and `test` don't. If the bundle holds exactly one policy, that's the root, so a single-file bundle needs no flag. Otherwise `--policy` (`-p`) names it, and leaving it out is an error that lists the policies found. `explain` is the exception: `--policy` also takes a pattern such as `'payments.*'`, and without it `explain` explains every policy in the bundle, one after another, which is handy for reviewing a whole ConfigMap. In a pattern, `*` matches any run of characters, dots included.

Every command that prints a result takes `-o text` (the default), `-o json` or `-o yaml`. Text output is colored on a terminal and plain when piped; `--color always` or `never` overrides that, and so does the `NO_COLOR` environment variable.

Every command ends its text output with one line that sums the run up, marked `✓`, `!` or `✗`, and exits non-zero after a `✗`:

```text
✓ checked 4 files, no problems found
! checked 4 files, 2 warnings
✗ checked 4 files, 1 error and 2 warnings
```

That's the report; a command that couldn't do its job at all, because a flag is wrong or a file is missing, prints an `Error:` block instead that says what to do:

```text
Error: the kind file couldn't be read

What you can do
  • pass the exported kind file with --kind

Caused by
  • open deploy_approval.sigil: no such file or directory
```

## Host functions and host binaries

A kind file carries each host function's signature but not its implementation. The stock `sigil` binary checks, formats and explains any policy, and evaluates it as long as no rule reaches a host function call. A call it reaches is a runtime error that names the function:

```text
runtime error: deploy/common.sigil:5:3: host function split failed: no implementation in this sigil binary; build a host binary with split linked in (see sigil's pkg/cli)
```

A host that wants `eval` and `test` to call its real functions builds its own `sigil` binary. Package `cli` is the whole command line with the host's kind linked in:

```go
package main

import (
	"github.com/spechtlabs/sigil/pkg/cli"

	"example.com/deploygate/policy"
)

func main() {
	cli.Main(cli.WithKind(policy.Deploy))
}
```

That binary decodes inputs into the host's own Go types and calls its functions. Every command uses the linked kind without `--kind`; a `--kind` file for the same kind must match it exactly, which catches a stale export. `cli.WithKind` can be repeated for a host with several kinds, and `--kind` then picks one. `go test` doesn't need any of this: [`policytest`](#policytest-for-go-hosts) runs inside the host.

## `sigil fmt`

Rewrites policy, module and kind documents into one canonical style. It needs nothing but the files themselves. With no paths it formats the current directory; directories are searched recursively. The formatted source is printed unless `--write` (`-w`) rewrites the files in place, which touches only files that change, keeps their permissions, and lists what it rewrote. `--check` prints the path of every file that isn't formatted and fails if there's one, which is what CI runs. A file that doesn't parse is reported and left alone.

```text
$ sigil fmt --check .
payments/production.sigil
✗ 1 of 4 files is not formatted
```

The formatter matters more than it looks. Sigil's grammar is whitespace-insensitive so that templating can't break it, and a whitespace-insensitive grammar lets styles drift: one team indents continuation lines by two spaces, another by four, a third puts `and` at the end of the line. One canonical form keeps diffs across teams readable and makes the formatter's output the only style anyone has to learn.

The canonical style:

- Two spaces of indentation, one space around binary operators, no trailing whitespace, and one newline at the end of the file.
- A blank line after each document's header. Top-level statements group by kind, with a blank line between the groups: `use`s, `param`s, `let`s and `assert`s in policies and modules, and `input`s, `fn`s, and the `collect`, `precedence` and `exclusive` lines in kinds. Every `when` block, invocation, `type`, `decision` and `default` stands alone with a blank line around it. Inside a `when` body only the author's blank lines are kept, never more than one in a row.
- In a file with several documents, one `---` line between each pair, with a blank line on each side, and none before the first or after the last.
- Line breaks follow the author, the way `gofmt` does. An `and`, `or` or `xor` chain breaks only where the source broke next to the operator, and the break always goes before the operator, with continuation lines one level deeper than the statement. A `let` value written on the line after `let x =` stays there, one level in. A list, map or argument list whose first item started a new line gets one item per line and a trailing comma; any other list is joined onto one line.
- A `when` with a single decision, invocation or assert written on one line stays on one line: `when frozen { deny(freeze) }`.
- A quantifier body whose top level is `and`, `or` or `xor` gets parentheses, so its extent is visible: `any r in actor.roles: (r like "sre-*" and release.hotfix)`. The parentheses change nothing, since a body extends as far right as possible; they only show where it ends. No other parentheses are added or removed.
- Literals keep their source text, so a raw string stays raw.
- A comment on the same line as code stays there; any other comment gets its own line at the indentation of what follows it. Trailing comments on consecutive lines are aligned.

`Schema()` prints kinds in this style, so an exported kind file passes `sigil fmt --check` as it is.

## `sigil check`

Parses and type-checks policies and modules against a kind, resolves imports and invocations, detects `let`, import and invocation cycles, compiles every policy, and reports [lints](#lints). This is the command a policy repository runs in CI. It doesn't need host function implementations, only their signatures from the kind file.

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

A directory named on the command line contributes only the files directly inside it, so a check that finds no `.sigil` files at all says so instead of passing quietly.

`-o json` and `-o yaml` print every diagnostic as a record with `severity`, `lint`, `file`, `document`, `line`, `column`, `message` and `help`, for annotating pull requests.

`--require` makes the same check a host makes with `policy.Require`: every root policy must invoke the named policy unconditionally, through top-level invocations only. Repeat the flag to require several. A policy repository that runs it in CI finds a gated or missing guardrail before the host refuses to load the policy.

```text
sigil check --kind deploy_approval.sigil \
  --require deploy.guardrails --trusted deploy/ \
  --policy 'payments.*' payments/
```

- `--trusted` does what `policy.From` does in the host: required policies, and everything they import and invoke, are read from those paths, and the bundle may not define any name they define. Run CI with the same trusted source the host uses.
- `--policy` names the roots, by name or by pattern, and can be repeated. Name them in CI. Without `--policy`, the roots are the bundle's policies that no other policy invokes, apart from the required ones. That guess misfires on a library bundle: checked on its own, the platform's `deploy/` has two uninvoked policies, and `deploy.production` would fail `--require` for not invoking the guardrails. With `--trusted deploy/`, the platform's documents aren't part of the bundle, so they're never roots.

Cost reporting comes with the static cost analysis on the [roadmap](/project/roadmap/) (see [Halting by construction](/understanding/halting/)).

## `sigil eval`

Evaluates a policy against a JSON input and prints the result and the full trace: every candidate, the outcome, which conditions held for each candidate of the winning decision, and any failing asserts. Candidates in the outcome are marked with `*`.

```text
$ sigil eval --kind deploy_approval.sigil --input owner-deploy.json --policy payments.production deploy/ payments/
payments.production: review service_owner
  approvers = ["payments-leads"]

trace: 2 candidates
* review   service_owner     payments/production.sigil:14:3 → deploy/production.sigil:16:5
           service.labels["compliance"] != "pci"
           and cleared
           and service.tier in ["standard", "internal"] and owns_service
           approvers = ["payments-leads"]
  approve  payments_sre      payments/production.sigil:18:3
           bake = 15m
```

The input is a JSON object with one key per input the kind declares, and `--input -` reads it from stdin. The decoding is strict where a mistake would go unnoticed and lenient where Go is:

- A key the kind doesn't declare, at any depth, is an error with a did-you-mean hint, so a typo in a fixture fails instead of silently testing the zero value.
- A missing key reads as its type's zero value, as it would after `encoding/json` decoded into the host's struct.
- A `duration` is a string in Sigil's syntax, `"1h30m"`; a `timestamp` is an RFC 3339 string, `"2026-09-28T14:00:00Z"`. A number where a duration belongs is an error, because nobody can tell whether `90` meant seconds or nanoseconds.
- `null` is allowed for optionals, lists and maps. Map keys of a non-string type are written the way their values are: `"3"` for an `int` key.

When the evaluation fails, with a runtime error, a conflict or a failing assert, `eval` prints the fallback the host would act on, the kind's default, then why it failed with a `= help:` line on what to do about it, and exits non-zero.

Diagnostics and trace entries name the document as well as the position, `policies.sigil:42:5 (payments.production)`, so a trace stays readable when many documents share one file. The name is left out when the file's path matches the name, as in `deploy/production.sigil:16:5`. This is the text form of [`policy.Position`](/reference/go-api/); whether the CLI should always print the name is [open](/project/open-questions/#document-names-in-text-output).

## `sigil explain`

Flattens a policy into one list of guarded decisions. Every invocation is inlined, and its gates are pushed down into each rule's condition. Invocation makes composition flexible; `explain` keeps it transparent, so the answer to "what does this policy actually do" is one command away.

```text
$ sigil explain --kind deploy_approval.sigil --policy payments.production deploy/ payments/
payments.production: 7 rules from 4 policies

deny     not_eligible      payments.production:7 → guardrails:8
         not eligible

deny     soak_too_short    payments.production:7 → guardrails:12
         release.soak < 4h and not release.hotfix

approve  release_manager   payments.production:10 → deploy.production:11
         service.labels["compliance"] == "pci"
         and cleared
         and service.tier == "critical" and "release_manager" in actor.roles

review   service_owner     payments.production:10 → deploy.production:16
         service.labels["compliance"] == "pci"
         and cleared
         and service.tier in ["standard", "internal"] and owns_service
         approvers = ["payments-leads", "security-leads"]

approve  release_manager   payments.production:14 → deploy.production:11
         service.labels["compliance"] != "pci"
         and cleared
         and service.tier == "critical" and "release_manager" in actor.roles

review   service_owner     payments.production:14 → deploy.production:16
         service.labels["compliance"] != "pci"
         and cleared
         and service.tier in ["standard", "internal"] and owns_service
         approvers = ["payments-leads"]

approve  payments_sre      payments.production:18
         cleared and "payments-sre" in actor.teams
         bake = 15m
```

Each entry names the decision, the reason and the call chain that reaches the rule, then the rule's full condition. A document is named by its last segment, or its full name when two documents share one, so `payments.production` and `deploy.production` read `payments.production:7 → deploy.production:16`. Asserts appear as entries too, marked `assert` in the decision column and `input` or `outcome` after the reason, so a reader can tell which ones run before the rules, with the condition under which they're checked and the condition they check: every `when` around every call on the chain, joined with `and`. Params show as their bound values, which is why invocation arguments can't depend on inputs. `let`s stay by name, so a condition reads the way its author wrote it.

A policy explained on its own, without a policy that invokes it, shows a required param by its name, `approvers = approvers`, since nothing binds it. A planned `--input` flag will also mark which rules fired and which candidate won; like `eval`, that needs a host binary for policies that call host functions.

[The tour](/getting-started/tour/#what-the-team-policy-adds-up-to) walks through this output.

## `sigil test`

Runs test cases, each an input plus the expected decision and reason. Asserting on the reason as well as the decision catches the case where a deploy is denied for the wrong reason, which is a common way policy regressions hide.

Test cases live in YAML files named `*_test.yaml`, next to the policies they test, one file per policy:

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

- **The input** is inline under `input:`, written in YAML, or in a file named by `input_file:`, relative to the test file: JSON, or YAML when the name ends in `.yaml`. Either way it's decoded by `eval`'s rules.
- **`decision` and `reason`** expect one outcome of a `collect one` kind; both are required. `payload` lists the fields to compare, in the same encoding as the input, and fields it leaves out aren't checked.
- **`outcome`** expects the whole outcome of a `collect all` kind: a list of `decision`, `reason` and optional `payload` entries, matched in any order. It passes only if the outcome holds exactly those entries; `outcome: []` expects nothing to fire.
- **`asserts`** expects the evaluation to fail its asserts, naming their reasons. Any other failing assert, or none, fails the case.

A case expects exactly one of the three. Everything a test file names is checked against the kind before anything runs: an unknown key, decision, reason or payload field is an error with a did-you-mean hint.

Every path is a file or a directory, searched recursively; with no paths, `test` searches the current directory. The `.sigil` files it finds form one bundle, and every test file found runs against it. `--run` takes a regular expression and runs only the cases whose names match it; `-v` lists passing cases too. The output follows `go test`. Here the second case expects the wrong reason:

```text
$ sigil test --kind deploy_approval.sigil
--- FAIL: payments/production_test.yaml:10: a short soak is denied
      got deny(soak_too_short), want deny(not_eligible)
FAIL  payments/production_test.yaml  1 of 3 cases failed
✗ 1 of 3 test cases failed in 1 file
```

Like `eval`, a case that reaches a host function needs a host binary.

## `sigil export`

Prints the kind file of the kind linked into a [host binary](#host-functions-and-host-binaries), the text `Schema()` returns. `--out` writes it to a file instead, and says whether it wrote it or found it current, which suits `go generate`; `--check` with `--out` only compares, and fails when the file is stale. With several kinds linked, the kind's name picks one: `sigil export DeployApproval`.

```go
//go:generate go run ./cmd/sigil export --out ../policies/deploy_approval.sigil
```

The stock binary links no kind, so `export` there only explains how to build one.

## `sigil breaking`

Planned. Compares two versions of a kind file and flags changes that would break existing policies, modeled on `buf breaking`. It also checks the two numbers in the kind header: it fails when the contract changed but `version` didn't, and when a change is breaking but `accepts` wasn't raised to the new version. The compatibility rules are in [Kind files](/reference/kind-files/#versioning), and [Evolve a kind safely](/guides/evolve-a-kind/) walks through using it in CI.

```text
sigil breaking old/deploy_approval.sigil deploy_approval.sigil
```

```text
deploy_approval.sigil: breaking: precedence changed
  - deny > review > approve
  + deny > approve > review
  = help: raise `accepts` to 4, so policies pinned to older versions are reviewed before they load
deploy_approval.sigil: breaking: decision deny lost reason `no_release`
  = help: policies that construct deny(no_release) no longer compile; raise `accepts` to 4
```

## Language server

Planned. `sigil lsp` reads the kind file and offers completion for inputs, fields, functions and decision payload keys, and hover that shows a decision's full signature. Editor completion working from a kind file alone is the exit criterion for the editor milestone on the [roadmap](/project/roadmap/).

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
| `gated-assert` | warn | A policy that contains asserts, directly or through the policies it invokes, is invoked inside `when` and isn't required. Its asserts only run while the gate holds |
| `gated-deny` | warn | A policy that contains denies, directly or through the policies it invokes, is invoked inside `when` and isn't required by `--require`. That may be intended, but it's the pattern that silently switches denies off. A deny is a constructor of the decision a `collect one` kind ranks highest; a `collect all` kind has none |
| `duplicate-invocation` | warn | A document invokes the same policy twice with identical arguments, in any order |
| `qualified-imports` | off | A selective import is used. For teams that want Go-style provenance at every use site |
| `path-matches-name` | off | A file holds a document whose name doesn't match the file's path (see [File names](/reference/policy-files/#file-names)). Repositories that protect required policies with CODEOWNERS should promote it to an error |

Lints only run on a bundle that type-checks, since they read what the checker learned about each document.

A repository configures lints in `sigil.yaml`, which `sigil check` looks for in the working directory and then in each parent, using the first one it finds; `--config` names a file instead. Each lint is `off`, `warn` or `error`, and lints the file doesn't name keep their defaults:

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
	policytest.Run(t, policy.Deploy, sub, policy.Require("deploy.guardrails"))
}

func TestKindFileIsCurrent(t *testing.T) {
	policytest.Schema(t, policy.Deploy, "../policies/deploy_approval.sigil")
}
```

`Run` loads each test file's policy with `Load` and the options given, the same ones the host passes, and runs every test file in the `fs.FS` as a subtest named after its path, with a subtest per case, so `go test -run` selects them. `Schema` fails when the checked-in kind file isn't the kind's `Schema()`.

## `sigil gen go`

Planned. Generates typed Go code from a kind file, so a second Go service can consume decisions with typed payload structs instead of loading the kind dynamically. See [Go API](/reference/go-api/).

## Error messages

Error messages follow filt-rs: file, line and column, the severity, what went wrong, and a concrete fix. When the file holds more than one document, or its path doesn't match the document's name, the document's name follows the position: `policies.sigil:42:5 (payments.production): error: ...`. Several diagnostics are separated by a blank line.

```text
deploy/production.sigil:9:16: error: unknown field "teir" on type Service
  |
9 |   when service.teir == "critical"
  |                ^^^^
  = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

When the error is a type or payload mismatch, the message quotes the relevant signature from the kind, so the author sees what `review` expects without opening another file. The same format applies to every tool and to errors returned from the Go API's `Load` and `Compile`.
