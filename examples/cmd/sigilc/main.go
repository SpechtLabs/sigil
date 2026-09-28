// Command sigilc is the sigil command line with the example's kinds linked
// in. Unlike the stock sigil binary it decodes inputs into the host's Go
// types and calls the real host functions, so `sigilc eval` and
// `sigilc test` run the same code the service runs, and `sigilc export`
// writes the kind file the policy repository checks in.
package main

import (
	"github.com/spechtlabs/sigil/pkg/cli"

	"github.com/spechtlabs/sigil/examples/internal/deploy"
)

// version is set by GoReleaser through -ldflags at release time.
var version = "dev"

//go:generate go run . export --out ../../policies/deploy_approval.sigil

func main() {
	cli.Main(cli.WithKind(deploy.Kind), cli.WithVersion(version))
}
