---
title: Bundles
icon: mdi:package-variant-closed
createTime: 2026/09/29 12:00:00
permalink: /reference/bundles/
---

How the documents in `.sigil` files form a bundle, how names resolve in it, and where required policies come from.

Why: [Bundles and trust](/understanding/bundles/).

## Documents

```sigil
module deploy.common: DeployApproval@1

pub let cleared = split(service.labels["regions"], ",") all in actor.regions

---

policy deploy.guardrails: DeployApproval@1

param min_soak: duration = 24h

when release.soak < min_soak and not release.hotfix {
  deny(reason: soak_too_short)
}
```

- A file holds one or more documents. A document is a policy, a module or a kind.
- A document starts with its header, `policy`, `module` or `kind`, and ends where the next header starts or at the end of the file.
- A header keyword can't start a statement inside a document, so a header at the top level always starts a new document.
- Inside braces, after `.` or as a named argument, `policy`, `module` and `kind` are ordinary names: a `type Resource { kind: string }` body or a `resource.kind` read never ends a document. See [Source files](/reference/grammar/#source-files).
- The `---` separator is optional. `sigil fmt` always writes it between documents. See [Document separators](/reference/lexical/#document-separators) for how it lexes.
- A `---` before the first document or after the last one is allowed, and so are several in a row. `sigil fmt` removes the extras.
- A comment directly above a header belongs to the document that follows it: `sigil fmt` writes the `---` above the comment, not between the comment and the header.
- A file with no documents at all is valid and contributes nothing.

## Bundles

A bundle is every document in every file the host or the CLI loads, indexed by the name in each header.

| Rule                                             | Consequence                                                                                                                                                                        |
| ------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Each name has one definition                     | A name defined twice in a bundle is a compile error that points at both definitions                                                                                                |
| The host names the root policy                   | One bundle can hold many policies; the host picks the entry point                                                                                                                  |
| A module can't be a root                         | It has no rules to evaluate. `Load` on a module's name fails with `deploy.common is a module, not a policy`                                                                        |
| A bundle holds the documents of one kind         | A policy or module written for another kind is a compile error, `document is for kind AccessGrant, not DeployApproval`. A host with two kinds reads them from separate directories |
| A bundle loads as a whole                        | A syntax error in any document fails the load, even in a document the root never uses                                                                                              |
| `Compile` takes a one-file bundle                | The source string may hold several documents, imports resolve among them, and every document is checked; see [Loading](/reference/go-api/#loading)                                 |
| The CLI builds one bundle from all its arguments | A name defined in two files is an error there too; see [Inputs](/reference/cli/#inputs)                                                                                            |

```text
policies.sigil:42:8: error: policy payments.access is defined twice
   |
42 | policy payments.access: DeployApproval@1
   |        ^^^^^^^^^^^^^^^
   = help: first defined at teams/payments.sigil:1:1; documents resolve by name, so each name has one definition
```

On a hot reload, the host keeps the last policy that loaded; see [Reload without an outage](/guides/configmaps/#reload-without-an-outage). To catch a broken document before a bundle ships, see [Check policies in CI](/guides/ci/).

## Name resolution

- `use deploy.common` resolves to the document named `deploy.common`, wherever it is. A name is never a file path.
- Files are plain containers. One key per team, one file per policy, or everything in one file all resolve the same way.
- The imported document must implement the same kind as the importing one; see [`use`](/reference/policy-files/#use).
- A `use` of a kind's name is a compile error.
- With a [trusted source](#trusted-sources), a required policy and everything it uses come from that source, not from the bundle.

## Loading files

The host passes the bundle as an `fs.FS`, such as an `embed.FS`, `os.DirFS` or `policy.MapFS`; see [Loading](/reference/go-api/#loading).

| Rule           | Detail                                                                                                                                                         |
| -------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Extension      | The loader reads every file whose name ends in `.sigil`, in every directory. ConfigMap keys need the extension too (`policies.sigil`)                          |
| Dot entries    | Every file or directory whose name starts with `.` is skipped, which includes kubelet's `..data` directory and its timestamped siblings in a mounted ConfigMap |
| Symbolic links | Followed. The loader checks each entry with `fs.Stat`, never with the directory entry's type (`DirEntry.Type()`)                                               |
| `policy.MapFS` | Turns a ConfigMap's `data`, a `map[string]string`, into an in-memory `fs.FS` with each key as a file name                                                      |

The CLI takes files, directories and stdin; see [Inputs](/reference/cli/#inputs). To load a ConfigMap, see [Policies in a ConfigMap](/guides/configmaps/).

## Kind documents in a bundle

- Kind documents aren't part of the name index.
- A kind document is never taken as the contract. The kind always comes from the host's Go definition, or from `--kind` in the CLI.
- A kind document with the host kind's name must match that contract (`Deploy.Schema()`) exactly, or the load fails. This catches a stale export.
- A kind document for another kind is ignored.

A mismatch fails with `kind document DeployApproval doesn't match the host's kind` and the help `the host's Go definition is the contract; regenerate this file from Schema()`.

## Trusted sources

```go
policy.Require("deploy.guardrails", policy.From(platformFS))
```

`policy.From(fsys)`, passed inside [`policy.Require`](/reference/evaluation/#required-policies), names the source a required policy must come from. The loader reads the trusted source as its own bundle, separate from the one passed to `Load`.

```go
//go:embed platform
var platformFS embed.FS // or a platform-owned ConfigMap, mounted separately

p, err := Deploy.Load(policy.MapFS(cm.Data), "payments.production",
	policy.Require("deploy.guardrails", policy.From(platformFS)))
```

- The required policy is taken from the trusted source, and so is everything it imports and invokes. A trusted policy never resolves a name in the untrusted bundle.
- Every name the trusted source defines is reserved. A document in the untrusted bundle that claims one of them, such as `deploy.guardrails` or `deploy.common`, is a compile error naming both definitions. Neither side overrides the other.
- Several `Require` options may name the same source, and the loader reads it once. It recognizes the same source by value for a comparable `fs.FS`, such as `embed.FS`, `os.DirFS` or `fs.Sub`, and by map identity for a map-backed one such as `policy.MapFS`.
- Team policies import and invoke trusted documents by name as usual: `use deploy.guardrails` and `use deploy.common.{cleared}` work unchanged.
- Without `From`, the required policy is looked up in the bundle like any other document.
- A required policy bounds its own params with [`min` and `max`](/reference/policy-files/#bounds). `Require` takes no bounds of its own.

```text
teams/payments.sigil:3:8: error: module deploy.common is defined twice
  |
3 | module deploy.common: DeployApproval@1
  |        ^^^^^^^^^^^^^
  = help: the name belongs to the trusted source, defined at deploy/common.sigil:1:1; documents resolve by name, so each name has one definition
```

Why: [Why required policies need a trusted source](/understanding/bundles/#why-required-policies-need-a-trusted-source).

## File names

Nothing in the language ties a file's path to the names inside it. The [`path-matches-name`](/reference/lints/) lint warns when a file holds a document whose name doesn't match the file's path, with each `.` turned into `/` and `.sigil` appended.

| Name                  | Expected file               |
| --------------------- | --------------------------- |
| `deploy.common`       | `deploy/common.sigil`       |
| `deploy.production`   | `deploy/production.sigil`   |
| `payments.production` | `payments/production.sigil` |

- The file may sit under any directory: `policies/deploy/common.sigil` matches `deploy.common` too.
- The lint is off by default.
- It does nothing for a bundle that arrives through `policy.MapFS`. Required policies are protected by [trusted sources](#trusted-sources).
- A file holding several documents, such as `deploy.sigil` with `deploy.common` and `deploy.guardrails`, gets a warning for each document.

::: warning Planned
A prefix rule for multi-document files; see [File names for multi-document files](/project/planned/#file-names-for-multi-document-files).
:::
