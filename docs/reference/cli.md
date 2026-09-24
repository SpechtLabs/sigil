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
| `sigil test`     | Runs test cases: input JSON plus expected decision and reason                                  | Kind file + bound functions |
| `sigil breaking` | Compares two kind versions and flags incompatible changes                                      | Two kind files              |
| `sigil gen go`   | Generates typed Go code from a kind file                                                       | Kind file                   |
| `sigil lsp`      | Completion from the kind, hover with decision signatures, go-to-definition for `let` and `use` | Kind file                   |

Policy files and kind files share the `.sigil` extension. The first keyword in the file, `policy` or `kind`, tells the tools which one they're looking at.

## `sigil fmt`

Rewrites policy and kind files into one canonical style. It needs nothing but the files themselves.

The formatter matters more than it looks. Sigil's grammar is whitespace-insensitive so that templating can't break it, and a whitespace-insensitive grammar lets styles drift: one team indents continuation lines by two spaces, another by four, a third puts `and` at the end of the line. One canonical form keeps diffs across teams readable and makes the formatter's output the only style anyone has to learn.

## `sigil check`

Parses and type-checks policies against a kind, resolves `use` statements, detects `let` and `use` cycles, and reports the estimated worst-case cost of each policy (see [Halting by construction](/understanding/halting/)). This is the command a policy repository runs in CI. It doesn't need host function implementations, only their signatures from the kind file.

```text
sigil check --kind deploy_approval.sigil deploy/production.sigil
```

## `sigil eval`

Evaluates a policy against a JSON input and prints the result and the full trace: every candidate, the winner, and which of the winner's conditions held. Because evaluation calls host functions, `eval` needs implementations for every `fn` the kind declares. How a standalone CLI gets those bindings is still open; a host can always build its own `sigil` binary with its functions linked in.

```text
sigil eval --kind deploy_approval.sigil --input release.json payments/production.sigil
```

## `sigil test`

Runs test cases, each an input JSON document plus the expected decision and reason. Asserting on the reason as well as the decision catches the case where a deploy is denied for the wrong reason, which is a common way policy regressions hide. Like `eval`, it needs bound functions.

## `sigil breaking`

Compares two versions of a kind file and flags changes that would break existing policies, modeled on `buf breaking`. The compatibility rules are in [Kind files](/reference/kind-files/), and [Evolve a kind safely](/guides/evolve-a-kind/) walks through using it in CI.

```text
sigil breaking old/deploy_approval.sigil deploy_approval.sigil
```

## Language server

`sigil lsp` reads the kind file and offers completion for inputs, fields, functions and decision payload keys; hover that shows a decision's full signature; and go-to-definition for `let` bindings and `use` targets. Editor completion working from a kind file alone is the exit criterion for the tooling milestone on the [roadmap](/project/roadmap/).

## `policytest` for Go hosts

Go hosts get a `policytest` package that runs the same test cases from `go test`. Policies then ship with table tests next to the code that consumes them, and the tests use the host's real function implementations, which sidesteps the binding question the CLI has.

## `sigil gen go`

Generates typed Go code from a kind file, so a second Go service can consume decisions with typed payload structs instead of loading the kind dynamically. See [Go API](/reference/go-api/).

## Error messages

Error messages follow filt-rs: file, line and column, what went wrong, and a concrete fix.

```text
deploy/production.sigil:27:16: error: unknown field "teir" on type Service
   |
27 |   when service.teir == "critical"
   |                ^^^^
   = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

When the error is a type or payload mismatch, the message quotes the relevant signature from the kind, so the author sees what `review` expects without opening another file. The same format applies to every tool and to errors returned from the Go API's `Load` and `Compile`.
