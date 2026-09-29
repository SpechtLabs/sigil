// Command deploygate is the example service built on Sigil: a deploy-approval
// API where each team's deploy policy is a Sigil document, the platform's
// guardrails are compiled into the binary and required of every team, and the
// team policies reload in place with last-known-good semantics.
//
// A deployment request is decided in two stages. The AccessGrant policy
// access.main grants the requestor roles for the team, and the team's
// DeployApproval policy, <team>.production, decides with those roles as
// actor.roles, so a client can't claim a role. The answer is approve, review
// or deny, with a reason, a typed payload and the trace of candidates that
// led there, and the HTTP status encodes the decision.
//
// Usage:
//
//	deploygate serve [flags]
//	deploygate healthcheck [--addr address]
//	deploygate version
//
// serve loads both bundles, from the directories given with --policies and
// --access-policies or from the copies embedded in the binary, and exits when
// either doesn't compile. It then serves the API, /healthz, /readyz and
// /metrics on --addr, reloads on SIGHUP and when the --reload-interval poll
// sees a change, and shuts down gracefully on SIGINT and SIGTERM. Every flag
// can also be set through a DEPLOYGATE_ environment variable; `deploygate
// serve --help` lists them.
//
// healthcheck asks the deploygate on the same host for /readyz and exits 0
// when it answers 200, for container images without a shell. version prints
// the version GoReleaser set, or dev.
//
// The service is split over the examples module's internal packages: config
// builds this command line, store loads and reloads the bundles, server is
// the HTTP API, and telemetry sets up traces, logs, metrics and profiles.
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
