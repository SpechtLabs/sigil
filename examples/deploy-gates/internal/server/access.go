package server

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/access"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/store"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/telemetry"
)

// accessStage is the outcome of evaluating the access policy for one
// request: the roles granted, the trace, and the error when the evaluation
// failed, in which case nothing is granted.
type accessStage struct {
	policy string
	grants []GrantResult
	trace  []CandidateResult
	err    error
}

// grants handles POST /api/v1/access/grants: it evaluates the access policy
// on its own and answers with the roles it grants. The status says whether
// anything was granted: 200 when at least one role was, 403 when none was.
// A failed evaluation grants nothing and answers with the status classify
// picks: 422 for a failed input assert, the caller's to fix, 500 for a
// conflict, a failed outcome assert or a runtime error, which are the
// policy's, and 503 when the evaluation ran out of time. A request the
// client canceled gets 499 and no body.
func (s *Server) grants(c *gin.Context) {
	snap, ok := s.access.Snapshot()
	if !ok {
		writeError(c, http.StatusServiceUnavailable, errNotLoaded())
		return
	}
	p, ok := snap.Single()
	if !ok {
		writeError(c, http.StatusInternalServerError, errNoAccessRoot(snap.PolicyNames()))
		return
	}

	req, herr := decodeJSON[AccessRequest](c, "send a JSON object with actor, team and environment; see the README for an example")
	if herr != nil {
		writeError(c, http.StatusBadRequest, herr)
		return
	}
	if req.Team == "" {
		writeError(c, http.StatusBadRequest, humane.New("the request names no team",
			"set team to the team whose services the actor wants access to, for example \"payments\""))
		return
	}

	in := access.Input{Actor: req.Actor, Team: req.Team, Environment: req.Environment}
	st := s.runAccess(c.Request.Context(), p, in)
	resp := AccessResponse{
		Policy:      st.policy,
		Team:        req.Team,
		Environment: req.Environment,
		Grants:      st.grants,
		Trace:       st.trace,
	}

	if st.err != nil {
		f := classify(st.policy, st.err, "no role is granted; act as if grants were empty")
		where := []zap.Field{zap.String("stage", telemetry.StageAccess), zap.String("team", req.Team), zap.String("policy", st.policy)}
		if answerUncounted(c, f, where...) {
			return
		}
		s.metrics.ObserveEvaluationError(telemetry.StageAccess, req.Team, f.kind)
		ctx := c.Request.Context()
		telemetry.Log(ctx, f.logLevel(), "access evaluation failed, granting nothing",
			f.logFields(where...)...)
		resp.Error, resp.Asserts, resp.Conflict = NewErrorResponse(f.herr), f.asserts, f.conflict
		c.JSON(f.status, resp)
		return
	}

	status := http.StatusOK
	if len(resp.Grants) == 0 {
		status = http.StatusForbidden
	}
	c.JSON(status, resp)
}

// runAccess evaluates the access policy in a span of its own, a sibling of
// the deploy evaluation's under the request's span, so a trace shows which
// roles the deploy policy was given and why.
func (s *Server) runAccess(ctx context.Context, p *policy.Policy[access.Input], in access.Input) accessStage {
	if p == nil {
		return accessStage{policy: store.AccessRoot, grants: []GrantResult{}, trace: []CandidateResult{},
			err: errNoAccessRoot(nil)}
	}

	ctx, span := s.tracer.Start(ctx, "deploygate.access", trace.WithAttributes(
		attribute.String("sigil.kind", access.Kind.Name()),
		attribute.String("sigil.policy", p.Name()),
		attribute.String("sigil.team", in.Team),
		attribute.String("sigil.environment", in.Environment),
	))
	defer span.End()

	timer := s.metrics.AccessTimer(in.Team)
	res, err := evalWithin(ctx, s.evaluationTimeout, p, in)
	took := timer.ObserveDuration()

	st := accessStage{policy: p.Name(), grants: []GrantResult{}, trace: []CandidateResult{}, err: err}
	if res != nil {
		st.grants = grantResults(res)
		st.trace = traceCandidates(res)
	}

	span.SetAttributes(attribute.Int("sigil.grants", len(st.grants)))
	for _, g := range st.grants {
		lifetime := ""
		if g.TTL != nil {
			lifetime = g.TTL.String()
		}
		span.AddEvent("sigil.grant", trace.WithAttributes(
			attribute.String("role", g.Role),
			attribute.String("reason", g.Reason),
			attribute.String("ttl", lifetime),
		))
	}

	if err != nil {
		failSpan(span, err, "access evaluation failed")
		return st
	}

	for _, g := range st.grants {
		s.metrics.ObserveGrant(in.Team, g.Role, g.Reason)
	}
	telemetry.FromContext(ctx).InfoContext(ctx, "access granted",
		zap.String("team", in.Team),
		zap.String("environment", in.Environment),
		zap.String("policy", p.Name()),
		zap.Strings("roles", grantedRoles(st.grants)),
		zap.Duration("took", took),
	)
	return st
}

// grantedRoles lists the roles of grants, for a log line.
func grantedRoles(grants []GrantResult) []string {
	roles := make([]string, 0, len(grants))
	for _, g := range grants {
		roles = append(roles, g.Role)
	}
	return roles
}

// errNoAccessRoot is the answer when the access store serves other than one
// root, which a misconfigured host would cause, not a request.
func errNoAccessRoot(roots []string) humane.Error {
	return humane.Newf("the access store serves %d policies, not the one root it needs: %v", len(roots), roots,
		humane.WithAdvice("configure the access store with a single root, store.WithRoots(\"access.main\")"))
}
