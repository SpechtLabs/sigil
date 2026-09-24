---
title: Policies in a ConfigMap
icon: mdi:kubernetes
createTime: 2026/09/25 10:00:00
permalink: /guides/configmaps/
---

This guide shows how to ship policies to a service running on Kubernetes as a ConfigMap, built with kustomize from files in a policy repository, and how to check in CI exactly what the service will load.

It builds on the `deploy.*` and `payments.production` documents from the [tour](/getting-started/tour/). The service is a deploy gate that loads `payments.production` and requires `deploy.guardrails`.

::: info Planned tooling
Nothing on this page is implemented yet. The commands and the Go API are the planned ones; see [CLI & editor tooling](/reference/cli/) and the [Go API](/reference/go-api/).
:::

## Why files are only containers

A ConfigMap is a flat map from keys to strings, and a key can't contain `/`. A layout such as `deploy/common.sigil` can't survive the trip into one. That's why Sigil resolves `use deploy.common` by the name in each document's header rather than by file path: the loader reads every document in every file it's given, and indexes them by name. Whether the documents arrive as one key per file, one key per team or one key for everything, they resolve the same way. The rules are in [Bundles and resolution](/reference/policy-files/#bundles-and-resolution).

## Lay out the repository

Keep one document per file in the repository, with paths that match the names. That's what reviewers and CODEOWNERS work with:

```text
policies/
├── deploy_approval.sigil
├── deploy/
│   ├── common.sigil
│   ├── guardrails.sigil
│   └── production.sigil
├── payments/
│   └── production.sigil
└── kustomization.yaml
```

The kind file stays out of the ConfigMap. The service defines the kind in Go and never reads it.

## Generate the ConfigMap

kustomize's `configMapGenerator` turns files into keys. By default a key is the file's base name, which would make `deploy/production.sigil` and `payments/production.sigil` collide, so give every file an explicit key:

```yaml title="policies/kustomization.yaml"
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

configMapGenerator:
  - name: deploy-policies
    files:
      - deploy.common.sigil=deploy/common.sigil
      - deploy.guardrails.sigil=deploy/guardrails.sigil
      - deploy.production.sigil=deploy/production.sigil
      - payments.production.sigil=payments/production.sigil
```

The key names don't matter to Sigil beyond ending in `.sigil`. Using the document name keeps them unique and readable.

If you'd rather ship one key, concatenate the documents into a single file. Separate them with `---`, which is optional but keeps a long bundle scannable, and which `sigil fmt` writes for you:

```sigil title="policies.sigil"
module deploy.common: DeployApproval

let owns_service = actor.teams any in service.owners
let cleared =
  split(service.labels["regions"], ",") all in actor.regions
let eligible =
  "deployer" in actor.roles
  and environment == "production"
  and service.labels has {
    "app.kubernetes.io/managed-by": "argocd",
    "platform.example.com/lifecycle": "ga",
  }

---

policy deploy.guardrails: DeployApproval

use deploy.common.{eligible}

param min_soak: duration = 24h

when not eligible {
  deny("not_eligible")
}

when release.soak < min_soak and not release.hotfix {
  deny("soak_too_short")
}

---

policy payments.production: DeployApproval

use deploy.guardrails
use deploy.production
use deploy.common.{cleared}

guardrails(min_soak: 4h)

// ...the rest of the team policy, and deploy.production, as in the tour
```

Inside a YAML block scalar the `---` lines are indented along with everything else, so YAML doesn't mistake them for its own document markers. Mixing works too: one key per team, each holding that team's documents.

## Load it in the service

Mount the ConfigMap as a volume:

```yaml
volumes:
  - name: policies
    configMap:
      name: deploy-policies
containers:
  - name: deploy-gate
    volumeMounts:
      - name: policies
        mountPath: /etc/sigil
        readOnly: true
```

and load it with `os.DirFS`:

```go
p, err := Deploy.Load(os.DirFS("/etc/sigil"), "payments.production",
	policy.Require("deploy.guardrails"))
```

Kubelet doesn't write the keys as plain files. It writes them into a hidden, timestamped directory, links that as `..data`, and links each key at the top level. The loader skips every entry whose name starts with `.`, so it reads each key exactly once. The host always names the root, so one ConfigMap can hold policies for several services, and each loads its own entry point.

A service that reads the ConfigMap through the Kubernetes API instead gets its `data` as a `map[string]string`, which `policy.MapFS` turns into a file system:

```go
p, err := Deploy.Load(policy.MapFS(cm.Data), "payments.production",
	policy.Require("deploy.guardrails"))
```

kustomize appends a content hash to the generated ConfigMap's name, so a policy change rolls the Deployment and new pods load the new bundle. If you turn the hash off and watch the mounted files instead, reload by compiling the new bundle and swapping the pointer; see [Hot reload](/reference/go-api/#hot-reload). A bundle that fails to compile then leaves the old policy in place.

## Check in CI what the service will load

The loader fails on any broken document in the bundle, even one the root never imports, and on any name defined twice. That keeps loading predictable, and it makes CI the place to catch problems. Check the same documents the ConfigMap is built from, with the same requirement the host sets:

```shell
sigil fmt --check .
sigil check --kind deploy_approval.sigil --require deploy.guardrails deploy/ payments/
```

`check` reads both directories into one bundle, reports duplicate names across files, and checks that every root policy, here `payments.production`, invokes `deploy.guardrails` unconditionally.

When overlays add documents to the ConfigMap, a check of one directory doesn't see what the overlays merge in, and two teams can pass CI separately and still collide on a name. Check the rendered output instead. Documents delimit themselves by their headers, so every key of the ConfigMap can go to `sigil check` as one stream:

```shell
kustomize build overlays/production \
  | yq 'select(.kind == "ConfigMap" and (.metadata.name | test("^deploy-policies"))) | .data[]' \
  | sigil check --kind deploy_approval.sigil --require deploy.guardrails -
```

Errors from stdin still name the document, as in `<stdin>:42:5 (payments.production)`, so they point back to a file in the repository.

## Protect the guardrails

`policy.Require` checks names, and names come from headers, not from where a file lives. Anyone who can add a document to the bundle can define a policy called `deploy.guardrails`. If the platform's definition is in the bundle as well, the duplicate fails the build. If a team's overlay replaced it, a guardrail without denies would satisfy the requirement.

In a repository, two settings close that:

- Turn on the `path-matches-name` lint and promote it to an error, so `deploy.guardrails` can only be defined in `deploy/guardrails.sigil` (or in `deploy.sigil`).
- Put CODEOWNERS on `deploy/`, so only the platform team can change what's there.

Pinning a required policy to a trusted source instead, for example one embedded in the service's binary, is an [open question](/project/open-questions/#trusted-sources-for-required-policies).

## Further reading

- [Bundles and resolution](/reference/policy-files/#bundles-and-resolution) has the exact rules for documents, duplicates and which files the loader reads.
- [CLI & editor tooling](/reference/cli/#inputs) covers files, directories, stdin and `--policy`.
- [Per-team policies](/guides/team-policies/) covers what goes into the documents themselves.
