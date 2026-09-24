---
title: CLI & editor tooling
icon: mdi:console
createTime: 2026/09/24 22:30:00
permalink: /reference/cli/
---

::: warning Planned tooling
None of these tools exist yet. Commands, flags and output formats will change. This page records what the tooling is meant to do so the language design accounts for it.
:::

All tooling reads the exported kind file (`deploy_approval.sigil` in the running example), so it works in a team's policy repository without the host's Go code. One `sigil` binary covers the command line.

| Command          | Does                                                                                           | Needs                       |
| ---------------- | ---------------------------------------------------------------------------------------------- | --------------------------- |
| `sigil fmt`      | Rewrites files into the one canonical style, like `gofmt`                                      | Nothing                     |
| `sigil check`    | Parses and type-checks policies against a kind, reports cost                                   | Kind file                   |
| `sigil eval`     | Evaluates a policy against a JSON input and prints the result and trace                        | Kind file + bound functions |
| `sigil explain`  | Flattens a policy into its guarded decisions, with every invocation inlined                    | Kind file                   |
| `sigil test`     | Runs test cases: input JSON plus expected decision and reason                                  | Kind file + bound functions |
| `sigil breaking` | Compares two kind versions and flags incompatible changes                                      | Two kind files              |
| `sigil gen go`   | Generates typed Go code from a kind file                                                       | Kind file                   |
| `sigil lsp`      | Completion from the kind and imports, hover, go-to-definition across imports and invocations   | Kind file                   |

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
- **Directories.** A directory contributes the `*.sigil` files directly inside it. `-R` (`--recursive`) includes subdirectories too. Entries whose names start with `.` are skipped, as the [loader](/reference/policy-files/#loading-files) does, so pointing the CLI at a mounted ConfigMap volume works.
- **Stdin.** `-` reads one stream, which may hold several documents. It's how a CI job checks a key extracted from a rendered ConfigMap.
- **One bundle.** Documents from all arguments are indexed by the names in their headers, exactly as the host's `Load` does, so a name defined in two files is an error here too.
- **The kind stays separate.** `--kind` names the kind file, and it's never part of the bundle. A kind document found among the inputs, for example because a glob matched it, is skipped with a note on stderr.

`eval` and `explain` need a root policy; `check`, `fmt` and `test` don't. If the bundle holds exactly one policy, that's the root, so a single-file bundle needs no flag. Otherwise `--policy` (`-p`) names it, and leaving it out is an error that lists the policies found. `explain` is the exception: without `--policy` it explains every policy in the bundle, one after another, which is handy for reviewing a whole ConfigMap.

## `sigil fmt`

Rewrites policy, module and kind documents into one canonical style. It needs nothing but the files themselves. In a file with several documents, it writes one `---` line between each pair, with a blank line on each side, and none before the first or after the last.

The formatter matters more than it looks. Sigil's grammar is whitespace-insensitive so that templating can't break it, and a whitespace-insensitive grammar lets styles drift: one team indents continuation lines by two spaces, another by four, a third puts `and` at the end of the line. One canonical form keeps diffs across teams readable and makes the formatter's output the only style anyone has to learn.

## `sigil check`

Parses and type-checks policies and modules against a kind, resolves imports and invocations, detects `let`, import and invocation cycles, and reports the estimated worst-case cost of each policy (see [Halting by construction](/understanding/halting/)). This is the command a policy repository runs in CI. It doesn't need host function implementations, only their signatures from the kind file.

```text
sigil check --kind deploy_approval.sigil --recursive .
```

Every document in the bundle is checked, including ones no policy imports, so a broken document fails CI instead of failing the host's `Load` later.

`--require` makes the same check a host makes with `policy.Require`: every root policy must invoke the named policy unconditionally, through top-level invocations only. Repeat the flag to require several. The roots are the policies named with `--policy`, or, without it, every policy in the bundle that no other policy invokes; the required policy itself is never a root. A policy repository that runs it in CI finds a gated or missing guardrail before the host refuses to load the policy.

```text
sigil check --kind deploy_approval.sigil --require deploy.guardrails deploy/ payments/
```

## `sigil eval`

Evaluates a policy against a JSON input and prints the result and the full trace: every candidate, the winner, and which of the winner's conditions held. Because evaluation calls host functions, `eval` needs implementations for every `fn` the kind declares. How a standalone CLI gets those bindings is still open; a host can always build its own `sigil` binary with its functions linked in.

```text
sigil eval --kind deploy_approval.sigil --input release.json --policy payments.production deploy/ payments/
```

Diagnostics and trace entries name the document as well as the position, `policies.sigil:42:5 (payments.production)`, so a trace stays readable when many documents share one file. The name is left out when the file holds only that document and its path matches the name. (proposed)

## `sigil explain`

Flattens a policy into one list of guarded decisions. Every invocation is inlined, and its gates are pushed down into each rule's condition. Invocation makes composition flexible; `explain` keeps it transparent, so the answer to "what does this policy actually do" is one command away.

```text
$ sigil explain --kind deploy_approval.sigil --policy payments.production deploy/ payments/
payments.production: 7 rules from 3 policies

deny     not_eligible      payments:7 → guardrails:8
         not eligible

deny     soak_too_short    payments:7 → guardrails:12
         release.soak < 4h and not release.hotfix

review   service_owner     payments:14 → production:16
         service.labels["compliance"] != "pci"
         and cleared
         and service.tier in ["standard", "internal"] and owns_service
         approvers = ["payments-leads"]

approve  payments_sre      payments:18
         cleared and "payments-sre" in actor.teams
         bake = 15m
...
```

Each entry names the decision, the reason and the call chain that reaches the rule, then the rule's full condition: every `when` around every call on the chain, joined with `and`. Params show as their bound values, which is why invocation arguments can't depend on inputs. `let`s stay by name, so a condition reads the way its author wrote it.

With `--input`, the output also marks which rules fired and which candidate won. Like `eval`, that needs bound functions; without `--input`, `explain` needs only the kind file.

```text
sigil explain --kind deploy_approval.sigil --input release.json --policy payments.production deploy/ payments/
```

The layout is illustrative; the exact format isn't fixed yet. [The tour](/getting-started/tour/#what-the-team-policy-adds-up-to) shows the complete output for this policy.

## `sigil test`

Runs test cases, each an input JSON document plus the expected decision and reason. Asserting on the reason as well as the decision catches the case where a deploy is denied for the wrong reason, which is a common way policy regressions hide. Like `eval`, it needs bound functions.

## `sigil breaking`

Compares two versions of a kind file and flags changes that would break existing policies, modeled on `buf breaking`. The compatibility rules are in [Kind files](/reference/kind-files/), and [Evolve a kind safely](/guides/evolve-a-kind/) walks through using it in CI.

```text
sigil breaking old/deploy_approval.sigil deploy_approval.sigil
```

## Language server

`sigil lsp` reads the kind file and offers completion for inputs, fields, functions and decision payload keys, and hover that shows a decision's full signature. Editor completion working from a kind file alone is the exit criterion for the tooling milestone on the [roadmap](/project/roadmap/).

Imports and invocations get their own support:

- Completion after `use deploy.common.{` lists the module's exported `let`s. Path-first imports are what make this work: the editor knows the file before you type the names.
- Go-to-definition works across imports and into invoked policies.
- A code lens on each invocation summarizes what it contributes, for example "production: 1 approve, 1 review, gated by compliance != pci".
- Hovering an invocation shows its flattened rules, the same view as `sigil explain`, scoped to that call.

## Lints

`sigil check` reports lints as warnings. They don't fail compilation unless a repository promotes them to errors.

| Lint | Default | Fires when |
| --- | --- | --- |
| `unused-import` | warn | A `use` binds a name nothing references |
| `gated-deny` | warn | A policy that contains denies is invoked inside `when` and isn't required by `--require` or the host. That may be intended, but it's the pattern that silently switches denies off |
| `duplicate-invocation` | warn | The same policy is invoked twice with identical arguments |
| `duplicate-reason` | warn | One policy uses the same reason twice (see [Decisions](/reference/decisions/)) |
| `qualified-imports` | off | A selective import is used. For teams that want Go-style provenance at every use site |
| `path-matches-name` | off | A file holds a document whose name doesn't match the file's path (see [File names](/reference/policy-files/#file-names)). Repositories that protect required policies with CODEOWNERS should promote it to an error |

How a repository configures lints, and how a promoted lint is spelled, isn't designed yet.

## `policytest` for Go hosts

Go hosts get a `policytest` package that runs the same test cases from `go test`. Policies then ship with table tests next to the code that consumes them, and the tests use the host's real function implementations, which sidesteps the binding question the CLI has.

## `sigil gen go`

Generates typed Go code from a kind file, so a second Go service can consume decisions with typed payload structs instead of loading the kind dynamically. See [Go API](/reference/go-api/).

## Error messages

Error messages follow filt-rs: file, line and column, what went wrong, and a concrete fix. When the file holds more than one document, or its path doesn't match the document's name, the document's name follows the position: `policies.sigil:42:5 (payments.production): error: ...`.

```text
deploy/production.sigil:9:16: error: unknown field "teir" on type Service
  |
9 |   when service.teir == "critical"
  |                ^^^^
  = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

When the error is a type or payload mismatch, the message quotes the relevant signature from the kind, so the author sees what `review` expects without opening another file. The same format applies to every tool and to errors returned from the Go API's `Load` and `Compile`.
