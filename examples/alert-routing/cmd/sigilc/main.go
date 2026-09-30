// Command sigilc is the sigil command line with the AlertRouting kind linked
// in. Unlike the stock sigil binary it decodes inputs into the host's Go
// types, so `sigilc test` evaluates a policy the way alertrouter does, and
// `sigilc export` writes the kind file the policy repository checks in.
//
// The whole program is one call to [cli.Main] with a [cli.WithKind] for
// AlertRouting, which is all a host needs to ship the sigil command line
// with its own kind. `go generate ./cmd/sigilc` rewrites
// policies/alert_routing.sigil from the Go definition.
package main

import (
	"github.com/spechtlabs/sigil/pkg/cli"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
)

// version is set by GoReleaser through -ldflags at release time.
var version = "dev"

//go:generate go run . export --out ../../policies/alert_routing.sigil

func main() {
	cli.Main(cli.WithKind(routing.Kind), cli.WithVersion(version))
}
