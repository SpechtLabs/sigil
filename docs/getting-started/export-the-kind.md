---
title: 3. Export the kind
icon: mdi:file-export-outline
createTime: 2026/09/30 12:00:00
permalink: /getting-started/export-the-kind/
---

So far the kind exists only as Go code, so the only way to find out whether a policy compiles is to run your program. That's fine while you're the only one writing policies. Once someone edits policies without building the service, or you want to check them in CI, they need the contract as a file. In this step your module writes the kind to `policies/alert_routing.sigil` and a test keeps that file current.

## Why a file

The **kind file** is the kind written out in Sigil's own syntax. You've read one already: it's the first file in the [tour](/getting-started/tour/#the-kind). The `sigil` CLI reads it in place of your Go code, so a policy repository can check, evaluate and test policies with nothing but the CLI and that one file.

Nobody edits it. It's generated from the Go definition, which stays the single source of truth, and a stale copy fails a test. [Kinds as contracts](/understanding/kinds/) explains why the contract lives in Go.

## Build your own `sigil`

Package `cli` is the whole `sigil` command line. Linking your kind into it gives you a `sigil` binary that knows `AlertRouting` natively, and one of its commands, `export`, writes the kind file. Create `cmd/sigil/main.go`:

```go
// Command sigil is the sigil CLI with the AlertRouting kind linked in.
package main

import (
	"example.com/alerting"
	"github.com/spechtlabs/sigil/pkg/cli"
)

//go:generate go run . export --out ../../policies/alert_routing.sigil

func main() {
	cli.Main(cli.WithKind(alerting.Kind))
}
```

Generate the file:

```text
$ go generate ./cmd/sigil
✓ wrote ../../policies/alert_routing.sigil
```

::: file-tree

- alerting
  - kind.go
  - ++ kind_test.go
  - policies.go
  - policies
    - ++ alert_routing.sigil # generated, committed
    - checkout
      - alerts.sigil
  - cmd
    - route
      - main.go
    - ++ sigil
      - main.go

:::

Commit the kind file next to the policies. Run `go generate ./cmd/sigil` again whenever you change the kind.

## Keep it current

A kind change that nobody exported leaves everyone checking policies against an outdated contract. Catch it in `go test`, with `kind_test.go`:

```go
package alerting_test

import (
	"testing"

	"example.com/alerting"
	"github.com/spechtlabs/sigil/pkg/policytest"
)

func TestKindFileIsCurrent(t *testing.T) {
	policytest.Schema(t, alerting.Kind, "policies/alert_routing.sigil")
}
```

Add a reason to `page` in Go without exporting, and the test fails with both versions side by side:

```text
$ go test .
--- FAIL: TestKindFileIsCurrent (0.00s)
    kind_test.go:11: policies/alert_routing.sigil is stale: it isn't AlertRouting's Schema(); regenerate it, for example with `sigil export --out policies/alert_routing.sigil` in a host binary
        --- got ---
        kind AlertRouting version 1
        ...
```

In CI, `export --check` compares without writing and exits 1 when the file is stale:

```text
$ go run ./cmd/sigil export --check --out policies/alert_routing.sigil
✗ policies/alert_routing.sigil is stale: it doesn't match the kind AlertRouting linked into this binary
  regenerate it with this binary's `export AlertRouting --out policies/alert_routing.sigil`
```

A stale file can't take effect in the service either: when the policies your program loads include a kind file for its own kind, `Load` fails unless the two match exactly.

Next: [Check, evaluate and test with the CLI](/getting-started/check-and-test/).
