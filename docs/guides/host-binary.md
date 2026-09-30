---
title: Build a host binary
icon: mdi:console
createTime: 2026/09/29 12:00:00
permalink: /guides/host-binary/
---

The stock `sigil` binary knows your kind only from its exported file, so it can check and explain policies but can't run your host functions. By the end of this guide your service ships its own `sigil` binary, whose `eval` and `test` decode inputs into your Go types and call your real functions, and whose `export` writes the kind file your policy repository checks in, with a test that fails when that file goes stale.

It builds on the `Deploy` kind from [Embed Sigil in a Go service](/guides/embed-go/). The layout used here:

```text
deploygate/
├── kind.go              # package deploygate: Input, the decisions, Deploy
├── kind_test.go
├── cmd/sigil/main.go    # the host binary
└── policies/
    └── deploy_approval.sigil
```

## Build the binary

Package `cli` is the whole `sigil` command line. Link your kind into it with `cli.WithKind`:

```go
// cmd/sigil/main.go
package main

import (
	"github.com/spechtlabs/sigil/pkg/cli"

	"example.com/deploygate"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cli.Main(cli.WithKind(deploygate.Deploy), cli.WithVersion(version))
}
```

`cli.Main` runs the command line on `os.Args` and exits. `cli.WithVersion` sets what `sigil version` reports. Build and run it like any Go command:

```sh
go run ./cmd/sigil eval --policy payments.production --input request.json -R policies/
```

Every command uses the linked kind for the documents written against it, with no kind file. A kind file for the same kind, whether among the paths or named with `--kind`, must match the linked kind exactly, which catches a stale export.

A host with several kinds repeats `cli.WithKind`, and each document then uses the kind its header names. The example service's `sigilc` links both `DeployApproval` and `AccessGrant`:

```go
cli.Main(cli.WithKind(deploy.Kind), cli.WithKind(access.Kind), cli.WithVersion(version))
```

The commands and their flags are in the [CLI reference](/reference/cli/); what the stock binary does at a host function call is in [Host functions and host binaries](/reference/cli/#host-functions-and-host-binaries).

## Export the kind

`Deploy.Schema()` returns the kind file text, and the host binary's `sigil export` writes it. Put a `go:generate` directive next to `main`, so `go run .` builds the binary and exports the kind:

```go
//go:generate go run . export --out ../../policies/deploy_approval.sigil
```

Run it whenever the kind changes:

```sh
go generate ./cmd/sigil
```

`export --out` says whether it wrote the file or found it current. With several kinds linked, name the kind to export, one directive per kind:

```go
//go:generate go run . export DeployApproval --out ../../policies/deploy_approval.sigil
//go:generate go run . export AccessGrant --out ../../policies/access_grant.sigil
```

A host that doesn't build its own `sigil` binary can write `Schema()` from any program instead, behind a `//go:generate go run ./cmd/export-kind` line in the root package:

```go
// cmd/export-kind/main.go
func main() {
	if err := os.WriteFile("policies/deploy_approval.sigil", []byte(deploygate.Deploy.Schema()), 0o644); err != nil {
		log.Fatal(err)
	}
}
```

Commit the kind file wherever the policies live. The stock `sigil` binary, and anyone who writes policies, reads the kind from it without your Go code. The file is in `sigil fmt`'s canonical style, so it passes `sigil fmt --check`. Why the Go types stay the source of truth: [Kinds as contracts](/understanding/kinds/).

## Keep the export current

A kind change that nobody exported leaves the policy repository checking against an outdated contract. Catch it in two places.

In the host's tests, `policytest.Schema` fails when the checked-in file isn't the kind's `Schema()`, and shows both versions:

```go
// kind_test.go
func TestKindFileIsCurrent(t *testing.T) {
	policytest.Schema(t, deploygate.Deploy, "policies/deploy_approval.sigil")
}
```

In CI, `sigil export --check` compares without writing, and exits 1 when the file is stale:

```text
$ sigil export --check --out ../../policies/deploy_approval.sigil
✓ ../../policies/deploy_approval.sigil is up to date
```

A stale file that slips through still can't take effect in the service: when a bundle holds a kind document with the host kind's name, `Load` fails unless it matches the Go definition exactly. See [Kind documents in a bundle](/reference/bundles/#kind-documents-in-a-bundle).

Where the export check fits among the other CI steps is in [Check policies in CI](/guides/ci/). Before you change the kind, read [Evolve a kind safely](/guides/evolve-a-kind/); the flags of `sigil export` are in [`sigil export`](/reference/cli/#sigil-export), and `policytest` in [Package policytest](/reference/go-api/#package-policytest).
