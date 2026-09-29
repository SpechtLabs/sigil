---
title: Planned designs
icon: mdi:pencil-ruler
createTime: 2026/09/29 12:00:00
permalink: /project/planned/
---

This page collects the designs that are decided or proposed but not implemented. The reference pages describe only what exists and point here with a one-line note, so each design below is written out in full in this one place. The [roadmap](/project/roadmap/) tracks them, and [Open questions](/project/open-questions/) lists what's still undecided around them.

## Host-ordered types

**Status:** not implemented; tracked on the [roadmap](/project/roadmap/) under Policies. Nothing blocks it, since kinds, the checker and the evaluator exist. **Open question:** [String literals for host-ordered types](/project/open-questions/#string-literals-for-host-ordered-types).

Host-ordered types are the chosen direction for versions and other domain values with their own ordering. Today `type X ordered` doesn't parse, and `policy.WithOrdered` doesn't exist. Until they do, a host function compares the values; see [Compare versions](/guides/patterns/#compare-versions).

Some values have an order the language can't know: semantic versions, calendar versions, a vendor's release numbers. A host declares a type for them in the kind, and the ordering comes from Go:

```sigil
// kind file
type Version ordered
fn semver(string) -> Version
```

```sigil
// policy
when semver(release.version) < semver("1.4.0") {
  deny(reason: client_too_old)
}
```

In a kind file, `type Version ordered` declares a type that's opaque, has no fields, and is ordered by the Go type's `Compare(T) int` method.

- **The Go side.** The host registers the Go type explicitly, and names it for policies:

  ```go
  policy.WithOrdered[*semver.Version]("Version")
  ```

  The type needs a method `Compare(T) int` that returns a negative number, zero or a positive number, the convention `time.Time`, `netip.Addr` and most version libraries already follow. The method is mandatory: `NewKind` panics if the type lacks it. It's never declared as an `fn` in the kind, so the kind file only says that the type is ordered, not how.
- **Pointers.** The registered Go type is exact. Most version libraries put `Compare` on a pointer, so registering `*semver.Version` makes that pointer type the ordered `Version`, and a field one pointer deeper, `**semver.Version`, is `?Version`. A nil value of a registered pointer type that reaches a comparison is a runtime error; a field that can really be missing should be declared one pointer deeper, as an optional. A type that isn't registered keeps its usual mapping even if it has a `Compare` method, so adding a method in Go never changes the contract by itself.
- **Text.** Traces, errors and test output print a value as text. `NewKind` picks the method once, when the type is registered: `MarshalText` from `encoding.TextMarshaler` if the type has it, otherwise `String` from `fmt.Stringer`. A type with neither makes `NewKind` panic, because the fallback, Go's `%v`, can print a pointer's address and would break [determinism](/reference/evaluation/#determinism). If `MarshalText` returns an error for a value, `String` is used when the type has it, and otherwise the text is `<Version: error text>`, so printing a trace never fails an evaluation.
- **JSON input.** `sigil eval` and `sigil test` read input through `encoding/json`, so a field of the type decodes from a JSON string when the type implements `encoding.TextUnmarshaler` (or `json.Unmarshaler`). Nothing requires it, but without it an input file can't set the field.
- **Operators.** `<`, `<=`, `>`, `>=`, `==` and `!=` call `Compare`. So do `in` and the list operators when the elements are of the type. Only values of the same type compare: a `Version` never compares with another ordered type or with a string.
- **Opaque.** A policy can't read inside the value, has no literal for it and can't declare a param of the type. Values come from inputs and host functions, such as `semver` above. Parsing stays with the host, so semver, calver or a custom scheme are all just Go.
- **Errors.** A string the parsing function rejects is that function's error, which is a [runtime error](/reference/evaluation/#runtime-errors). `Compare` must be a total order, pure and deterministic, the same contract as a host function.
- **Map keys.** An ordered type can't be a map key.

Why this and not a built-in `version` type is on [Why the language looks like this](/understanding/language-choices/#why-there-s-no-version-type).

## Dynamic input

**Status:** a proposal, not on the [roadmap](/project/roadmap/) yet. `Resolver` isn't an exported API.

Today a Go host decodes external input into its input struct before calling `Eval`, and the CLI uses its own JSON decoder. The proposal would let a host supply values by path instead:

```go
type Resolver interface {
	Get(path string) (Value, bool)
}
```

The evaluator would check each resolved value against the kind's schema and reject a type mismatch with the failing path. The interface and how it integrates with `Eval` are undecided.

## Loading a kind at run time

**Status:** in progress on the [roadmap](/project/roadmap/) as the LoadKind deliverable of the Hardening milestone. The internal kind-file loader and the Go binding it synthesizes already back the CLI and have fuzz coverage; the public API doesn't exist.

A public `policy.LoadKind` would let a Go service load a kind from its kind file at run time instead of defining the kind in Go. It needs a companion API to bind Go functions to the host functions a loaded kind file declares. Neither exists yet.

Until they do, a Go service that consumes a kind it doesn't define imports the defining host's package, and tooling reads the exported kind file through the CLI. The stock `sigil` binary checks policies against an exported kind file without the host's Go code, and evaluates them as long as no rule reaches a host function call; reaching one is a runtime error. A host binary built with `pkg/cli` supplies the real implementations. The facts are in [Host functions and host binaries](/reference/cli/#host-functions-and-host-binaries).

The planned [`sigil gen go`](#sigil-gen-go) command covers the other half: services that want typed payload structs from a kind file without importing the host.

## Static cost analysis

**Status:** not implemented; tracked on the [roadmap](/project/roadmap/) as a deliverable of the Hardening milestone, deferred from its fuzzing and correctness pass. **Open question:** [Static cost analysis](/project/open-questions/#static-cost-analysis).

The language terminates, but termination doesn't make evaluation cheap. Nested quantifiers multiply collection sizes, so `any a in xs: any b in ys: a == b` may compare every pair, costing `len(xs) * len(ys)`. List membership and distinct-element operators scan collections, policy invocations repeat work for each instantiation, and strings, patterns and host functions have costs of their own. [Halting by construction](/understanding/halting/) has the background.

The proposed analyzer would combine declared collection limits with operator and host-function costs, then reject policies over a host's budget, pointing at the expression that exceeds it. Three parts have no design yet:

- how a kind declares the maximum size of a collection,
- how a host function declares what it costs,
- what the budget API looks like.

Today kinds declare no collection limits, and neither the compiler nor `sigil check` computes, reports or enforces a cost. Hosts must bound their inputs and the work their host functions do, and should evaluate under a deadline; see [Bound evaluation time](/guides/handle-errors/#bound-evaluation-time).

## `sigil breaking`

**Status:** not implemented; tracked on the [roadmap](/project/roadmap/) as a required deliverable of the Tooling II milestone. The command is registered and exits with "not implemented yet"; see [`sigil breaking`](/reference/cli/#sigil-breaking).

`sigil breaking` compares two versions of a kind file and flags changes that would break existing policies, modeled on `buf breaking`.

```text
sigil breaking OLD_KIND_FILE NEW_KIND_FILE [flags]
```

It takes only the global flags. It classifies every change by the compatibility table in [Versioning](/reference/kind-files/#versioning); a removed reason, for example, is breaking because every policy that constructs it stops compiling. Among the enum rules, it flags a value that another enum of the new kind also declares, such as `standard` added to `Plan` while `Tier` has it, because a bare `standard` without context becomes ambiguous; a new enum counts when its values overlap an existing one. It also checks the two numbers in the kind header: it fails when the contract changed but `version` didn't, and when a change is breaking but `accepts` wasn't raised to the new version. It needs nothing but the two kind files, so it can run in either the host repository or the policy repository, against the kind file on the main branch. The intended output:

```text
deploy_approval.sigil: breaking: precedence changed
  - deny > review > approve
  + deny > approve > review
  = help: raise `accepts` to 4, so policies pinned to older versions are reviewed before they load
deploy_approval.sigil: breaking: decision deny lost reason `no_release`
  = help: policies that construct deny(reason: no_release) no longer compile; raise `accepts` to 4
deploy_approval.sigil: breaking: enum Plan declares `standard`, which Tier declares too
  = help: a bare `standard` without context becomes ambiguous; raise `accepts` to 4, and qualify it as `Tier.standard`
```

A CI job would run it as:

```sh
sigil breaking old/deploy_approval.sigil deploy_approval.sigil
```

Until it exists, reviewers check `version` and `accepts` by hand; [Evolve a kind safely](/guides/evolve-a-kind/) shows what CI catches today.

## `sigil gen go`

**Status:** not implemented; tracked on the [roadmap](/project/roadmap/) under Tooling II. The command is registered and exits with "not implemented yet"; see [`sigil gen go`](/reference/cli/#sigil-gen-go).

`sigil gen go` generates typed Go code from a kind file: a struct for every input type and decision payload. A second Go service can then consume decisions with typed payload structs instead of importing the host or [loading the kind at run time](#loading-a-kind-at-run-time).

```text
sigil gen go KIND_FILE [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `-p`, `--package` | the kind's name | Go package name of the generated code |
| `--out` | stdout | File to write the generated code to |

## `sigil lsp`

**Status:** not implemented; tracked on the [roadmap](/project/roadmap/) under Tooling II. Editor completion working from a kind file alone is that milestone's exit criterion. The command is registered and exits with "not implemented yet"; see [`sigil lsp`](/reference/cli/#sigil-lsp).

`sigil lsp` runs the Sigil language server, which editors start in the background and talk to over stdin and stdout.

```text
sigil lsp [flags]
```

| Flag | Default | Does |
| --- | --- | --- |
| `--stdio` | on | Talks to the editor over stdin and stdout, the only transport. Editors pass it by convention |

The server reads the kind file and offers completion for inputs, fields, functions and decision payload keys, and hover that shows a decision's full signature.

Imports and invocations get their own support:

- Completion after `use deploy.common.{` lists the module's `pub let`s. Path-first imports are what make this work: the editor knows the file before you type the names.
- Go-to-definition works across imports and into invoked policies.
- A code lens on each invocation summarizes what it contributes, for example "production: 1 approve, 1 review, gated by compliance != pci".
- Hovering an invocation shows its flattened rules, the same view as `sigil explain`, scoped to that call.

## `explain --input`

**Status:** not implemented; noted as planned on the [roadmap](/project/roadmap/) with the `sigil explain` deliverable of the Composition milestone.

An `--input` flag on [`sigil explain`](/reference/cli/#sigil-explain) would take an input document and mark, in the flattened output, which rules fired and which candidate won. Like `sigil eval`, it would need a host binary for policies that call host functions.

## File names for multi-document files

**Status:** not implemented, and not on the [roadmap](/project/roadmap/) yet.

The `path-matches-name` lint (see [Lints](/reference/lints/) and [File names](/reference/bundles/#file-names)) expects a document named `deploy.common` in `deploy/common.sigil`. A file that holds several documents gets a warning for each one today, so `deploy.sigil` holding `deploy.common` and `deploy.guardrails` gets two.

The plan is a prefix rule: accept a file whose documents all share a prefix when its path is that prefix. `deploy.sigil` would then pass, because both of its documents start with `deploy`.
