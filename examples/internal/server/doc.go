// Package server is deploygate's HTTP API: one gin router serving the
// deploy decisions, the policy administration endpoints, health checks and
// Prometheus metrics, instrumented with OpenTelemetry spans and zap logs.
//
// [New] builds the [Server] from functional options; the two policy stores,
// [WithStore] and [WithAccessStore], are required. [Server.Serve] runs it
// until its context ends, and [Server.Handler] hands the router to tests.
//
// # Evaluating
//
// POST /api/v1/teams/{team}/deployments runs two policies. The access policy
// grants the requestor roles for the team, and the team's deploy policy
// decides with those roles as actor.roles. Both results are read through the
// typed decision handles: deploy.Approve.Match and deploy.Review.Match give
// the winner's payload as a struct, and access.Deployer.MatchAll and its
// siblings give every grant. The decision maps to the status, approve 200,
// review 202 and deny 403, so a client can act on the status alone.
//
// A failed evaluation still answers with a decision, the kind's fallback,
// deny, next to the error and the failed asserts or conflicting candidates
// that explain it. The status says whose fault the failure is, the same in
// both stages: a failed input assert is the caller's and answers 422, and a
// conflict, a failed outcome assert or a runtime error is the policy's and
// answers 500. Every other error is a humane error, rendered as an
// [ErrorEnvelope].
//
// # Observability
//
// Each evaluation runs in a span of its own, deploygate.access or
// deploygate.evaluate, with one event per grant or trace candidate, and is
// counted on the [telemetry.Metrics] passed with [WithMetrics]. The request
// metrics label a request with its route template, so a client can't create
// series without bound. Probes and scrapes are neither traced nor logged.
package server
