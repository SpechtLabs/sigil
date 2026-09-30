---
title: Bundles and trust
icon: mdi:package-variant-closed
createTime: 2026/09/29 12:00:00
permalink: /understanding/bundles/
---

A host doesn't load a policy file. It loads a _bundle_: every document in every file it's given, indexed by the name in each document's header, and then compiles the root policy it names. That choice makes policies easy to ship in whatever container a platform already has, a Git repository, an `embed.FS` or a Kubernetes ConfigMap. It also means a name, not a path, decides which document a `use` gets, and that has consequences for who can be trusted to write what.

The rules are in [Bundles](/reference/bundles/). The setup for a Kubernetes service is in [Policies in a ConfigMap](/guides/configmaps/).

## Why files are only containers

A ConfigMap is a flat map from keys to strings, and a key can't contain `/`. A repository layout such as `deploy/common.sigil` can't survive the trip into one. If `use deploy.common` meant "read `deploy/common.sigil`", every policy would need a second layout for Kubernetes.

So Sigil resolves `use deploy.common` by the name in each document's header. The loader reads every document in every file it's given and indexes them by name. Whether the documents arrive as one file per policy, one key per team or one key for everything, they resolve the same way, and the host names the root, so one ConfigMap can serve many policies. A repository can still keep each document at the path its name spells, which helps readers and lets CODEOWNERS map to namespaces. That's a convention a lint can check, not something the loader relies on; see [`path-matches-name` is a review aid](#path-matches-name-is-a-review-aid).

## Why a bundle loads as a whole

A syntax error in any document fails the load, even in a document the root never uses, and so does a name defined twice. The obvious alternative is to load only what the root reaches, following its imports and invocations and ignoring the rest. That would let a healthy team policy load next to a broken one. It would also make the answer to "does this bundle load?" depend on which root a host asks for, so a document could sit broken in a shared bundle, unnoticed, until the day some host named a root that reaches it. Loading as a whole keeps the answer simple: a bundle loads or it doesn't, for every root. It also gives CI one thing to check. `sigil check` compiles every policy in the bundle, including ones nothing imports, so a broken document fails the pull request instead of a host's `Load` later.

The price is paid at run time, in a bundle several teams share: one team's typo fails the reload for every team. A host absorbs that by keeping the last policy that loaded and alerting on the failure, and per-team keys keep each team's mistakes out of other teams' review diffs, even though the load still fails as a whole. [Reload without an outage](/guides/configmaps/#reload-without-an-outage) shows the setup.

## Why required policies need a trusted source

A host protects its guardrails by requiring them. `policy.Require("deploy.guardrails")` makes the compiler check that every root policy invokes `deploy.guardrails` unconditionally, so no team can gate its denies behind a `when`; [Composition without templating](/understanding/composition/#the-safety-guarantee) explains that guarantee.

But documents resolve by name. On its own, `Require` checks that _a_ policy called `deploy.guardrails` is invoked, not _which_ one. Anyone who can write to the bundle, for example to the ConfigMap behind `policy.MapFS(cm.Data)`, could ship their own `deploy.guardrails` with no denies in it, and it would pass the check. They wouldn't even need to touch the guardrails: a document that redefines `deploy.common` with an `eligible` that's always true hollows out every guardrail that imports it.

That's why the platform's documents and the team policies deserve different trust, and shouldn't share a source. If the guardrails lived in the same ConfigMap as the team policies, anyone who can edit that ConfigMap could replace them. `policy.From` closes that by naming the source a required policy must come from:

```go
p, err := Deploy.Load(policy.MapFS(cm.Data), "payments.production",
	policy.Require("deploy.guardrails", policy.From(platformFS)))
```

The loader reads the trusted source as a bundle of its own. The required policy comes from there, and so does everything it imports and invokes, so a trusted policy never resolves a name in the untrusted bundle and a team can't redefine `deploy.common.eligible` from underneath it. Every name the trusted source defines is reserved: a team document that claims `deploy.guardrails` or `deploy.common` is a compile error naming both definitions, not a silent override in either direction. Team policies still `use deploy.guardrails` by name, as before.

With that in place, the team ConfigMap can be writable by the teams, by a GitOps controller or by anything else, without the writer being able to weaken the guardrails. Without `From`, the required policy is looked up in the bundle like any other document. That's fine when the whole bundle is trusted, such as an `embed.FS` built from a reviewed repository, and it's how most examples in these docs read. Whenever someone other than the platform team can write to the bundle, pass `From`. The exact rules are under [Trusted sources](/reference/bundles/#trusted-sources).

## Bounds are only as trustworthy as their file

A guardrail bounds its params so a team can tune a threshold but not disable the rule:

```sigil
param min_soak: duration = 24h, min: 1h, max: 48h
```

Those bounds live in the guardrail's own file. A hollow `deploy.guardrails` could declare `min: 0s` just as easily as it could drop its denies. So bounds are only as trustworthy as the file that declares them, and for a required guardrail, `policy.From` is what makes that file the platform's. `Require` takes no bounds of its own.

## `path-matches-name` is a review aid

In a repository, keeping each document at the path its name spells, `deploy.common` in `deploy/common.sigil`, helps readers and lets CODEOWNERS route a change to `deploy/` to the platform team. The `path-matches-name` [lint](/reference/lints/) checks that layout, and a repository that protects its required policies with CODEOWNERS should promote it to an error.

It's still a review aid, not a security control. It helps in a repository whose pull requests someone reviews, and does nothing for a bundle that arrives through `policy.MapFS` from wherever the ConfigMap was written. The loader never looks at paths, so the only thing the running service enforces is `policy.From`.

## Why a kind document must match the host's kind

A policy repository usually keeps the exported kind file, `deploy_approval.sigil`, next to the policies, and sometimes it ends up in the bundle. The loader never takes a kind document as the contract. The contract is the host's Go definition, as [Kinds as contracts](/understanding/kinds/#why-the-contract-comes-from-go) explains. The CLI is the exception, [below](#why-the-cli-finds-kinds-among-its-inputs).

A kind document isn't ignored either, when it carries the host's kind name. The loader compares it with the host's `Schema()` and fails on any difference. That catches a stale export: a policy repository that forgot to regenerate `deploy_approval.sigil` after a kind change fails to load, instead of its CI having checked the policies against an outdated contract. A kind document for another kind is ignored, since it describes a contract this host doesn't have.

## Why the CLI finds kinds among its inputs

The stock `sigil` binary has no Go definition to take the contract from, so for the CLI the exported kind file is the contract. Its header names the kind, and so does the header of every policy written against it. Asking for the kind file with a flag as well would say the same thing twice. So the CLI reads kind documents from its inputs, like any other document, and joins each policy to the kind its header names. That makes a repository holding two kinds one `sigil check` run, and a self-contained file, the kind and its policies separated by `---`, something every command can read.

The guarantee the Go loader gives still holds where it matters: the same kind from two sources must be identical, and a host binary compares every kind file with the kind linked into it. A stale export fails there as it fails in `Load`. `--kind` stays for a kind file kept outside the paths, such as one vendored from the host's repository.

## Related

- [Composition without templating](/understanding/composition/) covers what required policies guarantee.
- [Kinds as contracts](/understanding/kinds/) covers where the kind comes from.
- [Policies in a ConfigMap](/guides/configmaps/) sets all of this up on Kubernetes.
