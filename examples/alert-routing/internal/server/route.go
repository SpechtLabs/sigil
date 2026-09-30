package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/alertmanager"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/dispatch"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
)

// alertJob is one firing alert on its way to a decision, as either endpoint
// hands it to route: what the alert says, and what the router already found
// out about it. The route endpoint only sends alerts it has checked, while a
// webhook's alert may have no owner or be unreadable, and still gets routed.
type alertJob struct {
	// name and severity are the alert's own, as received, for the span and
	// the log even when they didn't parse.
	name     string
	severity string
	// fingerprint is Alertmanager's, empty for an alert posted on its own.
	fingerprint string
	// teamLabel is the team the alert names, owned whether the directory
	// has it, and team the directory's entry.
	teamLabel string
	owned     bool
	team      routing.Team
	// alert is the kind's alert, valid when invalid is nil.
	alert   routing.Alert
	invalid humane.Error
}

// routed is how route answered one alert.
type routed struct {
	resp RouteResponse
	// status is one of the Status constants, never StatusResolved.
	status string
	// err says why the policy didn't route the alert, nil when it did.
	err humane.Error
	// failure is set when the evaluation failed.
	failure *failure
	// canceled is set when the client left during the evaluation. Nothing
	// was dispatched or counted, and no one is left to answer.
	canceled bool
	// took is how long the evaluation ran, zero when none did.
	took time.Duration
}

// routeOne handles POST /api/v1/teams/{team}/route: it routes one alert of
// the team in the path, dispatches the decision and answers with it. The
// status says whether the policy decided: 200 when it did, and the status
// classify picks, 422, 500 or 503, with the fallback decision when the
// evaluation failed. A request the client canceled gets 499 and no body.
func (s *Server) routeOne(c *gin.Context) {
	name := c.Param("team")

	snap, ok := s.store.Snapshot()
	if !ok {
		writeError(c, http.StatusServiceUnavailable, errNotLoaded())
		return
	}
	team, ok := s.directory.Lookup(name)
	if !ok {
		writeError(c, http.StatusNotFound, humane.New(fmt.Sprintf("team %q isn't in the team directory", name),
			"teams: "+strings.Join(s.directory.Names(), ", "),
			"GET /api/v1/teams lists the teams with their on-call targets and channels"))
		return
	}

	req, herr := decodeJSON[RouteRequest](c, strictFields,
		`send a JSON object like {"alert": {"name": "CheckoutLatencyHigh", "severity": "warning", "labels": {"env": "production"}, "firing_for": "12m"}}`)
	if herr != nil {
		writeError(c, bodyStatus(herr), herr)
		return
	}
	alert, herr := checkAlert(req.Alert)
	if herr != nil {
		writeError(c, http.StatusUnprocessableEntity, herr)
		return
	}

	s.metrics.ObserveReceived(telemetry.AlertFiring)
	r := s.route(c.Request.Context(), snap, alertJob{
		name:      alert.Name,
		severity:  string(alert.Severity),
		teamLabel: team.Name,
		owned:     true,
		team:      team,
		alert:     alert,
	})
	switch {
	case r.canceled:
		c.Status(StatusClientClosedRequest)
	case r.failure != nil:
		c.JSON(r.failure.status, r.resp)
	default:
		c.JSON(http.StatusOK, r.resp)
	}
}

// route decides one firing alert and delivers the decision, in the
// alertrouter.route span: the team's policy decides an owned, readable
// alert, and the kind's default any other, so every alert ends in a
// notification. It counts the alert, logs one "alert routed" line for it,
// and hands the decision to the notifier, except when the client left
// during the evaluation, which it reports as canceled.
func (s *Server) route(ctx context.Context, snap *store.Snapshot[routing.Input], j alertJob) routed {
	ctx, span := s.tracer.Start(ctx, "alertrouter.route", trace.WithAttributes(
		attribute.String("alert.name", j.name),
		attribute.String("alert.severity", j.severity),
		attribute.String("alert.fingerprint", j.fingerprint),
		attribute.String("alertrouter.team", j.owner()),
	))
	defer span.End()
	if !j.owned {
		span.SetAttributes(attribute.String("alertrouter.team_label", j.teamLabel))
	}

	r := s.decide(ctx, snap, j)
	if r.canceled {
		failSpan(span, r.failure.herr, "the client closed the request")
		telemetry.Log(ctx, r.failure.logLevel(), "the client closed the request during the evaluation",
			append(alertFields(j), r.failure.logFields()...)...)
		return r
	}
	recordRoute(span, r)

	n := dispatch.Notification{
		Team:        r.resp.Team,
		AlertName:   j.name,
		Fingerprint: j.fingerprint,
		Decision:    r.resp.Decision,
		Reason:      r.resp.Reason,
		Target:      r.resp.Target,
		Channel:     r.resp.Channel,
	}
	notifyErr := s.notifier.Notify(ctx, n)
	if notifyErr == nil {
		s.metrics.ObserveNotification(n.Decision, n.Destination())
	}
	s.metrics.ObserveRouted(j.owner(), r.status)

	fields := append(alertFields(j),
		zap.String("status", r.status),
		zap.String("policy", r.resp.Policy),
		zap.String("decision", r.resp.Decision),
		zap.String("reason", r.resp.Reason),
		zap.String("destination", n.Destination()),
	)
	if r.took > 0 {
		fields = append(fields, zap.Duration("took", r.took))
	}
	level := r.logLevel()
	switch {
	case r.failure != nil:
		fields = append(fields, r.failure.logFields()...)
	case r.err != nil:
		fields = append(fields, zap.Error(r.err))
	}
	if notifyErr != nil {
		// The decision stands; the caller learns that it didn't go out.
		herr := humane.Wrap(notifyErr, "dispatching the "+n.Decision+" to "+n.Destination()+" failed",
			"the decision in this answer is right; check the notifier's logs for why it didn't go out")
		span.RecordError(herr)
		span.SetStatus(codes.Error, "dispatching the notification failed")
		fields = append(fields, zap.NamedError("dispatch_error", herr))
		level = zapcore.ErrorLevel
		if r.err == nil {
			r.err = herr
		}
		if r.resp.Error == nil {
			r.resp.Error = NewErrorResponse(herr)
		}
	}
	telemetry.Log(ctx, level, "alert routed", fields...)
	return r
}

// decide finds the alert's decision: the kind's default for an alert no
// team owns or the router can't read, and otherwise the owning team's
// policy's, or the fallback that policy's failed evaluation returned.
func (s *Server) decide(ctx context.Context, snap *store.Snapshot[routing.Input], j alertJob) routed {
	switch {
	case !j.owned:
		return routed{resp: fallbackResponse("", ""), status: StatusUnowned, err: s.errUnowned(j.teamLabel)}
	case j.invalid != nil:
		return routed{resp: fallbackResponse(j.team.Name, j.team.Name+store.RootSuffix), status: StatusInvalid, err: j.invalid}
	}
	if snap == nil {
		// The handlers answer 503 before a first load; this guards the
		// contract rather than an expected case.
		return failedWithout(j, errNotLoaded(), 0)
	}

	p, ok := snap.Policy(j.team.Name)
	if !ok {
		// The store compiles a policy for every team in the directory, so
		// this is a store and a directory that disagree.
		return failedWithout(j, humane.New(
			fmt.Sprintf("team %s is in the team directory, but the loaded bundle has no policy for it", j.team.Name),
			"build the store with store.WithTeams(directory.Names()...), so both list the same teams",
		), 0)
	}

	timer := s.metrics.EvaluationTimer(j.team.Name)
	res, err := evalWithin(ctx, s.evaluationTimeout, p, routing.Input{Alert: j.alert, Team: j.team})
	took := timer.ObserveDuration()
	if res == nil {
		// Eval documents a result on every path; this guards the contract
		// rather than an expected case.
		return failedWithout(j, humane.Wrap(err,
			"evaluating "+p.Name()+" returned no result", "this is a bug in alertrouter; please report it"), took)
	}

	resp := newRouteResponse(j.team.Name, p.Name(), res)
	if err != nil {
		f := classify(p.Name(), err)
		if f.status == StatusClientClosedRequest {
			return routed{resp: resp, failure: &f, canceled: true, took: took}
		}
		// The alert goes out with the fallback, but the policy didn't make
		// it, so it counts as an evaluation error and not as a decision: a
		// real notify(reason: unrouted) and a failure stay apart.
		if f.kind != "" {
			s.metrics.ObserveEvaluationError(j.team.Name, f.kind)
		}
		resp.Error, resp.Asserts, resp.Conflict = NewErrorResponse(f.herr), f.asserts, f.conflict
		return routed{resp: resp, status: StatusFailed, err: f.herr, failure: &f, took: took}
	}

	s.metrics.ObserveDecision(j.team.Name, p.Name(), res.Decision, res.Reason)
	return routed{resp: resp, status: StatusRouted, took: took}
}

// failedWithout is the answer for an owned alert the router couldn't evaluate
// for a reason of its own, herr, rather than the policy's: the kind's default,
// failed with a 500 that no evaluation error counts, since no policy failed.
func failedWithout(j alertJob, herr humane.Error, took time.Duration) routed {
	f := failure{status: http.StatusInternalServerError, herr: herr}
	resp := fallbackResponse(j.team.Name, j.team.Name+store.RootSuffix)
	resp.Error = NewErrorResponse(herr)
	return routed{resp: resp, status: StatusFailed, err: herr, failure: &f, took: took}
}

// errUnowned says why no team owns an alert whose team label is label.
func (s *Server) errUnowned(label string) humane.Error {
	advice := "the alert was routed with the kind's default, notify(reason: unrouted), to " + routing.DefaultChannel
	if label == "" {
		return humane.New("the alert has no "+alertmanager.LabelTeam+" label", advice,
			"add a team label to the alerting rule, one of: "+strings.Join(s.directory.Names(), ", "))
	}
	return humane.New(fmt.Sprintf("team %q isn't in the team directory", label), advice,
		"set the alerting rule's team label to one of: "+strings.Join(s.directory.Names(), ", "),
		"or add the team to the team directory, --teams-file, with a policy "+label+store.RootSuffix)
}

// logLevel is the level of an alert's "alert routed" line: info when the
// alert went where it should, a policy's decision or an unowned alert's
// default, a warning for an alert the router couldn't read, and the
// failure's level for a failed evaluation.
func (r routed) logLevel() zapcore.Level {
	switch {
	case r.failure != nil:
		if r.failure.status < http.StatusInternalServerError {
			return zapcore.WarnLevel
		}
		return zapcore.ErrorLevel
	case r.status == StatusInvalid:
		return zapcore.WarnLevel
	}
	return zapcore.InfoLevel
}

// checkAlert turns the route endpoint's alert into the kind's, refusing
// what a policy couldn't evaluate: an alert without a name, a severity the
// kind doesn't declare, and a negative firing time.
func checkAlert(a AlertRequest) (routing.Alert, humane.Error) {
	if a.Name == "" {
		return routing.Alert{}, humane.New("alert.name is empty",
			"send the alert's name, its alertname, such as \"CheckoutLatencyHigh\"; a policy mutes alerts by name")
	}
	if _, ok := routing.ParseSeverity(a.Severity); !ok {
		return routing.Alert{}, humane.New(fmt.Sprintf("alert.severity %q isn't a severity", a.Severity),
			"send one of the severities the AlertRouting kind declares: "+severityList())
	}
	if a.FiringFor < 0 {
		return routing.Alert{}, humane.New("alert.firing_for is negative: "+a.FiringFor.String(),
			"send how long the alert has been firing, zero or more, such as \"12m\"")
	}
	return a.RoutingAlert(), nil
}

// recordRoute puts the alert's routing on its span, with one event per
// trace candidate, so a trace in Tempo explains the decision on its own. A
// failed evaluation marks the span failed; an unowned or invalid alert
// doesn't, since it was routed as designed.
func recordRoute(span trace.Span, r routed) {
	span.SetAttributes(
		attribute.String("alertrouter.outcome", r.status),
		attribute.String("sigil.policy", r.resp.Policy),
		attribute.String("sigil.decision", r.resp.Decision),
		attribute.String("sigil.reason", r.resp.Reason),
		attribute.Int("sigil.candidates", len(r.resp.Trace)),
	)
	for _, c := range r.resp.Trace {
		span.AddEvent("sigil.candidate", trace.WithAttributes(
			attribute.String("decision", c.Decision),
			attribute.String("reason", c.Reason),
			attribute.String("policy", c.Policy),
			attribute.String("location", c.Location),
			attribute.Bool("winner", c.Winner),
		))
	}
	if r.failure != nil {
		failSpan(span, r.failure.herr, r.failure.herr.Error())
	}
}

// owner is the team an alert counts for in metrics, logs and spans: the
// owning team, or [telemetry.NoTeam] for an unowned alert. Log pipelines and
// Prometheus index it, and an alert rule can write any team label, so an
// unowned alert's own label goes in a separate team_label instead.
func (j alertJob) owner() string {
	if !j.owned {
		return telemetry.NoTeam
	}
	return j.team.Name
}

// alertFields identify an alert in its log lines, with [alertJob.owner] as
// the team and an unowned alert's own label as team_label.
func alertFields(j alertJob) []zap.Field {
	fields := []zap.Field{
		zap.String("team", j.owner()),
		zap.String("alertname", j.name),
		zap.String("fingerprint", j.fingerprint),
	}
	if !j.owned {
		fields = append(fields, zap.String("team_label", j.teamLabel))
	}
	return fields
}

// severityList is routing.Severities as the advice of a refused severity
// spells it.
func severityList() string {
	names := make([]string, len(routing.Severities))
	for i, sev := range routing.Severities {
		names[i] = string(sev)
	}
	return strings.Join(names, ", ")
}
