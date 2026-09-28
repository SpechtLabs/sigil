// Command deploygate is the example service built on Sigil: a deploy-approval
// API where each team's deploy policy is a Sigil document, the platform's
// guardrails are compiled into the binary and required of every team, and the
// team policies reload in place with last-known-good semantics.
package main

import (
	"context"
	"os"

	humane "github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/examples/internal/config"
)

// version is set by GoReleaser through -ldflags "-X main.version=...".
var version = "dev"

func main() {
	root := config.NewRootCommand(version, serve)
	if err := root.ExecuteContext(context.Background()); err != nil {
		humane.Eprint(err)
		os.Exit(1)
	}
}
