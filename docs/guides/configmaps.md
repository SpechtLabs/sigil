---
title: Policies in a ConfigMap
icon: mdi:kubernetes
createTime: 2026/09/25 10:00:00
permalink: /guides/configmaps/
---

This guide shows how to ship policies to a service running on Kubernetes as a ConfigMap, built with kustomize from files in a policy repository, how to keep the platform's guardrails out of reach of whoever writes that ConfigMap, and how to check in CI exactly what the service will load.

It builds on the `deploy.*` and `payments.production` documents from the [tour](/getting-started/tour/). The service is a deploy gate that loads `payments.production` and requires `deploy.guardrails`.

::: info Planned tooling
Nothing on this page is implemented yet. The commands and the Go API are the planned ones; see [CLI & editor tooling](/reference/cli/) and the [Go API](/reference/go-api/).
:::

## Why files are only containers

A ConfigMap is a flat map from keys to strings, and a key can't contain `/`. A layout such as `deploy/common.sigil` can't survive the trip into one. That's why Sigil resolves `use deploy.common` by the name in each document's header rather than by file path: the loader reads every document in every file it's given, and indexes them by name. Whether the documents arrive as one key per file, one key per team or one key for everything, they resolve the same way. The rules are in [Bundles and resolution](/reference/policy-files/#bundles-and-resolution).

## Decide what's trusted

Two sets of documents end up in the service, and they deserve different trust:

| Documents | Owned by | Ships as |
| --- | --- | --- |
| `deploy.common`, `deploy.guardrails`, `deploy.production` | The platform team | Embedded in the service binary with `embed.FS`, or a separate ConfigMap only the platform team can write |
| `payments.production` and every other team policy | Each team | A shared ConfigMap, one key per team |

The split matters because documents resolve by name. If the guardrails lived in the same ConfigMap as the team policies, anyone who can edit that ConfigMap could replace `deploy.guardrails` with a document of the same name and no denies, and `policy.Require` would still pass. Loading the platform's documents from their own source with `policy.From` rules that out; see [Protect the guardrails](#protect-the-guardrails).

## Lay out the repository

Keep one document per file, with paths that match the names. That's what reviewers and CODEOWNERS work with:

```text
policies/
├── deploy_approval.sigil
├── deploy/                  # platform, trusted
│   ├── common.sigil
│   ├── guardrails.sigil
│   └── production.sigil
├── teams/
│   ├── payments.sigil       # payments.production, and any other payments documents
│   └── checkout.sigil
└── kustomization.yaml
```

The kind file stays out of the ConfigMap. The service defines the kind in Go and never trusts a kind document from a bundle; if one turns up with the same kind name, the load fails unless it matches the Go definition exactly.

## Generate the ConfigMap

Use one key per team. The load still fails as a whole when any document is broken, but separate keys keep each team's changes in its own diff, and let each team own its key through CODEOWNERS on its file. kustomize's `configMapGenerator` turns files into keys, named after the file by default:

```yaml title="policies/kustomization.yaml"
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

configMapGenerator:
  - name: deploy-team-policies
    files:
      - teams/payments.sigil
      - teams/checkout.sigil
```

The key names don't matter to Sigil beyond ending in `.sigil`, as long as they're unique; base names such as `production.sigil` in several directories would collide, so give those an explicit key (`payments.sigil=payments/production.sigil`).

A key can hold several documents. Separate them with `---`, which is optional but keeps a long file scannable, and which `sigil fmt` writes for you:

```sigil title="teams/payments.sigil"
policy payments.production: DeployApproval

use deploy.guardrails
use deploy.production
use deploy.common.{cleared}

guardrails(min_soak: 4h)

when service.labels["compliance"] == "pci" {
  production(approvers: ["payments-leads", "security-leads"])
}

when service.labels["compliance"] != "pci" {
  production(approvers: ["payments-leads"])
}

when cleared and "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}

---

policy payments.staging: DeployApproval

use deploy.guardrails
use deploy.production

guardrails(min_soak: 1h)
production(approvers: ["payments-leads"], tiers: ["standard", "internal", "critical"])
```

Inside a YAML block scalar the `---` lines are indented along with everything else, so YAML doesn't mistake them for its own document markers.

## Load it in the service

Embed the platform's documents in the binary, mount the team ConfigMap as a volume:

```yaml
volumes:
  - name: team-policies
    configMap:
      name: deploy-team-policies
containers:
  - name: deploy-gate
    volumeMounts:
      - name: team-policies
        mountPath: /etc/sigil
        readOnly: true
```

and load the team bundle with the guardrails pinned to the embedded source:

```go
// The platform's deploy/*.sigil, copied in at build time.
//
//go:embed platform/*.sigil
var platformFS embed.FS

func load() (*policy.Policy[Input], error) {
	return Deploy.Load(os.DirFS("/etc/sigil"), "payments.production",
		policy.Require("deploy.guardrails", policy.From(platformFS)))
}
```

`payments.production` imports `deploy.guardrails`, `deploy.production` and `deploy.common` by name as usual, and they resolve to the embedded documents. A team document that claims any of those names is a compile error.

Kubelet doesn't write the keys as plain files. It writes them into a hidden, timestamped directory, links that as `..data`, and links each key at the top level. The loader skips every entry whose name starts with `.` and follows the symbolic links, so it reads each key exactly once.

A service that reads the ConfigMap through the Kubernetes API instead gets its `data` as a `map[string]string`, which `policy.MapFS` turns into a file system:

```go
p, err := Deploy.Load(policy.MapFS(cm.Data), "payments.production",
	policy.Require("deploy.guardrails", policy.From(platformFS)))
```

## Reload without an outage

kustomize appends a content hash to the generated ConfigMap's name, so by default a policy change rolls the Deployment. New pods load the new bundle, and a bundle that fails to load stops the new pods from becoming ready, so the rollout stalls on the old pods instead of serving without a policy.

To pick up changes without a rollout, turn the hash off (`generatorOptions: {disableNameSuffixHash: true}`) and reload in place:

- **Watch `..data`, not the key.** Kubelet updates a mounted ConfigMap by writing a new timestamped directory and swapping the `..data` symlink to it. The key files themselves never change, so a watcher on `/etc/sigil/payments.sigil` can miss the update. Watch the directory and reload when `..data` is replaced.
- **Keep the last known good policy.** A bundle loads as a whole, so one team's typo fails the reload for every team. Compile first, swap only on success, and otherwise keep serving the old policy, log the error and increment a failure metric you alert on; the [Go API](/reference/go-api/#hot-reload) shows the pattern.
- **Don't use `subPath` mounts.** A ConfigMap mounted with `subPath` never receives updates at all.

## Check in CI what the service will load

The loader fails on any broken document, even one the root never imports, and on any name defined twice. That keeps loading predictable, and it makes CI the place to catch problems. Check the team documents with the same trusted source and the same requirement the host uses, and name the roots:

```shell
sigil fmt --check .
sigil check --kind deploy_approval.sigil \
  --require deploy.guardrails --trusted deploy/ \
  --policy 'payments.*' --policy 'checkout.*' \
  teams/
```

`check` reads `teams/` into one bundle, reads `deploy/` as the trusted source, fails on a team document that claims a platform name or on a name two teams both define, and checks that every root invokes `deploy.guardrails` unconditionally.

When overlays add documents to the ConfigMap, a check of one directory doesn't see what the overlays merge in, and two teams can pass CI separately and still collide on a name. Check the rendered output instead. Documents delimit themselves by their headers, so every key of the ConfigMap can go to `sigil check` as one stream:

```shell
kustomize build overlays/production \
  | yq 'select(.kind == "ConfigMap" and (.metadata.name | test("^deploy-team-policies"))) | .data[]' \
  | sigil check --kind deploy_approval.sigil \
      --require deploy.guardrails --trusted deploy/ --policy '*' -
```

Errors from stdin still name the document, as in `<stdin>:42:5 (payments.production)`, so they point back to a file in the repository.

## Protect the guardrails

`policy.Require` checks that a policy with the required name is invoked unconditionally. `policy.From` decides which document that is. Together:

- The guardrails, and everything they import and invoke, come from the platform's source. A team can't redefine `deploy.common.eligible` to hollow them out.
- Every name the platform's source defines is reserved. A team document named `deploy.guardrails` or `deploy.common` is a compile error in CI and at load time, not a silent override.
- The team ConfigMap can then be writable by teams, by a GitOps controller or by anything else, without that writer being able to switch the guardrails off.

In the repository, CODEOWNERS on `deploy/` and on each team's file, plus the `path-matches-name` lint, keep reviews routed to the right people. They're review aids; `policy.From` is what the service enforces.

## Further reading

- [Bundles and resolution](/reference/policy-files/#bundles-and-resolution) has the exact rules for documents, duplicates and which files the loader reads.
- [Where required policies come from](/reference/go-api/#where-required-policies-come-from) specifies `policy.From`.
- [CLI & editor tooling](/reference/cli/#inputs) covers files, directories, stdin, `--policy` and `--trusted`.
- [Per-team policies](/guides/team-policies/) covers what goes into the documents themselves.
