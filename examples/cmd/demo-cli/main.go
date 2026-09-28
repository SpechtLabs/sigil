// Command demo-cli is the example platform's client for deploygate.
package main

import (
	"context"
	"os"

	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/command"
)

// version is set by GoReleaser through -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(command.Execute(context.Background(), command.NewCommand(command.WithVersion(version)), nil))
}
