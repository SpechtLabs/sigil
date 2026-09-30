---
title: Policies in a ConfigMap
icon: mdi:kubernetes
createTime: 2026/09/25 10:00:00
permalink: /guides/configmaps/
---

By the end of this guide your teams' policies ship to a Sigil host service as a ConfigMap built with kustomize, the platform's guardrails stay out of reach of whoever writes that ConfigMap, the service reloads a changed ConfigMap without an outage, and CI checks exactly what the service will load. The Go snippets are for whoever maintains the service; the rest is YAML and shell.

It builds on the `deploy.*` and `payments.production` documents from [Per-team policies](/guides/team-policies/) and on the host from [Embed Sigil in a Go service](/guides/embed-go/). The service is a deploy gate that loads `payments.production` and requires `deploy.guardrails`.

Documents resolve by the name in their header, not by file path, so the flat keys of a ConfigMap hold them however you split them; see [Name resolution](/reference/bundles/#name-resolution) and [Bundles and trust](/understanding/bundles/).

## Decide what's trusted

Two sets of documents end up in the service, and they deserve different trust:

| Documents                                                 | Owned by          | Ships as                                                                                                 |
| --------------------------------------------------------- | ----------------- | -------------------------------------------------------------------------------------------------------- |
| `deploy.common`, `deploy.guardrails`, `deploy.production` | The platform team | Embedded in the service binary with `embed.FS`, or a separate ConfigMap only the platform team can write |
| `payments.production` and every other team policy         | Each team         | A shared ConfigMap, one key per team                                                                     |

Keep the platform's documents out of the team ConfigMap, and load them with `policy.From` as [Load it in the service](#load-it-in-the-service) shows, so whoever writes the ConfigMap can't replace the guardrails. [Why required policies need a trusted source](/understanding/bundles/#why-required-policies-need-a-trusted-source) explains the threat.

## Lay out the repository

Give each platform document its own file, at the path its name spells, and give each team one file for all its documents, so reviewers and CODEOWNERS can work per file:

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

Put CODEOWNERS on `deploy/` and on each team's file, and turn on the [`path-matches-name`](/reference/lints/#path-matches-name) lint only if your teams keep one document per file, as the [example service](/guides/example-service/) does with `teams/payments/production.sigil`, since it flags a file that holds several; both are review aids, and `policy.From` is what the service enforces ([why](/understanding/bundles/)).

Keep the kind file out of the ConfigMap. The service defines the kind in Go and never takes a kind document from a bundle as the contract; if one turns up with the same kind name, the load fails unless it matches the Go definition exactly. See [Kind documents in a bundle](/reference/bundles/#kind-documents-in-a-bundle).

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

Sigil only needs each key to end in `.sigil`, which the loader requires of every file, and to be unique. Base names such as `production.sigil` in several directories would collide, so give those an explicit key (`payments.sigil=payments/production.sigil`).

A key can hold several documents. Separate them with `---`, which is optional but keeps a long file scannable, and which `sigil fmt` writes for you:

```sigil title="teams/payments.sigil"
policy payments.production: DeployApproval@1

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
  approve(reason: payments_sre, bake: 15m)
}

---

policy payments.staging: DeployApproval@1

use deploy.guardrails
use deploy.production

guardrails(min_soak: 1h)

production(approvers: ["payments-leads"], tiers: [standard, internal, critical])
```

The `---` lines don't clash with YAML. In the generated ConfigMap the value is a block scalar written with `|`, and the `---` lines are indented with the rest of the text, so YAML doesn't read them as its own document markers:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: deploy-team-policies-5f7h8m2k9t
data:
  payments.sigil: |
    policy payments.production: DeployApproval@1
    ...

    ---

    policy payments.staging: DeployApproval@1
    ...
```

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

and load the team bundle with `os.DirFS`, with the guardrails pinned to the embedded source:

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

`os.DirFS` reads the mounted volume unchanged, although kubelet doesn't write the keys as plain files. It writes them into a hidden, timestamped directory such as `..2026_09_25_…`, links that as `..data`, and links each key at the top level. Two loader rules make that work, and together they read each key exactly once:

- The loader skips every file or directory whose name starts with `.`. Without that, it would read every document three times, through the key, `..data` and the timestamped directory, and each would collide with itself.
- The loader follows symbolic links: it checks each entry with `fs.Stat`, not with the directory entry's type. Every top-level key is a link into `..data/`, so a loader that only accepted regular files would load nothing at all, without an error.

The exact rules are in [Loading files](/reference/bundles/#loading-files).

A service that reads the ConfigMap through the Kubernetes API instead gets its `data` as a `map[string]string`. `policy.MapFS` turns that into an in-memory `fs.FS`, with each key as a file name:

```go
cm, err := client.CoreV1().ConfigMaps("deploy-gate").Get(ctx, "deploy-team-policies", metav1.GetOptions{})
if err != nil {
	return err
}

p, err := Deploy.Load(policy.MapFS(cm.Data), "payments.production",
	policy.Require("deploy.guardrails", policy.From(platformFS)))
```

Either way, the service names the root it loads, so one ConfigMap can serve many policies.

## Reload without an outage

kustomize appends a content hash to the generated ConfigMap's name, so by default a policy change rolls the Deployment. New pods load the new bundle, and a bundle that fails to load stops the new pods from becoming ready, so the rollout stalls on the old pods instead of serving without a policy.

To pick up changes without a rollout, turn the hash off (`generatorOptions: {disableNameSuffixHash: true}`) and reload in place.

Keep the compiled policy behind an `atomic.Pointer`. A compiled policy is immutable, so a reload is a pointer swap, and in-flight evaluations keep using the policy they started with:

```go
var current atomic.Pointer[policy.Policy[Input]]

res, err := current.Load().Eval(ctx, input)
```

Reload with last-known-good semantics. A bundle loads as a whole, so one team's typo fails the reload for every team. Compile the new bundle first, swap only on success, and otherwise keep serving the old policy, log the error and record it in a metric you alert on:

```go
func reload(fsys fs.FS) {
	p, err := Deploy.Load(fsys, "payments.production",
		policy.Require("deploy.guardrails", policy.From(platformFS)))

	if err != nil {
		slog.Error("policy reload failed, keeping the last good policy", "err", err)
		lastReloadSuccessful.Set(0) // alert on this: the running policy is now stale
		return
	}

	current.Store(p)
	lastReloadSuccessful.Set(1)
}
```

Alert on a gauge like this one staying at `0`, not on the rate of a failure counter: a service that only reloads when the content changes reports a broken bundle once, and an alert on the counter's rate resolves while the stale bundle keeps serving.

At startup there's no last good policy, so a failed `Load` should stop the process. On Kubernetes that holds a rollout at the old pods instead of serving without a policy.

Trigger the reload the way the mounted volume actually changes:

- **Watch `..data`, not the key.** Kubelet updates a mounted ConfigMap by writing a new timestamped directory and swapping the `..data` symlink to it. The key files themselves never change, so a watcher on `/etc/sigil/payments.sigil` can miss the update. Watch the directory and reload when `..data` is replaced. Polling works too: the [example service](/guides/example-service/) hashes the content of every `.sigil` file it can reach through the links, every 30 seconds by default, and reloads when the hash changes.
- **Don't use `subPath` mounts.** A ConfigMap mounted with `subPath` never receives updates at all.

The example service's `internal/store` package does all of this for several roots at once, with reloads on a signal and on a poll, and reports each bundle's reload health; see [Watch it reload](/guides/example-service/#watch-it-reload).

## Check in CI what the service will load

The loader fails on any broken document, even one the root never imports, and on any name defined twice, so CI is the place to catch problems. Check the team documents with the same trusted source and the same requirement the host uses, and name the roots:

```shell
sigil fmt --check .
sigil check \
  --require deploy.guardrails --trusted deploy/ \
  --policy 'payments.*' --policy 'checkout.*' \
  deploy_approval.sigil teams/
```

`check` reads `teams/` into one bundle, with the kind from `deploy_approval.sigil`, reads `deploy/` as the trusted source, fails on a team document that claims a platform name or on a name two teams both define, and checks that every root invokes `deploy.guardrails` unconditionally.

When overlays add documents to the ConfigMap, a check of one directory doesn't see what the overlays merge in, and two teams can pass CI separately and still collide on a name. Check the rendered output instead. Documents delimit themselves by their headers, so every key of the ConfigMap can go to `sigil check` as one stream:

```shell
kustomize build overlays/production \
  | yq 'select(.kind == "ConfigMap" and (.metadata.name | test("^deploy-team-policies"))) | .data[]' \
  | sigil check --kind deploy_approval.sigil \
      --require deploy.guardrails --trusted deploy/ --policy '*' -
```

The rendered ConfigMap holds only the policies, so `--kind` names the kind file. Errors from stdin still name the document, as in `<stdin>:42:5 (payments.production)`, so they point back to a file in the repository.

The rest of a policy repository's pipeline, tests and the kind export check included, is in [Check policies in CI](/guides/ci/).

## Further reading

- [Bundles](/reference/bundles/) has the exact rules for documents, duplicates and which files the loader reads.
- [Trusted sources](/reference/bundles/#trusted-sources) specifies `policy.From`.
- [CLI reference](/reference/cli/#inputs) covers files, directories, stdin, `--policy` and `--trusted`.
- [Per-team policies](/guides/team-policies/) covers what goes into the documents themselves.
