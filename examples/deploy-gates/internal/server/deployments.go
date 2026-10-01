package server

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/telemetry"
)

// deployFallbackAdvice tells the client what the decision fields of a
// failed deploy evaluation mean.
const deployFallbackAdvice = "the decision fields hold the fallback decision; act on it"

// evaluate handles POST /api/v1/teams/{team}/deployments in two stages. The
// access policy grants the requestor roles for the team, and the team's
// deploy policy decides with those roles as actor.roles, so a client can't
// claim a role it wasn't granted, and with the freeze deploygate's freeze
// source answers, so it can't claim an unfrozen environment either. The
// status encodes the decision, so a client can act on the status alone.
func (s *Server) evaluate(c *gin.Context) {
	team := c.Param("team")

	deploySnap, ok := s.deploy.Snapshot()
	accessSnap, accessOK := s.access.Snapshot()
	if !ok || !accessOK {
		writeError(c, http.StatusServiceUnavailable, errNotLoaded())
		return
	}
	p, ok := deploySnap.Policy(team)
	if !ok {
		writeError(c, http.StatusNotFound, humane.New(fmt.Sprintf("team %q isn't served", team),
			"served teams: "+strings.Join(deploySnap.TeamNames(), ", "),
			"GET /api/v1/policies lists the teams and their policies"))
		return
	}
	ap, ok := accessSnap.Single()
	if !ok {
		writeError(c, http.StatusInternalServerError, errNoAccessRoot(accessSnap.PolicyNames()))
		return
	}

	req, herr := decodeJSON[DeploymentRequest](c, "send a JSON object with release, service, actor and environment; see the README for an example")
	if herr == nil && req.Release.Soak < 0 {
		herr = humane.New("release.soak is negative: "+req.Release.Soak.String(),
			"send how long the release has soaked, zero or more, such as \"6h\"")
	}
	if herr == nil && !req.Service.Tier.Valid() {
		herr = humane.New(fmt.Sprintf("service.tier %q isn't a tier", req.Service.Tier),
			"send one of the tiers the DeployApproval kind declares: "+tierList())
	}
	if herr != nil {
		writeError(c, http.StatusBadRequest, herr)
		return
	}

	ctx := c.Request.Context()
	st := s.runAccess(ctx, ap, req.AccessInput(team))
	if st.err != nil {
		s.accessFailed(c, team, p.Name(), st)
		return
	}

	roles := deployRoles(st.grants)
	in := req.DeployInput(roles)
	// The freeze is the host's fact, not the client's: whatever was decoded,
	// the policy reads the freeze source's answer, and the response, the span
	// and the log carry it, so the input the policy read can be replayed.
	in.Freeze = s.freeze.Freeze()
	resp, status, failed := s.runDeploy(ctx, deploySnap.Kind, p, team, in, roles)
	resp.Access = &AccessBlock{Policy: st.policy, Grants: st.grants}
	resp.Freeze = &in.Freeze
	if failed != nil {
		s.deployFailed(c, &resp, *failed)
		return
	}
	c.JSON(status, resp)
}

// runDeploy evaluates the team's deploy policy in the deploygate.evaluate
// span and renders the result. A failure the policy caused comes back
// classified, with the fallback decision in the response; one it didn't is
// answered with 500 by the caller through the failure's empty kind.
func (s *Server) runDeploy(ctx context.Context, kind string, p *policy.Policy[deploy.Input], team string, in deploy.Input, roles []string) (DecisionResponse, int, *failure) {
	if p == nil {
		herr := humane.New("team "+team+" has no compiled deploy policy", "this is a bug in deploygate; please report it")
		return fallbackResponse(team, team+".production"), http.StatusInternalServerError, &failure{herr: herr, status: http.StatusInternalServerError}
	}

	ctx, span := s.tracer.Start(ctx, "deploygate.evaluate", trace.WithAttributes(
		attribute.String("sigil.kind", kind),
		attribute.String("sigil.policy", p.Name()),
		attribute.String("sigil.team", team),
		attribute.StringSlice("sigil.roles", roles),
		attribute.StringSlice("sigil.freeze.environments", in.Freeze.Environments),
		attribute.Bool("sigil.freeze.unknown", in.Freeze.Unknown),
	))
	defer span.End()

	timer := s.metrics.EvaluationTimer(team)
	res, err := evalWithin(ctx, s.evaluationTimeout, p, in)
	took := timer.ObserveDuration()
	if res == nil {
		// Eval documents a result on every path; this guards the contract
		// rather than an expected case.
		herr := humane.Wrap(err, "evaluating "+p.Name()+" returned no result", "this is a bug in deploygate; please report it")
		span.SetStatus(codes.Error, herr.Error())
		return fallbackResponse(team, p.Name()), http.StatusInternalServerError, &failure{herr: herr, status: http.StatusInternalServerError}
	}

	resp, status := newDecisionResponse(team, p.Name(), res)
	recordResult(span, resp)

	if err != nil {
		f := classify(p.Name(), err, deployFallbackAdvice)
		failSpan(span, err, f.herr.Error())
		return resp, f.status, &f
	}

	s.metrics.ObserveDecision(team, p.Name(), res.Decision, res.Reason)
	telemetry.FromContext(ctx).InfoContext(ctx, "deploy decision",
		zap.String("team", team),
		zap.String("policy", p.Name()),
		zap.Strings("roles", roles),
		zap.Strings("freeze_environments", in.Freeze.Environments),
		zap.Bool("freeze_unknown", in.Freeze.Unknown),
		zap.String("decision", res.Decision),
		zap.String("reason", res.Reason),
		zap.Duration("took", took),
	)
	return resp, status, nil
}

// deployFailed answers a failed deploy evaluation with the status classify
// picked, 422 for a failed input assert, 500 for a failure of the policy and
// 503 for one that ran out of time: the fallback decision the host acts on,
// the error, and for failed asserts which ones. A request the client
// canceled gets 499 and no body.
func (s *Server) deployFailed(c *gin.Context, resp *DecisionResponse, f failure) {
	where := []zap.Field{zap.String("stage", telemetry.StageDeploy), zap.String("team", resp.Team), zap.String("policy", resp.Policy)}
	if answerUncounted(c, f, where...) {
		return
	}

	// The answer carries the fallback decision, but the policy didn't make
	// it, so it counts as an evaluation error and not as a decision: a real
	// deny(reason: no_rule_matched) and a failure stay apart in the metrics, as
	// they do for a failed access stage.
	s.metrics.ObserveEvaluationError(telemetry.StageDeploy, resp.Team, f.kind)
	ctx := c.Request.Context()
	fallback := []zap.Field{zap.String("decision", resp.Decision), zap.String("reason", resp.Reason)}
	if resp.Freeze != nil {
		fallback = append(fallback, zap.Strings("freeze_environments", resp.Freeze.Environments), zap.Bool("freeze_unknown", resp.Freeze.Unknown))
	}
	telemetry.Log(ctx, f.logLevel(), "deploy evaluation failed, answering with the fallback decision",
		f.logFields(slices.Concat(where, fallback)...)...)

	resp.Error, resp.Asserts, resp.Conflict = NewErrorResponse(f.herr), f.asserts, f.conflict
	c.JSON(f.status, resp)
}

// accessFailed answers a deployment whose access stage failed, before the
// deploy policy ran, with the status classify picked: 422 for a failed input
// assert, 500 for a conflict, a failed outcome assert or a runtime error,
// 503 for an evaluation that ran out of time, and 499 without a body for a
// request the client canceled. The deploy kind's default is the decision
// the host acts on. The deploy stage didn't decide anything, so no decision
// is counted.
func (s *Server) accessFailed(c *gin.Context, team, policyName string, st accessStage) {
	f := classify(st.policy, st.err, "the deploy policy didn't run; the decision fields hold the fallback, deny")
	where := []zap.Field{zap.String("stage", telemetry.StageAccess), zap.String("team", team), zap.String("policy", st.policy)}
	if answerUncounted(c, f, where...) {
		return
	}

	s.metrics.ObserveEvaluationError(telemetry.StageAccess, team, f.kind)
	ctx := c.Request.Context()
	telemetry.Log(ctx, f.logLevel(), "access evaluation failed, denying the deployment",
		f.logFields(where...)...)

	resp := fallbackResponse(team, policyName)
	resp.Access = &AccessBlock{Policy: st.policy, Grants: st.grants}
	resp.Error, resp.Asserts, resp.Conflict = NewErrorResponse(f.herr), f.asserts, f.conflict
	c.JSON(f.status, resp)
}

// recordResult puts the decision on the evaluation span, with one event per
// trace candidate, so a trace in Tempo explains the decision on its own.
func recordResult(span trace.Span, resp DecisionResponse) {
	span.SetAttributes(
		attribute.String("sigil.decision", resp.Decision),
		attribute.String("sigil.reason", resp.Reason),
		attribute.Int("sigil.candidates", len(resp.Trace)),
	)
	for _, c := range resp.Trace {
		span.AddEvent("sigil.candidate", trace.WithAttributes(
			attribute.String("decision", c.Decision),
			attribute.String("reason", c.Reason),
			attribute.String("policy", c.Policy),
			attribute.String("location", c.Location),
			attribute.Bool("winner", c.Winner),
		))
	}
}

// tierList is deploy.Tiers as the advice of a refused tier spells it.
func tierList() string {
	names := make([]string, len(deploy.Tiers))
	for i, t := range deploy.Tiers {
		names[i] = string(t)
	}
	return strings.Join(names, ", ")
}
