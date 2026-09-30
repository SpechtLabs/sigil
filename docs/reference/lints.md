---
title: Lints
icon: mdi:clipboard-check-outline
createTime: 2026/09/29 12:00:00
permalink: /reference/lints/
---

The lints [`sigil check`](/reference/cli/#sigil-check) reports, their default levels, and how [the configuration file](/reference/config/) sets them.

## Lints

| Lint | Default |
| --- | --- |
| [`unused-import`](#unused-import) | warn |
| [`shadowed-kind-name`](#shadowed-kind-name) | warn |
| [`unused-let`](#unused-let) | warn |
| [`gated-assert`](#gated-assert) | warn |
| [`gated-deny`](#gated-deny) | warn |
| [`duplicate-invocation`](#duplicate-invocation) | warn |
| [`qualified-imports`](#qualified-imports) | off |
| [`path-matches-name`](#path-matches-name) | off |

- A lint at `warn` is reported as a warning, which doesn't fail the check. At `error` it's reported as an error, which does. At `off` it isn't reported.
- A finding names its lint in brackets at the end of the message line.
- Lints only run on a bundle that type-checks, since they read what the checker learned about each document.

```text
payments/production.sigil:11:3: warning: deploy.guardrails holds deny rules and is invoked under `when` [gated-deny]
   |
11 |   guardrails()
   |   ^^^^^^^^^^^^
   = help: its deny rules only fire while the condition holds; invoke it at the top level, or have the host require it
```

### `unused-import`

Default `warn`. Fires when a `use` binds a name nothing references. Invoking an imported policy counts as a reference.

### `shadowed-kind-name`

Default `warn`. Fires when a document pinned to an older kind version keeps a name the kind has since given to an input, host function, decision, [enum](/reference/kind-files/#enum) or enum value. Rename it and raise the pin; see [Adding a name never breaks a policy](/understanding/kinds/#adding-a-name-never-breaks-a-policy).

A document pinned to `DeployApproval@1` keeps its `let batch` after version 2 adds `batch` to `enum Tier`:

```text
deploy/production.sigil:6:5: warning: batch shadows the kind's Tier value batch, added after the version this document pins [shadowed-kind-name]
  |
6 | let batch = service.labels["schedule"] == "nightly"
  |     ^^^^^
  = help: the kind's Tier value is out of reach here; rename batch and raise the document's pin to DeployApproval@2
```

### `unused-let`

Default `warn`. Fires when a `let` that isn't `pub` is never read. Only private lets are checked, since a `pub let` may have importers in other files.

### `gated-assert`

Default `warn`. Fires when a policy that contains asserts, directly or through the policies it invokes, is invoked inside `when` and isn't required, by `require:` in [the configuration file](/reference/config/) or by `--require`. Its asserts only run while the gate holds.

### `gated-deny`

Default `warn`. Fires when a policy that contains denies, directly or through the policies it invokes, is invoked inside `when` and isn't required, by `require:` in [the configuration file](/reference/config/) or by `--require`. Its denies only fire while the gate holds. A deny is a constructor of the decision a `collect one` kind ranks highest; a `collect all` kind has none.

### `duplicate-invocation`

Default `warn`. Fires when a document invokes the same policy twice with identical arguments, in any order.

### `qualified-imports`

Default `off`. Fires when a selective import is used. It's for teams that want Go-style provenance at every use site.

### `path-matches-name`

Default `off`. Fires when a file holds a document whose name doesn't match the file's path; see [File names](/reference/bundles/#file-names). Repositories that protect required policies with CODEOWNERS should promote it to `error`. Why it isn't a security control: [Bundles and trust](/understanding/bundles/).

## Setting levels

The `lints` key of [the configuration file](/reference/config/) sets each lint to `off`, `warn` or `error`. Lints it doesn't name keep their defaults, and an unknown lint or level is an error.

```yaml
# sigil.yaml
lints:
  gated-deny: error
  path-matches-name: error
  qualified-imports: warn
```
