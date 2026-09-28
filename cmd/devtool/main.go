// Command devtool is sigil's development tooling: benchmark comparisons and
// fuzz campaigns. It isn't released; run it with go run ./cmd/devtool.
package main

import (
	"context"
	"os"

	"github.com/spechtlabs/sigil/cmd/devtool/command"
)

func main() {
	os.Exit(command.Execute(context.Background(), command.NewCommand(), nil))
}
