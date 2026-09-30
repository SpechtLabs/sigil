// Command demo-cli is the example alerting platform's client for
// alertrouter. It stands in for Alertmanager and for an engineer asking why
// an alert went where it did: it sends a named scenario, a sample request
// embedded in the binary, or an alert described on the command line, and
// prints what alertrouter decided.
//
// Usage:
//
//	demo-cli route [scenario] [--file path --team team] [--explain]
//	demo-cli route --team team --name name --severity severity [--label k=v]... [--firing-for 12m]
//	demo-cli webhook [scenario] [--file path]
//	demo-cli scenario [list]
//	demo-cli policy [list | reload]
//	demo-cli teams
//	demo-cli status
//	demo-cli metric
//	demo-cli version
//
// --url picks the service, by default $ALERTROUTER_URL or
// http://localhost:8080, and --timeout bounds each request, ten seconds by
// default. --json prints the response body as indented JSON instead of a
// summary, and --explain adds the trace: every candidate with its conditions
// and source location.
//
// Every response is printed before the exit status is decided. A 2xx status
// exits 0, whatever the policy decided: a drop is as much a route as a
// page. A 422, 500 or 503 whose body carries a failed evaluation's fallback
// exits 2, so a script can tell a policy that failed, whose alert still went
// to #alerts, from any other failure, which exits 1.
package main

import (
	"context"
	"os"

	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/command"
)

// version is set by GoReleaser through -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(command.Execute(context.Background(), command.NewCommand(command.WithVersion(version)), nil))
}
