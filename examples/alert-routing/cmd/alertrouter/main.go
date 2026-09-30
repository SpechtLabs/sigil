// Command alertrouter is the alert-routing example service built on Sigil:
// it receives Alertmanager's webhook and asks the owning team's Sigil policy
// what to do with each firing alert, page someone, drop it or post it to a
// channel, and dispatches the decision. The platform's paging rule is
// compiled into the binary and required of every team, and the team policies
// reload in place with last-known-good semantics.
//
// An alert's team label picks the team in the team directory, which gives
// the team's on-call target and channel, and the team's AlertRouting policy,
// <team>.alerts, decides with them. The answer is page, drop or notify, with
// a reason, the target or channel, and the trace of candidates that led
// there. An alert no team owns, or one the router can't read, goes to the
// kind's default, notify(reason: unrouted) to #alerts, so no alert is
// silently lost.
//
// Usage:
//
//	alertrouter serve [flags]
//	alertrouter healthcheck [--addr address]
//	alertrouter version
//
// serve loads the team directory, from --teams-file or the copy embedded in
// the binary, and the team policies, from the directory given with
// --policies or the copies embedded in the binary, and exits when either
// doesn't load. It then serves the API, /healthz, /readyz and /metrics on
// --addr, reloads the policies on SIGHUP and when the --reload-interval poll
// sees a change, and shuts down gracefully on SIGINT and SIGTERM. Every flag
// can also be set through an ALERTROUTER_ environment variable; `alertrouter
// serve --help` lists them.
//
// healthcheck asks the alertrouter on the same host for /readyz and exits 0
// when it answers 200, for container images without a shell. version prints
// the version GoReleaser set, or dev.
//
// The service is split over the examples module's internal packages: config
// builds this command line, teams loads the team directory, store loads and
// reloads the policies, server is the HTTP API, dispatch delivers the
// decisions, and telemetry sets up traces, logs, metrics and profiles.
package main

import (
	"context"
	"os"

	humane "github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/config"
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
