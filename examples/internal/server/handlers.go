package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/store"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
)

// evaluate handles POST /api/v1/teams/{team}/deployments: it evaluates the
// team's policy against the request and answers with the decision, its
// payload and the trace. The status encodes the decision, so a client can
// act on the status alone.
func (s *Server) evaluate(c *gin.Context) {
	team := c.Param("team")

	snap, ok := s.store.Snapshot()
	if !ok {
		writeError(c, http.StatusServiceUnavailable, errNotLoaded())
		return
	}
	p, ok := snap.Policy(team)
	if !ok {
		writeError(c, http.StatusNotFound, humane.New(fmt.Sprintf("team %q isn't served", team),
			"served teams: "+strings.Join(snap.TeamNames(), ", "),
			"GET /api/v1/policies lists the teams and their policies"))
		return
	}

	req, herr := decodeRequest(c)
	if herr != nil {
		writeError(c, http.StatusBadRequest, herr)
		return
	}

	ctx, span := s.tracer.Start(c.Request.Context(), "deploygate.evaluate", trace.WithAttributes(
		attribute.String("sigil.kind", snap.Kind),
		attribute.String("sigil.policy", p.Name()),
		attribute.String("sigil.team", team),
	))
	defer span.End()

	timer := s.metrics.EvaluationTimer(team)
	res, err := p.Eval(ctx, req.Input())
	took := timer.ObserveDuration()
	if res == nil {
		// Eval documents a result on every path; this guards the contract
		// rather than an expected case.
		herr := humane.Wrap(err, "evaluating "+p.Name()+" returned no result", "this is a bug in deploygate; please report it")
		span.SetStatus(codes.Error, herr.Error())
		writeError(c, http.StatusInternalServerError, herr)
		return
	}

	resp, status := newDecisionResponse(team, p.Name(), res)
	recordResult(span, resp)

	if err != nil {
		s.evaluationFailed(c, span, &resp, err)
		return
	}
	s.metrics.ObserveDecision(team, p.Name(), res.Decision, res.Reason)

	telemetry.FromContext(ctx).InfoContext(ctx, "deploy decision",
		zap.String("team", team),
		zap.String("policy", p.Name()),
		zap.String("decision", res.Decision),
		zap.String("reason", res.Reason),
		zap.Duration("took", took),
	)
	c.JSON(status, resp)
}

// listPolicies handles GET /api/v1/policies.
func (s *Server) listPolicies(c *gin.Context) {
	snap, ok := s.store.Snapshot()
	if !ok {
		writeError(c, http.StatusServiceUnavailable, errNotLoaded())
		return
	}
	c.JSON(http.StatusOK, newPoliciesResponse(snap))
}

// reload handles POST /api/v1/policies/reload. A bundle that doesn't load is
// a 500 with the compiler's diagnostics; the previous bundle keeps serving
// either way.
func (s *Server) reload(c *gin.Context) {
	if herr := s.store.Load(c.Request.Context()); herr != nil {
		writeError(c, http.StatusInternalServerError, herr)
		return
	}
	s.listPolicies(c)
}

// healthz handles GET /healthz: the process is up and serving HTTP.
func (s *Server) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, StatusResponse{Status: "ok"})
}

// readyz handles GET /readyz: ready once a bundle is loaded, so a pod whose
// first load failed never receives traffic.
func (s *Server) readyz(c *gin.Context) {
	snap, ok := s.store.Snapshot()
	if !ok {
		c.JSON(http.StatusServiceUnavailable, StatusResponse{Status: "not ready"})
		return
	}
	loadedAt := snap.LoadedAt.UTC()
	c.JSON(http.StatusOK, StatusResponse{Status: "ready", LoadedAt: &loadedAt})
}

// evaluationFailed answers a failed evaluation with 422: the fallback
// decision the host acts on, the error, and for failed asserts which ones.
func (s *Server) evaluationFailed(c *gin.Context, span trace.Span, resp *DecisionResponse, err error) {
	kind, herr, asserts := classify(resp.Policy, err)
	span.RecordError(err)
	span.SetStatus(codes.Error, herr.Error())

	if kind == "" {
		writeError(c, http.StatusInternalServerError, herr)
		return
	}

	// A policy's failure still answers with the fallback decision, so it
	// counts as that decision too; an error that isn't the policy's, which
	// answered 500 above, decided nothing.
	s.metrics.ObserveEvaluationError(resp.Team, kind)
	s.metrics.ObserveDecision(resp.Team, resp.Policy, resp.Decision, resp.Reason)
	ctx := c.Request.Context()
	telemetry.FromContext(ctx).WarnContext(ctx, "deploy evaluation failed, answering with the fallback decision",
		zap.String("team", resp.Team),
		zap.String("policy", resp.Policy),
		zap.String("error_kind", kind),
		zap.String("decision", resp.Decision),
		zap.String("reason", resp.Reason),
		zap.Error(err),
	)

	resp.Error = NewErrorResponse(herr)
	resp.Asserts = asserts
	c.JSON(http.StatusUnprocessableEntity, resp)
}

// classify names the kind of evaluation error for the metrics and wraps it
// with advice. The kind is empty for an error that isn't the policy's, such
// as a canceled request.
func classify(policyName string, err error) (string, humane.Error, []AssertResult) {
	var (
		assertErr   *policy.AssertionError
		runtimeErr  *policy.RuntimeError
		conflictErr *policy.ConflictError
	)
	switch {
	case errors.As(err, &assertErr):
		asserts := assertResults(assertErr)
		reasons := make([]string, 0, len(asserts))
		for _, a := range asserts {
			reasons = append(reasons, a.Reason)
		}
		return telemetry.ErrorKindAssertion, humane.Wrap(err,
			fmt.Sprintf("the request fails %s's asserts: %s", policyName, strings.Join(reasons, ", ")),
			"the decision fields hold the fallback decision; act on it",
			"fix the request so the asserts listed in asserts hold, then ask again",
		), asserts
	case errors.As(err, &runtimeErr):
		return telemetry.ErrorKindRuntime, humane.Wrap(err,
			fmt.Sprintf("%s can't be evaluated against this request: %s at %s", policyName, runtimeErr.Message, runtimeErr.Position),
			"the decision fields hold the fallback decision; act on it",
			"a runtime error usually means the policy doesn't guard against this input; tell the policy's owners",
		), nil
	case errors.As(err, &conflictErr):
		return telemetry.ErrorKindConflict, humane.Wrap(err,
			fmt.Sprintf("%s produced decisions that can't stand together: %s", policyName, conflictErr.Message),
			"the decision fields hold the fallback decision; act on it",
			"a conflict is a defect in the policy, not in the request; tell the policy's owners",
		), nil
	default:
		return "", humane.Wrap(err, "evaluating "+policyName+" failed",
			"retry the request; if it keeps failing, check the deploygate logs"), nil
	}
}

// decodeRequest reads the body strictly: unknown fields, trailing data and
// numeric durations are errors, so a typo in a field name is reported
// instead of silently evaluating a zero value.
func decodeRequest(c *gin.Context) (*DeploymentRequest, humane.Error) {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes))
	dec.DisallowUnknownFields()

	var req DeploymentRequest
	if err := dec.Decode(&req); err != nil {
		return nil, badRequest(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, humane.New("the request body holds more than one JSON value",
			"send exactly one deploy request object per call")
	}
	if req.Release.Soak < 0 {
		return nil, humane.New("release.soak is negative: "+req.Release.Soak.String(),
			"send how long the release has soaked, zero or more, such as \"6h\"")
	}
	return &req, nil
}

// badRequest explains why a body didn't decode.
func badRequest(err error) humane.Error {
	const advice = "send a JSON object with release, service, actor and environment; see the README for an example"

	if errors.Is(err, io.EOF) {
		return humane.New("the request body is empty", advice)
	}

	// A duration error is a humane error already, with its own advice.
	if _, ok := errors.AsType[humane.Error](err); ok {
		return humane.Wrap(err, "the request body isn't a valid deploy request", advice)
	}

	if maxErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return humane.Wrap(err, fmt.Sprintf("the request body is larger than %d bytes", maxErr.Limit), advice)
	}

	return humane.Wrap(err, "the request body isn't a valid deploy request: "+err.Error(), advice,
		"field names are case-sensitive and unknown fields are rejected")
}

// recordResult puts the decision on the evaluation span, with one event per
// trace candidate, so a trace in Jaeger explains the decision on its own.
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

// newPoliciesResponse renders a snapshot.
func newPoliciesResponse(snap *store.Snapshot) PoliciesResponse {
	if snap == nil {
		return PoliciesResponse{Policies: []PolicyResponse{}}
	}
	resp := PoliciesResponse{
		Kind:     snap.Kind,
		Version:  snap.KindVersion,
		LoadedAt: snap.LoadedAt.UTC(),
		Source:   snap.Source,
		Policies: make([]PolicyResponse, 0, len(snap.Teams)),
	}
	for _, tp := range snap.Teams {
		resp.Policies = append(resp.Policies, PolicyResponse{Team: tp.Team, Policy: tp.Policy})
	}
	return resp
}

// errNotLoaded is the answer while no bundle has loaded yet.
func errNotLoaded() humane.Error {
	return humane.New("no policy bundle is loaded yet",
		"wait until GET /readyz reports ready",
		"if it never does, the deploygate logs name the policy that fails to compile")
}

// writeError answers with a humane error as JSON. Server-side failures are
// logged too, since the client can't fix them.
func writeError(c *gin.Context, status int, herr humane.Error) {
	if status >= http.StatusInternalServerError {
		ctx := c.Request.Context()
		telemetry.FromContext(ctx).ErrorContext(ctx, "request failed",
			zap.Int("status", status),
			zap.String("path", c.Request.URL.Path),
			zap.Error(herr),
			zap.Strings("advice", herr.Advice()),
		)
	}
	c.JSON(status, ErrorEnvelope{Error: NewErrorResponse(herr)})
}

// decisionStatus maps a decision to its HTTP status through the typed
// decision handles: approve is 200, review 202, and deny, or anything a
// newer kind adds, 403, so an unknown decision fails closed.
func decisionStatus(res *policy.Result) int {
	if _, ok := deploy.Approve.Match(res); ok {
		return http.StatusOK
	}
	if _, ok := deploy.Review.Match(res); ok {
		return http.StatusAccepted
	}
	return http.StatusForbidden
}
