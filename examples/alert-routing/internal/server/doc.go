// Package server is alertrouter's HTTP API: one gin router serving the
// Alertmanager webhook, single-alert routing, the policy and team
// administration endpoints, health checks and Prometheus metrics,
// instrumented with OpenTelemetry spans and zap logs.
//
// [New] builds the [Server] from functional options; the policy store,
// [WithStore], and the team directory, [WithDirectory], are required.
// [Server.Serve] runs it until its context ends, and [Server.Handler] hands
// the router to tests.
//
// # Routing
//
// POST /api/v1/alerts takes Alertmanager's webhook and routes each firing
// alert; POST /api/v1/teams/{team}/route routes one alert posted as JSON.
// Both go through the same path. The alert's team label, or the path, picks
// the team in the directory, and the team's policy, <team>.alerts, decides
// with the team's on-call target and channel as its input. The result is
// read through the typed decision handles: routing.Page.Match gives the
// target to page and routing.Notify.Match the channel to post to. The
// decision goes to the [dispatch.Notifier], [WithNotifier], and back to the
// caller with the trace of candidates that led there.
//
// No firing alert is lost. An alert no team owns and one the router can't
// read, such as one without a known severity, go to the kind's default,
// notify(reason: unrouted), without an evaluation. An evaluation that fails
// or runs past the evaluation timeout, [WithEvaluationTimeout], goes to the
// fallback its result holds, the same default. Each is still dispatched and
// reported with its status: unowned, invalid or failed. The webhook answers
// 200 once the batch is processed, since Alertmanager retries anything
// else. The route endpoint's status says whose fault a failure is: a failed
// input assert is the caller's and answers 422, a conflict, a failed
// outcome assert or a runtime error is the policy's and answers 500, and a
// timeout is alertrouter's and answers 503, each with the fallback decision
// in the body. A request the client canceled answers
// [StatusClientClosedRequest] with no body and counts as no failure. Every
// other error is a humane error, rendered as an [ErrorEnvelope].
//
// # Observability
//
// Each firing alert is routed in an alertrouter.route span of its own, with
// the alert, the team, the decision and one event per trace candidate, and
// logged in one "alert routed" line carrying the span's trace id. It is
// counted on the [telemetry.Metrics] passed with [WithMetrics]: received,
// routed by outcome, decided, failed, and dispatched. The request metrics
// label a request with its route template, and an unowned alert's team as
// [telemetry.NoTeam], so a client can't create series without bound. Probes
// and scrapes are neither traced nor logged.
package server
