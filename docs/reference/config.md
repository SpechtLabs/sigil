---
title: Configuration file
icon: mdi:file-cog-outline
createTime: 2026/09/30 12:00:00
permalink: /reference/config/
---

The configuration file a policy repository keeps at its root: the kind files the policy commands load, the policies [`sigil check`](/reference/cli/#sigil-check) requires, and the level of each [lint](/reference/lints/). It's written in YAML, JSON or TOML, with the same keys in each. The first line of each example attaches the [schema](#schema).

::: tabs#format

@tab YAML

```yaml
# yaml-language-server: $schema=https://sigil.specht-labs.de/schema/config.json
kinds:
  - ../vendor/deploy_approval.sigil
require:
  - policy: deploy.guardrails
    trusted: [platform/deploy]
    roots: ["payments.*", "checkout.*"]
  - policy: access.guardrails
    trusted: [platform/access]
    roots: [access.main]
lints:
  gated-deny: error
```

@tab JSON

```json
{
  "$schema": "https://sigil.specht-labs.de/schema/config.json",
  "kinds": ["../vendor/deploy_approval.sigil"],
  "require": [
    {"policy": "deploy.guardrails", "trusted": ["platform/deploy"], "roots": ["payments.*", "checkout.*"]},
    {"policy": "access.guardrails", "trusted": ["platform/access"], "roots": ["access.main"]}
  ],
  "lints": {"gated-deny": "error"}
}
```

@tab TOML

```toml
#:schema https://sigil.specht-labs.de/schema/config.json
kinds = ["../vendor/deploy_approval.sigil"]

[[require]]
policy = "deploy.guardrails"
trusted = ["platform/deploy"]
roots = ["payments.*", "checkout.*"]

[[require]]
policy = "access.guardrails"
trusted = ["platform/access"]
roots = ["access.main"]

[lints]
gated-deny = "error"
```

:::

## Finding the file

| Name | Format |
| --- | --- |
| `sigil.yaml`, `.sigil.yaml` | YAML |
| `sigil.json`, `.sigil.json` | JSON |
| `sigil.toml`, `.sigil.toml` | TOML |

- `check`, `eval`, `explain` and `test` look for the file under all six names in the working directory, then in each parent, and use the one in the nearest directory that has one.
- Two of them in the same directory are an error.
- `--config` names a file instead, anywhere and under any name. Its extension picks the format: `.yaml` or `.yml`, `.json`, or `.toml`. Any other extension is an error.
- Without a file, there are no extra kind files and no requirements, and every lint keeps its default.
- Paths in the file are relative to the directory it's in, wherever the command runs. An absolute path is used as it is.
- JSON is strict JSON: no comments and no trailing commas.

## Schema

The file's JSON Schema is published at `https://sigil.specht-labs.de/schema/config.json`. One schema covers all three formats, so an editor completes the keys and lint names and flags a typo as you type.

::: tabs#format

@tab YAML

A modeline comment, read by [yaml-language-server](https://github.com/redhat-developer/yaml-language-server) and the editors built on it:

```yaml
# yaml-language-server: $schema=https://sigil.specht-labs.de/schema/config.json
```

@tab JSON

The top-level `$schema` key, read by editors natively:

```json
{
  "$schema": "https://sigil.specht-labs.de/schema/config.json"
}
```

The tools accept `$schema` in every format and ignore it.

@tab TOML

A `#:schema` directive on the first line, read by [Taplo](https://taplo.tamasfe.dev/) and the editors built on it:

```toml
#:schema https://sigil.specht-labs.de/schema/config.json
```

:::

The schema is stricter than the tools in one respect: it wants a string where the tools also read a number or a boolean as one.

## Keys

Every key is optional, and the file holds no others. A TOML `require` entry is a `[[require]]` table, or an inline table in a `require` array.

| Key | Holds |
| --- | --- |
| `kinds` | Kind files outside the paths a command reads. A list, or a single path |
| `require` | The policies `sigil check` enforces, one entry each |
| `lints` | A map from lint name to level: `off`, `warn` or `error` |
| `$schema` | The [schema](#schema) the file follows, for editors. The tools ignore it |

`kinds`:

- Every policy command loads them as if they were named with `--kind`, after the ones `--kind` names. See [Kinds](/reference/cli/#kinds).
- Listing every kind file of the repository lets a command run on one directory of it, such as `sigil check teams/payments`. A kind file that is also among the paths is read once, and counted once.
- A kind file that doesn't exist fails the command, naming the configuration file.

`require` entries:

| Key | Holds |
| --- | --- |
| `policy` | The required policy's name. Required; one name, not a pattern |
| `trusted` | Files and directories the required policy comes from, as `--trusted` and the host's `policy.From` read it. A list, or a single path. Optional |
| `roots` | Name patterns of the policies it applies to, as `--policy` does. A list, or a single pattern. Optional |

- Every root the entry applies to must invoke `policy` unconditionally, the check a host makes with [`policy.Require`](/reference/go-api/#require).
- Without `roots`, the entry applies to every policy of the required policy's kind that no other policy invokes, apart from the required ones.
- Name the roots. A policy that another policy invokes isn't a root, even when the invocation sits under a `when`, so without `roots` the requirement doesn't reach it: if `evil.wrap` invokes `evil.prod` under a `when`, only `evil.wrap` must invoke the required policy, although a host can load `evil.prod` as a root.
- With `trusted`, the required policy must be defined below this entry's `trusted` paths, where the host reads it. One defined anywhere else, among the paths or below another entry's `trusted`, is an error at the entry.
- A required policy applies only to roots of its own kind, so one file can hold the requirements of several kinds.
- A policy can be required once.
- A `trusted` path that doesn't exist is an error at the entry.
- When the command reads the directory the configuration file is in, or one above it, every entry must apply: a required policy no document defines, a `roots` pattern that matches nothing, and `roots` that match no policy of the required policy's kind are errors at the entry.
- When it reads only part of that directory, such as one team's, `roots` pick among the policies it read, and an entry without `trusted` whose policy isn't among them is skipped.
- `eval`, `explain` and `test` read the `trusted` paths too, as trusted sources, apart from files among their paths, so a policy in the directory they read finds the required policies it uses.
- `--require` on the command line replaces `require` for that run. `--trusted` without `--require` is an error, since a requirement's trusted paths belong in its entry. `--policy` keeps it, and narrows each entry's `roots`, or its default roots, to the policies it matches; an entry whose roots it leaves out is skipped, and a `roots` pattern matching nothing is then no error.

`lints`:

- Lints the file doesn't name keep their defaults; [Lints](/reference/lints/) lists them.

## Errors

An unknown key, lint or level is an error at its line and column, with the nearest valid name, so a typo can't silently leave a setting at its default. The advice writes keys the way the file's format does. A TOML array has no position of its own, so an error about one, such as `lints = ["gated-deny"]`, is at its key.

::: tabs#format

@tab YAML

```yaml
kinds:
  - ../vendor/deploy_approval.sigil
require:
  - policy: deploy.guardrails
    root: ["payments.*"]
```

```text
Error: sigil.yaml:5:5: unknown key "root" in require[0]

What you can do
  • did you mean "roots"?
  • a require entry holds `policy:`, `trusted:` and `roots:`
```

@tab JSON

```json
{
  "kinds": ["../vendor/deploy_approval.sigil"],
  "require": [
    {"policy": "deploy.guardrails", "root": ["payments.*"]}
  ]
}
```

```text
Error: sigil.json:4:37: unknown key "root" in require[0]

What you can do
  • did you mean "roots"?
  • a require entry holds `"policy"`, `"trusted"` and `"roots"`
```

@tab TOML

```toml
kinds = ["../vendor/deploy_approval.sigil"]

[[require]]
policy = "deploy.guardrails"
root = ["payments.*"]
```

```text
Error: sigil.toml:5:1: unknown key "root" in require[0]

What you can do
  • did you mean "roots"?
  • a require entry holds `policy`, `trusted` and `roots`
```

:::

A required name that no document defines:

::: tabs#format

@tab YAML

```yaml
require:
  - policy: deploy.guardrail
```

```text
Error: sigil.yaml:2:13: deploy.guardrail is required, but no policy deploy.guardrail was found

What you can do
  • did you mean "deploy.guardrails"?
  • check reads a required policy from the entry's trusted: paths, or else from its paths
```

@tab JSON

```json
{
  "require": [
    {"policy": "deploy.guardrail"}
  ]
}
```

```text
Error: sigil.json:3:16: deploy.guardrail is required, but no policy deploy.guardrail was found

What you can do
  • did you mean "deploy.guardrails"?
  • check reads a required policy from the entry's trusted: paths, or else from its paths
```

@tab TOML

```toml
[[require]]
policy = "deploy.guardrail"
```

```text
Error: sigil.toml:2:10: deploy.guardrail is required, but no policy deploy.guardrail was found

What you can do
  • did you mean "deploy.guardrails"?
  • check reads a required policy from the entry's trusted: paths, or else from its paths
```

:::

Two configuration files in one directory:

```text
Error: sigil.yaml and .sigil.json in the working directory are both configuration files

What you can do
  • keep one of them; a command reads one configuration file, the nearest
```
