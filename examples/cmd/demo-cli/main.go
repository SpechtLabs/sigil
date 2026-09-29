// Command demo-cli is the example platform's client for deploygate. It stands
// in for the deployment tooling around the service: where a real platform
// would look up the actor's identity, the service's metadata and the release
// history, demo-cli sends a named scenario, a JSON request embedded in the
// binary, and prints the decision deploygate answers with.
//
// Usage:
//
//	demo-cli deploy [scenario] [--file path --team team] [--explain]
//	demo-cli access [scenario] [--file path] [--explain]
//	demo-cli policies [list | reload]
//	demo-cli status
//	demo-cli metrics
//	demo-cli scenarios
//	demo-cli version
//
// --url picks the service, by default $DEPLOYGATE_URL or
// http://localhost:8080, and --timeout bounds each request, ten seconds by
// default. --json prints the response body as indented JSON instead of a
// summary, and --explain adds the trace: every candidate with its conditions
// and source location.
//
// Every response is printed before the exit status is decided. A 2xx status
// exits 0. A refusal exits 2, so a script can tell a policy saying no from a
// failure, which exits 1. A refusal is a 403, a 422, or a 500 whose body
// carries a failed evaluation's fallback; a 500 without one exits 1.
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
